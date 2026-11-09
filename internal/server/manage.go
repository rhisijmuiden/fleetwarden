package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Management without taking over the screen: the server asks the agent over
// the control channel and waits for its answer (see agentproto/manage.go).
// Nothing is stored except the audit log and the files of running transfers,
// which live in DataDir/transfers for an hour.

const (
	manageListWait    = 45 * time.Second
	manageActionWait  = 150 * time.Second // the agent gives up after 2 minutes
	transferKeep      = time.Hour         // a finished transfer stays downloadable
	transferIdle      = 10 * time.Minute  // no chunk from the agent for this long: failed
	maxTransfersPerOp = 3                 // running transfers per operator
	maxUploadDirLen   = 1024
)

// Transfer states.
const (
	transferWaiting = "waiting" // for the agent
	transferRunning = "running"
	transferReady   = "ready"
	transferFailed  = "failed"
)

// transfer is a file on its way between a device and the server.
type transfer struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"deviceId"`
	Direction string    `json:"direction"` // download (device -> server) or upload
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Message   string    `json:"message,omitempty"`
	Size      int64     `json:"size"`
	Done      int64     `json:"done"`
	Zip       bool      `json:"zip,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	userID int64
	file   string
	sha    string    // of an upload
	hash   hash.Hash // of the chunks of a download so far
	seen   time.Time
}

type manageHub struct {
	mu        sync.Mutex
	pending   map[string]pendingManage
	transfers map[string]*transfer
}

type pendingManage struct {
	deviceID string
	ch       chan agentproto.ManageResult
}

func (s *Server) transfersDir() string { return filepath.Join(s.cfg.DataDir, "transfers") }

func (s *Server) manageRoutes(mux *http.ServeMux) {
	m := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermRemoteManage, 0, h) }
	mux.Handle("GET /api/v1/devices/{id}/manage/processes", m(s.handleManageProcesses))
	mux.Handle("POST /api/v1/devices/{id}/manage/processes/{pid}/kill", m(s.handleManageKill))
	mux.Handle("GET /api/v1/devices/{id}/manage/services", m(s.handleManageServices))
	mux.Handle("POST /api/v1/devices/{id}/manage/services/action", m(s.handleManageServiceAction))
	mux.Handle("GET /api/v1/devices/{id}/manage/logs", m(s.handleManageLogSources))
	mux.Handle("GET /api/v1/devices/{id}/manage/logs/events", m(s.handleManageLogEvents))
	mux.Handle("GET /api/v1/devices/{id}/manage/files", m(s.handleManageFiles))
	mux.Handle("POST /api/v1/devices/{id}/manage/files/mkdir", m(s.handleManageMkdir))
	mux.Handle("POST /api/v1/devices/{id}/manage/files/delete", m(s.handleManageDelete))
	mux.Handle("POST /api/v1/devices/{id}/manage/files/rename", m(s.handleManageRename))
	mux.Handle("POST /api/v1/devices/{id}/manage/files/download", m(s.handleManageDownload))
	mux.Handle("POST /api/v1/devices/{id}/manage/files/upload", m(s.handleManageUpload))
	mux.Handle("GET /api/v1/transfers", m(s.handleListTransfers))
	mux.Handle("GET /api/v1/transfers/{tid}", m(s.handleGetTransfer))
	mux.Handle("GET /api/v1/transfers/{tid}/file", m(s.handleTransferFile))
	mux.Handle("DELETE /api/v1/transfers/{tid}", m(s.handleDeleteTransfer))
	mux.HandleFunc("GET "+agentproto.PathTransfer+"{tid}", s.handleAgentTransferGet)
	mux.HandleFunc("POST "+agentproto.PathTransfer+"{tid}", s.handleAgentTransferPost)
}

// ---- request / answer ----

var errAgentOffline = errors.New("device offline")
var errAgentTimeout = errors.New("no answer from the agent")

