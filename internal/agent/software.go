package agent

// Software jobs: install a package or uninstall a program. The server sends
// one job at a time. The agent downloads the installer (from the vendor's
// URL, checked against the catalog hash, or from the server), skips the job
// when the program is already in the wanted state, runs the installer or
// uninstaller unattended and reports the outcome with the exit code and the
// end of the output. A fresh inventory follows every finished job.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// SoftwareBackend installs and removes programs on the platform.
type SoftwareBackend interface {
	// Find reports whether a program matching d is installed, and its version.
	Find(ctx context.Context, d agentproto.Detect) (bool, string, error)
	// Install runs an installer. file is the downloaded installer (empty for apt).
	Install(ctx context.Context, inst agentproto.Installer, file string) (RunResult, error)
	// Uninstall removes a program; found is false when it is not installed.
	Uninstall(ctx context.Context, u agentproto.Uninstall) (res RunResult, found bool, err error)
}

// RunResult is the outcome of an installer or uninstaller run.
type RunResult struct {
	ExitCode int
	Ran      bool // ExitCode is set
	Output   string
	Reboot   bool
}

const (
	softwareJobFile    = "software-job.json"
	softwareSubdir     = "software"
	maxInstallerSize   = 4 << 30
	softwareDefaultMin = 60
	softwareOutputMax  = 4000
)

// ErrSoftwareUnsupported is returned on platforms without a software backend.
var ErrSoftwareUnsupported = errors.New("software deployment is not supported on this platform")

type softwareState struct {
	running bool
	jobID   int64
	last    *agentproto.SoftwareStatus
}

func (a *Agent) softwareBackend() SoftwareBackend {
	if a.Software == nil {
		a.Software = newPlatformSoftwareBackend()
	}
	return a.Software
}

func (a *Agent) handleSoftwareJob(e *agentproto.Envelope) {
	var job agentproto.SoftwareJob
	if err := e.Decode(&job); err != nil || job.JobID <= 0 {
		a.Log.Warn("ignoring malformed software job")
		return
	}
	fail := func(msg string) {
		a.emit(agentproto.TypeSoftwareStatus, agentproto.SoftwareStatus{JobID: job.JobID, State: agentproto.SoftwareFailed, Message: msg, At: time.Now().UTC()})
	}
	if msg := checkSoftwareJob(job); msg != "" {
		fail(msg)
		return
	}
	a.mu.Lock()
	sw := &a.software
	if sw.running {
		same := sw.jobID == job.JobID
		last := sw.last
		a.mu.Unlock()
		if same && last != nil {
			a.emit(agentproto.TypeSoftwareStatus, *last)
		} else if !same {
			fail("another software job is running")
		}
		return
	}
	sw.running, sw.jobID, sw.last = true, job.JobID, nil
	a.mu.Unlock()
	go a.runSoftwareJob(a.runCtx(), job)
}

var (
	sha256RE   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	fileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,150}$`)
	aptNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`)
)

// checkSoftwareJob validates what the agent is about to run.
func checkSoftwareJob(job agentproto.SoftwareJob) string {
	switch job.Action {
	case agentproto.ActionInstall:
		in := job.Installer
		if in == nil || in.Kind == "" {
			return "the job has no installer"
		}
		switch in.Kind {
		case agentproto.KindApt:
			if !aptNameRE.MatchString(in.Package) {
				return "invalid package name"
			}
			return ""
		}
		if (in.URL == "") == (in.File == "") {
			return "the installer needs either a download URL or a server file"
		}
		if in.URL != "" {
			u, err := url.Parse(in.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return "the download URL must use https"
			}
			if !sha256RE.MatchString(in.SHA256) && !in.AnyHash {
				return "the installer has no SHA-256"
			}
		}
		if in.File != "" && (!sha256RE.MatchString(in.File) || !strings.EqualFold(in.File, in.SHA256)) {
			return "invalid server file"
		}
		if in.FileName != "" && !fileNameRE.MatchString(in.FileName) {
			return "invalid installer file name"
		}
	case agentproto.ActionUninstall:
		if job.Uninstall == nil || strings.TrimSpace(job.Uninstall.Name) == "" {
			return "the job does not name a program"
		}
	default:
		return "unknown action " + job.Action
	}
	return ""
}

