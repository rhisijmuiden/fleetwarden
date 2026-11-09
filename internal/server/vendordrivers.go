package server

// BIOS updates from the Dell, HP and Lenovo catalogs (phase 10b).
//
// Once a day the server reads the vendor catalogs for the models among its
// Windows devices: only the catalog files, never an update. The newest BIOS
// a device can take is offered like any repository update; an administrator
// adds it, the server downloads it from the vendor and checks its SHA-256,
// and patch jobs install it with the vendor's silent installer.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/drivercat"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const (
	vendorCatalogAge   = 24 * time.Hour
	vendorCatalogLimit = 64 << 20 // one catalog file
	vendorSyncTime     = 30 * time.Minute
)

// vendorSettings switches the vendor catalogs on or off (setting
// "vendor_catalogs"). All are on by default.
type vendorSettings struct {
	Dell   bool `json:"dell"`
	HP     bool `json:"hp"`
	Lenovo bool `json:"lenovo"`
}

func (v vendorSettings) enabled(vendor string) bool {
	switch vendor {
	case drivercat.Dell:
		return v.Dell
	case drivercat.HP:
		return v.HP
	case drivercat.Lenovo:
		return v.Lenovo
	}
	return false
}

func (s *Server) loadVendorSettings(ctx context.Context) vendorSettings {
	v := vendorSettings{Dell: true, HP: true, Lenovo: true}
	_ = s.st.GetSetting(ctx, "vendor_catalogs", &v)
	return v
}

// vendorSync is the state of the catalog reader.
type vendorSync struct {
	mu      sync.Mutex
	running bool
	lastRun time.Time
	lastErr string
	// extractCab is replaced by tests.
	extractCab func(ctx context.Context, data []byte) ([]byte, error)
}

func init() {
	for vendor, hosts := range drivercat.Hosts {
		driverHosts[vendor] = hosts
	}
}

var errCatalogMissing = errors.New("the vendor has no catalog for this model")

// fetchCatalog fetches a catalog file from a vendor host.
func (s *Server) fetchCatalog(ctx context.Context, vendor, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && s.drivers.allowHost == nil) || !s.driverAllowHost(vendor, u.Hostname()) {
		return nil, errors.New("not a catalog address of " + vendor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FleetWarden/"+version.Version)
	res, err := s.driverClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusForbidden {
		return nil, errCatalogMissing
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", u.Host, res.Status)
	}
	if !s.driverAllowHost(vendor, res.Request.URL.Hostname()) {
		return nil, errors.New("redirected to an unknown host " + res.Request.URL.Hostname())
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, vendorCatalogLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > vendorCatalogLimit {
		return nil, errors.New("the catalog is too large")
	}
	return raw, nil
}

// fetchCabCatalog fetches a catalog packed in a cabinet and unpacks it.
func (s *Server) fetchCabCatalog(ctx context.Context, vendor, link, sha string) ([]byte, error) {
	raw, err := s.fetchCatalog(ctx, vendor, link)
	if err != nil {
		return nil, err
	}
	if sha != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != sha {
			return nil, errors.New("the catalog does not match its checksum")
		}
	}
	extract := s.vendors.extractCab
	if extract == nil {
		extract = cabextract
	}
	return extract(ctx, raw)
}

// cabextract unpacks the single file of a catalog cabinet.
func cabextract(ctx context.Context, data []byte) ([]byte, error) {
	bin, err := exec.LookPath("cabextract")
	if err != nil {
		return nil, errors.New("cabextract is not installed on the server (apt install cabextract)")
	}
	f, err := os.CreateTemp("", "fw-catalog-*.cab")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-q", "-p", f.Name())
	cmd.Stdout, cmd.Stderr = &limitedBuffer{buf: &out, limit: vendorCatalogLimit * 4}, &errOut
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return nil, fmt.Errorf("cabextract: %v: %s", err, lastLine(errOut.String()))
	}
	return out.Bytes(), nil
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.limit {
		return 0, errors.New("the catalog is too large")
	}
	return l.buf.Write(p)
}

