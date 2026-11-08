package server

// Driver and firmware updates through FleetWarden's own repository.
//
// Agents report the driver and firmware updates their platform offers
// (Windows Update, fwupd) with each patch scan. Nothing is downloaded until
// an administrator adds an update to the repository. The server then fetches
// the files from the vendor (only from the vendor's known download hosts),
// checks the vendor's checksum when there is one and stores each file under
// DataDir/repository/<sha256>. Patch jobs carry the repository updates a
// device may install (per patch policy: drivers, firmware), and agents fetch
// the files from the server with a signed request.

import (
	"context"
	"crypto/sha1" //nolint:gosec // the vendor's own checksum (fwupd, Windows Update)
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const (
	maxDriversPerDevice  = 500
	maxDriverFiles       = 20
	maxDriverUpdateBytes = 4 << 30 // all files of one update together
	driverDownloadTries  = 3
	driverDownloadTime   = 2 * time.Hour
	// driverFreeSpace is kept free on the data disk after a download.
	driverFreeSpace = 2 << 30
)

// driverRepo is the state of the repository downloader.
type driverRepo struct {
	mu      sync.Mutex
	running bool
	// client and allowHost are replaced by tests.
	client    *http.Client
	allowHost func(source, host string) bool
}

func (s *Server) repositoryDir() string { return filepath.Join(s.cfg.DataDir, "repository") }

// driverHosts are the download hosts per source. Files are only fetched
// from these, whatever an agent reports.
var driverHosts = map[string][]string{
	agentproto.DriverSourceWindowsUpdate: {"windowsupdate.com", "update.microsoft.com", "delivery.mp.microsoft.com", "download.microsoft.com"},
	agentproto.DriverSourceFwupd:         {"fwupd.org"},
}

func defaultAllowHost(source, host string) bool {
	host = strings.ToLower(host)
	for _, h := range driverHosts[source] {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func (s *Server) driverAllowHost(source, host string) bool {
	if s.drivers.allowHost != nil {
		return s.drivers.allowHost(source, host)
	}
	return defaultAllowHost(source, host)
}

// driverURL checks a vendor download address. Windows Update still hands
// out http addresses; those hosts also answer on https, which is used.
func (s *Server) driverURL(source, raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", false
	}
	if !s.driverAllowHost(source, u.Hostname()) {
		return "", false
	}
	if u.Scheme == "http" && s.drivers.allowHost == nil {
		u.Scheme = "https"
	}
	return u.String(), true
}

func validDriverSource(v string) bool {
	return v == agentproto.DriverSourceWindowsUpdate || v == agentproto.DriverSourceFwupd
}

var hexRE = func(n int) func(string) bool {
	return func(v string) bool {
		if len(v) != n {
			return false
		}
		_, err := hex.DecodeString(v)
		return err == nil
	}
}

var isSHA256, isSHA1 = hexRE(64), hexRE(40)

// cleanDriverUpdate checks one reported update. It returns false when the
// update cannot be used (unknown source, no files from an allowed host).
func (s *Server) cleanDriverUpdate(platform string, d agentproto.DriverUpdate) (store.DriverScanItem, bool) {
	u := store.DriverUpdate{
		Source: d.Source, SourceID: cleanText(d.SourceID, 128), Kind: d.Kind, Platform: platform,
		Title: cleanText(d.Title, 300), Vendor: cleanText(d.Vendor, 120), Class: cleanText(d.Class, 60),
		Version: cleanText(d.Version, 100), SizeBytes: max(d.SizeBytes, 0), HardwareID: cleanText(d.HardwareID, 300),
		Description: cleanText(d.Description, 2000), Reboot: d.RebootRequired, ReleasedAt: validTime(d.ReleasedAt),
	}
	if !validDriverSource(u.Source) || u.SourceID == "" {
		return store.DriverScanItem{}, false
	}
	u.SourceID = strings.ToLower(u.SourceID)
	if u.Kind != agentproto.DriverKindFirmware {
		u.Kind = agentproto.DriverKindDriver
	}
	if u.Title == "" {
		u.Title = u.SourceID
	}
	var total int64
	for _, f := range d.Files {
		if len(u.Files) >= maxDriverFiles {
			break
		}
		link, ok := s.driverURL(u.Source, f.URL)
		if !ok {
			continue
		}
		name := safeFileName(f.Name)
		if name == "" {
			if p, err := url.Parse(link); err == nil {
				name = safeFileName(filepath.Base(p.Path))
			}
		}
		if name == "" {
			name = "file" + strconv.Itoa(len(u.Files)+1)
		}
		df := store.DriverFile{Name: name, URL: link, Size: max(f.Size, 0)}
		if v := strings.ToLower(strings.TrimSpace(f.SHA256)); isSHA256(v) {
			df.SHA256 = v
		}
		if v := strings.ToLower(strings.TrimSpace(f.SHA1)); isSHA1(v) {
			df.SHA1 = v
		}
		total += df.Size
		u.Files = append(u.Files, df)
	}
	if len(u.Files) == 0 {
		return store.DriverScanItem{}, false
	}
	if u.SizeBytes == 0 {
		u.SizeBytes = total
	}
	return store.DriverScanItem{Update: u, DeviceName: cleanText(d.Device, 200), FwDeviceID: cleanText(d.DeviceID, 128),
		CurrentVersion: cleanText(d.CurrentVersion, 100)}, true
}

// saveDriverScan stores the driver part of a patch scan.
func (s *Server) saveDriverScan(ctx context.Context, deviceID string, res agentproto.PatchScanResult) error {
	dev, err := s.st.DeviceByID(ctx, deviceID)
	if err != nil {
		return err
	}
	platform := dev.Platform
	scan := store.DriverScan{Platform: platform, Error: cleanText(res.DriverError, 1000)}
	seen := map[string]bool{}
	for _, d := range res.Drivers {
		if len(scan.Updates) >= maxDriversPerDevice {
			break
		}
		it, ok := s.cleanDriverUpdate(platform, d)
		if !ok || seen[it.Update.Source+"\x00"+it.Update.SourceID] {
			continue
		}
		seen[it.Update.Source+"\x00"+it.Update.SourceID] = true
		scan.Updates = append(scan.Updates, it)
	}
	return s.st.SaveDriverScan(ctx, deviceID, scan)
}

// ---- downloads into the repository ----

// driversTick starts the downloader when updates are waiting.
func (s *Server) driversTick(ctx context.Context) {
	s.drivers.mu.Lock()
	if s.drivers.running || s.bgCtx == nil {
		s.drivers.mu.Unlock()
		return
	}
	s.drivers.running = true
	s.drivers.mu.Unlock()
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer func() {
			s.drivers.mu.Lock()
			s.drivers.running = false
			s.drivers.mu.Unlock()
		}()
		for s.bgCtx.Err() == nil {
			u, err := s.st.NextDriverDownload(s.bgCtx)
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			if err != nil {
				s.log.Warn("driver repository", "err", err)
				return
			}
			s.downloadDriver(s.bgCtx, u)
		}
	}()
}

// downloadDriversSoon runs the downloader right away instead of on the
// next tick.
func (s *Server) downloadDriversSoon() { s.driversTick(context.Background()) }

func (s *Server) downloadDriver(ctx context.Context, u store.DriverUpdate) {
	ctx, cancel := context.WithTimeout(ctx, driverDownloadTime)
	defer cancel()
	files := slices.Clone(u.Files)
	var stored int64
	var err error
	for i := range files {
		var n int64
		files[i].Stored, n, err = s.fetchDriverFile(ctx, u, files[i], maxDriverUpdateBytes-stored)
		if err != nil {
			err = fmt.Errorf("%s: %w", files[i].Name, err)
			break
		}
		stored += n
	}
	if ctx.Err() != nil && s.bgCtx.Err() != nil {
		// The server stops; the download starts again after the restart.
		_ = s.st.FinishDriverDownload(context.Background(), u.ID, store.DriverQueued, u.Files, 0, "")
		return
	}
	if err != nil {
		state := store.DriverQueued
		if u.Attempts >= driverDownloadTries {
			state = store.DriverFailed
		}
		s.log.Warn("driver download failed", "id", u.ID, "title", u.Title, "attempt", u.Attempts, "err", err)
		_ = s.st.FinishDriverDownload(context.Background(), u.ID, state, u.Files, 0, cleanText(err.Error(), 1000))
		return
	}
	if err := s.st.FinishDriverDownload(context.Background(), u.ID, store.DriverReady, files, stored, ""); err != nil {
		s.log.Warn("driver repository", "err", err)
		return
	}
	s.log.Info("driver stored in the repository", "id", u.ID, "title", u.Title, "bytes", stored)
}

func (s *Server) driverClient() *http.Client {
	if s.drivers.client != nil {
		return s.drivers.client
	}
	return &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" {
			return errors.New("redirect to a non-https address")
		}
		return nil
	}}
}