// askAgent sends a request and waits for the answer.
func (s *Server) askAgent(ctx context.Context, deviceID string, req agentproto.ManageRequest, wait time.Duration) (agentproto.ManageResult, error) {
	id := newUUID()
	ch := make(chan agentproto.ManageResult, 1)
	s.manage.mu.Lock()
	if s.manage.pending == nil {
		s.manage.pending = map[string]pendingManage{}
	}
	s.manage.pending[id] = pendingManage{deviceID: deviceID, ch: ch}
	s.manage.mu.Unlock()
	defer func() {
		s.manage.mu.Lock()
		delete(s.manage.pending, id)
		s.manage.mu.Unlock()
	}()
	if !s.hub.sendID(deviceID, agentproto.TypeManageRequest, id, req) {
		return agentproto.ManageResult{}, errAgentOffline
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-t.C:
		return agentproto.ManageResult{}, errAgentTimeout
	case <-ctx.Done():
		return agentproto.ManageResult{}, ctx.Err()
	}
}

func (s *Server) handleManageResultMsg(c *agentConn, e *agentproto.Envelope) error {
	var r agentproto.ManageResult
	if err := e.Decode(&r); err != nil {
		return err
	}
	s.manage.mu.Lock()
	p, ok := s.manage.pending[e.ID]
	s.manage.mu.Unlock()
	if !ok || p.deviceID != c.deviceID {
		return nil // too late, or not this device's request
	}
	select {
	case p.ch <- r:
	default:
	}
	return nil
}

// manageCall runs a request for the API and writes errors. It reports
// whether the result can be used.
func (s *Server) manageCall(w http.ResponseWriter, r *http.Request, dev store.Device, req agentproto.ManageRequest, wait time.Duration) (agentproto.ManageResult, bool) {
	if !s.hub.online(dev.ID) {
		apiError(w, http.StatusConflict, "device_offline")
		return agentproto.ManageResult{}, false
	}
	res, err := s.askAgent(r.Context(), dev.ID, req, wait)
	switch {
	case errors.Is(err, errAgentOffline):
		apiError(w, http.StatusConflict, "device_offline")
		return res, false
	case errors.Is(err, errAgentTimeout):
		apiError(w, http.StatusGatewayTimeout, "agent_timeout")
		return res, false
	case err != nil:
		apiError(w, http.StatusServiceUnavailable, "cancelled")
		return res, false
	case res.Error != "":
		code := http.StatusUnprocessableEntity
		switch res.Error {
		case agentproto.ManageErrNotFound:
			code = http.StatusNotFound
		case agentproto.ManageErrDenied, agentproto.ManageErrProtected:
			code = http.StatusForbidden
		case agentproto.ManageErrExists, agentproto.ManageErrBusy:
			code = http.StatusConflict
		case agentproto.ManageErrUnsupported:
			code = http.StatusNotImplemented
		case agentproto.ManageErrTooLarge:
			code = http.StatusRequestEntityTooLarge
		}
		apiErrorDetail(w, code, "manage_"+res.Error, cleanText(res.Message, 500))
		return res, false
	}
	return res, true
}

// manageAudit logs an action on a device.
func (s *Server) manageAudit(r *http.Request, dev store.Device, action string, ok bool, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	details["host"] = dev.Hostname
	s.audit(r, action, "device:"+dev.ID, ok, details)
}

// ---- processes ----

type processList struct {
	Processes []agentproto.Process `json:"processes"`
	At        time.Time            `json:"at"`
}

func (s *Server) handleManageProcesses(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageProcesses}, manageListWait)
	if !ok {
		return
	}
	ps := res.Processes
	if ps == nil {
		ps = []agentproto.Process{}
	}
	for i := range ps {
		p := &ps[i]
		p.Name, p.Path, p.Command, p.User = cleanText(p.Name, 260), cleanText(p.Path, 1024), cleanText(p.Command, 2000), cleanText(p.User, 260)
	}
	slices.SortFunc(ps, func(a, b agentproto.Process) int { return a.PID - b.PID })
	writeJSON(w, http.StatusOK, processList{Processes: ps, At: s.now().UTC()})
}