func lastLine(v string) string {
	lines := strings.Split(strings.TrimSpace(v), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// catalogTarget is one model whose catalog is needed.
type catalogTarget struct {
	vendor, key string
	platform    drivercat.Platform // one device of the model (OS release for HP, Lenovo)
}

func devicePlatform(p store.DevicePlatform) drivercat.Platform {
	return drivercat.Platform{Manufacturer: p.Manufacturer, Model: p.Model, SKU: p.SKU, Baseboard: p.Baseboard,
		BIOSVersion: p.BIOSVersion, OSName: p.OSName, OSVersion: p.OSVersion}
}

// syncVendorCatalogs reads the catalogs that are missing or older than a
// day (all of them with force) and matches the devices again.
func (s *Server) syncVendorCatalogs(ctx context.Context, force bool) error {
	s.vendors.mu.Lock()
	if s.vendors.running {
		s.vendors.mu.Unlock()
		return nil
	}
	s.vendors.running = true
	s.vendors.mu.Unlock()
	defer func() {
		s.vendors.mu.Lock()
		s.vendors.running = false
		s.vendors.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, vendorSyncTime)
	defer cancel()
	err := s.readVendorCatalogs(ctx, force)
	if merr := s.matchVendorDrivers(ctx); err == nil {
		err = merr
	}
	s.vendors.mu.Lock()
	s.vendors.lastRun = s.now()
	s.vendors.lastErr = ""
	if err != nil {
		s.vendors.lastErr = err.Error()
	}
	s.vendors.mu.Unlock()
	return err
}

func (s *Server) readVendorCatalogs(ctx context.Context, force bool) error {
	devs, err := s.st.DevicePlatforms(ctx)
	if err != nil {
		return err
	}
	set := s.loadVendorSettings(ctx)
	targets := map[string]catalogTarget{}
	for _, d := range devs {
		if d.Platform != "windows" {
			continue
		}
		p := devicePlatform(d)
		vendor, key := drivercat.Key(p)
		if key == "" || !set.enabled(vendor) {
			continue
		}
		if _, ok := targets[vendor+"/"+key]; !ok {
			targets[vendor+"/"+key] = catalogTarget{vendor: vendor, key: key, platform: p}
		}
	}
	have, err := s.st.VendorCatalogs(ctx)
	if err != nil {
		return err
	}
	fresh := map[string]bool{}
	for _, c := range have {
		if c.FetchedAt != nil && s.now().Sub(*c.FetchedAt) < vendorCatalogAge {
			fresh[c.Vendor+"/"+c.Key] = true
		}
	}
	var dellIndex map[string]drivercat.DellModelCatalog
	var dellErr error
	for id, t := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if fresh[id] && !force {
			continue
		}
		var items []drivercat.Item
		var err error
		switch t.vendor {
		case drivercat.Dell:
			if dellIndex == nil && dellErr == nil {
				dellIndex, dellErr = s.readDellIndex(ctx)
			}
			if dellErr != nil {
				err = dellErr
				break
			}
			items, err = s.readDellModel(ctx, dellIndex, t.key)
		case drivercat.HP:
			items, err = s.readHP(ctx, t)
		case drivercat.Lenovo:
			items, err = s.readLenovo(ctx, t)
		}
		if items == nil {
			items = []drivercat.Item{}
		}
		errText := ""
		switch {
		case errors.Is(err, errCatalogMissing):
			// The vendor does not list the model: no items, and say so.
			if err := s.st.SaveVendorCatalog(ctx, t.vendor, t.key, items, ""); err != nil {
				return err
			}
			errText = err.Error()
		case err != nil:
			errText = cleanText(err.Error(), 500)
			s.log.Warn("vendor catalog", "vendor", t.vendor, "model", t.key, "err", err)
		}
		if err := s.st.SaveVendorCatalog(ctx, t.vendor, t.key, items, errText); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) readDellIndex(ctx context.Context) (map[string]drivercat.DellModelCatalog, error) {
	raw, err := s.fetchCabCatalog(ctx, drivercat.Dell, drivercat.DellIndexURL, "")
	if err != nil {
		return nil, fmt.Errorf("Dell catalog index: %w", err)
	}
	return drivercat.ParseDellIndex(raw)
}

func (s *Server) readDellModel(ctx context.Context, idx map[string]drivercat.DellModelCatalog, key string) ([]drivercat.Item, error) {
	m, ok := idx[key]
	if !ok {
		return nil, errCatalogMissing
	}
	raw, err := s.fetchCabCatalog(ctx, drivercat.Dell, drivercat.DellBase+strings.TrimPrefix(m.Path, "/"), m.SHA256)
	if err != nil {
		return nil, err
	}
	return drivercat.ParseDellModel(raw, key)
}

func (s *Server) readHP(ctx context.Context, t catalogTarget) ([]drivercat.Item, error) {
	for _, os := range drivercat.HPOSVersions(t.platform) {
		raw, err := s.fetchCabCatalog(ctx, drivercat.HP, drivercat.HPRef(t.key, os), "")
		if errors.Is(err, errCatalogMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return drivercat.ParseHPRef(raw)
	}
	return nil, errCatalogMissing
}

func (s *Server) readLenovo(ctx context.Context, t catalogTarget) ([]drivercat.Item, error) {
	for _, link := range drivercat.LenovoCatalog(t.key, t.platform) {
		raw, err := s.fetchCatalog(ctx, drivercat.Lenovo, link)
		if errors.Is(err, errCatalogMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		refs, err := drivercat.ParseLenovoCatalog(raw)
		if err != nil {
			return nil, err
		}
		var items []drivercat.Item
		for _, ref := range refs {
			desc, err := s.fetchCatalog(ctx, drivercat.Lenovo, ref.Location)
			if err != nil {
				return nil, err
			}
			if ref.SHA256 != "" {
				if sum := sha256.Sum256(desc); hex.EncodeToString(sum[:]) != ref.SHA256 {
					return nil, errors.New("a Lenovo package description does not match its checksum")
				}
			}
			it, err := drivercat.ParseLenovoPackage(desc, ref.Location)
			if err != nil {
				s.log.Info("Lenovo package skipped", "package", ref.Location, "err", err)
				continue
			}
			items = append(items, it)
		}
		return items, nil
	}
	return nil, errCatalogMissing
}

var vendorNames = map[string]string{drivercat.Dell: "Dell", drivercat.HP: "HP", drivercat.Lenovo: "Lenovo"}

// matchVendorDrivers offers each Windows device the newest BIOS its vendor
// catalog has for it, and matches the uploaded packages.
func (s *Server) matchVendorDrivers(ctx context.Context) error {
	cats, err := s.st.VendorCatalogs(ctx)
	if err != nil {
		return err
	}
	items := map[string][]drivercat.Item{}
	for _, c := range cats {
		var list []drivercat.Item
		if json.Unmarshal(c.Items, &list) == nil {
			items[c.Vendor+"/"+c.Key] = list
		}
	}
	devs, err := s.st.DevicePlatforms(ctx)
	if err != nil {
		return err
	}
	set := s.loadVendorSettings(ctx)
	for _, d := range devs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Platform != "windows" {
			continue
		}
		var offer []store.DriverScanItem
		vendor, key := drivercat.Key(devicePlatform(d))
		if key != "" && set.enabled(vendor) {
			if it, ok := drivercat.Best(vendor, d.BIOSVersion, items[vendor+"/"+key]); ok {
				offer = append(offer, vendorScanItem(vendor, it, d))
			}
		}
		if err := s.st.SaveVendorMatches(ctx, d.DeviceID, d.Platform, offer); err != nil {
			return err
		}
	}
	return s.st.MatchDriverUploads(ctx)
}

func vendorScanItem(vendor string, it drivercat.Item, d store.DevicePlatform) store.DriverScanItem {
	u := store.DriverUpdate{
		Source: vendor, SourceID: it.ID, Kind: agentproto.DriverKindFirmware, Title: cleanText(it.Title, 300),
		Vendor: vendorNames[vendor], Class: "BIOS", Version: cleanText(it.Version, 100), SizeBytes: it.Size,
		Description: cleanText(it.Description, 2000), Reboot: true, Installer: vendor, Command: cleanText(it.Command, 500),
		ExitCodes: it.ExitCodes, Critical: it.Critical,
		Files: []store.DriverFile{{Name: safeFileName(it.Name), URL: it.URL, Size: it.Size, SHA256: it.SHA256}},
	}
	if !it.Released.IsZero() {
		r := it.Released
		u.ReleasedAt = &r
	}
	if u.Title == "" {
		u.Title = vendorNames[vendor] + " BIOS " + u.Version
	}
	return store.DriverScanItem{Update: u, DeviceName: cleanText(d.Model, 200), CurrentVersion: cleanText(d.BIOSVersion, 100)}
}

// vendorTick reads stale catalogs once an hour (in the background).
func (s *Server) vendorTick() {
	if s.bgCtx == nil {
		return
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		if err := s.syncVendorCatalogs(s.bgCtx, false); err != nil {
			s.log.Warn("vendor catalogs", "err", err)
		}
	}()
}

// uploadsDir holds uploaded packages. Unlike the repository (which can be
// downloaded again) it is part of the backup.
func (s *Server) uploadsDir() string { return filepath.Join(s.cfg.DataDir, "driver-uploads") }

// ---- API: vendor catalogs ----

type vendorStatus struct {
	Settings  vendorSettings        `json:"settings"`
	Running   bool                  `json:"running"`
	LastRun   *time.Time            `json:"lastRun"`
	LastError string                `json:"lastError"`
	Catalogs  []store.VendorCatalog `json:"catalogs"`
	// Unmatched counts Windows devices of these vendors whose model could
	// not be read (no SKU or baseboard reported yet: an older agent).
	Unmatched map[string]int `json:"unmatched"`
}

func (s *Server) handleVendorStatus(w http.ResponseWriter, r *http.Request) {
	out := vendorStatus{Settings: s.loadVendorSettings(r.Context()), Unmatched: map[string]int{}}
	cats, err := s.st.VendorCatalogs(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	devs, err := s.st.DevicePlatforms(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	count := map[string]int{}
	for _, d := range devs {
		if d.Platform != "windows" {
			continue
		}
		vendor, key := drivercat.Key(devicePlatform(d))
		switch {
		case vendor == "":
		case key == "":
			out.Unmatched[vendor]++
		default:
			count[vendor+"/"+key]++
		}
	}
	out.Catalogs = []store.VendorCatalog{}
	for _, c := range cats {
		if c.Devices = count[c.Vendor+"/"+c.Key]; c.Devices > 0 {
			out.Catalogs = append(out.Catalogs, c)
		}
	}
	s.vendors.mu.Lock()
	out.Running = s.vendors.running
	if !s.vendors.lastRun.IsZero() {
		t := s.vendors.lastRun
		out.LastRun = &t
	}
	out.LastError = s.vendors.lastErr
	s.vendors.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutVendorSettings(w http.ResponseWriter, r *http.Request) {
	var v vendorSettings
	if !decode(w, r, &v) {
		return
	}
	if err := s.st.PutSetting(r.Context(), "vendor_catalogs", v); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "driver.vendor_settings", "", true, map[string]any{"dell": v.Dell, "hp": v.HP, "lenovo": v.Lenovo})
	s.vendorTick()
	s.handleVendorStatus(w, r)
}

func (s *Server) handleVendorSync(w http.ResponseWriter, r *http.Request) {
	if s.bgCtx != nil {
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			if err := s.syncVendorCatalogs(s.bgCtx, true); err != nil {
				s.log.Warn("vendor catalogs", "err", err)
			}
		}()
	}
	s.audit(r, "driver.vendor_sync", "", true, nil)
	// Give a quick sync the chance to finish before answering.
	for range 20 {
		s.vendors.mu.Lock()
		running := s.vendors.running
		s.vendors.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.handleVendorStatus(w, r)
}

// ---- API: uploaded packages ----

const maxDriverUpload = 2 << 30

// driverUploadKind tells from the file name how an upload is installed.
func driverUploadKind(platform, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case platform == "windows" && (ext == ".zip" || ext == ".cab"):
		return agentproto.InstallerINF
	case platform == "windows" && ext == ".exe":
		return agentproto.InstallerExe
	case platform == "linux" && ext == ".cab":
		return agentproto.InstallerFwupd
	}
	return ""
}

// handleDriverUploadFile stores a package sent as the raw request body
// (file name in X-File-Name) under its SHA-256.
func (s *Server) handleDriverUploadFile(w http.ResponseWriter, r *http.Request) {
	name := safeFileName(r.Header.Get("X-File-Name"))
	if name == "" || (driverUploadKind("windows", name) == "" && driverUploadKind("linux", name) == "") {
		apiError(w, http.StatusBadRequest, "unsupported_driver_file")
		return
	}
	if r.ContentLength > maxDriverUpload {
		apiError(w, http.StatusRequestEntityTooLarge, "installer_too_large")
		return
	}
	dir := s.uploadsDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.internalError(w, r, err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), http.MaxBytesReader(w, r.Body, maxDriverUpload))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		apiError(w, http.StatusRequestEntityTooLarge, "installer_too_large")
		return
	case err != nil:
		apiError(w, http.StatusBadRequest, "upload_failed")
		return
	case n == 0:
		apiError(w, http.StatusBadRequest, "unsupported_driver_file")
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if strings.EqualFold(filepath.Ext(name), ".zip") && !zipHasINF(tmp.Name()) {
		apiError(w, http.StatusBadRequest, "no_inf_in_zip")
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, sum)); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, uploadResult{SHA256: sum, Size: n, Name: name})
}

type driverUploadReq struct {
	SHA256       string `json:"sha256"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Version      string `json:"version"`
	Vendor       string `json:"vendor"`
	Description  string `json:"description"`
	Reboot       bool   `json:"reboot"`
	Command      string `json:"command"` // arguments of an uploaded program
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
}

func (req driverUploadReq) update() (store.DriverUpdate, string) {
	u := store.DriverUpdate{
		Kind: req.Kind, Platform: req.Platform, Title: cleanText(req.Title, 300), Version: cleanText(req.Version, 100),
		Vendor: cleanText(req.Vendor, 120), Description: cleanText(req.Description, 2000), Reboot: req.Reboot,
		Command: cleanText(req.Command, 500),
		Target:  store.DriverTarget{Manufacturer: cleanText(req.Manufacturer, 120), Model: cleanText(req.Model, 120)},
	}
	switch {
	case u.Platform != "windows" && u.Platform != "linux":
		return u, "invalid_platform"
	case u.Kind != agentproto.DriverKindDriver && u.Kind != agentproto.DriverKindFirmware:
		return u, "invalid_kind"
	case u.Title == "":
		return u, "title_required"
	}
	if u.Kind == agentproto.DriverKindFirmware {
		u.Reboot = true
	}
	return u, ""
}

func (s *Server) handleCreateDriverUpload(w http.ResponseWriter, r *http.Request) {
	var req driverUploadReq
	if !decode(w, r, &req) {
		return
	}
	u, code := req.update()
	if code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	sum := strings.ToLower(req.SHA256)
	name := safeFileName(req.Name)
	u.Installer = driverUploadKind(u.Platform, name)
	if u.Installer == "" {
		apiError(w, http.StatusBadRequest, "unsupported_driver_file")
		return
	}
	if u.Installer != agentproto.InstallerExe {
		u.Command = ""
	}
	if u.Installer == agentproto.InstallerExe {
		u.ExitCodes = []int{0, 3010, 1641}
	}
	st, err := os.Stat(filepath.Join(s.uploadsDir(), sum))
	if !sha256Hex.MatchString(sum) || err != nil || !st.Mode().IsRegular() {
		apiError(w, http.StatusBadRequest, "upload_missing")
		return
	}
	u.SourceID = sum
	u.SizeBytes = st.Size()
	u.Files = []store.DriverFile{{Name: name, Size: st.Size(), SHA256: sum, Stored: sum}}
	cu, _ := currentUser(r)
	u.DecidedBy = cu.Username
	out, err := s.st.CreateDriverUpload(r.Context(), u)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "driver_already_uploaded")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.MatchDriverUploads(r.Context()); err != nil {
		s.log.Warn("driver uploads", "err", err)
	}
	s.audit(r, "driver.upload", out.Title, true, map[string]any{"id": out.ID, "sha256": sum, "platform": out.Platform})
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateDriverUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req driverUploadReq
	if !decode(w, r, &req) {
		return
	}
	cur, err := s.st.DriverByID(r.Context(), id)
	if err != nil || cur.Source != agentproto.DriverSourceUpload {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	req.Platform = cur.Platform
	u, code := req.update()
	if code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	u.ID = id
	if cur.Installer != agentproto.InstallerExe {
		u.Command = ""
	}
	out, err := s.st.UpdateDriverUpload(r.Context(), u)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.MatchDriverUploads(r.Context()); err != nil {
		s.log.Warn("driver uploads", "err", err)
	}
	s.audit(r, "driver.upload_update", out.Title, true, map[string]any{"id": id})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteDriverUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	cur, err := s.st.DriverByID(r.Context(), id)
	if err != nil || cur.Source != agentproto.DriverSourceUpload {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.st.DeleteDriverUpload(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	// The file goes unless another entry has the same contents.
	if keep, err := s.st.StoredDriverFiles(r.Context()); err == nil {
		for _, f := range cur.Files {
			if f.Stored != "" && !keep[f.Stored] {
				_ = os.Remove(filepath.Join(s.uploadsDir(), f.Stored))
			}
		}
	}
	s.audit(r, "driver.upload_delete", cur.Title, true, map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

// zipHasINF reports whether a ZIP archive holds a driver (.inf).
func zipHasINF(path string) bool {
	z, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.EqualFold(filepath.Ext(f.Name), ".inf") {
			return true
		}
	}
	return false
}
