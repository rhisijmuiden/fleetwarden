package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// ManageBackend lists and controls processes, services and event logs. The
// file operations are the same on every platform and live in this file.
type ManageBackend interface {
	Processes(ctx context.Context) ([]agentproto.Process, error)
	KillProcess(pid int) error
	Services(ctx context.Context) ([]agentproto.Service, error)
	ServiceAction(ctx context.Context, name, action string) error
	LogSources(ctx context.Context) ([]agentproto.LogSource, error)
	// QueryLog returns the matching events, newest first, and whether there
	// were more.
	QueryLog(ctx context.Context, q agentproto.LogQuery) ([]agentproto.LogEvent, bool, error)
	// Roots lists the drives (Windows) or "/" (Linux).
	Roots() ([]agentproto.FileEntry, error)
}

// manageError carries a protocol error code.
type manageError struct {
	code, msg string
}

func (e *manageError) Error() string { return e.code + ": " + e.msg }

func mErr(code, format string, args ...any) error {
	return &manageError{code: code, msg: fmt.Sprintf(format, args...)}
}

// manageResultOf turns an error into a result.
func manageResultOf(err error) agentproto.ManageResult {
	var me *manageError
	switch {
	case err == nil:
		return agentproto.ManageResult{}
	case errors.As(err, &me):
		return agentproto.ManageResult{Error: me.code, Message: me.msg}
	case errors.Is(err, fs.ErrNotExist):
		return agentproto.ManageResult{Error: agentproto.ManageErrNotFound, Message: err.Error()}
	case errors.Is(err, fs.ErrPermission):
		return agentproto.ManageResult{Error: agentproto.ManageErrDenied, Message: err.Error()}
	case errors.Is(err, fs.ErrExist):
		return agentproto.ManageResult{Error: agentproto.ManageErrExists, Message: err.Error()}
	}
	return agentproto.ManageResult{Error: agentproto.ManageErrFailed, Message: err.Error()}
}

const (
	manageParallel   = 4
	manageOpTimeout  = 2 * time.Minute
	transferDeadline = 6 * time.Hour
)

// manageSlots limits how many operations run at the same time; transfers
// have their own.
var (
	manageSlots   = make(chan struct{}, manageParallel)
	transferSlots = make(chan struct{}, 2)
)

func (a *Agent) handleManage(e *agentproto.Envelope, reply func(id string, v any)) {
	var req agentproto.ManageRequest
	if err := e.Decode(&req); err != nil || e.ID == "" {
		return
	}
	slots := manageSlots
	if req.Op == agentproto.ManageFilesDownload || req.Op == agentproto.ManageFilesUpload {
		slots = transferSlots
	}
	select {
	case slots <- struct{}{}:
	default:
		reply(e.ID, agentproto.ManageResult{Error: agentproto.ManageErrBusy, Message: "too many operations at once"})
		return
	}
	go func() {
		defer func() { <-slots }()
		timeout := manageOpTimeout
		if slots == transferSlots {
			timeout = transferDeadline
		}
		ctx, cancel := context.WithTimeout(a.runCtx(), timeout)
		defer cancel()
		res := a.runManage(ctx, req)
		if res.Error != "" {
			a.Log.Info("manage", "op", req.Op, "error", res.Error, "message", res.Message)
		} else {
			a.Log.Info("manage", "op", req.Op, "pid", req.PID, "name", req.Name, "action", req.Action, "path", req.Path)
		}
		reply(e.ID, res)
	}()
}

func (a *Agent) manageBackend() ManageBackend {
	if a.Manage != nil {
		return a.Manage
	}
	return platformManage()
}

