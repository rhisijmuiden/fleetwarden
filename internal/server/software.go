package server

// Software deployment. The library holds packages from the winget catalog,
// uploaded installers and apt packages. A deployment installs a package, or
// uninstalls a program from the inventory, on devices and groups. Every
// device gets one job; the scheduler sends a device its next job when it is
// online and has no job running, so offline devices catch up when they come
// back (until the deployment's expiry).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	softwareJobsPerTick = 200
	softwareJobSlack    = 30 * time.Minute // on top of the package timeout before a silent job fails
	maxUploadSize       = 2 << 30
	maxJobOutput        = 8000
)

func (s *Server) softwareDir() string { return filepath.Join(s.cfg.DataDir, "software") }

// ---- scheduler ----

// softwareTick refreshes the catalog when due, adds jobs for devices that
// joined groups, expires old jobs and sends due jobs to online devices.
func (s *Server) softwareTick(ctx context.Context) {
	if s.winget.Due() {
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			rctx, cancel := context.WithTimeout(s.bgCtx, 5*time.Minute)
			defer cancel()
			if err := s.winget.Refresh(rctx); err != nil {
				s.log.Warn("winget catalog", "err", err)
			}
		}()
	}
	if _, err := s.st.ExpandDeployments(ctx); err != nil {
		s.log.Warn("software deployments", "err", err)
	}
	if err := s.st.ExpireSoftwareJobs(ctx, softwareJobSlack); err != nil {
		s.log.Warn("software jobs", "err", err)
	}
	s.sendSoftwareJobs(ctx, s.hub.onlineIDs())
}

// sendSoftwareJobs sends the next job to each listed device that is idle.
func (s *Server) sendSoftwareJobs(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	due, err := s.st.DueSoftwareJobs(ctx, ids, softwareJobsPerTick)
	if err != nil {
		s.log.Warn("software jobs", "err", err)
		return
	}
	for _, d := range due {
		job, err := s.buildSoftwareJob(ctx, d)
		if err != nil {
			_ = s.st.FinishSoftwareJob(ctx, d.JobID, agentproto.SoftwareFailed, err.Error())
			continue
		}
		ok, err := s.st.MarkSoftwareJobSent(ctx, d.JobID)
		if err != nil || !ok {
			continue
		}
		if !s.hub.send(d.DeviceID, agentproto.TypeSoftwareJob, job) {
			_ = s.st.ResetSoftwareJob(ctx, d.JobID)
			continue
		}
		s.log.Info("software job sent", "device", d.DeviceID, "job", d.JobID, "action", job.Action, "name", job.Name)
	}
}

// buildSoftwareJob turns a due job into what the agent runs.
func (s *Server) buildSoftwareJob(ctx context.Context, d store.DueJob) (agentproto.SoftwareJob, error) {
	dep := d.Deployment
	job := agentproto.SoftwareJob{JobID: d.JobID, Action: dep.Action, Name: dep.Name, TimeoutMinutes: 60}
	if dep.Action == agentproto.ActionUninstall {
		job.Name = dep.UninstallName
		job.Uninstall = &agentproto.Uninstall{Name: dep.UninstallName, Version: dep.UninstallVersion, Args: dep.UninstallArgs, Purge: dep.UninstallPurge}
		return job, nil
	}
	if dep.Action == agentproto.ActionBrowsers {
		return s.buildBrowserJob(ctx, d, job)
	}
	p := d.Package
	if p == nil {
		return job, errors.New("the package was deleted")
	}
	job.Name, job.TimeoutMinutes = p.Name, p.TimeoutMinutes
	switch p.Source {
	case store.SourceWinget:
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		m, err := s.winget.Manifest(rctx, p.WingetID, p.Version)
		if err != nil {
			return job, fmt.Errorf("winget catalog: %w", err)
		}
		c, err := m.Resolve(d.Arch)
		if err != nil {
			return job, err
		}
		in := c.Installer
		if p.InstallArgs != "" {
			in.Args = p.InstallArgs
		}
		in.AnyHash = p.AnyHash
		det := c.Detect
		if p.DetectName != "" {
			det.Name = p.DetectName
		}
		if p.ProductCode != "" {
			det.ProductCode = p.ProductCode
		}
		job.Installer, job.Detect = &in, &det
	case store.SourceUpload:
		job.Installer = &agentproto.Installer{Kind: p.InstallerKind, File: p.FileSHA256, SHA256: p.FileSHA256, Size: p.FileSize,
			FileName: p.FileName, Args: p.InstallArgs}
		if p.DetectName != "" || p.ProductCode != "" {
			job.Detect = &agentproto.Detect{Name: p.DetectName, ProductCode: p.ProductCode, Version: p.Version}
		}
	case store.SourceApt:
		job.Installer = &agentproto.Installer{Kind: agentproto.KindApt, Package: p.AptPackage, Args: p.InstallArgs}
		job.Detect = &agentproto.Detect{Name: p.AptPackage, Version: p.Version}
	default:
		return job, fmt.Errorf("unknown package source %q", p.Source)
	}
	return job, nil
}