func (s *Server) handleManageKill(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		apiError(w, http.StatusBadRequest, "invalid_pid")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	// The name is only for the audit log; the body may be empty.
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	_, ok = s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageKillProcess, PID: pid}, manageActionWait)
	s.manageAudit(r, dev, "device.process.kill", ok, map[string]any{"pid": pid, "name": cleanText(body.Name, 260)})
	if ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- services ----

func (s *Server) handleManageServices(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageServices}, manageListWait)
	if !ok {
		return
	}
	ss := res.Services
	if ss == nil {
		ss = []agentproto.Service{}
	}
	slices.SortFunc(ss, func(a, b agentproto.Service) int {
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	writeJSON(w, http.StatusOK, map[string]any{"services": ss})
}

type serviceActionReq struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

func (s *Server) handleManageServiceAction(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req serviceActionReq
	if !decode(w, r, &req) {
		return
	}
	switch req.Action {
	case agentproto.ServiceStart, agentproto.ServiceStop, agentproto.ServiceRestart, agentproto.ServiceAuto,
		agentproto.ServiceManual, agentproto.ServiceDisabled:
	default:
		apiError(w, http.StatusBadRequest, "invalid_service_action")
		return
	}
	if req.Name == "" || len(req.Name) > 256 {
		apiError(w, http.StatusBadRequest, "invalid_service")
		return
	}
	_, ok = s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageServiceAction, Name: req.Name, Action: req.Action}, manageActionWait)
	s.manageAudit(r, dev, "device.service."+req.Action, ok, map[string]any{"service": req.Name})
	if ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- event logs ----