func (a *Agent) runManage(ctx context.Context, req agentproto.ManageRequest) agentproto.ManageResult {
	b := a.manageBackend()
	switch req.Op {
	case agentproto.ManageProcesses:
		ps, err := b.Processes(ctx)
		if err != nil {
			return manageResultOf(err)
		}
		self := os.Getpid()
		for i := range ps {
			if ps[i].PID == self {
				ps[i].Agent, ps[i].Protected = true, true
			}
		}
		return agentproto.ManageResult{Processes: ps}
	case agentproto.ManageKillProcess:
		if req.PID <= 0 {
			return manageResultOf(mErr(agentproto.ManageErrInvalid, "no process id"))
		}
		if req.PID == os.Getpid() {
			return manageResultOf(mErr(agentproto.ManageErrProtected, "the agent does not stop itself"))
		}
		return manageResultOf(b.KillProcess(req.PID))
	case agentproto.ManageServices:
		ss, err := b.Services(ctx)
		return withServices(ss, err)
	case agentproto.ManageServiceAction:
		if !validServiceName(req.Name) {
			return manageResultOf(mErr(agentproto.ManageErrInvalid, "invalid service name"))
		}
		switch req.Action {
		case agentproto.ServiceStart, agentproto.ServiceStop, agentproto.ServiceRestart, agentproto.ServiceAuto,
			agentproto.ServiceManual, agentproto.ServiceDisabled:
		default:
			return manageResultOf(mErr(agentproto.ManageErrInvalid, "invalid action"))
		}
		if isAgentService(req.Name) {
			return manageResultOf(mErr(agentproto.ManageErrProtected, "the agent does not change its own service"))
		}
		return manageResultOf(b.ServiceAction(ctx, req.Name, req.Action))
	case agentproto.ManageLogSources:
		src, err := b.LogSources(ctx)
		if err != nil {
			return manageResultOf(err)
		}
		return agentproto.ManageResult{Sources: src}
	case agentproto.ManageLogQuery:
		q := req.Log
		if q.Max <= 0 || q.Max > agentproto.MaxManageEvents {
			q.Max = agentproto.MaxManageEvents
		}
		ev, more, err := b.QueryLog(ctx, q)
		if err != nil {
			return manageResultOf(err)
		}
		return agentproto.ManageResult{Events: ev, Truncated: more}
	case agentproto.ManageFilesList:
		if req.Path == "" {
			roots, err := b.Roots()
			if err != nil {
				return manageResultOf(err)
			}
			return agentproto.ManageResult{Files: roots}
		}
		return listDir(req.Path)
	case agentproto.ManageFilesMkdir:
		p, err := absPath(req.Path)
		if err == nil {
			err = os.Mkdir(p, 0o755)
		}
		r := manageResultOf(err)
		r.Path = p
		return r
	case agentproto.ManageFilesDelete:
		return manageResultOf(deletePath(req.Path, req.Recursive))
	case agentproto.ManageFilesRename:
		return manageResultOf(renamePath(req.Path, req.NewPath, req.Overwrite))
	case agentproto.ManageFilesDownload:
		return a.sendFile(ctx, req)
	case agentproto.ManageFilesUpload:
		return a.receiveFile(ctx, req)
	}
	return agentproto.ManageResult{Error: agentproto.ManageErrUnsupported, Message: "unknown operation " + req.Op}
}

func withServices(ss []agentproto.Service, err error) agentproto.ManageResult {
	if err != nil {
		return manageResultOf(err)
	}
	for i := range ss {
		if isAgentService(ss[i].Name) {
			ss[i].Protected = true
		}
	}
	return agentproto.ManageResult{Services: ss}
}

// isAgentService reports whether a service is this agent's own.
func isAgentService(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(name, ".service"))
	return n == "fleetwarden-agent" || n == "fleetwardenagent"
}

func validServiceName(n string) bool {
	if n == "" || len(n) > 256 || strings.HasPrefix(n, "-") {
		return false
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("@._:-+ $", r):
		default:
			return false
		}
	}
	return true
}

// ---- files ----