func (s *Server) softwareOnConnect(ctx context.Context, deviceID string) {
	s.sendSoftwareJobs(ctx, []string{deviceID})
}

func (s *Server) handleSoftwareStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.SoftwareStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	switch st.State {
	case agentproto.SoftwareDownloading, agentproto.SoftwareInstalling, agentproto.SoftwareUninstalling, agentproto.SoftwareWaiting,
		agentproto.SoftwareCompleted, agentproto.SoftwareSkipped, agentproto.SoftwareFailed:
	default:
		return errors.New("unknown software job state " + strconv.Quote(st.State))
	}
	u := store.SoftwareJobUpdate{State: st.State, Message: cleanText(st.Message, 1000), ExitCode: st.ExitCode,
		Output: cleanOutput(st.Output, maxJobOutput), RebootRequired: st.RebootRequired, Version: cleanText(st.Version, 100),
		Finished: agentproto.SoftwareFinished(st.State)}
	err := s.st.UpdateSoftwareJob(ctx, st.JobID, c.deviceID, u)
	if errors.Is(err, store.ErrNotFound) {
		return nil // a late report for a closed job
	}
	if err == nil && u.Finished {
		s.log.Info("software job finished", "device", c.deviceID, "job", st.JobID, "state", st.State, "message", u.Message)
		go s.sendSoftwareJobs(context.WithoutCancel(ctx), []string{c.deviceID})
	}
	return err
}

// cleanOutput keeps the end of installer output: printable text and line breaks.
func cleanOutput(v string, max int) string {
	v = strings.ToValidUTF8(v, "")
	v = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if len(v) > max {
		v = v[len(v)-max:]
		for len(v) > 0 && v[0]&0xc0 == 0x80 {
			v = v[1:]
		}
	}
	return v
}

// ---- uploaded installers ----

