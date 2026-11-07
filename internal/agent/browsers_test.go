package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type fakeBrowserHost struct {
	mu        sync.Mutex
	installed map[string]string // detect name -> version
	running   map[string]bool   // process -> running
	answers   []bool            // postpone answers, in order
	prompts   []Prompt
	closed    []CloseApp
	launched  []string
	killed    int
	failURL   string
}

func (f *fakeBrowserHost) Find(_ context.Context, d agentproto.Detect) (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.installed[d.Name]
	return ok, v, nil
}

func (f *fakeBrowserHost) Install(_ context.Context, in agentproto.Installer, file string) (RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.URL == f.failURL {
		return RunResult{Ran: true, ExitCode: 1603, Output: "fatal error"}, errors.New("MSI error 1603")
	}
	// The test installers are named after the browser they update.
	for name := range f.installed {
		if strings.Contains(in.URL, strings.ReplaceAll(strings.ToLower(name), " ", "-")) {
			f.installed[name] = "2.0"
		}
	}
	return RunResult{Ran: true, Output: "ok"}, nil
}

func (f *fakeBrowserHost) Uninstall(context.Context, agentproto.Uninstall) (RunResult, bool, error) {
	return RunResult{}, false, nil
}

func (f *fakeBrowserHost) Running(names []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range names {
		if f.running[n] {
			out = append(out, n)
		}
	}
	return out
}

func (f *fakeBrowserHost) Kill(names []string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, x := range names {
		if f.running[x] {
			f.running[x] = false
			n++
		}
	}
	f.killed += n
	return n
}

func (f *fakeBrowserHost) Ask(_ context.Context, p Prompt) (Answer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, p)
	if len(f.answers) == 0 {
		return Answer{}, nil
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return Answer{Postpone: a}, nil
}

func (f *fakeBrowserHost) CloseApps(_ context.Context, _ string, apps []CloseApp, _ time.Duration) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, apps...)
	var out []string
	for _, a := range apps {
		if f.running[a.Process+".exe"] {
			f.running[a.Process+".exe"] = false
			out = append(out, a.Key)
		}
	}
	return out
}

func (f *fakeBrowserHost) LaunchApps(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launched = append(f.launched, id)
}

func browserTestJob(base string) agentproto.SoftwareJob {
	mk := func(key, name, proc string) agentproto.BrowserUpdate {
		slug := strings.ReplaceAll(strings.ToLower(name), " ", "-")
		sum := sha256.Sum256([]byte("installer " + slug))
		return agentproto.BrowserUpdate{Key: key, Name: name, Version: "2.0", Process: proc,
			Installer: agentproto.Installer{Kind: agentproto.KindMSI, URL: base + "/" + slug + ".msi", SHA256: hex.EncodeToString(sum[:])},
			Detect:    agentproto.Detect{Name: name, Exact: true, MachineOnly: true}, RestoreArgs: "--restore-last-session"}
	}
	ff := mk("firefox", "Mozilla Firefox", "firefox.exe")
	ff.RestoreArgs, ff.Firefox = "", true
	return agentproto.SoftwareJob{JobID: 7, Action: agentproto.ActionBrowsers, Name: "Browser updates", TimeoutMinutes: 60,
		Browsers: []agentproto.BrowserUpdate{mk("chrome", "Google Chrome", "chrome.exe"), mk("edge", "Microsoft Edge", "msedge.exe"), ff},
		Prompt:   &agentproto.UserPrompt{MaxPostpones: 2, PostponeMinutes: 1, CountdownSeconds: 60}}
}

func runBrowserJob(t *testing.T, f *fakeBrowserHost, job agentproto.SoftwareJob, srv *httptest.Server) ([]agentproto.SoftwareStatus, agentproto.SoftwareStatus) {
	t.Helper()
	a := &Agent{StateDir: t.TempDir(), Software: f, Processes: f, Notify: f, Minute: time.Millisecond, HTTP: srv.Client()}
	a.init()
	var mu sync.Mutex
	var sts []agentproto.SoftwareStatus
	a.patch.sendFn = func(typ string, v any) bool {
		if st, ok := v.(agentproto.SoftwareStatus); ok && typ == agentproto.TypeSoftwareStatus {
			mu.Lock()
			sts = append(sts, st)
			mu.Unlock()
		}
		return true
	}
	if msg := checkSoftwareJob(job); msg != "" {
		t.Fatal(msg)
	}
	a.runSoftwareJob(context.Background(), job)
	mu.Lock()
	defer mu.Unlock()
	if len(sts) == 0 {
		t.Fatal("no status")
	}
	return sts, sts[len(sts)-1]
}

