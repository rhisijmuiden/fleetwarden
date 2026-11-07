package server

// Browser updates: a deployment with action "browsers" brings Google
// Chrome, Microsoft Edge and Mozilla Firefox to the newest version in the
// winget catalog, and the browser report shows the versions per device.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

type browserDef struct {
	Key         string
	Name        string // display name in Apps & features (Chrome, Edge)
	WingetID    string
	Process     string
	RestoreArgs string
	Firefox     bool
}

var browserDefs = []browserDef{
	{Key: "chrome", Name: "Google Chrome", WingetID: "Google.Chrome", Process: "chrome.exe", RestoreArgs: "--restore-last-session"},
	{Key: "edge", Name: "Microsoft Edge", WingetID: "Microsoft.Edge", Process: "msedge.exe", RestoreArgs: "--restore-last-session"},
	{Key: "firefox", Name: "Mozilla Firefox", WingetID: "Mozilla.Firefox", Process: "firefox.exe", Firefox: true},
}

func browserDefByKey(key string) (browserDef, bool) {
	for _, b := range browserDefs {
		if b.Key == key {
			return b, true
		}
	}
	return browserDef{}, false
}

// firefoxNameRE reads the edition and language from Firefox's display name:
// "Mozilla Firefox (x64 nl)", "Mozilla Firefox ESR (x64 en-US)".
var firefoxNameRE = regexp.MustCompile(`^Mozilla Firefox( ESR)? \((?:x64|x86|arm64|ARM64|AArch64)? ?([A-Za-z]{2,3}(?:-[A-Za-z]{2,4})?)\)$`)

// firefoxVariant picks the winget package for an installed Firefox, so an
// update keeps its edition and language.
type firefoxVariant struct {
	WingetID string
	Prefix   string // display name prefix for detection
}

func firefoxVariantOf(name string) firefoxVariant {
	v := firefoxVariant{WingetID: "Mozilla.Firefox", Prefix: "Mozilla Firefox ("}
	m := firefoxNameRE.FindStringSubmatch(name)
	if m == nil {
		return v
	}
	if m[1] != "" {
		v = firefoxVariant{WingetID: "Mozilla.Firefox.ESR", Prefix: "Mozilla Firefox ESR ("}
	}
	if !strings.EqualFold(m[2], "en-US") {
		v.WingetID += "." + m[2]
	}
	return v
}

// buildBrowserJob resolves the newest installer of each browser for the
// device, and the postpone settings of its patch policy.
func (s *Server) buildBrowserJob(ctx context.Context, d store.DueJob, job agentproto.SoftwareJob) (agentproto.SoftwareJob, error) {
	job.Name = "Browser updates"
	prompt := agentproto.UserPrompt{MaxPostpones: 3, PostponeMinutes: 60, CountdownSeconds: 60}
	if devs, err := s.st.PatchDevices(ctx, []string{d.DeviceID}); err == nil && len(devs) == 1 && devs[0].PolicyID != nil {
		if p, err := s.st.PatchPolicyByID(ctx, *devs[0].PolicyID); err == nil {
			prompt.MaxPostpones, prompt.PostponeMinutes = p.MaxPostpones, p.PostponeMinutes
		}
	}
	job.Prompt = &prompt
	job.TimeoutMinutes = 60 + prompt.MaxPostpones*prompt.PostponeMinutes

	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var problems []string
	for _, key := range d.Deployment.Browsers {
		def, ok := browserDefByKey(key)
		if !ok {
			continue
		}
		id := def.WingetID
		det := agentproto.Detect{Name: def.Name, Exact: true, MachineOnly: true}
		if def.Firefox {
			v := firefoxVariantOf("")
			if names, err := s.st.DeviceSoftwareLike(ctx, d.DeviceID, "Mozilla Firefox"); err == nil {
				for _, n := range names {
					if firefoxNameRE.MatchString(n) {
						v = firefoxVariantOf(n)
						break
					}
				}
			}
			id = v.WingetID
			det = agentproto.Detect{Name: v.Prefix, MachineOnly: true}
		}
		m, err := s.winget.Manifest(rctx, id, "")
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: winget catalog: %v", def.Name, err))
			continue
		}
		c, err := m.Resolve(d.Arch)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", def.Name, err))
			continue
		}
		job.Browsers = append(job.Browsers, agentproto.BrowserUpdate{Key: def.Key, Name: def.Name, Version: m.PackageVersion,
			Installer: c.Installer, Detect: det, Process: def.Process, RestoreArgs: def.RestoreArgs, Firefox: def.Firefox})
	}
	if len(job.Browsers) == 0 {
		if len(problems) == 0 {
			return job, errors.New("no browsers to update")
		}
		return job, errors.New(strings.Join(problems, "; "))
	}
	if len(problems) > 0 {
		s.log.Warn("browser update", "device", d.DeviceID, "skipped", strings.Join(problems, "; "))
	}
	return job, nil
}