func (s *Server) handleSoftwareFile(w http.ResponseWriter, r *http.Request) {
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
	// Only a device with an open job for the file may fetch it.
	if ok, err := s.st.DeviceMayDownload(r.Context(), dev.ID, sum); err != nil || !ok {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	f, err := os.Open(filepath.Join(s.softwareDir(), sum))
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

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

type uploadResult struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

// handleSoftwareUpload stores an installer sent as the raw request body
// (file name in the X-File-Name header) under its SHA-256.
func (s *Server) handleSoftwareUpload(w http.ResponseWriter, r *http.Request) {
	name := safeFileName(r.Header.Get("X-File-Name"))
	kind := kindFromName(name)
	if name == "" || kind == "" {
		apiError(w, http.StatusBadRequest, "unsupported_file")
		return
	}
	if r.ContentLength > maxUploadSize {
		apiError(w, http.StatusRequestEntityTooLarge, "installer_too_large")
		return
	}
	tmpDir := filepath.Join(s.cfg.DataDir, "tmp")
	if err := os.MkdirAll(s.softwareDir(), 0o750); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		s.internalError(w, r, err)
		return
	}
	tmp, err := os.CreateTemp(tmpDir, "upload-*")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), http.MaxBytesReader(w, r.Body, maxUploadSize))
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
		apiError(w, http.StatusBadRequest, "unsupported_file")
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmp.Name(), filepath.Join(s.softwareDir(), sum)); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.upload", name, true, map[string]any{"sha256": sum, "size": n})
	writeJSON(w, http.StatusCreated, uploadResult{SHA256: sum, Size: n, Name: name, Kind: kind})
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9 ._()+-]`)

func safeFileName(v string) string {
	v = filepath.Base(strings.ReplaceAll(cleanText(v, 200), `\`, "/"))
	v = strings.TrimLeft(unsafeFileChars.ReplaceAllString(v, "_"), ".-_ ")
	if len(v) > 150 {
		v = v[len(v)-150:]
	}
	return strings.TrimLeft(v, ".-_ ")
}

func kindFromName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".msi":
		return agentproto.KindMSI
	case ".exe":
		return agentproto.KindEXE
	case ".deb":
		return agentproto.KindDeb
	}
	return ""
}

// pruneSoftwareFiles removes uploads that no package uses (after a day, so
// an upload whose package is still being filled in survives).
func (s *Server) pruneSoftwareFiles(ctx context.Context) {
	used, err := s.st.SoftwareFiles(ctx)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(s.softwareDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || used[e.Name()] || !sha256Hex.MatchString(e.Name()) || s.now().Sub(info.ModTime()) < 24*time.Hour {
			continue
		}
		_ = os.Remove(filepath.Join(s.softwareDir(), e.Name()))
	}
}

// ---- API ----

func (s *Server) softwareRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSoftwareManage, 0, h) }
	deploy := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSoftwareDeploy, 0, h) }

	mux.HandleFunc("GET "+agentproto.PathSoftware+"{sha}", s.handleSoftwareFile)

	mux.Handle("GET /api/v1/software/packages", view(s.handleListPackages))
	mux.Handle("POST /api/v1/software/packages", manage(s.handleCreatePackage))
	mux.Handle("PUT /api/v1/software/packages/{id}", manage(s.handleUpdatePackage))
	mux.Handle("DELETE /api/v1/software/packages/{id}", manage(s.handleDeletePackage))
	mux.Handle("POST /api/v1/software/uploads", manage(s.handleSoftwareUpload))
	mux.Handle("GET /api/v1/software/catalog", manage(s.handleCatalogSearch))
	mux.Handle("GET /api/v1/software/catalog/package", manage(s.handleCatalogPackage))
	mux.Handle("POST /api/v1/software/catalog/refresh", manage(s.handleCatalogRefresh))
	mux.Handle("GET /api/v1/software/installed", view(s.handleInstalledSoftware))
	mux.Handle("GET /api/v1/software/installed/devices", view(s.handleInstalledDevices))
	mux.Handle("GET /api/v1/software/browsers", view(s.handleBrowserReport))

	mux.Handle("GET /api/v1/deployments", view(s.handleListDeployments))
	mux.Handle("POST /api/v1/deployments", deploy(s.handleCreateDeployment))
	mux.Handle("GET /api/v1/deployments/{id}", view(s.handleGetDeployment))
	mux.Handle("POST /api/v1/deployments/{id}/cancel", deploy(s.handleCancelDeployment))
	mux.Handle("POST /api/v1/deployments/{id}/retry", deploy(s.handleRetryDeployment))
	mux.Handle("GET /api/v1/devices/{id}/software-jobs", view(s.handleDeviceSoftwareJobs))
}

type packagesView struct {
	Packages     []store.SoftwarePackage `json:"packages"`
	Kinds        map[string][]string     `json:"kinds"`
	DefaultArgs  map[string]string       `json:"defaultArgs"`
	Catalog      catalog.Status          `json:"catalog"`
	MaxUploadMiB int                     `json:"maxUploadMiB"`
}

func (s *Server) handleListPackages(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListSoftwarePackages(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, packagesView{Packages: list, Kinds: agentproto.InstallerKinds, DefaultArgs: agentproto.DefaultSilentArgs,
		Catalog: s.winget.Status(), MaxUploadMiB: maxUploadSize >> 20})
}

var (
	wingetIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	aptNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`)
	guidRE     = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)
)