type softwareRun struct {
	a   *Agent
	job agentproto.SoftwareJob
	st  agentproto.SoftwareStatus
}

func (r *softwareRun) report(state, msg string) {
	r.st.State, r.st.Message, r.st.At = state, msg, time.Now().UTC()
	st := r.st
	r.a.mu.Lock()
	r.a.software.last = &st
	r.a.mu.Unlock()
	if !agentproto.SoftwareFinished(state) {
		_ = writeJSONFile(filepath.Join(r.a.stateDir(), softwareJobFile), st)
	}
	r.a.emit(agentproto.TypeSoftwareStatus, st)
}

func (a *Agent) runSoftwareJob(ctx context.Context, job agentproto.SoftwareJob) {
	minutes := job.TimeoutMinutes
	if minutes <= 0 {
		minutes = softwareDefaultMin
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(minutes)*time.Minute+downloadDeadline)
	defer cancel()
	r := &softwareRun{a: a, job: job, st: agentproto.SoftwareStatus{JobID: job.JobID}}
	dir := filepath.Join(a.stateDir(), softwareSubdir, fmt.Sprint(job.JobID))
	defer func() {
		_ = os.RemoveAll(dir)
		a.mu.Lock()
		a.software.running, a.software.jobID, a.software.last = false, 0, nil
		a.mu.Unlock()
	}()
	a.Log.Info("software job", "job", job.JobID, "action", job.Action, "name", job.Name)
	state, msg := a.doSoftwareJob(ctx, r, dir, minutes)
	if a.runCtx().Err() != nil {
		return // the agent stops; the next start reports the job as interrupted
	}
	r.st.Output = tailString(r.st.Output, softwareOutputMax)
	r.report(state, msg)
	_ = os.Remove(filepath.Join(a.stateDir(), softwareJobFile))
	a.Log.Info("software job finished", "job", job.JobID, "state", state, "message", msg)
	a.requestInventory()
}

func (a *Agent) doSoftwareJob(ctx context.Context, r *softwareRun, dir string, minutes int) (string, string) {
	be := a.softwareBackend()
	job := r.job
	runCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, time.Duration(minutes)*time.Minute)
	}
	if job.Action == agentproto.ActionUninstall {
		r.report(agentproto.SoftwareUninstalling, "")
		a.installMu.Lock()
		cctx, cancel := runCtx()
		res, found, err := be.Uninstall(cctx, *job.Uninstall)
		cancel()
		a.installMu.Unlock()
		r.apply(res)
		switch {
		case err != nil:
			return agentproto.SoftwareFailed, err.Error()
		case !found:
			return agentproto.SoftwareSkipped, "not installed"
		}
		return agentproto.SoftwareCompleted, ""
	}

	in := *job.Installer
	hasDetect := job.Detect != nil && (job.Detect.Name != "" || job.Detect.ProductCode != "")
	if hasDetect {
		if ok, v, err := be.Find(ctx, *job.Detect); err == nil && ok && (job.Detect.Version == "" || (v != "" && compareVersions(v, job.Detect.Version) >= 0)) {
			r.st.Version = v
			return agentproto.SoftwareSkipped, "already installed" + versionSuffix(v)
		}
	}
	file := ""
	if in.Kind != agentproto.KindApt {
		r.report(agentproto.SoftwareDownloading, "")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return agentproto.SoftwareFailed, err.Error()
		}
		_ = restrictDir(dir)
		file = filepath.Join(dir, installerFileName(in))
		var err error
		if in.File != "" {
			err = a.downloadSigned(ctx, agentproto.PathSoftware+strings.ToLower(in.File), in.File, in.Size, file)
		} else {
			err = a.downloadPublic(ctx, in, file)
		}
		if err != nil {
			return agentproto.SoftwareFailed, "download: " + err.Error()
		}
	}
	r.report(agentproto.SoftwareInstalling, "")
	a.installMu.Lock()
	cctx, cancel := runCtx()
	res, err := be.Install(cctx, in, file)
	cancel()
	a.installMu.Unlock()
	r.apply(res)
	if err != nil {
		return agentproto.SoftwareFailed, err.Error()
	}
	msg := ""
	if hasDetect {
		if ok, v, err := be.Find(ctx, *job.Detect); err == nil && ok {
			r.st.Version = v
		} else if err == nil {
			msg = "installed, but the program does not show up under its expected name"
		}
	}
	return agentproto.SoftwareCompleted, msg
}