func (s *Server) handleManageLogSources(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageLogSources}, manageListWait)
	if !ok {
		return
	}
	src := res.Sources
	if src == nil {
		src = []agentproto.LogSource{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": src})
}

type logEvents struct {
	Events    []agentproto.LogEvent `json:"events"`
	Truncated bool                  `json:"truncated"`
}

func (s *Server) handleManageLogEvents(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	lq := agentproto.LogQuery{Source: strings.TrimSpace(q.Get("source")), Search: strings.TrimSpace(q.Get("search")),
		Provider: strings.TrimSpace(q.Get("provider"))}
	if lq.Source == "" || len(lq.Source) > 255 || len(lq.Search) > 200 || len(lq.Provider) > 255 {
		apiError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	if v := q.Get("level"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > agentproto.LevelVerbose {
			apiError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		lq.MaxLevel = n
	}
	if v := q.Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 24*366 {
			apiError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		if n > 0 {
			lq.Since = s.now().Add(-time.Duration(n) * time.Hour)
		}
	}
	if v := q.Get("eventId"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			apiError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		lq.EventID = n
	}
	lq.Max = 500
	if v := q.Get("max"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > agentproto.MaxManageEvents {
			apiError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		lq.Max = n
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageLogQuery, Log: lq}, manageActionWait)
	if !ok {
		return
	}
	ev := res.Events
	if ev == nil {
		ev = []agentproto.LogEvent{}
	}
	if len(ev) > lq.Max {
		ev = ev[:lq.Max]
	}
	writeJSON(w, http.StatusOK, logEvents{Events: ev, Truncated: res.Truncated})
}

// ---- files ----

type fileList struct {
	Path      string                 `json:"path"`
	Files     []agentproto.FileEntry `json:"files"`
	Truncated bool                   `json:"truncated"`
}

func (s *Server) handleManageFiles(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	if len(p) > maxUploadDirLen {
		apiError(w, http.StatusBadRequest, "invalid_path")
		return
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: agentproto.ManageFilesList, Path: p}, manageListWait)
	if !ok {
		return
	}
	files := res.Files
	if files == nil {
		files = []agentproto.FileEntry{}
	}
	writeJSON(w, http.StatusOK, fileList{Path: res.Path, Files: files, Truncated: res.Truncated})
}

type pathReq struct {
	Path      string `json:"path"`
	NewPath   string `json:"newPath"`
	Recursive bool   `json:"recursive"`
	Overwrite bool   `json:"overwrite"`
}

func validRemotePath(p string) bool {
	return p != "" && len(p) <= maxUploadDirLen && !strings.ContainsFunc(p, func(r rune) bool { return r == 0 || unicode.IsControl(r) })
}

func (s *Server) fileOp(w http.ResponseWriter, r *http.Request, op, action string) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req pathReq
	if !decode(w, r, &req) {
		return
	}
	if !validRemotePath(req.Path) || (op == agentproto.ManageFilesRename && !validRemotePath(req.NewPath)) {
		apiError(w, http.StatusBadRequest, "invalid_path")
		return
	}
	res, ok := s.manageCall(w, r, dev, agentproto.ManageRequest{Op: op, Path: req.Path, NewPath: req.NewPath, Recursive: req.Recursive,
		Overwrite: req.Overwrite}, manageActionWait)
	details := map[string]any{"path": req.Path}
	if req.NewPath != "" {
		details["newPath"] = req.NewPath
	}
	if req.Recursive {
		details["recursive"] = true
	}
	s.manageAudit(r, dev, action, ok, details)
	if ok {
		writeJSON(w, http.StatusOK, map[string]string{"path": res.Path})
	}
}

func (s *Server) handleManageMkdir(w http.ResponseWriter, r *http.Request) {
	s.fileOp(w, r, agentproto.ManageFilesMkdir, "device.file.mkdir")
}

func (s *Server) handleManageDelete(w http.ResponseWriter, r *http.Request) {
	s.fileOp(w, r, agentproto.ManageFilesDelete, "device.file.delete")
}

func (s *Server) handleManageRename(w http.ResponseWriter, r *http.Request) {
	s.fileOp(w, r, agentproto.ManageFilesRename, "device.file.rename")
}

// ---- transfers ----

func (s *Server) newTransfer(r *http.Request, dev store.Device, dir, p, name string) (*transfer, bool) {
	u, _ := currentUser(r)
	now := s.now()
	t := &transfer{ID: newUUID(), DeviceID: dev.ID, Direction: dir, Path: p, Name: name, State: transferWaiting,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(), userID: u.ID, seen: now}
	t.file = filepath.Join(s.transfersDir(), t.ID)
	s.manage.mu.Lock()
	defer s.manage.mu.Unlock()
	running := 0
	for _, o := range s.manage.transfers {
		if o.userID == u.ID && (o.State == transferWaiting || o.State == transferRunning) {
			running++
		}
	}
	if running >= maxTransfersPerOp {
		return nil, false
	}
	if s.manage.transfers == nil {
		s.manage.transfers = map[string]*transfer{}
	}
	s.manage.transfers[t.ID] = t
	return t, true
}

// transferSnapshot returns a copy for the API, or false when the transfer
// does not exist or belongs to someone else.
func (s *Server) transferFor(r *http.Request, id string) (transfer, bool) {
	u, _ := currentUser(r)
	s.manage.mu.Lock()
	defer s.manage.mu.Unlock()
	t, ok := s.manage.transfers[id]
	if !ok || t.userID != u.ID {
		return transfer{}, false
	}
	return *t, true
}

func (s *Server) updateTransfer(id string, fn func(t *transfer)) {
	s.manage.mu.Lock()
	defer s.manage.mu.Unlock()
	if t, ok := s.manage.transfers[id]; ok {
		fn(t)
		t.UpdatedAt = s.now().UTC()
	}
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if b := path.Base(p); b != "." && b != "/" && b != "" {
		return strings.TrimSuffix(b, ":")
	}
	return "download"
}

func (s *Server) handleManageDownload(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req pathReq
	if !decode(w, r, &req) {
		return
	}
	if !validRemotePath(req.Path) {
		apiError(w, http.StatusBadRequest, "invalid_path")
		return
	}
	if !s.hub.online(dev.ID) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	if err := os.MkdirAll(s.transfersDir(), 0o750); err != nil {
		s.internalError(w, r, err)
		return
	}
	t, ok := s.newTransfer(r, dev, "download", req.Path, baseName(req.Path))
	if !ok {
		apiError(w, http.StatusTooManyRequests, "too_many_transfers")
		return
	}
	f, err := os.OpenFile(t.file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		s.dropTransfer(t.ID)
		s.internalError(w, r, err)
		return
	}
	f.Close()
	s.manageAudit(r, dev, "device.file.download", true, map[string]any{"path": req.Path, "transfer": t.ID})
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.runTransfer(t.ID, dev.ID, agentproto.ManageRequest{Op: agentproto.ManageFilesDownload, Path: req.Path, Transfer: t.ID})
	}()
	snap, _ := s.transferFor(r, t.ID)
	writeJSON(w, http.StatusAccepted, snap)
}