// validPackage checks and normalises a package; it returns an error code.
func (s *Server) validPackage(ctx context.Context, p *store.SoftwarePackage) string {
	p.Name = cleanText(p.Name, 200)
	p.Publisher = cleanText(p.Publisher, 200)
	p.Description = cleanText(p.Description, 1000)
	p.Version = cleanText(p.Version, 100)
	p.InstallArgs = cleanText(p.InstallArgs, 2000)
	p.DetectName = cleanText(p.DetectName, 300)
	p.ProductCode = strings.ToUpper(cleanText(p.ProductCode, 40))
	if p.TimeoutMinutes == 0 {
		p.TimeoutMinutes = 60
	}
	switch {
	case p.Name == "":
		return "invalid_name"
	case p.TimeoutMinutes < 5 || p.TimeoutMinutes > 600:
		return "invalid_timeout"
	case p.ProductCode != "" && !guidRE.MatchString(p.ProductCode):
		return "invalid_product_code"
	}
	switch p.Source {
	case store.SourceWinget:
		p.Platform = "windows"
		p.WingetID = cleanText(p.WingetID, 128)
		p.FileSHA256, p.FileName, p.FileSize, p.AptPackage, p.InstallerKind = "", "", 0, "", ""
		if !wingetIDRE.MatchString(p.WingetID) {
			return "invalid_winget_id"
		}
		if _, err := s.winget.Get(ctx, p.WingetID); err != nil {
			if errors.Is(err, catalog.ErrNotLoaded) {
				return "catalog_not_loaded"
			}
			return "unknown_winget_id"
		}
	case store.SourceUpload:
		p.FileSHA256 = strings.ToLower(p.FileSHA256)
		p.FileName = safeFileName(p.FileName)
		p.WingetID, p.AptPackage, p.AnyHash = "", "", false
		if !sha256Hex.MatchString(p.FileSHA256) {
			return "invalid_file"
		}
		st, err := os.Stat(filepath.Join(s.softwareDir(), p.FileSHA256))
		if err != nil {
			return "invalid_file"
		}
		p.FileSize = st.Size()
		if p.FileName == "" {
			p.FileName = "setup.exe"
		}
		if p.Platform == "" {
			p.Platform = "windows"
			if p.InstallerKind == agentproto.KindDeb {
				p.Platform = "linux"
			}
		}
		if !agentproto.ValidKind(p.Platform, p.InstallerKind) || p.InstallerKind == agentproto.KindApt {
			return "invalid_installer_kind"
		}
		if p.InstallerKind == agentproto.KindEXE && p.InstallArgs == "" {
			return "silent_args_required"
		}
	case store.SourceApt:
		p.Platform = "linux"
		p.AptPackage = strings.ToLower(cleanText(p.AptPackage, 128))
		p.WingetID, p.FileSHA256, p.FileName, p.FileSize, p.InstallerKind, p.AnyHash, p.ProductCode = "", "", "", 0, agentproto.KindApt, false, ""
		if !aptNameRE.MatchString(p.AptPackage) || p.AptPackage == "fleetwarden-agent" || p.AptPackage == "fleetwarden-server" {
			return "invalid_apt_package"
		}
		if p.DetectName == "" {
			p.DetectName = p.AptPackage
		}
	default:
		return "invalid_source"
	}
	return ""
}