// ---- report ----

type browserLatest struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Latest   string `json:"latest"`
	ESR      string `json:"esr,omitempty"`
	Devices  int    `json:"devices"`  // with the browser
	UpToDate int    `json:"upToDate"` // of those
}

type browserVersion struct {
	Version  string `json:"version"`
	Edition  string `json:"edition,omitempty"` // Firefox: ESR
	UpToDate bool   `json:"upToDate"`
}

type browserDeviceRow struct {
	store.BrowserDevice
	Online   bool                      `json:"online"`
	Browsers map[string]browserVersion `json:"browsers"`
	Outdated bool                      `json:"outdated"`
}

type browserReport struct {
	Catalog  catalog.Status     `json:"catalog"`
	Browsers []browserLatest    `json:"browsers"`
	Devices  []browserDeviceRow `json:"devices"`
}

func (s *Server) handleBrowserReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, err := s.st.BrowserInventory(ctx, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	latest := func(id string) string {
		p, err := s.winget.Get(ctx, id)
		if err != nil {
			return ""
		}
		return p.Version
	}
	rep := browserReport{Catalog: s.winget.Status(), Devices: []browserDeviceRow{}}
	esr := latest("Mozilla.Firefox.ESR")
	for _, def := range browserDefs {
		b := browserLatest{Key: def.Key, Name: def.Name, Latest: latest(def.WingetID)}
		if def.Firefox {
			b.ESR = esr
		}
		rep.Browsers = append(rep.Browsers, b)
	}
	for _, d := range devs {
		row := browserDeviceRow{BrowserDevice: d, Online: s.hub.online(d.ID), Browsers: map[string]browserVersion{}}
		for i, def := range browserDefs {
			var found *store.BrowserEntry
			for j, e := range d.Entries {
				if (def.Firefox && strings.HasPrefix(e.Name, "Mozilla Firefox")) || e.Name == def.Name {
					if found == nil || catalog.CompareVersions(e.Version, found.Version) > 0 {
						found = &d.Entries[j]
					}
				}
			}
			if found == nil {
				continue
			}
			want, edition := rep.Browsers[i].Latest, ""
			if def.Firefox && strings.HasPrefix(found.Name, "Mozilla Firefox ESR") {
				want, edition = esr, "ESR"
			}
			v := browserVersion{Version: found.Version, Edition: edition, UpToDate: want == "" || catalog.CompareVersions(found.Version, want) >= 0}
			row.Browsers[def.Key] = v
			rep.Browsers[i].Devices++
			if v.UpToDate {
				rep.Browsers[i].UpToDate++
			} else {
				row.Outdated = true
			}
		}
		rep.Devices = append(rep.Devices, row)
	}
	writeJSON(w, http.StatusOK, rep)
}

// validBrowsers keeps the known browser keys of a request, in a fixed order.
func validBrowsers(keys []string) []string {
	var out []string
	for _, def := range browserDefs {
		if slices.Contains(keys, def.Key) {
			out = append(out, def.Key)
		}
	}
	return out
}