func (r *softwareRun) apply(res RunResult) {
	if res.Ran {
		code := res.ExitCode
		r.st.ExitCode = &code
	}
	r.st.Output = res.Output
	r.st.RebootRequired = r.st.RebootRequired || res.Reboot
}

func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return " (" + v + ")"
}

// installerFileName keeps the extension the installer needs.
func installerFileName(in agentproto.Installer) string {
	if in.FileName != "" && fileNameRE.MatchString(in.FileName) {
		return in.FileName
	}
	if u, err := url.Parse(in.URL); err == nil {
		if b := filepath.Base(u.Path); fileNameRE.MatchString(b) && strings.Contains(b, ".") {
			return b
		}
	}
	switch in.Kind {
	case agentproto.KindMSI:
		return "installer.msi"
	case agentproto.KindDeb:
		return "package.deb"
	}
	return "setup.exe"
}

// downloadPublic fetches a catalog installer from its vendor and checks the hash.
func (a *Agent) downloadPublic(ctx context.Context, in agentproto.Installer, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "fleetwarden-agent/"+version.Version)
	client := &http.Client{Transport: a.HTTP.Transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" {
			return errors.New("redirect to a non-https address")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	return saveChecked(res.Body, dst, in.SHA256, 0, in.AnyHash)
}

// saveChecked writes r to dst and checks its size and SHA-256.
func saveChecked(r io.Reader, dst, sum string, size int64, anyHash bool) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, maxInstallerSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxInstallerSize {
		err = errors.New("the installer is too large")
	}
	if err == nil && size > 0 && n != size {
		err = fmt.Errorf("got %d bytes, expected %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); err == nil && !anyHash && !strings.EqualFold(got, sum) {
		err = fmt.Errorf("SHA-256 %s does not match the expected %s; the vendor may have published a newer file at the same address", got, strings.ToLower(sum))
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// resumeSoftwareJob reports a job that an earlier run left unfinished.
func (a *Agent) resumeSoftwareJob() {
	path := filepath.Join(a.stateDir(), softwareJobFile)
	var st agentproto.SoftwareStatus
	if !readJSONFile(path, &st) || st.JobID == 0 {
		_ = os.Remove(path)
		return
	}
	_ = os.Remove(path)
	_ = os.RemoveAll(filepath.Join(a.stateDir(), softwareSubdir))
	st.State, st.Message, st.At = agentproto.SoftwareFailed, "the agent stopped during the job", time.Now().UTC()
	a.emit(agentproto.TypeSoftwareStatus, st)
}

// requestInventory asks the session to send a full inventory soon.
func (a *Agent) requestInventory() {
	select {
	case a.invNow <- struct{}{}:
	default:
	}
}

// compareVersions compares dotted versions part by part, numbers as
// numbers ("10.0" > "9.5", "1.2" == "1.2.0").
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := "0", "0"
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := comparePart(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func versionParts(v string) []string {
	return strings.FieldsFunc(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v"), func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+' || r == ' ' || r == ':' || r == '~'
	})
}

func comparePart(x, y string) int {
	nx, ny := numeric(x), numeric(y)
	switch {
	case nx && ny:
		x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
		if len(x) != len(y) {
			return cmpVersionInt(len(x), len(y))
		}
		return strings.Compare(x, y)
	case nx:
		return 1 // 1.0.1 > 1.0.beta
	case ny:
		return -1
	}
	return strings.Compare(x, y)
}

func numeric(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func cmpVersionInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if len(s) > n {
		s = s[len(s)-n:]
		for len(s) > 0 && s[0]&0xc0 == 0x80 {
			s = s[1:]
		}
		s = "…" + s
	}
	return s
}