func (s *Server) handleCreatePackage(w http.ResponseWriter, r *http.Request) {
	var p store.SoftwarePackage
	if !decode(w, r, &p) {
		return
	}
	if bad := s.validPackage(r.Context(), &p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	u, _ := currentUser(r)
	p.CreatedBy = u.Username
	p, err := s.st.CreateSoftwarePackage(r.Context(), p)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "package_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.package.create", p.Name, true, map[string]any{"source": p.Source, "platform": p.Platform})
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdatePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var p store.SoftwarePackage
	if !decode(w, r, &p) {
		return
	}
	if bad := s.validPackage(r.Context(), &p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	p.ID = id
	err := s.st.UpdateSoftwarePackage(r.Context(), p)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "package_exists")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.package.update", p.Name, true, nil)
	p, _ = s.st.SoftwarePackageByID(r.Context(), id)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeletePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := s.st.SoftwarePackageByID(r.Context(), id)
	if err == nil {
		err = s.st.DeleteSoftwarePackage(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.package.delete", p.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

type catalogSearch struct {
	Status  catalog.Status    `json:"status"`
	Results []catalog.Package `json:"results"`
}

func (s *Server) handleCatalogSearch(w http.ResponseWriter, r *http.Request) {
	out := catalogSearch{Status: s.winget.Status(), Results: []catalog.Package{}}
	res, err := s.winget.Search(r.Context(), r.URL.Query().Get("q"), 50)
	switch {
	case errors.Is(err, catalog.ErrNotLoaded):
	case err != nil:
		s.internalError(w, r, err)
		return
	default:
		out.Results = res
	}
	writeJSON(w, http.StatusOK, out)
}

type catalogPackageView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Publisher   string           `json:"publisher"`
	Description string           `json:"description"`
	License     string           `json:"license"`
	Homepage    string           `json:"homepage"`
	Version     string           `json:"version"`
	Versions    []string         `json:"versions"`
	Choice      *catalog.Choice  `json:"choice"`
	ChoiceError string           `json:"choiceError"`
	Installers  []catalogVariant `json:"installers"`
}

type catalogVariant struct {
	Architecture string `json:"architecture"`
	Type         string `json:"type"`
	Scope        string `json:"scope"`
	Locale       string `json:"locale"`
}

func (s *Server) handleCatalogPackage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, version := cleanText(q.Get("id"), 128), cleanText(q.Get("version"), 100)
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	vs, err := s.winget.Versions(ctx, id)
	if err == nil && len(vs) == 0 {
		err = catalog.ErrNotFound
	}
	var m *catalog.Manifest
	if err == nil {
		m, err = s.winget.Manifest(ctx, id, version)
	}
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, catalog.ErrNotLoaded):
		apiError(w, http.StatusConflict, "catalog_not_loaded")
		return
	case err != nil:
		apiErrorDetail(w, http.StatusBadGateway, "catalog_unreachable", err.Error())
		return
	}
	out := catalogPackageView{ID: m.PackageIdentifier, Name: m.PackageName, Publisher: m.Publisher, Description: m.ShortDescription,
		License: m.License, Homepage: m.PackageURL, Version: m.PackageVersion, Versions: []string{}, Installers: []catalogVariant{}}
	for i, v := range vs {
		if i < 50 {
			out.Versions = append(out.Versions, v.Version)
		}
	}
	for _, in := range m.Installers {
		t, sc, loc := in.InstallerType, in.Scope, in.InstallerLocale
		out.Installers = append(out.Installers, catalogVariant{Architecture: in.Architecture, Type: t, Scope: sc, Locale: loc})
	}
	if c, err := m.Resolve("amd64"); err == nil {
		out.Choice = &c
	} else {
		out.ChoiceError = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := s.winget.Refresh(ctx); err != nil {
		apiErrorDetail(w, http.StatusBadGateway, "catalog_unreachable", err.Error())
		return
	}
	s.audit(r, "software.catalog.refresh", "winget", true, nil)
	writeJSON(w, http.StatusOK, s.winget.Status())
}