// fetchDriverFile downloads one file into the repository and returns its
// SHA-256. A file that is already there is not fetched again.
func (s *Server) fetchDriverFile(ctx context.Context, u store.DriverUpdate, f store.DriverFile, limit int64) (string, int64, error) {
	dir := s.repositoryDir()
	if f.SHA256 != "" {
		if st, err := os.Stat(filepath.Join(dir, f.SHA256)); err == nil && st.Mode().IsRegular() {
			return f.SHA256, st.Size(), nil
		}
	}
	link, ok := s.driverURL(u.Source, f.URL)
	if !ok {
		return "", 0, errors.New("not a download address of " + u.Source)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, err
	}
	if f.Size > 0 {
		if f.Size > limit {
			return "", 0, errors.New("the update is too large")
		}
		if free, ok := freeBytes(dir); ok && free < uint64(f.Size)+driverFreeSpace {
			return "", 0, errors.New("not enough free disk space for the repository")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "FleetWarden/"+version.Version)
	res, err := s.driverClient().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	if !s.driverAllowHost(u.Source, res.Request.URL.Hostname()) {
		return "", 0, errors.New("redirected to an unknown host " + res.Request.URL.Hostname())
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	h256 := sha256.New()
	var h1 hash.Hash = sha1.New() //nolint:gosec
	n, err := io.Copy(io.MultiWriter(tmp, h256, h1), io.LimitReader(res.Body, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return "", 0, err
	case n > limit:
		return "", 0, errors.New("the update is too large")
	case f.Size > 0 && n != f.Size:
		return "", 0, fmt.Errorf("got %d bytes, expected %d", n, f.Size)
	}
	sum := hex.EncodeToString(h256.Sum(nil))
	if f.SHA256 != "" && f.SHA256 != sum {
		return "", 0, errors.New("SHA-256 does not match the vendor's checksum")
	}
	if f.SHA1 != "" && f.SHA1 != hex.EncodeToString(h1.Sum(nil)) {
		return "", 0, errors.New("SHA-1 does not match the vendor's checksum")
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, sum)); err != nil {
		return "", 0, err
	}
	return sum, n, nil
}

// checkRepository puts ready updates whose files are gone (a restore, the
// folder removed by hand) back in the download queue and removes files no
// update refers to any more. It runs every hour.
func (s *Server) checkRepository(ctx context.Context) {
	dir := s.repositoryDir()
	ready, err := s.st.ReadyDriverEntries(ctx)
	if err != nil {
		s.log.Warn("driver repository", "err", err)
		return
	}
	requeued := false
	for _, u := range ready {
		for _, f := range u.Files {
			if _, err := os.Stat(filepath.Join(dir, f.Stored)); f.Stored == "" || err != nil {
				_ = s.st.RequeueDriver(ctx, u.ID)
				requeued = true
				break
			}
		}
	}
	if requeued {
		s.downloadDriversSoon()
	}
	keep, err := s.st.StoredDriverFiles(ctx)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".download-") {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 2*driverDownloadTime {
				_ = os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		if isSHA256(name) && !keep[name] {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// ---- patch jobs ----

// deviceDriverJobs returns the repository updates a device gets in a patch
// job: with ids, those (manual install); otherwise what the policy allows.
func (s *Server) deviceDriverJobs(ctx context.Context, deviceID string, pol *store.PatchPolicy, ids []int64) ([]agentproto.DriverInstall, error) {
	rows, err := s.st.ReadyDrivers(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	var out []agentproto.DriverInstall
	for _, r := range rows {
		switch {
		case ids != nil:
			if !slices.Contains(ids, r.ID) {
				continue
			}
		case pol == nil:
			continue
		case r.Kind == agentproto.DriverKindFirmware && !pol.Firmware:
			continue
		case r.Kind == agentproto.DriverKindDriver && !pol.Drivers:
			continue
		}
		job := agentproto.DriverInstall{ID: agentproto.DriverResultID(r.ID), Source: r.Source, SourceID: r.SourceID, Kind: r.Kind,
			Title: r.Title, DeviceID: r.FwDeviceID}
		for _, f := range r.Files {
			job.Files = append(job.Files, agentproto.RepoFile{Name: f.Name, SHA256: f.Stored, Size: f.Size})
		}
		out = append(out, job)
	}
	return out, nil
}

// ---- agent file downloads ----

func (s *Server) handleRepositoryFile(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	id, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(agentproto.HeaderAuth))
	if err != nil {
		s.agentFails.Fail(ip)
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	dev, err := s.st.DeviceByID(r.Context(), id)
	if err != nil || !agentproto.VerifyRequest(dev.PublicKey, dev.ID, r.Method, r.URL.Path, ts, sig, s.now(), 5*time.Minute) {
		s.agentFails.Fail(ip)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	sum := strings.ToLower(r.PathValue("sha"))
	if !sha256Hex.MatchString(sum) {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	// Only a device with an open patch job for the update may fetch it.
	if ok, err := s.st.DeviceMayDownloadDriver(r.Context(), dev.ID, sum); err != nil || !ok {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	f, err := os.Open(filepath.Join(s.repositoryDir(), sum))
	if err != nil {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, sum, st.ModTime(), f)
}

// ---- API ----

func (s *Server) driverRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesManage, 0, h) }
	repo := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDriversManage, 0, h) }

	mux.HandleFunc("GET "+agentproto.PathRepository+"{sha}", s.handleRepositoryFile)
	mux.Handle("GET /api/v1/drivers/summary", view(s.handleDriverSummary))
	mux.Handle("GET /api/v1/drivers", view(s.handleListDrivers))
	mux.Handle("GET /api/v1/drivers/{id}", view(s.handleGetDriver))
	mux.Handle("POST /api/v1/drivers/state", repo(s.handleDriverState))
	mux.Handle("GET /api/v1/devices/{id}/drivers", view(s.handleDeviceDrivers))
	mux.Handle("POST /api/v1/devices/{id}/drivers/install", manage(s.handleDriverInstallNow))
}

type driverSummary struct {
	Stats       store.DriverStats `json:"stats"`
	FreeBytes   *uint64           `json:"freeBytes"`
	Downloading bool              `json:"downloading"`
}

func (s *Server) handleDriverSummary(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.DriverStats(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := driverSummary{Stats: st}
	if free, ok := freeBytes(s.cfg.DataDir); ok {
		out.FreeBytes = &free
	}
	s.drivers.mu.Lock()
	out.Downloading = s.drivers.running
	s.drivers.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListDrivers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dq := store.DriverQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")), Kind: q.Get("kind")}
	switch v := q.Get("state"); v {
	case "", "repository", store.DriverAvailable, store.DriverQueued, store.DriverDownloading, store.DriverReady, store.DriverFailed,
		store.DriverRejected:
		dq.State = v
	default:
		apiError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	if dq.Kind != "" && dq.Kind != agentproto.DriverKindDriver && dq.Kind != agentproto.DriverKindFirmware {
		apiError(w, http.StatusBadRequest, "invalid_kind")
		return
	}
	// The repository shows every stored update; the other lists only what
	// a device in the user's scope can install.
	dq.Offered = dq.State != "repository" && dq.State != store.DriverReady && dq.State != store.DriverQueued &&
		dq.State != store.DriverDownloading
	list, err := s.st.ListDrivers(r.Context(), dq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type driverDetail struct {
	store.DriverUpdate
	DeviceList []store.DriverDevice `json:"deviceList"`
}

func (s *Server) handleGetDriver(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	u, err := s.st.DriverByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	devs, err := s.st.DriverDevices(r.Context(), id, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range devs {
		devs[i].Online = s.hub.online(devs[i].DeviceID)
	}
	u.Devices = len(devs)
	writeJSON(w, http.StatusOK, driverDetail{DriverUpdate: u, DeviceList: devs})
}

type driverStateReq struct {
	IDs []int64 `json:"ids"`
	// Action: add (to the repository; also retries a failed download),
	// reject, or remove (from the repository, or undo a rejection).
	Action string `json:"action"`
}

func (s *Server) handleDriverState(w http.ResponseWriter, r *http.Request) {
	var req driverStateReq
	if !decode(w, r, &req) {
		return
	}
	state := map[string]string{"add": store.DriverQueued, "reject": store.DriverRejected, "remove": store.DriverAvailable}[req.Action]
	if state == "" {
		apiError(w, http.StatusBadRequest, "invalid_driver_action")
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > 500 {
		apiError(w, http.StatusBadRequest, "invalid_ids")
		return
	}
	u, _ := currentUser(r)
	changed, err := s.st.SetDriverState(r.Context(), req.IDs, state, u.Username)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, d := range changed {
		s.audit(r, "driver."+req.Action, d.Title, true, map[string]any{"id": d.ID, "source": d.Source, "version": d.Version})
	}
	if state == store.DriverQueued && len(changed) > 0 {
		s.downloadDriversSoon()
	}
	if state == store.DriverAvailable && len(changed) > 0 {
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.checkRepository(s.bgCtx)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": len(changed)})
}

type deviceDriversView struct {
	ScanAt    *time.Time              `json:"scanAt"`
	ScanError string                  `json:"scanError"`
	Policy    *policyView             `json:"policy"`
	Drivers   []store.DeviceDriverRow `json:"drivers"`
}

func (s *Server) handleDeviceDrivers(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	rows, err := s.st.DeviceDrivers(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := deviceDriversView{Drivers: rows}
	out.ScanAt, out.ScanError, _ = s.st.DriverScanInfo(r.Context(), d.ID)
	if g, err := s.st.GroupByID(r.Context(), d.GroupID); err == nil && g.PatchPolicy != nil {
		if p, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err == nil {
			v := s.policyView(p)
			out.Policy = &v
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type driverInstallReq struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) handleDriverInstallNow(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req driverInstallReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		apiError(w, http.StatusBadRequest, "nothing_to_install")
		return
	}
	if !d.Online {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	var pol *store.PatchPolicy
	if g, err := s.st.GroupByID(r.Context(), d.GroupID); err == nil && g.PatchPolicy != nil {
		if p, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err == nil {
			pol = &p
		}
	}
	jobs, err := s.deviceDriverJobs(r.Context(), d.ID, pol, req.IDs)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(jobs) != len(slices.Compact(slices.Sorted(slices.Values(req.IDs)))) {
		apiError(w, http.StatusBadRequest, "driver_not_ready")
		return
	}
	u, _ := currentUser(r)
	run, err := s.startPatchRun(r.Context(), d.ID, pol, nil, jobs, "manual", u.Username)
	switch {
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "run_open")
		return
	case errors.Is(err, errDeviceOffline):
		apiError(w, http.StatusConflict, "device_offline")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "driver.install", d.Hostname, true, map[string]any{"run": run.ID, "drivers": len(jobs)})
	writeJSON(w, http.StatusAccepted, run)
}