// absPath checks that a path is absolute and returns it cleaned.
func absPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", mErr(agentproto.ManageErrInvalid, "no path")
	}
	if runtime.GOOS == "windows" && len(p) == 2 && p[1] == ':' {
		p += `\`
	}
	if !filepath.IsAbs(p) {
		return "", mErr(agentproto.ManageErrInvalid, "the path must be absolute")
	}
	return filepath.Clean(p), nil
}

func fileEntry(dir string, fi fs.FileInfo) agentproto.FileEntry {
	mod := fi.ModTime().UTC()
	e := agentproto.FileEntry{Name: fi.Name(), Path: filepath.Join(dir, fi.Name()), Dir: fi.IsDir(), Modified: &mod,
		Mode: fi.Mode().String(), Link: fi.Mode()&fs.ModeSymlink != 0, Hidden: isHidden(fi)}
	if !e.Dir {
		e.Size = fi.Size()
	}
	return e
}

func listDir(path string) agentproto.ManageResult {
	p, err := absPath(path)
	if err != nil {
		return manageResultOf(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return manageResultOf(err)
	}
	if !fi.IsDir() {
		return agentproto.ManageResult{Path: p, Files: []agentproto.FileEntry{fileEntry(filepath.Dir(p), fi)}}
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return manageResultOf(err)
	}
	out := agentproto.ManageResult{Path: p, Files: []agentproto.FileEntry{}}
	for _, de := range ents {
		if len(out.Files) >= agentproto.MaxManageFiles {
			out.Truncated = true
			break
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := fileEntry(p, info)
		if e.Link {
			if t, err := os.Stat(e.Path); err == nil && t.IsDir() {
				e.Dir = true
			}
		}
		out.Files = append(out.Files, e)
	}
	slices.SortFunc(out.Files, func(a, b agentproto.FileEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// isRoot reports whether a path is a file system root ("/", "C:\").
func isRoot(p string) bool { return filepath.Dir(p) == p }

func deletePath(path string, recursive bool) error {
	p, err := absPath(path)
	if err != nil {
		return err
	}
	if isRoot(p) {
		return mErr(agentproto.ManageErrProtected, "a drive or the root folder cannot be deleted")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() && recursive {
		return os.RemoveAll(p)
	}
	if err := os.Remove(p); err != nil {
		if fi.IsDir() {
			return mErr(agentproto.ManageErrExists, "the folder is not empty")
		}
		return err
	}
	return nil
}

func renamePath(from, to string, overwrite bool) error {
	src, err := absPath(from)
	if err != nil {
		return err
	}
	dst, err := absPath(to)
	if err != nil {
		return err
	}
	if isRoot(src) {
		return mErr(agentproto.ManageErrProtected, "a drive cannot be renamed")
	}
	if _, err := os.Lstat(dst); err == nil && !overwrite {
		return mErr(agentproto.ManageErrExists, "%s already exists", dst)
	}
	return os.Rename(src, dst)
}

// signedRequest sends a request to the server signed with the device key.
func (a *Agent) signedRequest(ctx context.Context, method, path, query string, body io.Reader, size int64) (*http.Response, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	u := cfg.Server + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	req.Header.Set(agentproto.HeaderAuth, agentproto.SignRequest(cfg.Key(), cfg.DeviceID, method, path, a.serverNow()))
	req.Header.Set("User-Agent", "fleetwarden-agent/"+version.Version)
	client := &http.Client{Transport: a.HTTP.Transport}
	return client.Do(req)
}

// sendFile uploads a file, or a folder as a zip file, to the server in chunks.
func (a *Agent) sendFile(ctx context.Context, req agentproto.ManageRequest) agentproto.ManageResult {
	p, err := absPath(req.Path)
	if err != nil {
		return manageResultOf(err)
	}
	if req.Transfer == "" {
		return manageResultOf(mErr(agentproto.ManageErrInvalid, "no transfer id"))
	}
	fi, err := os.Stat(p)
	if err != nil {
		return manageResultOf(err)
	}
	var src io.Reader
	zipped := fi.IsDir()
	if zipped {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(zipDir(ctx, p, pw)) }()
		defer pr.Close()
		src = pr
	} else {
		if fi.Size() > agentproto.MaxTransferSize {
			return manageResultOf(mErr(agentproto.ManageErrTooLarge, "the file is larger than %d bytes", int64(agentproto.MaxTransferSize)))
		}
		f, err := os.Open(p)
		if err != nil {
			return manageResultOf(err)
		}
		defer f.Close()
		src = f
	}
	h := sha256.New()
	src = io.TeeReader(src, h)
	buf := make([]byte, agentproto.TransferChunk)
	var off int64
	for {
		n, rerr := io.ReadFull(src, buf)
		if n > 0 || off == 0 {
			if off+int64(n) > agentproto.MaxTransferSize {
				return manageResultOf(mErr(agentproto.ManageErrTooLarge, "the download is larger than %d bytes", int64(agentproto.MaxTransferSize)))
			}
			if err := a.postChunk(ctx, req.Transfer, off, buf[:n]); err != nil {
				return manageResultOf(mErr(agentproto.ManageErrFailed, "upload to the server: %v", err))
			}
			off += int64(n)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return manageResultOf(rerr)
		}
	}
	return agentproto.ManageResult{Path: p, Size: off, SHA256: hex.EncodeToString(h.Sum(nil)), Zip: zipped}
}

func (a *Agent) postChunk(ctx context.Context, transfer string, off int64, data []byte) error {
	var last error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		res, err := a.signedRequest(ctx, http.MethodPost, agentproto.PathTransfer+transfer, "offset="+strconv.FormatInt(off, 10),
			bytes.NewReader(data), int64(len(data)))
		if err != nil {
			last = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		res.Body.Close()
		switch {
		case res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK:
			return nil
		case res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests:
			last = fmt.Errorf("server answered %s", res.Status)
		default: // cancelled, unknown transfer, wrong offset: no point retrying
			return fmt.Errorf("server answered %s: %s", res.Status, strings.TrimSpace(string(body)))
		}
	}
	return last
}

// zipDir writes a folder as a zip file. Files it cannot read are skipped.
func zipDir(ctx context.Context, root string, w io.Writer) error {
	zw := zip.NewWriter(w)
	base := filepath.Dir(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if d.IsDir() {
			hdr, _ := zip.FileInfoHeader(info)
			hdr.Name = name + "/"
			_, err := zw.CreateHeader(hdr)
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return nil // in use or no access
		}
		defer f.Close()
		hdr, _ := zip.FileInfoHeader(info)
		hdr.Name, hdr.Method = name, zip.Deflate
		zf, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(zf, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// receiveFile downloads a file from the server and writes it to req.Path.
func (a *Agent) receiveFile(ctx context.Context, req agentproto.ManageRequest) agentproto.ManageResult {
	p, err := absPath(req.Path)
	if err != nil {
		return manageResultOf(err)
	}
	if req.Transfer == "" || len(req.SHA256) != 64 || req.Size < 0 || req.Size > agentproto.MaxTransferSize {
		return manageResultOf(mErr(agentproto.ManageErrInvalid, "incomplete upload request"))
	}
	if fi, err := os.Stat(p); err == nil {
		if fi.IsDir() {
			return manageResultOf(mErr(agentproto.ManageErrExists, "%s is a folder", p))
		}
		if !req.Overwrite {
			return manageResultOf(mErr(agentproto.ManageErrExists, "%s already exists", p))
		}
	}
	dir := filepath.Dir(p)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return manageResultOf(mErr(agentproto.ManageErrNotFound, "the folder %s does not exist", dir))
	}
	tmp, err := os.CreateTemp(dir, ".fleetwarden-upload-*")
	if err != nil {
		return manageResultOf(err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName) // after a successful rename this does nothing
	if err := a.downloadTransfer(ctx, req, tmpName); err != nil {
		return manageResultOf(mErr(agentproto.ManageErrFailed, "download from the server: %v", err))
	}
	if req.Overwrite {
		_ = os.Remove(p) // Windows does not rename over an existing file in use
	}
	if err := os.Rename(tmpName, p); err != nil {
		return manageResultOf(err)
	}
	return agentproto.ManageResult{Path: p, Size: req.Size, SHA256: req.SHA256}
}

func (a *Agent) downloadTransfer(ctx context.Context, req agentproto.ManageRequest, dst string) error {
	res, err := a.signedRequest(ctx, http.MethodGet, agentproto.PathTransfer+req.Transfer, "", nil, 0)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered %s", res.Status)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, req.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n != req.Size:
		return fmt.Errorf("got %d bytes, expected %d", n, req.Size)
	case !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), req.SHA256):
		return errors.New("SHA-256 does not match")
	}
	return nil
}
