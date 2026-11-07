package agent

// Browser updates: bring Chrome, Edge and Firefox to the version the server
// names. When a browser to update is open, the agent asks the logged-on user
// (with the postpone settings of the patch policy), lets the tray app close
// the browser gracefully so it saves its session, stops what is left,
// installs, and lets the tray app open the browser again with its tabs.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// ProcessControl finds and stops programs by executable name (chrome.exe),
// in every session.
type ProcessControl interface {
	Running(names []string) []string // the names that run
	Kill(names []string) int         // stops them; returns how many processes
}

// CloseApp is a program the tray app closes and opens again.
type CloseApp struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Process     string `json:"process"` // without .exe
	RestoreArgs string `json:"restoreArgs,omitempty"`
	Firefox     bool   `json:"firefox,omitempty"`
}

// AppCloser closes programs in the users' sessions and opens them again
// (the tray app). The Notifier may implement it.
type AppCloser interface {
	// CloseApps asks every session to close the apps and waits up to wait;
	// it returns the keys of the apps that were open and are now closed.
	CloseApps(ctx context.Context, id string, apps []CloseApp, wait time.Duration) []string
	// LaunchApps opens what CloseApps closed for id again.
	LaunchApps(ctx context.Context, id string)
}

const (
	browserCountdown   = 60
	browserCloseWait   = 30 * time.Second
	browserInstallTime = 30 * time.Minute
	// While postponed the agent reports again this often, so the server
	// sees the job is alive.
	browserHeartbeat = 10 * time.Minute
)

