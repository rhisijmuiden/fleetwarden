package agent

// Self-update. The running agent downloads the new package (and the package
// of its own version, for a rollback), checks both hashes and starts a copy of
// itself as a detached helper ("update-helper"). The helper installs the
// package, which restarts the service, and waits until the new version has
// connected to the server. When the install fails or the new version does not
// connect in time, it reinstalls the previous package. The outcome is written
// to update-result.json, which the agent reports on its next connection.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const (
	maxPackageSize   = 512 << 20
	resultFile       = "update-result.json"
	connectedFile    = "connected.json"
	planFile         = "plan.json"
	updateSubdir     = "update"
	downloadDeadline = 30 * time.Minute
)

// Hooks, replaced in tests.
var (
	installPackage = platformInstall
	startService   = platformStartService
	// HelperHealthTimeout is how long the helper waits for the new version to connect.
	HelperHealthTimeout = 5 * time.Minute
)

var packageNameRE = regexp.MustCompile(`^fleetwarden-agent_[0-9A-Za-z.~+-]+_(amd64|arm64|x64)\.(deb|msi)$`)

func validPackageName(n string) bool { return packageNameRE.MatchString(n) }

// updatePlan is handed to the helper.
type updatePlan struct {
	Version     string    `json:"version"`
	FromVersion string    `json:"fromVersion"`
	Package     string    `json:"package"`
	Previous    string    `json:"previous,omitempty"`
	StateDir    string    `json:"stateDir"`
	StartedAt   time.Time `json:"startedAt"`
}

type connectedMarker struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
}

func (a *Agent) stateDir() string {
	if a.StateDir != "" {
		return a.StateDir
	}
	return defaultStateDir()
}

// handleUpdate starts an update in the background; one at a time.
func (a *Agent) handleUpdate(ctx context.Context, e *agentproto.Envelope, send func(string, any)) {
	var u agentproto.Update
	if err := e.Decode(&u); err != nil || u.Version == "" || !validPackageName(u.Package) || len(u.SHA256) != 64 {
		a.Log.Warn("ignoring malformed update request")
		return
	}
	if u.Version == version.Version {
		send(agentproto.TypeUpdateStatus, agentproto.UpdateStatus{Version: u.Version, State: agentproto.UpdateCompleted, Message: "already running this version"})
		return
	}
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	go func() {
		err := a.update(ctx, u, send)
		if err != nil {
			a.updating.Store(false)
			a.Log.Warn("agent update failed", "version", u.Version, "err", err)
			send(agentproto.TypeUpdateStatus, agentproto.UpdateStatus{Version: u.Version, State: agentproto.UpdateFailed, Message: err.Error()})
		}
		// On success the helper restarts the service; this process ends.
	}()
}

func (a *Agent) update(ctx context.Context, u agentproto.Update, send func(string, any)) error {
	a.Log.Info("updating the agent", "from", version.Version, "to", u.Version)
	send(agentproto.TypeUpdateStatus, agentproto.UpdateStatus{Version: u.Version, State: agentproto.UpdateDownloading})
	dir := filepath.Join(a.stateDir(), updateSubdir)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictDir(dir); err != nil {
		return err
	}
	pkg := filepath.Join(dir, u.Package)
	if err := a.download(ctx, u.Package, u.SHA256, u.Size, pkg); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	plan := updatePlan{Version: u.Version, FromVersion: version.Version, Package: pkg, StateDir: a.stateDir(), StartedAt: time.Now().UTC()}
	if u.Previous != "" && validPackageName(u.Previous) && len(u.PreviousSHA256) == 64 {
		prev := filepath.Join(dir, u.Previous)
		if err := a.download(ctx, u.Previous, u.PreviousSHA256, 0, prev); err == nil {
			plan.Previous = prev
		} else {
			a.Log.Warn("no rollback package; updating without one", "err", err)
		}
	}
	planPath := filepath.Join(dir, planFile)
	if err := writeJSONFile(planPath, plan); err != nil {
		return err
	}
	helper, err := copySelf(dir)
	if err != nil {
		return fmt.Errorf("prepare helper: %w", err)
	}
	send(agentproto.TypeUpdateStatus, agentproto.UpdateStatus{Version: u.Version, State: agentproto.UpdateInstalling})
	start := a.StartHelper
	if start == nil {
		start = startHelper
	}
	if err := start(helper, planPath); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	return nil
}