func (s *Server) handleInstalledSoftware(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	plat := q.Get("platform")
	if plat != "" && !validPlatform(plat) {
		plat = ""
	}
	list, err := s.st.ListInstalledSoftware(r.Context(), store.InstalledQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")), Platform: plat})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleInstalledDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	plat := q.Get("platform")
	if plat != "" && !validPlatform(plat) {
		plat = ""
	}
	list, err := s.st.DevicesWithSoftware(r.Context(), s.scope(r), q.Get("name"), q.Get("version"), plat)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pkg, _ := strconv.ParseInt(q.Get("package"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.st.ListDeployments(r.Context(), store.DeploymentQuery{Scope: s.scope(r), PackageID: pkg, Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type deploymentReq struct {
	Name       string   `json:"name"`
	Action     string   `json:"action"`
	PackageID  int64    `json:"packageId"`
	DeviceIDs  []string `json:"deviceIds"`
	GroupIDs   []int64  `json:"groupIds"`
	Ongoing    bool     `json:"ongoing"`
	StartAt    string   `json:"startAt"` // RFC 3339; empty: now
	ExpireDays int      `json:"expireDays"`
	Browsers   []string `json:"browsers"`
	Uninstall  struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Platform string `json:"platform"`
		Args     string `json:"args"`
		Purge    bool   `json:"purge"`
	} `json:"uninstall"`
}

type deploymentCreated struct {
	store.Deployment
	Jobs int `json:"jobs"`
}

func (s *Server) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req deploymentReq
	if !decode(w, r, &req) {
		return
	}
	sc := s.scope(r)
	u, _ := currentUser(r)
	d := store.NewDeployment{Deployment: store.Deployment{Action: req.Action, Ongoing: req.Ongoing, ExpireDays: req.ExpireDays,
		RequestedBy: u.Username, StartAt: s.now()}}
	if d.ExpireDays == 0 {
		d.ExpireDays = 7
	}
	if d.ExpireDays < 1 || d.ExpireDays > 90 {
		apiError(w, http.StatusBadRequest, "invalid_expiry")
		return
	}
	if req.StartAt != "" {
		t, err := time.Parse(time.RFC3339, req.StartAt)
		if err != nil || t.After(s.now().AddDate(1, 0, 0)) {
			apiError(w, http.StatusBadRequest, "invalid_start")
			return
		}
		if t.After(s.now()) {
			d.StartAt = t
		}
	}
	switch req.Action {
	case agentproto.ActionInstall:
		p, err := s.st.SoftwarePackageByID(r.Context(), req.PackageID)
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusBadRequest, "unknown_package")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		d.PackageID, d.Platform, d.Name = &p.ID, p.Platform, p.Name
	case agentproto.ActionUninstall:
		un := req.Uninstall
		d.UninstallName = cleanText(un.Name, 300)
		d.UninstallVersion = cleanText(un.Version, 100)
		d.UninstallArgs = cleanText(un.Args, 2000)
		d.UninstallPurge = un.Purge
		d.Platform = un.Platform
		if d.UninstallName == "" || !validPlatform(d.Platform) {
			apiError(w, http.StatusBadRequest, "bad_request")
			return
		}
		if d.Platform == "linux" && (!aptNameRE.MatchString(d.UninstallName) || d.UninstallName == "fleetwarden-agent") {
			apiError(w, http.StatusBadRequest, "invalid_apt_package")
			return
		}
		if strings.HasPrefix(strings.ToLower(d.UninstallName), "fleetwarden agent") {
			apiError(w, http.StatusBadRequest, "cannot_uninstall_agent")
			return
		}
		d.Name = d.UninstallName
	case agentproto.ActionBrowsers:
		d.Browsers = validBrowsers(req.Browsers)
		if len(d.Browsers) == 0 {
			apiError(w, http.StatusBadRequest, "no_browsers")
			return
		}
		if s.winget.Status().Packages == 0 {
			apiError(w, http.StatusBadRequest, "catalog_not_loaded")
			return
		}
		d.Platform = "windows"
		var names []string
		for _, k := range d.Browsers {
			def, _ := browserDefByKey(k)
			names = append(names, def.Name)
		}
		d.Name = "Browser updates: " + strings.Join(names, ", ")
	default:
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if name := cleanText(req.Name, 200); name != "" {
		d.Name = name
	}
	// Every target must lie within the user's scope.
	for _, g := range req.GroupIDs {
		if !sc.Allows(g) {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		if _, err := s.st.GroupByID(r.Context(), g); err != nil {
			apiError(w, http.StatusBadRequest, "unknown_group")
			return
		}
		if !slices.Contains(d.GroupIDs, g) {
			d.GroupIDs = append(d.GroupIDs, g)
		}
	}
	if len(req.DeviceIDs) > 5000 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if len(req.DeviceIDs) > 0 {
		found, err := s.st.DevicesInScope(r.Context(), sc, req.DeviceIDs)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, id := range req.DeviceIDs {
			if _, ok := found[id]; !ok {
				apiError(w, http.StatusBadRequest, "unknown_device")
				return
			}
		}
		d.DeviceIDs = req.DeviceIDs
	}
	if len(d.DeviceIDs) == 0 && len(d.GroupIDs) == 0 {
		apiError(w, http.StatusBadRequest, "no_targets")
		return
	}
	if d.Ongoing && len(d.GroupIDs) == 0 {
		d.Ongoing = false
	}
	id, jobs, err := s.st.CreateDeployment(r.Context(), d)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.deploy", d.Name, true, map[string]any{"deployment": id, "action": d.Action, "jobs": jobs,
		"groups": d.GroupIDs, "devices": len(d.DeviceIDs), "ongoing": d.Ongoing})
	if !d.StartAt.After(s.now()) {
		go s.sendSoftwareJobs(context.WithoutCancel(r.Context()), s.hub.onlineIDs())
	}
	dep, err := s.st.DeploymentByID(r.Context(), id, sc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, deploymentCreated{Deployment: dep, Jobs: jobs})
}

type deploymentView struct {
	Deployment store.Deployment       `json:"deployment"`
	Package    *store.SoftwarePackage `json:"package"`
	Jobs       []store.SoftwareJob    `json:"jobs"`
}

// deploymentInScope loads a deployment the user may see.
func (s *Server) deploymentInScope(w http.ResponseWriter, r *http.Request) (store.Deployment, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.Deployment{}, false
	}
	sc := s.scope(r)
	d, err := s.st.DeploymentByID(r.Context(), id, sc)
	if err == nil && !sc.All && d.Counts.Total == 0 {
		inGroups := false
		for _, g := range d.GroupIDs {
			inGroups = inGroups || sc.Allows(g)
		}
		if !inGroups {
			err = store.ErrNotFound
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return d, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return d, false
	}
	return d, true
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentInScope(w, r)
	if !ok {
		return
	}
	out := deploymentView{Deployment: d}
	if d.PackageID != nil {
		if p, err := s.st.SoftwarePackageByID(r.Context(), *d.PackageID); err == nil {
			out.Package = &p
		}
	}
	jobs, err := s.st.ListSoftwareJobs(r.Context(), store.JobQuery{Scope: s.scope(r), DeploymentID: d.ID, Limit: 5000})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out.Jobs = jobs
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentInScope(w, r)
	if !ok {
		return
	}
	u, _ := currentUser(r)
	if err := s.st.CancelDeployment(r.Context(), d.ID, u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.deploy.cancel", d.Name, true, map[string]any{"deployment": d.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRetryDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentInScope(w, r)
	if !ok {
		return
	}
	if d.CancelledAt != nil {
		apiError(w, http.StatusConflict, "deployment_cancelled")
		return
	}
	n, err := s.st.RetrySoftwareJobs(r.Context(), d.ID, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "software.deploy.retry", d.Name, true, map[string]any{"deployment": d.ID, "jobs": n})
	go s.sendSoftwareJobs(context.WithoutCancel(r.Context()), s.hub.onlineIDs())
	writeJSON(w, http.StatusOK, map[string]int{"jobs": n})
}

func (s *Server) handleDeviceSoftwareJobs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	jobs, err := s.st.ListSoftwareJobs(r.Context(), store.JobQuery{Scope: store.Scope{All: true}, DeviceID: d.ID, Limit: 50})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}