// validUploadName checks a file name for the device: no folders, nothing
// Windows refuses.
func validUploadName(n string) bool {
	if n == "" || n == "." || n == ".." || len(n) > 255 || strings.TrimSpace(n) != n || strings.HasSuffix(n, ".") {
		return false
	}
	return !strings.ContainsFunc(n, func(r rune) bool {
		return unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r)
	})
}

// joinRemote joins a folder and a name with the device's separator.
func joinRemote(platform, dir, name string) string {
	sep := "/"
	if platform == "windows" {
		sep = `\`
	}
	if strings.HasSuffix(dir, sep) {
		return dir + name
	}
	return dir + sep + name
}

func (s *Server) handleManageUpload(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	dir, name := q.Get("dir"), q.Get("name")
	if !validRemotePath(dir) {
		apiError(w, http.StatusBadRequest, "invalid_path")
		return
	}
	if !validUploadName(name) {
		apiError(w, http.StatusBadRequest, "invalid_file_name")
		return
	}
	if r.ContentLength > agentproto.MaxTransferSize {
		apiError(w, http.StatusRequestEntityTooLarge, "transfer_too_large")
		return
	}
	if !s.hub.online(dev.ID) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	if free, ok := freeBytes(s.cfg.DataDir); ok && r.ContentLength > 0 && uint64(r.ContentLength)+512<<20 > free {
		apiError(w, http.StatusInsufficientStorage, "disk_full")
		return
	}
	if err := os.MkdirAll(s.transfersDir(), 0o750); err != nil {
		s.internalError(w, r, err)
		return
	}
	target := joinRemote(dev.Platform, dir, name)
	t, ok := s.newTransfer(r, dev, "upload", target, name)
	if !ok {
		apiError(w, http.StatusTooManyRequests, "too_many_transfers")
		return
	}
	f, err := os.OpenFile(t.file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		s.dropTransfer(t.ID)
		s.internalError(w, r, err)
		return
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), http.MaxBytesReader(w, r.Body, agentproto.MaxTransferSize))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.dropTransfer(t.ID)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apiError(w, http.StatusRequestEntityTooLarge, "transfer_too_large")
			return
		}
		apiError(w, http.StatusBadRequest, "upload_failed")
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	s.updateTransfer(t.ID, func(t *transfer) { t.Size, t.sha = n, sum })
	s.manageAudit(r, dev, "device.file.upload", true, map[string]any{"path": target, "size": n, "sha256": sum, "transfer": t.ID})
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.runTransfer(t.ID, dev.ID, agentproto.ManageRequest{Op: agentproto.ManageFilesUpload, Path: target, Transfer: t.ID,
			Size: n, SHA256: sum, Overwrite: q.Get("overwrite") == "1" || q.Get("overwrite") == "true"})
	}()
	snap, _ := s.transferFor(r, t.ID)
	writeJSON(w, http.StatusAccepted, snap)
}

// runTransfer asks the agent and waits for it to finish.
func (s *Server) runTransfer(id, deviceID string, req agentproto.ManageRequest) {
	ctx, cancel := context.WithCancel(s.bgCtx)
	defer cancel()
	// Watch for an agent that stops sending.
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.manage.mu.Lock()
				t, ok := s.manage.transfers[id]
				idle := !ok || s.now().Sub(t.seen) > transferIdle
				s.manage.mu.Unlock()
				if idle {
					cancel()
					return
				}
			}
		}
	}()
	res, err := s.askAgent(ctx, deviceID, req, 6*time.Hour)
	s.updateTransfer(id, func(t *transfer) {
		switch {
		case errors.Is(err, errAgentOffline):
			t.State, t.Error = transferFailed, "device_offline"
		case err != nil:
			t.State, t.Error = transferFailed, "agent_timeout"
		case res.Error != "":
			t.State, t.Error, t.Message = transferFailed, "manage_"+res.Error, cleanText(res.Message, 500)
		case req.Op == agentproto.ManageFilesDownload && (res.Size != t.Done || !strings.EqualFold(res.SHA256, hashHex(t.hash))):
			t.State, t.Error = transferFailed, "transfer_corrupt"
		default:
			t.State = transferReady
			if req.Op == agentproto.ManageFilesDownload {
				t.Size, t.Zip = res.Size, res.Zip
				if res.Zip {
					t.Name += ".zip"
				}
			} else {
				t.Done = t.Size
				_ = os.Remove(t.file) // delivered
			}
		}
		if t.State == transferFailed {
			_ = os.Remove(t.file)
		}
		t.seen = s.now()
	})
}

func hashHex(h hash.Hash) string {
	if h == nil {
		h = sha256.New()
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Server) dropTransfer(id string) {
	s.manage.mu.Lock()
	t, ok := s.manage.transfers[id]
	delete(s.manage.transfers, id)
	s.manage.mu.Unlock()
	if ok {
		_ = os.Remove(t.file)
	}
}

// pruneTransfers removes finished transfers after an hour.
func (s *Server) pruneTransfers() {
	now := s.now()
	var drop []string
	s.manage.mu.Lock()
	for id, t := range s.manage.transfers {
		done := t.State == transferReady || t.State == transferFailed
		if done && now.Sub(t.seen) > transferKeep {
			drop = append(drop, id)
		}
	}
	s.manage.mu.Unlock()
	for _, id := range drop {
		s.dropTransfer(id)
	}
}

// cleanTransfers empties the transfer folder at start: transfers do not
// survive a restart.
func (s *Server) cleanTransfers() {
	_ = os.RemoveAll(s.transfersDir())
}

func (s *Server) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	dev := r.URL.Query().Get("device")
	out := []transfer{}
	s.manage.mu.Lock()
	for _, t := range s.manage.transfers {
		if t.userID == u.ID && (dev == "" || t.DeviceID == dev) {
			out = append(out, *t)
		}
	}
	s.manage.mu.Unlock()
	slices.SortFunc(out, func(a, b transfer) int { return b.CreatedAt.Compare(a.CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"transfers": out})
}

func (s *Server) handleGetTransfer(w http.ResponseWriter, r *http.Request) {
	t, ok := s.transferFor(r, r.PathValue("tid"))
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTransfer(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.transferFor(r, r.PathValue("tid")); !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	s.dropTransfer(r.PathValue("tid"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTransferFile(w http.ResponseWriter, r *http.Request) {
	t, ok := s.transferFor(r, r.PathValue("tid"))
	if !ok || t.Direction != "download" || t.State != transferReady {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	f, err := os.Open(t.file)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", contentDisposition(t.Name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", t.UpdatedAt, f)
}

// contentDisposition names a download, with a UTF-8 name for browsers that
// read it and an ASCII fallback.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}

// ---- agent side of transfers ----

// agentTransfer checks the signature and returns the device's transfer.
func (s *Server) agentTransfer(w http.ResponseWriter, r *http.Request) (*transfer, bool) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return nil, false
	}
	id, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(agentproto.HeaderAuth))
	if err != nil {
		s.agentFails.Fail(ip)
		http.Error(w, "signature required", http.StatusUnauthorized)
		return nil, false
	}
	dev, err := s.st.DeviceByID(r.Context(), id)
	if err != nil || !agentproto.VerifyRequest(dev.PublicKey, dev.ID, r.Method, r.URL.Path, ts, sig, s.now(), 5*time.Minute) {
		s.agentFails.Fail(ip)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return nil, false
	}
	s.manage.mu.Lock()
	t, ok := s.manage.transfers[r.PathValue("tid")]
	s.manage.mu.Unlock()
	if !ok || t.DeviceID != dev.ID || (t.State != transferWaiting && t.State != transferRunning) {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return nil, false
	}
	return t, true
}

// handleAgentTransferGet serves an upload to the device.
func (s *Server) handleAgentTransferGet(w http.ResponseWriter, r *http.Request) {
	t, ok := s.agentTransfer(w, r)
	if !ok {
		return
	}
	if t.Direction != "upload" {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	f, err := os.Open(t.file)
	if err != nil {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	defer f.Close()
	s.updateTransfer(t.ID, func(t *transfer) { t.State, t.seen = transferRunning, s.now() })
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	pw := &progressWriter{w: w, fn: func(n int64) {
		s.updateTransfer(t.ID, func(t *transfer) { t.Done, t.seen = n, s.now() })
	}}
	http.ServeContent(pw, r, "", time.Time{}, f)
}

// progressWriter reports how much was written, at most every 256 KiB.
type progressWriter struct {
	w    http.ResponseWriter
	n    int64
	last int64
	fn   func(int64)
}

func (p *progressWriter) Header() http.Header         { return p.w.Header() }
func (p *progressWriter) WriteHeader(code int)        { p.w.WriteHeader(code) }
func (p *progressWriter) Flush()                      { http.NewResponseController(p.w).Flush() } //nolint:errcheck
func (p *progressWriter) Unwrap() http.ResponseWriter { return p.w }
func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if p.n-p.last >= 256<<10 {
		p.last = p.n
		p.fn(p.n)
	}
	return n, err
}

// handleAgentTransferPost receives a chunk of a download from the device.
func (s *Server) handleAgentTransferPost(w http.ResponseWriter, r *http.Request) {
	t, ok := s.agentTransfer(w, r)
	if !ok {
		return
	}
	if t.Direction != "download" {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	off, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || off < 0 {
		http.Error(w, "bad offset", http.StatusBadRequest)
		return
	}
	// One chunk at a time, in order.
	s.manage.mu.Lock()
	done, file := t.Done, t.file
	s.manage.mu.Unlock()
	if off != done {
		http.Error(w, "offset does not match", http.StatusConflict)
		return
	}
	if off+r.ContentLength > agentproto.MaxTransferSize || r.ContentLength > agentproto.TransferChunk {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	if free, ok := freeBytes(s.cfg.DataDir); ok && free < 512<<20 {
		http.Error(w, "disk full", http.StatusInsufficientStorage)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentproto.TransferChunk))
	if err != nil {
		http.Error(w, "bad chunk", http.StatusBadRequest)
		return
	}
	f, err := os.OpenFile(file, os.O_WRONLY, 0o600)
	if err != nil {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	_, err = f.WriteAt(data, off)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.log.Warn("transfer chunk", "transfer", t.ID, "err", err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	s.manage.mu.Lock()
	if t.Done == off { // the hash covers the chunks in order
		if t.hash == nil {
			t.hash = sha256.New()
		}
		t.hash.Write(data)
		t.Done = off + int64(len(data))
		t.State, t.seen, t.UpdatedAt = transferRunning, s.now(), s.now().UTC()
	}
	s.manage.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