var (
	processNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\.exe$`)
	browserKeyRE  = regexp.MustCompile(`^[a-z]{2,20}$`)
)

func checkBrowserJob(job agentproto.SoftwareJob) string {
	if len(job.Browsers) == 0 || len(job.Browsers) > 5 {
		return "the job names no browsers"
	}
	for _, b := range job.Browsers {
		if !browserKeyRE.MatchString(b.Key) || !processNameRE.MatchString(b.Process) || b.Version == "" || b.Detect.Name == "" {
			return "invalid browser " + strconv.Quote(b.Key)
		}
		if b.RestoreArgs != "" && b.RestoreArgs != "--restore-last-session" {
			return "invalid restore arguments for " + b.Key
		}
		if msg := checkSoftwareJob(agentproto.SoftwareJob{Action: agentproto.ActionInstall, Installer: &b.Installer}); msg != "" {
			return b.Name + ": " + msg
		}
		if b.Installer.URL == "" {
			return b.Name + ": the installer needs a download URL"
		}
	}
	return ""
}

func (a *Agent) processes() ProcessControl {
	if a.Processes == nil {
		a.Processes = newPlatformProcessControl()
	}
	return a.Processes
}

type browserItem struct {
	b    agentproto.BrowserUpdate
	from string
	file string
}

func (a *Agent) doBrowserJob(ctx context.Context, r *softwareRun, dir string) (string, string) {
	be := a.softwareBackend()
	var todo []browserItem
	var notes []string
	for _, b := range r.job.Browsers {
		ok, v, err := be.Find(ctx, b.Detect)
		switch {
		case err != nil:
			return agentproto.SoftwareFailed, err.Error()
		case !ok:
			notes = append(notes, b.Name+" is not installed")
		case compareVersions(v, b.Version) >= 0:
			notes = append(notes, b.Name+" "+v+" is up to date")
		default:
			todo = append(todo, browserItem{b: b, from: v})
		}
	}
	if len(todo) == 0 {
		return agentproto.SoftwareSkipped, strings.Join(notes, "; ")
	}

	// Download first, so the browsers are closed as briefly as possible.
	r.report(agentproto.SoftwareDownloading, browserNames(todo))
	for i := range todo {
		sub := filepath.Join(dir, todo[i].b.Key)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return agentproto.SoftwareFailed, err.Error()
		}
		_ = restrictDir(dir)
		todo[i].file = filepath.Join(sub, installerFileName(todo[i].b.Installer))
		if err := a.downloadPublic(ctx, todo[i].b.Installer, todo[i].file); err != nil {
			return agentproto.SoftwareFailed, todo[i].b.Name + ": download: " + err.Error()
		}
	}

	procs := a.processes()
	exes := func() []string {
		var out []string
		for _, it := range todo {
			out = append(out, it.b.Process)
		}
		return out
	}()
	running := func() []browserItem {
		if procs == nil {
			return nil
		}
		on := procs.Running(exes)
		var out []browserItem
		for _, it := range todo {
			if slices.ContainsFunc(on, func(n string) bool { return strings.EqualFold(n, it.b.Process) }) {
				out = append(out, it)
			}
		}
		return out
	}
	open := running()
	if len(open) > 0 {
		var ok bool
		if open, ok = a.askBrowserUser(ctx, r, open, running); !ok {
			return agentproto.SoftwareFailed, "the agent stopped"
		}
	}

	closer, _ := a.Notify.(AppCloser)
	closeID := "browsers-" + strconv.FormatInt(r.job.JobID, 10)
	reopen := false
	if len(open) > 0 {
		if closer != nil {
			var apps []CloseApp
			for _, it := range open {
				apps = append(apps, CloseApp{Key: it.b.Key, Name: it.b.Name, Process: strings.TrimSuffix(strings.ToLower(it.b.Process), ".exe"),
					RestoreArgs: it.b.RestoreArgs, Firefox: it.b.Firefox})
			}
			r.report(agentproto.SoftwareInstalling, "closing "+browserNames(open))
			reopen = len(closer.CloseApps(ctx, closeID, apps, browserCloseWait)) > 0
		}
	}
	if procs != nil {
		if n := procs.Kill(exes); n > 0 {
			a.Log.Info("stopped browser processes", "count", n)
		}
	}

	r.report(agentproto.SoftwareInstalling, browserNames(todo))
	var failed, done []string
	var output []string
	a.installMu.Lock()
	for _, it := range todo {
		cctx, cancel := context.WithTimeout(ctx, browserInstallTime)
		res, err := be.Install(cctx, it.b.Installer, it.file)
		cancel()
		r.st.RebootRequired = r.st.RebootRequired || res.Reboot
		if res.Ran {
			code := res.ExitCode
			r.st.ExitCode = &code
		}
		if res.Output != "" {
			output = append(output, "== "+it.b.Name+" ==\n"+tailString(res.Output, softwareOutputMax/len(todo)))
		}
		if err != nil {
			failed = append(failed, it.b.Name+": "+err.Error())
			continue
		}
		done = append(done, it.b.Key)
	}
	a.installMu.Unlock()
	r.st.Output = strings.Join(output, "\n")
	if reopen && closer != nil {
		closer.LaunchApps(ctx, closeID)
	}

	var versions []string
	for _, it := range todo {
		ok, v, _ := be.Find(ctx, it.b.Detect)
		switch {
		case !slices.Contains(done, it.b.Key):
		case ok && compareVersions(v, it.b.Version) >= 0:
			notes = append(notes, fmt.Sprintf("%s %s → %s", it.b.Name, it.from, v))
		case ok:
			failed = append(failed, fmt.Sprintf("%s is still at %s after the install", it.b.Name, v))
		default:
			failed = append(failed, it.b.Name+" no longer shows up after the install")
		}
		if ok {
			versions = append(versions, it.b.Name+" "+v)
		}
	}
	r.st.Version = tailString(strings.Join(versions, ", "), 100)
	if len(failed) > 0 {
		return agentproto.SoftwareFailed, strings.Join(append(failed, notes...), "; ")
	}
	return agentproto.SoftwareCompleted, strings.Join(notes, "; ")
}

// askBrowserUser shows the pop-up until the user accepts, the postpones
// run out, nobody answers or the browsers were closed meanwhile. It returns
// the browsers that are still open; false when the agent stops.
func (a *Agent) askBrowserUser(ctx context.Context, r *softwareRun, open []browserItem, running func() []browserItem) ([]browserItem, bool) {
	n := a.Notify
	if n == nil {
		return open, true
	}
	set := agentproto.UserPrompt{MaxPostpones: 3, PostponeMinutes: 60, CountdownSeconds: browserCountdown}
	if r.job.Prompt != nil {
		set = *r.job.Prompt
	}
	cfg := a.patchConfig()
	minute := a.Minute
	if minute <= 0 {
		minute = time.Minute
	}
	for used, asked := 0, 1; ; asked++ {
		left := max(set.MaxPostpones-used, 0)
		var names []string
		for _, it := range open {
			names = append(names, it.b.Name)
		}
		p := Prompt{
			ID: fmt.Sprintf("b%d-%d", r.job.JobID, asked), Kind: PromptBrowser, Apps: names,
			CanPostpone: left > 0, PostponesLeft: left, PostponeMinutes: max(set.PostponeMinutes, 1),
			CountdownSeconds: max(set.CountdownSeconds, 10), ProductName: cfg.ProductName, AccentColor: cfg.AccentColor,
		}
		r.report(agentproto.SoftwareWaiting, "asking the user to close "+strings.Join(names, ", "))
		ans, err := n.Ask(ctx, p)
		if ctx.Err() != nil {
			return nil, false
		}
		if err != nil {
			if !errors.Is(err, ErrNoUser) {
				a.Log.Warn("user prompt", "err", err)
			}
			return open, true
		}
		if !ans.Postpone || !p.CanPostpone {
			return open, true
		}
		used++
		wait := time.Duration(p.PostponeMinutes) * minute
		until := time.Now().Add(wait)
		for {
			r.report(agentproto.SoftwareWaiting, fmt.Sprintf("postponed by the user until %s UTC (%d of %d)",
				until.UTC().Format("15:04"), used, set.MaxPostpones))
			step := min(time.Until(until), browserHeartbeat)
			if step <= 0 {
				break
			}
			if !sleep(ctx, step) {
				return nil, false
			}
		}
		if open = running(); len(open) == 0 {
			return nil, true // closed meanwhile: update without asking
		}
	}
}

func browserNames(items []browserItem) string {
	var names []string
	for _, it := range items {
		names = append(names, it.b.Name)
	}
	return strings.Join(names, ", ")
}