// download fetches a package with a signed request and checks its hash.
func (a *Agent) download(ctx context.Context, name, sum string, size int64, dst string) error {
	return a.downloadSigned(ctx, agentproto.PathDownload+name, sum, size, dst)
}

// downloadSigned fetches a file from the server with a signed request and
// checks its size and hash.
func (a *Agent) downloadSigned(ctx context.Context, path, sum string, size int64, dst string) error {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, downloadDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Server+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(agentproto.HeaderAuth, agentproto.SignRequest(cfg.Key(), cfg.DeviceID, http.MethodGet, path, time.Now()))
	req.Header.Set("User-Agent", "fleetwarden-agent/"+version.Version)
	client := &http.Client{Transport: a.HTTP.Transport}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered %s", res.Status)
	}
	limit := int64(maxPackageSize)
	if strings.HasPrefix(path, agentproto.PathSoftware) || strings.HasPrefix(path, agentproto.PathRepository) {
		limit = maxInstallerSize
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errors.New("package is too large")
	}
	if err == nil && size > 0 && n != size {
		err = fmt.Errorf("got %d bytes, expected %d", n, size)
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sum) {
		err = errors.New("SHA-256 does not match")
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

func copySelf(dir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "fleetwarden-agent-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	in, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}

func writeJSONFile(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// markConnected records that this version reached the server; the update
// helper waits for it.
func (a *Agent) markConnected() {
	if err := writeJSONFile(filepath.Join(a.stateDir(), connectedFile), connectedMarker{Version: version.Version, At: time.Now().UTC()}); err != nil {
		a.Log.Debug("write connection marker", "err", err)
	}
}

// reportUpdateResult sends and removes the helper's outcome, if any.
func (a *Agent) reportUpdateResult(send func(string, any)) {
	path := filepath.Join(a.stateDir(), resultFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var res agentproto.UpdateStatus
	if json.Unmarshal(raw, &res) == nil && res.Version != "" {
		send(agentproto.TypeUpdateStatus, res)
		a.Log.Info("reported agent update result", "version", res.Version, "state", res.State)
	}
	_ = os.Remove(path)
}

// RunUpdateHelper installs the package of an update plan and rolls back when
// the new version does not connect. It runs detached from the service.
func RunUpdateHelper(ctx context.Context, planPath string, log *slog.Logger) error {
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan updatePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("%s: %w", planPath, err)
	}
	res := agentproto.UpdateStatus{Version: plan.Version, State: agentproto.UpdateCompleted}
	log.Info("installing agent update", "from", plan.FromVersion, "to", plan.Version, "package", plan.Package)
	err = installPackage(ctx, plan.Package)
	if err == nil && !waitConnected(ctx, plan.StateDir, plan.Version, plan.StartedAt, HelperHealthTimeout) {
		err = fmt.Errorf("version %s did not connect to the server within %s", plan.Version, HelperHealthTimeout)
	}
	if err != nil {
		log.Warn("agent update failed", "err", err)
		res.State, res.Message = agentproto.UpdateFailed, err.Error()
		if plan.Previous != "" {
			if rerr := installPackage(ctx, plan.Previous); rerr == nil {
				res.State = agentproto.UpdateRolledBack
				res.Message = err.Error() + "; reinstalled " + plan.FromVersion
			} else {
				res.Message += "; rollback failed: " + rerr.Error()
			}
		}
		if serr := startService(ctx); serr != nil {
			log.Warn("start agent service", "err", serr)
		}
	}
	if werr := writeJSONFile(filepath.Join(plan.StateDir, resultFile), res); werr != nil {
		log.Warn("write update result", "err", werr)
	}
	for _, p := range []string{plan.Package, plan.Previous} {
		if p != "" {
			_ = os.Remove(p)
		}
	}
	log.Info("agent update finished", "state", res.State, "message", res.Message)
	return nil
}

func waitConnected(ctx context.Context, dir, v string, since time.Time, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		var m connectedMarker
		if raw, err := os.ReadFile(filepath.Join(dir, connectedFile)); err == nil && json.Unmarshal(raw, &m) == nil {
			if m.Version == v && !m.At.Before(since) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}