func TestBrowserJob(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("installer " + strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".msi")))
	}))
	defer srv.Close()

	t.Run("postpone, close, update, reopen", func(t *testing.T) {
		f := &fakeBrowserHost{installed: map[string]string{"Google Chrome": "1.0", "Microsoft Edge": "2.0.1"},
			running: map[string]bool{"chrome.exe": true, "msedge.exe": true}, answers: []bool{true, false}}
		sts, last := runBrowserJob(t, f, browserTestJob(srv.URL), srv)
		if last.State != agentproto.SoftwareCompleted || !strings.Contains(last.Message, "Google Chrome 1.0 → 2.0") ||
			!strings.Contains(last.Message, "Microsoft Edge 2.0.1 is up to date") || !strings.Contains(last.Message, "Mozilla Firefox is not installed") {
			t.Fatalf("result: %+v", last)
		}
		if len(f.prompts) != 2 || !slices.Equal(f.prompts[0].Apps, []string{"Google Chrome"}) || f.prompts[0].Kind != PromptBrowser ||
			f.prompts[0].PostponesLeft != 2 || f.prompts[1].PostponesLeft != 1 || f.prompts[0].CountdownSeconds != 60 {
			t.Errorf("prompts: %+v", f.prompts)
		}
		if len(f.closed) != 1 || f.closed[0].Process != "chrome" || f.closed[0].RestoreArgs != "--restore-last-session" {
			t.Errorf("closed: %+v", f.closed)
		}
		if !slices.Equal(f.launched, []string{"browsers-7"}) || !f.running["msedge.exe"] {
			t.Errorf("launched %v, edge still running %v", f.launched, f.running["msedge.exe"])
		}
		if !slices.ContainsFunc(sts, func(s agentproto.SoftwareStatus) bool {
			return s.State == agentproto.SoftwareWaiting && strings.Contains(s.Message, "postponed by the user")
		}) {
			t.Errorf("no postponed status: %+v", sts)
		}
		if last.Version != "Google Chrome 2.0" {
			t.Errorf("version %q", last.Version)
		}
	})

	t.Run("not running: no prompt", func(t *testing.T) {
		f := &fakeBrowserHost{installed: map[string]string{"Mozilla Firefox": "1.5"}, running: map[string]bool{}}
		_, last := runBrowserJob(t, f, browserTestJob(srv.URL), srv)
		if last.State != agentproto.SoftwareCompleted || len(f.prompts) != 0 || len(f.launched) != 0 {
			t.Errorf("%+v prompts %d launched %v", last, len(f.prompts), f.launched)
		}
	})

	t.Run("all up to date", func(t *testing.T) {
		f := &fakeBrowserHost{installed: map[string]string{"Google Chrome": "2.0", "Microsoft Edge": "3.0"}, running: map[string]bool{"chrome.exe": true}}
		_, last := runBrowserJob(t, f, browserTestJob(srv.URL), srv)
		if last.State != agentproto.SoftwareSkipped || len(f.prompts) != 0 || f.killed != 0 {
			t.Errorf("%+v", last)
		}
	})

	t.Run("install fails, the browser comes back anyway", func(t *testing.T) {
		job := browserTestJob(srv.URL)
		f := &fakeBrowserHost{installed: map[string]string{"Google Chrome": "1.0"}, running: map[string]bool{"chrome.exe": true},
			failURL: job.Browsers[0].Installer.URL}
		_, last := runBrowserJob(t, f, job, srv)
		if last.State != agentproto.SoftwareFailed || !strings.Contains(last.Message, "1603") || last.ExitCode == nil || *last.ExitCode != 1603 {
			t.Errorf("%+v", last)
		}
		if len(f.launched) != 1 {
			t.Error("not reopened after a failed install")
		}
	})

	t.Run("hash mismatch fails before closing anything", func(t *testing.T) {
		job := browserTestJob(srv.URL)
		job.Browsers[0].Installer.SHA256 = strings.Repeat("0", 64)
		f := &fakeBrowserHost{installed: map[string]string{"Google Chrome": "1.0"}, running: map[string]bool{"chrome.exe": true}}
		_, last := runBrowserJob(t, f, job, srv)
		if last.State != agentproto.SoftwareFailed || !strings.Contains(last.Message, "SHA-256") || len(f.prompts) != 0 || f.killed != 0 {
			t.Errorf("%+v", last)
		}
	})
}

func TestCheckBrowserJob(t *testing.T) {
	job := browserTestJob("https://example.com")
	if msg := checkSoftwareJob(job); msg != "" {
		t.Fatal(msg)
	}
	bad := browserTestJob("https://example.com")
	bad.Browsers[0].RestoreArgs = "--load-extension=evil"
	if checkSoftwareJob(bad) == "" {
		t.Error("arbitrary restore arguments accepted")
	}
	bad = browserTestJob("https://example.com")
	bad.Browsers[1].Process = `..\cmd.exe /c`
	if checkSoftwareJob(bad) == "" {
		t.Error("bad process name accepted")
	}
	bad = browserTestJob("http://example.com")
	if checkSoftwareJob(bad) == "" {
		t.Error("http accepted")
	}
}

// hangingInstall waits, like the Firefox installer, until its helper
// process is gone.
type hangingInstall struct{ fakeBrowserHost }

func (h *hangingInstall) Install(ctx context.Context, _ agentproto.Installer, _ string) (RunResult, error) {
	for {
		if len(h.Running([]string{"default-browser-agent.exe"})) == 0 {
			return RunResult{Ran: true}, nil
		}
		select {
		case <-ctx.Done():
			return RunResult{}, errors.New("the installer did not finish in time")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestInstallBrowserEndsFirefoxHelpers(t *testing.T) {
	grace, every := firefoxHelperGrace, firefoxHelperEvery
	firefoxHelperGrace, firefoxHelperEvery = 20*time.Millisecond, 10*time.Millisecond
	defer func() { firefoxHelperGrace, firefoxHelperEvery = grace, every }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h := &hangingInstall{fakeBrowserHost{running: map[string]bool{"default-browser-agent.exe": true, "firefox.exe": true}}}
	if _, err := installBrowser(ctx, h, h, agentproto.BrowserUpdate{Firefox: true}, "x"); err != nil {
		t.Fatal(err)
	}
	if h.killedN() != 2 {
		t.Errorf("killed %d", h.killedN())
	}

	// Chrome's installer is left alone.
	h = &hangingInstall{fakeBrowserHost{running: map[string]bool{"default-browser-agent.exe": true}}}
	sctx, scancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer scancel()
	if _, err := installBrowser(sctx, h, h, agentproto.BrowserUpdate{}, "x"); err == nil || h.killedN() != 0 {
		t.Errorf("chrome: %v, killed %d", err, h.killedN())
	}
}

func (f *fakeBrowserHost) killedN() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}
