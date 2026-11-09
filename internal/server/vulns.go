package server

// Vulnerabilities (CVE) per device (phase 13).
//
// Once a day the server reads four public sources: the NVD data feeds
// (every CVE with its CVSS score, and the affected versions of the Windows
// programs FleetWarden recognises), CISA's Known Exploited Vulnerabilities
// catalog, the Debian security tracker and Microsoft's security update
// guide. Nothing about the devices leaves the server. Each device is then
// matched against them:
//
//   - Windows: the build against the build that fixes a CVE for its
//     release, the missing updates (KB) against the CVEs they fix, and the
//     installed programs against the NVD's affected versions;
//   - Debian and Proxmox VE: each package's source version against the
//     version that fixes a CVE in the device's release.
//
// The fix is what FleetWarden can roll out: the missing updates (a patch
// job) or the newest version of a program (a software deployment).

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
	"github.com/rhisijmuiden/fleetwarden/internal/vulndb"
)

// Vulnerability sources.
const (
	vulnSourceNVD    = "nvd"
	vulnSourceKEV    = "kev"
	vulnSourceDebian = "debian"
	vulnSourceMSRC   = "msrc"
)

const (
	vulnSyncAge   = 24 * time.Hour
	vulnRetryAge  = 2 * time.Hour
	vulnNVDFull   = 7 * 24 * time.Hour // the modified feed covers 8 days
	vulnSyncTime  = 3 * time.Hour
	vulnChunk     = 5000
	vulnFetchMax  = 1 << 30 // one source file, uncompressed
	vulnFixMaxCVE = 200
)

// vulnURLs are the addresses of the sources.
type vulnURLs struct {
	NVD    string // directory of the feeds
	KEV    string
	Debian string
	MSRC   string // cvrf/v3.0
}

var defaultVulnURLs = vulnURLs{
	NVD:    "https://nvd.nist.gov/feeds/json/cve/2.0/",
	KEV:    "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json",
	Debian: "https://security-tracker.debian.org/tracker/data/json",
	MSRC:   "https://api.msrc.microsoft.com/cvrf/v3.0/",
}

// vulnSync is the state of the source reader and the matcher.
type vulnSync struct {
	mu       sync.Mutex
	running  bool
	matching bool
	step     string // what the reader is doing, for the interface
	lastRun  time.Time
	// urls, client and firstYear are replaced by tests.
	urls      *vulnURLs
	client    *http.Client
	firstYear int
}

// vulnSettings is the setting "vulnerabilities".
type vulnSettings struct {
	Enabled    bool `json:"enabled"`
	NVD        bool `json:"nvd"`
	KEV        bool `json:"kev"`
	Debian     bool `json:"debian"`
	MSRC       bool `json:"msrc"`
	MSRCMonths int  `json:"msrcMonths"` // monthly Microsoft releases read
}

func defaultVulnSettings() vulnSettings {
	return vulnSettings{Enabled: true, NVD: true, KEV: true, Debian: true, MSRC: true, MSRCMonths: 12}
}

func (s *Server) loadVulnSettings(ctx context.Context) vulnSettings {
	v := defaultVulnSettings()
	_ = s.st.GetSetting(ctx, "vulnerabilities", &v)
	if v.MSRCMonths < 3 || v.MSRCMonths > 36 {
		v.MSRCMonths = 12
	}
	return v
}

func (v vulnSettings) sourceOn(src string) bool {
	switch src {
	case vulnSourceNVD:
		return v.NVD
	case vulnSourceKEV:
		return v.KEV
	case vulnSourceDebian:
		return v.Debian
	case vulnSourceMSRC:
		return v.MSRC
	}
	return false
}

func (s *Server) vulnURLs() vulnURLs {
	if s.vulns.urls != nil {
		return *s.vulns.urls
	}
	return defaultVulnURLs
}

func (s *Server) vulnClient() *http.Client {
	if s.vulns.client != nil {
		return s.vulns.client
	}
	return s.driverClient() // https only, also after redirects
}

func (s *Server) setVulnStep(step string) {
	s.vulns.mu.Lock()
	s.vulns.step = step
	s.vulns.mu.Unlock()
}

// fetchVuln opens a source file; gzip files are unpacked.
func (s *Server) fetchVuln(ctx context.Context, link string, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FleetWarden/"+version.Version)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := s.vulnClient().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	body := io.ReadCloser(res.Body)
	if strings.HasSuffix(req.URL.Path, ".gz") {
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			res.Body.Close()
			return nil, err
		}
		body = struct {
			io.Reader
			io.Closer
		}{zr, res.Body}
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(body, vulnFetchMax), body}, nil
}

// vulnTick starts the daily read of the sources and matches devices whose
// inventory or updates changed.
func (s *Server) vulnTick(ctx context.Context) {
	set := s.loadVulnSettings(ctx)
	if !set.Enabled {
		return
	}
	s.vulns.mu.Lock()
	due := !s.vulns.running && s.now().Sub(s.vulns.lastRun) >= time.Hour
	s.vulns.mu.Unlock()
	if due {
		s.startVulnSync(false)
	}
	s.startVulnMatch()
}

// startVulnSync reads the sources in the background.
func (s *Server) startVulnSync(force bool) bool {
	s.vulns.mu.Lock()
	if s.vulns.running {
		s.vulns.mu.Unlock()
		return false
	}
	s.vulns.running, s.vulns.lastRun = true, s.now()
	s.vulns.mu.Unlock()
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		ctx, cancel := context.WithTimeout(s.bgCtx, vulnSyncTime)
		defer cancel()
		changed := s.syncVulns(ctx, force)
		s.vulns.mu.Lock()
		s.vulns.running, s.vulns.step = false, ""
		s.vulns.mu.Unlock()
		if changed {
			if err := s.st.MarkAllVulnDirty(ctx); err != nil {
				s.log.Warn("vulnerabilities", "err", err)
			}
			s.startVulnMatch()
		}
	}()
	return true
}

// syncVulns reads each source that is due; it reports whether data changed.
func (s *Server) syncVulns(ctx context.Context, force bool) bool {
	set := s.loadVulnSettings(ctx)
	sources, err := s.st.VulnSources(ctx)
	if err != nil {
		s.log.Warn("vulnerability sources", "err", err)
		return false
	}
	changed := false
	for _, src := range []string{vulnSourceKEV, vulnSourceMSRC, vulnSourceDebian, vulnSourceNVD} {
		if !set.sourceOn(src) || ctx.Err() != nil {
			continue
		}
		st := sources[src]
		if !force && st.SyncedAt != nil && s.now().Sub(*st.SyncedAt) < vulnSyncAge {
			continue
		}
		if !force && st.Error != "" && st.CheckedAt != nil && s.now().Sub(*st.CheckedAt) < vulnRetryAge {
			continue
		}
		var err error
		switch src {
		case vulnSourceKEV:
			err = s.syncKEV(ctx)
		case vulnSourceMSRC:
			err = s.syncMSRC(ctx, set.MSRCMonths, st)
		case vulnSourceDebian:
			err = s.syncDebian(ctx)
		case vulnSourceNVD:
			err = s.syncNVD(ctx, st)
		}
		if err != nil {
			s.log.Warn("vulnerability source", "source", src, "err", err)
			_ = s.st.SetVulnSourceError(context.WithoutCancel(ctx), src, cleanText(err.Error(), 500))
			continue
		}
		s.log.Info("vulnerability source read", "source", src)
		changed = true
	}
	return changed
}

func (s *Server) syncKEV(ctx context.Context) error {
	s.setVulnStep(vulnSourceKEV)
	body, err := s.fetchVuln(ctx, s.vulnURLs().KEV, "application/json")
	if err != nil {
		return err
	}
	defer body.Close()
	list, err := vulndb.ParseKEV(body)
	if err != nil {
		return err
	}
	rows := make([]store.VulnKEV, 0, len(list))
	for _, k := range list {
		rows = append(rows, store.VulnKEV{CVE: k.CVE, Name: k.Name, Added: k.Added, Due: k.Due, Ransomware: k.Ransomware})
	}
	if err := s.st.SaveKEV(ctx, rows); err != nil {
		return err
	}
	return s.st.SetVulnSourceOK(ctx, vulnSourceKEV, len(rows), nil)
}

func (s *Server) syncDebian(ctx context.Context) error {
	s.setVulnStep(vulnSourceDebian)
	body, err := s.fetchVuln(ctx, s.vulnURLs().Debian, "application/json")
	if err != nil {
		return err
	}
	defer body.Close()
	releases := map[string]bool{}
	for _, r := range vulndb.SupportedDebianReleases {
		releases[r] = true
	}
	var entries []store.VulnDebian
	summaries := map[string]string{}
	err = vulndb.ParseDebian(body, releases, func(e vulndb.DebianEntry) error {
		entries = append(entries, store.VulnDebian{Release: e.Release, Package: e.Package, CVE: e.CVE, Status: e.Status,
			FixedVersion: e.FixedVersion, Urgency: e.Urgency, NoDSA: e.NoDSA})
		if _, ok := summaries[e.CVE]; !ok {
			summaries[e.CVE] = vulndb.Clip(e.Summary, 300)
		}
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("the security tracker lists nothing for the supported releases")
	}
	if err := s.st.SaveDebian(ctx, entries, summaries); err != nil {
		return err
	}
	return s.st.SetVulnSourceOK(ctx, vulnSourceDebian, len(entries), nil)
}

// msrcState remembers which monthly releases were read, by their revision.
type msrcState struct {
	Months map[string]time.Time `json:"months"`
}

func (s *Server) syncMSRC(ctx context.Context, months int, src store.VulnSource) error {
	s.setVulnStep(vulnSourceMSRC)
	base := s.vulnURLs().MSRC
	body, err := s.fetchVuln(ctx, base+"updates", "application/json")
	if err != nil {
		return err
	}
	docs, err := vulndb.ParseMSRCUpdates(body)
	body.Close()
	if err != nil {
		return err
	}
	sort.Slice(docs, func(i, j int) bool {
		a, _ := vulndb.MSRCMonth(docs[i].ID)
		b, _ := vulndb.MSRCMonth(docs[j].ID)
		return a.After(b)
	})
	if len(docs) > months {
		docs = docs[:months]
	}
	var state msrcState
	if len(src.State) > 0 {
		_ = json.Unmarshal(src.State, &state)
	}
	if state.Months == nil || src.SyncedAt == nil {
		state.Months = map[string]time.Time{}
	}
	keep := make([]string, 0, len(docs))
	for _, d := range docs {
		keep = append(keep, d.ID)
		if t, ok := state.Months[d.ID]; ok && t.Equal(d.Updated) {
			continue
		}
		s.setVulnStep(vulnSourceMSRC + " " + d.ID)
		if err := s.syncMSRCMonth(ctx, base, d.ID); err != nil {
			return fmt.Errorf("%s: %w", d.ID, err)
		}
		state.Months[d.ID] = d.Updated
		// Progress survives an interrupted read.
		if err := s.st.SetVulnSourceState(ctx, vulnSourceMSRC, state); err != nil {
			return err
		}
	}
	for m := range state.Months {
		if !slices.Contains(keep, m) {
			delete(state.Months, m)
		}
	}
	if err := s.st.PruneMSRC(ctx, keep); err != nil {
		return err
	}
	n, err := s.st.CountVulnRows(ctx, "vuln_msrc")
	if err != nil {
		return err
	}
	return s.st.SetVulnSourceOK(ctx, vulnSourceMSRC, n, state)
}

func (s *Server) syncMSRCMonth(ctx context.Context, base, id string) error {
	body, err := s.fetchVuln(ctx, base+"cvrf/"+id, "application/json")
	if err != nil {
		return err
	}
	defer body.Close()
	var vulns []store.VulnMSRC
	err = vulndb.ParseCVRF(body, func(v vulndb.MSRCVuln) error {
		row := store.VulnMSRC{CVE: v.CVE, Title: v.Title, Score: v.Score, Severity: v.Severity, Exploited: v.Exploited}
		for _, f := range v.Fixes {
			row.Fixes = append(row.Fixes, store.VulnMSRCFix{Product: f.Product, KB: f.KB, FixedBuild: f.FixedBuild, Supersedes: f.Supersedes})
		}
		vulns = append(vulns, row)
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	return s.st.SaveMSRCMonth(ctx, id, vulns)
}

// nvdState says when all yearly feeds were last read, and for which list
// of programs.
type nvdState struct {
	FullAt   *time.Time `json:"fullAt"`
	Products int        `json:"products"`
}

func (s *Server) syncNVD(ctx context.Context, src store.VulnSource) error {
	var state nvdState
	if len(src.State) > 0 {
		_ = json.Unmarshal(src.State, &state)
	}
	base := s.vulnURLs().NVD
	full := state.FullAt == nil || state.Products != vulndb.ProductsVersion || s.now().Sub(*state.FullAt) > vulnNVDFull
	var feeds []string
	if full {
		first := 2002
		if s.vulns.firstYear > 0 {
			first = s.vulns.firstYear
		}
		for y := first; y <= s.now().Year(); y++ {
			feeds = append(feeds, strconv.Itoa(y))
		}
	}
	feeds = append(feeds, "modified")
	for _, f := range feeds {
		s.setVulnStep(vulnSourceNVD + " " + f)
		if err := s.readNVDFeed(ctx, base+"nvdcve-2.0-"+f+".json.gz"); err != nil {
			return fmt.Errorf("feed %s: %w", f, err)
		}
	}
	if full {
		now := s.now()
		state = nvdState{FullAt: &now, Products: vulndb.ProductsVersion}
	}
	n, err := s.st.CountVulnRows(ctx, "vulnerabilities")
	if err != nil {
		return err
	}
	return s.st.SetVulnSourceOK(ctx, vulnSourceNVD, n, state)
}

func (s *Server) readNVDFeed(ctx context.Context, link string) error {
	body, err := s.fetchVuln(ctx, link, "")
	if err != nil {
		return err
	}
	defer body.Close()
	chunk := make([]store.VulnCVE, 0, vulnChunk)
	flush := func() error {
		err := s.st.SaveNVD(ctx, chunk)
		chunk = chunk[:0]
		return err
	}
	err = vulndb.ParseNVD(body, vulndb.WantProduct, func(c vulndb.CVE) error {
		row := store.VulnCVE{ID: c.ID, Summary: c.Summary, Score: c.Score, Severity: c.Severity, Vector: c.Vector,
			Published: c.Published, Modified: c.Modified, Rejected: c.Rejected}
		for _, r := range c.Ranges {
			row.Ranges = append(row.Ranges, store.VulnRange{Product: r.Product, Version: r.Version, StartIncl: r.StartIncl,
				StartExcl: r.StartExcl, EndIncl: r.EndIncl, EndExcl: r.EndExcl})
		}
		chunk = append(chunk, row)
		if len(chunk) >= vulnChunk {
			return flush()
		}
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	return flush()
}

// ---- matching ----

// startVulnMatch matches the devices whose data changed, in the background.
func (s *Server) startVulnMatch() {
	s.vulns.mu.Lock()
	if s.vulns.matching {
		s.vulns.mu.Unlock()
		return
	}
	s.vulns.matching = true
	s.vulns.mu.Unlock()
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer func() {
			s.vulns.mu.Lock()
			s.vulns.matching = false
			s.vulns.mu.Unlock()
		}()
		if err := s.matchDirtyDevices(s.bgCtx); err != nil && s.bgCtx.Err() == nil {
			s.log.Warn("vulnerability matching", "err", err)
		}
	}()
}

// matchDirtyDevices matches devices until none is left to do. Nothing is
// matched before a source was read (after a restore the sources are empty).
func (s *Server) matchDirtyDevices(ctx context.Context) error {
	sources, err := s.st.VulnSources(ctx)
	if err != nil {
		return err
	}
	ready := false
	for _, v := range sources {
		ready = ready || v.SyncedAt != nil
	}
	if !ready || !s.loadVulnSettings(ctx).Enabled {
		return nil
	}
	cache := newVulnCache()
	for ctx.Err() == nil {
		devs, err := s.st.VulnDirtyDevices(ctx, 100)
		if err != nil {
			return err
		}
		if len(devs) == 0 {
			return nil
		}
		for _, d := range devs {
			found, err := s.matchDevice(ctx, d, cache)
			if err != nil {
				return fmt.Errorf("device %s: %w", d.ID, err)
			}
			if err := s.st.SaveDeviceVulns(ctx, d.ID, found); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// vulnCache keeps source rows between the devices of one matching run.
type vulnCache struct {
	msrc   map[string][]store.MSRCRow
	ranges map[string][]store.VulnRange
}

func newVulnCache() *vulnCache {
	return &vulnCache{msrc: map[string][]store.MSRCRow{}, ranges: map[string][]store.VulnRange{}}
}

// matchDevice finds the vulnerabilities of one device.
func (s *Server) matchDevice(ctx context.Context, d store.VulnDevice, cache *vulnCache) ([]store.DeviceVuln, error) {
	sw, err := s.st.VulnDeviceSoftware(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	patches, err := s.st.DevicePatches(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	var found []store.DeviceVuln
	switch d.Platform {
	case "windows":
		osVulns, err := s.matchWindowsOS(ctx, d, patches, cache)
		if err != nil {
			return nil, err
		}
		found = append(found, osVulns...)
		prog, err := s.matchPrograms(ctx, sw, cache)
		if err != nil {
			return nil, err
		}
		found = append(found, prog...)
	case "linux":
		pkgs, err := s.matchDebian(ctx, d, sw, patches)
		if err != nil {
			return nil, err
		}
		found = append(found, pkgs...)
	}
	if len(found) == 0 {
		return found, nil
	}
	ids := make([]string, 0, len(found))
	for _, f := range found {
		ids = append(ids, f.CVE)
	}
	rejected, err := s.st.RejectedCVEs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := found[:0]
	for _, f := range found {
		if !rejected[f.CVE] {
			out = append(out, f)
		}
	}
	return out, nil
}

// matchWindowsOS compares the Windows build with the builds that fix CVEs
// in its release, and the missing updates with the CVEs they fix.
func (s *Server) matchWindowsOS(ctx context.Context, d store.VulnDevice, patches []store.PatchRow, cache *vulnCache) ([]store.DeviceVuln, error) {
	var out []store.DeviceVuln
	done := map[string]bool{}
	missingByKB := map[string][]string{} // KB -> update IDs
	for _, p := range patches {
		if kb := strings.TrimPrefix(strings.ToUpper(p.KB), "KB"); kb != "" {
			missingByKB[kb] = append(missingByKB[kb], p.UpdateID)
		}
	}
	product := vulndb.WindowsProduct(d.OSName, d.OSVersion, d.Arch)
	build, ubr, okBuild := vulndb.ParseOSBuild(d.OSBuild)
	if product != "" && okBuild {
		rows, ok := cache.msrc[product]
		if !ok {
			var err error
			if rows, err = s.st.MSRCForProduct(ctx, product); err != nil {
				return nil, err
			}
			cache.msrc[product] = rows
		}
		// Which newer updates replace an update (cumulative updates).
		replacedBy := map[string][]string{}
		for _, r := range rows {
			if r.Supersedes != "" && !slices.Contains(replacedBy[r.Supersedes], r.KB) {
				replacedBy[r.Supersedes] = append(replacedBy[r.Supersedes], r.KB)
			}
		}
		type osFix struct {
			ubr   int
			build string
			kbs   []string
		}
		fixes := map[string]*osFix{}
		var order []string
		for _, r := range rows {
			fb, fu, ok := vulndb.ParseFixedBuild(r.FixedBuild)
			if !ok || fb != build {
				continue
			}
			f := fixes[r.CVE]
			if f == nil {
				f = &osFix{}
				fixes[r.CVE] = f
				order = append(order, r.CVE)
			}
			if fu > f.ubr {
				f.ubr, f.build = fu, r.FixedBuild
			}
			if !slices.Contains(f.kbs, r.KB) {
				f.kbs = append(f.kbs, r.KB)
			}
		}
		installed := fmt.Sprintf("10.0.%d.%d", build, ubr)
		cumulative := cumulativeUpdates(patches)
		for _, cve := range order {
			f := fixes[cve]
			if ubr >= f.ubr {
				continue
			}
			v := store.DeviceVuln{CVE: cve, Kind: "os", Product: windowsLabel(product), Installed: installed, Fixed: f.build, Fix: "update"}
			v.FixRef = updatesFor(f.kbs, replacedBy, missingByKB)
			if len(v.FixRef) == 0 {
				v.FixRef = cumulative
			}
			if len(v.FixRef) > 0 {
				v.Fix = "patch"
			}
			out = append(out, v)
			done[cve] = true
		}
	}
	// Other Microsoft products: the CVEs the missing updates fix.
	if len(missingByKB) > 0 {
		kbs := make([]string, 0, len(missingByKB))
		for kb := range missingByKB {
			kbs = append(kbs, kb)
		}
		rows, err := s.st.MSRCForKBs(ctx, kbs)
		if err != nil {
			return nil, err
		}
		titles := map[string]string{}
		for _, p := range patches {
			if kb := strings.TrimPrefix(strings.ToUpper(p.KB), "KB"); kb != "" {
				titles[kb] = p.Title
			}
		}
		for _, r := range rows {
			if done[r.CVE] {
				continue
			}
			done[r.CVE] = true
			label := cleanText(titles[r.KB], 200)
			if label == "" {
				label = "KB" + r.KB
			}
			out = append(out, store.DeviceVuln{CVE: r.CVE, Kind: "update", Product: label, Fixed: "KB" + r.KB, Fix: "patch",
				FixRef: slices.Clone(missingByKB[r.KB])})
		}
	}
	return out, nil
}

// windowsLabel shortens the MSRC product name for display.
func windowsLabel(product string) string {
	for _, suffix := range []string{" for x64-based Systems", " for ARM64-based Systems", " for 32-bit Systems"} {
		product = strings.TrimSuffix(product, suffix)
	}
	return product
}

// updatesFor returns the missing updates that are, or replace, one of kbs.
func updatesFor(kbs []string, replacedBy, missing map[string][]string) []string {
	var out []string
	seen := map[string]bool{}
	queue := slices.Clone(kbs)
	for len(queue) > 0 && len(seen) < 500 {
		kb := queue[0]
		queue = queue[1:]
		if seen[kb] {
			continue
		}
		seen[kb] = true
		for _, id := range missing[kb] {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		queue = append(queue, replacedBy[kb]...)
	}
	sort.Strings(out)
	return out
}

// cumulativeUpdates are the missing Windows cumulative updates: the newest
// one fixes every older CVE of the build.
func cumulativeUpdates(patches []store.PatchRow) []string {
	var out []string
	for _, p := range patches {
		t := strings.ToLower(p.Title)
		if strings.Contains(t, "cumulative update for windows") || strings.Contains(t, "cumulative update for microsoft server") {
			out = append(out, p.UpdateID)
		}
	}
	sort.Strings(out)
	return out
}

// matchPrograms compares the installed Windows programs with the NVD's
// affected versions.
func (s *Server) matchPrograms(ctx context.Context, sw []store.VulnSoftware, cache *vulnCache) ([]store.DeviceVuln, error) {
	type prog struct {
		p  vulndb.Product
		sw store.VulnSoftware
	}
	var progs []prog
	var need []string
	for _, x := range sw {
		if x.Source == "dpkg" {
			continue
		}
		p, ok := vulndb.MatchProgram(x.Name)
		if !ok {
			continue
		}
		progs = append(progs, prog{p, x})
		if _, cached := cache.ranges[p.CPE]; !cached && !slices.Contains(need, p.CPE) {
			need = append(need, p.CPE)
		}
	}
	if len(need) > 0 {
		rows, err := s.st.RangesForProducts(ctx, need)
		if err != nil {
			return nil, err
		}
		for _, c := range need {
			cache.ranges[c] = nil
		}
		for _, r := range rows {
			cache.ranges[r.Product] = append(cache.ranges[r.Product], r)
		}
	}
	var out []store.DeviceVuln
	for _, pr := range progs {
		ver := vulndb.ProgramVersion(pr.p.CPE, pr.sw.Version)
		if ver == "" {
			continue
		}
		label := pr.p.Name
		fix, ref := "none", []string(nil)
		switch {
		case pr.p.Browser != "":
			fix, ref = "software", []string{"browser:" + pr.p.Browser}
		default:
			if m, ok := s.winget.MatchInstalled(ctx, pr.sw.Name, pr.sw.Publisher, pr.sw.Version); ok {
				fix, ref = "software", []string{m.ID}
			} else if pr.p.WingetID != "" {
				fix, ref = "software", []string{pr.p.WingetID}
			}
		}
		byCVE := map[string]*store.DeviceVuln{}
		var order []string
		for _, r := range cache.ranges[pr.p.CPE] {
			rg := vulndb.Range{Version: r.Version, StartIncl: r.StartIncl, StartExcl: r.StartExcl, EndIncl: r.EndIncl, EndExcl: r.EndExcl}
			if !rg.Affects(ver, vulndb.CompareProgram) {
				continue
			}
			v := byCVE[r.CVE]
			if v == nil {
				v = &store.DeviceVuln{CVE: r.CVE, Kind: "program", Product: label, Installed: pr.sw.Version, Fix: fix, FixRef: ref}
				byCVE[r.CVE] = v
				order = append(order, r.CVE)
			}
			if f := rg.Fixed(); f != "" && (v.Fixed == "" || vulndb.CompareProgram(f, v.Fixed) > 0) {
				v.Fixed = f
			}
		}
		for _, c := range order {
			out = append(out, *byCVE[c])
		}
	}
	return out, nil
}

// matchDebian compares the source versions of a Debian or Proxmox VE
// device's packages with the security tracker.
func (s *Server) matchDebian(ctx context.Context, d store.VulnDevice, sw []store.VulnSoftware, patches []store.PatchRow) ([]store.DeviceVuln, error) {
	release := vulndb.DebianRelease(d.OSName, d.OSVersion)
	if release == "" {
		return nil, nil
	}
	type src struct {
		version  string
		binaries []string
	}
	srcs := map[string]*src{}
	for _, x := range sw {
		if x.Source != "dpkg" {
			continue
		}
		name, ver := x.SrcPackage, x.SrcVersion
		if name == "" {
			name = x.Name
		}
		if ver == "" {
			ver = vulndb.SourceVersion(x.Version)
		}
		e := srcs[name]
		if e == nil {
			e = &src{version: ver}
			srcs[name] = e
		} else if vulndb.CompareDebian(ver, e.version) < 0 {
			e.version = ver // the oldest installed build counts
		}
		e.binaries = append(e.binaries, x.Name)
	}
	if len(srcs) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	rows, err := s.st.DebianVulns(ctx, release, names)
	if err != nil {
		return nil, err
	}
	offered := map[string]string{} // binary package -> version on offer
	for _, p := range patches {
		offered[p.UpdateID] = p.NewVersion
	}
	var out []store.DeviceVuln
	for _, r := range rows {
		e := srcs[r.Package]
		if e == nil {
			continue
		}
		if r.Status == "resolved" && vulndb.CompareDebian(e.version, r.FixedVersion) >= 0 {
			continue
		}
		v := store.DeviceVuln{CVE: r.CVE, Kind: "package", Product: r.Package, Installed: e.version, Fixed: r.FixedVersion, Fix: "none"}
		if r.FixedVersion != "" {
			v.Fix = "update"
			for _, b := range e.binaries {
				nv, ok := offered[b]
				if ok && (nv == "" || vulndb.CompareDebian(vulndb.SourceVersion(nv), r.FixedVersion) >= 0) {
					v.FixRef = append(v.FixRef, b)
				}
			}
			if len(v.FixRef) > 0 {
				v.Fix = "patch"
				sort.Strings(v.FixRef)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- API ----

func (s *Server) vulnRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesManage, 0, h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermVulnsManage, 0, h) }
	mux.Handle("GET /api/v1/vulnerabilities/summary", view(s.handleVulnSummary))
	mux.Handle("GET /api/v1/vulnerabilities", view(s.handleListVulns))
	mux.Handle("GET /api/v1/vulnerabilities/devices", view(s.handleVulnDevices))
	mux.Handle("GET /api/v1/vulnerabilities/cve/{cve}", view(s.handleGetVuln))
	mux.Handle("POST /api/v1/vulnerabilities/fix", manage(s.handleVulnFix))
	mux.Handle("GET /api/v1/vulnerabilities/exceptions", view(s.handleListVulnExceptions))
	mux.Handle("POST /api/v1/vulnerabilities/exceptions", admin(s.handleCreateVulnException))
	mux.Handle("DELETE /api/v1/vulnerabilities/exceptions/{id}", admin(s.handleDeleteVulnException))
	mux.Handle("GET /api/v1/vulnerabilities/settings", view(s.handleGetVulnSettings))
	mux.Handle("PUT /api/v1/vulnerabilities/settings", admin(s.handlePutVulnSettings))
	mux.Handle("POST /api/v1/vulnerabilities/sync", admin(s.handleVulnSync))
	mux.Handle("GET /api/v1/devices/{id}/vulnerabilities", view(s.handleDeviceVulns))
}

// vulnStatus is the state of the sources for the interface.
type vulnStatus struct {
	Enabled  bool                `json:"enabled"`
	Running  bool                `json:"running"`
	Matching bool                `json:"matching"`
	Step     string              `json:"step"`
	Sources  []store.VulnSource  `json:"sources"`
	Settings *vulnSettings       `json:"settings,omitempty"`
	Products []vulnProductStatus `json:"products,omitempty"`
}

type vulnProductStatus struct {
	CPE    string `json:"cpe"`
	Name   string `json:"name"`
	Winget string `json:"winget"`
}

func (s *Server) vulnStatus(ctx context.Context) (vulnStatus, error) {
	set := s.loadVulnSettings(ctx)
	out := vulnStatus{Enabled: set.Enabled}
	s.vulns.mu.Lock()
	out.Running, out.Matching, out.Step = s.vulns.running, s.vulns.matching, s.vulns.step
	s.vulns.mu.Unlock()
	src, err := s.st.VulnSources(ctx)
	if err != nil {
		return out, err
	}
	for _, name := range []string{vulnSourceNVD, vulnSourceKEV, vulnSourceMSRC, vulnSourceDebian} {
		v, ok := src[name]
		if !ok {
			v = store.VulnSource{Source: name}
		}
		out.Sources = append(out.Sources, v)
	}
	return out, nil
}

type vulnSummaryView struct {
	store.VulnStats
	Status vulnStatus `json:"status"`
}

func (s *Server) handleVulnSummary(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.VulnStats(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	status, err := s.vulnStatus(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vulnSummaryView{VulnStats: st, Status: status})
}

func validSeverity(v string) bool {
	switch v {
	case "", vulndb.SeverityCritical, vulndb.SeverityHigh, vulndb.SeverityMedium, vulndb.SeverityLow, vulndb.SeverityNone:
		return true
	}
	return false
}

func (s *Server) handleListVulns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	vq := store.VulnQuery{Scope: s.scope(r), Search: cleanText(q.Get("search"), 100), Severity: q.Get("severity"),
		KEV: q.Get("kev") == "1", Fixable: q.Get("fixable") == "1", Kind: q.Get("kind"), Accepted: q.Get("accepted") == "1"}
	if !validSeverity(vq.Severity) {
		apiError(w, http.StatusBadRequest, "invalid_severity")
		return
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		vq.Limit = v
	}
	switch vq.Kind {
	case "", "os", "update", "package", "program":
	default:
		apiError(w, http.StatusBadRequest, "invalid_kind")
		return
	}
	if v := q.Get("group"); v != "" {
		g, err := strconv.ParseInt(v, 10, 64)
		if err != nil || !vq.Scope.Allows(g) {
			apiError(w, http.StatusBadRequest, "unknown_group")
			return
		}
		vq.GroupID = g
	}
	list, err := s.st.ListVulns(r.Context(), vq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleVulnDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var group int64
	if v := q.Get("group"); v != "" {
		g, err := strconv.ParseInt(v, 10, 64)
		if err != nil || !s.scope(r).Allows(g) {
			apiError(w, http.StatusBadRequest, "unknown_group")
			return
		}
		group = g
	}
	list, err := s.st.VulnDeviceSummaries(r.Context(), s.scope(r), cleanText(q.Get("search"), 100), group)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type vulnDetailView struct {
	store.VulnDetail
	Devices    []store.VulnDeviceRow `json:"devices"`
	Exceptions []store.VulnException `json:"exceptions"`
	NVDURL     string                `json:"nvdUrl"`
}

func (s *Server) handleGetVuln(w http.ResponseWriter, r *http.Request) {
	id := strings.ToUpper(r.PathValue("cve"))
	if !vulndb.ValidCVE(id) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	sc := s.scope(r)
	devs, err := s.st.VulnAffected(r.Context(), sc, id, nil)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	d, err := s.st.VulnByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		if len(devs) == 0 {
			apiError(w, http.StatusNotFound, "not_found")
			return
		}
		d = store.VulnDetail{CVE: id, Severity: vulndb.SeverityNone, Ranges: []store.VulnRange{}, Updates: []string{}}
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range devs {
		devs[i].Online = s.hub.online(devs[i].DeviceID)
	}
	all, err := s.st.ListVulnExceptions(r.Context(), sc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ex := []store.VulnException{}
	for _, x := range all {
		if x.CVE == id {
			ex = append(ex, x)
		}
	}
	writeJSON(w, http.StatusOK, vulnDetailView{VulnDetail: d, Devices: devs, Exceptions: ex,
		NVDURL: "https://nvd.nist.gov/vuln/detail/" + id})
}

type deviceVulnsView struct {
	Vulnerabilities []store.DeviceVulnRow `json:"vulnerabilities"`
	CheckedAt       *time.Time            `json:"checkedAt"`
	Pending         bool                  `json:"pending"`
	Supported       bool                  `json:"supported"`
}

func (s *Server) handleDeviceVulns(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	rows, err := s.st.DeviceVulns(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := deviceVulnsView{Vulnerabilities: rows}
	out.CheckedAt, out.Pending, _ = s.st.DeviceVulnCheck(r.Context(), d.ID)
	out.Supported = d.Platform == "windows" || vulndb.DebianRelease(d.OSName, d.OSVersion) != ""
	writeJSON(w, http.StatusOK, out)
}

// ---- rolling out fixes ----

type vulnFixReq struct {
	CVEs      []string `json:"cves"`
	DeviceIDs []string `json:"deviceIds"`
}

type vulnFixSkip struct {
	DeviceID string `json:"deviceId"`
	Hostname string `json:"hostname"`
	CVE      string `json:"cve,omitempty"`
	Reason   string `json:"reason"` // offline, run_open, cluster_busy, no_fix, no_permission, catalog_not_loaded, failed
}

type vulnFixDeployment struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Devices int    `json:"devices"`
}

type vulnFixResult struct {
	PatchRuns   int                 `json:"patchRuns"`
	Deployments []vulnFixDeployment `json:"deployments"`
	Skipped     []vulnFixSkip       `json:"skipped"`
}

// handleVulnFix rolls out the fixes of CVEs: a patch job with the missing
// updates on each online device, and a deployment of the newest version of
// each vulnerable program.
func (s *Server) handleVulnFix(w http.ResponseWriter, r *http.Request) {
	var req vulnFixReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.CVEs) == 0 || len(req.CVEs) > vulnFixMaxCVE || len(req.DeviceIDs) > 5000 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	sc := s.scope(r)
	u, _ := currentUser(r)
	role := auth.Role(u.Role)
	ctx := r.Context()
	type devInfo struct {
		hostname string
		groupID  int64
	}
	devs := map[string]devInfo{}
	patchIDs := map[string][]string{}
	software := map[string][]string{} // fix reference -> devices
	var refOrder []string
	out := vulnFixResult{Deployments: []vulnFixDeployment{}, Skipped: []vulnFixSkip{}}
	skip := func(id, cve, reason string) {
		out.Skipped = append(out.Skipped, vulnFixSkip{DeviceID: id, Hostname: devs[id].hostname, CVE: cve, Reason: reason})
	}
	for _, raw := range req.CVEs {
		cve := strings.ToUpper(strings.TrimSpace(raw))
		if !vulndb.ValidCVE(cve) {
			apiError(w, http.StatusBadRequest, "invalid_cve")
			return
		}
		rows, err := s.st.VulnAffected(ctx, sc, cve, req.DeviceIDs)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, row := range rows {
			if row.Accepted {
				continue
			}
			devs[row.DeviceID] = devInfo{hostname: row.Hostname, groupID: row.GroupID}
			switch row.Fix {
			case "patch":
				for _, id := range row.FixRef {
					if !slices.Contains(patchIDs[row.DeviceID], id) {
						patchIDs[row.DeviceID] = append(patchIDs[row.DeviceID], id)
					}
				}
			case "software":
				for _, ref := range row.FixRef {
					if _, ok := software[ref]; !ok {
						refOrder = append(refOrder, ref)
					}
					if !slices.Contains(software[ref], row.DeviceID) {
						software[ref] = append(software[ref], row.DeviceID)
					}
				}
			default:
				skip(row.DeviceID, cve, "no_fix")
			}
		}
	}
	// Patch jobs on the online devices.
	ids := make([]string, 0, len(patchIDs))
	for id := range patchIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d, err := s.st.DeviceByID(ctx, id)
		if err != nil {
			skip(id, "", "failed")
			continue
		}
		if !s.hub.online(id) {
			skip(id, "", "offline")
			continue
		}
		if d.PatchCluster != "" {
			if busy, err := s.st.ClusterBusy(ctx, d.PatchCluster, d.ID); err == nil && busy {
				skip(id, "", "cluster_busy")
				continue
			}
		}
		var pol *store.PatchPolicy
		if g, err := s.st.GroupByID(ctx, d.GroupID); err == nil && g.PatchPolicy != nil {
			if p, err := s.st.PatchPolicyByID(ctx, *g.PatchPolicy); err == nil {
				pol = &p
			}
		}
		upd := patchIDs[id]
		sort.Strings(upd)
		_, err = s.startPatchRun(ctx, id, pol, upd, nil, "manual", u.Username)
		switch {
		case errors.Is(err, store.ErrConflict):
			skip(id, "", "run_open")
		case errors.Is(err, errDeviceOffline):
			skip(id, "", "offline")
		case err != nil:
			s.log.Warn("vulnerability fix", "device", id, "err", err)
			skip(id, "", "failed")
		default:
			out.PatchRuns++
		}
	}
	// Deployments of the newest program versions.
	for _, ref := range refOrder {
		targets := software[ref]
		if !role.Has(auth.PermSoftwareDeploy) {
			for _, id := range targets {
				skip(id, "", "no_permission")
			}
			continue
		}
		dep, err := s.vulnFixDeployment(ctx, ref, targets, u.Username)
		if err != nil {
			reason := "failed"
			if errors.Is(err, errCatalogNotLoaded) {
				reason = "catalog_not_loaded"
			} else {
				s.log.Warn("vulnerability fix", "package", ref, "err", err)
			}
			for _, id := range targets {
				skip(id, "", reason)
			}
			continue
		}
		out.Deployments = append(out.Deployments, dep)
	}
	if len(out.Deployments) > 0 {
		go s.sendSoftwareJobs(context.WithoutCancel(ctx), s.hub.onlineIDs())
	}
	s.audit(r, "vulnerabilities.fix", strings.Join(req.CVEs, ", "), true, map[string]any{"patchRuns": out.PatchRuns,
		"deployments": len(out.Deployments), "skipped": len(out.Skipped)})
	writeJSON(w, http.StatusOK, out)
}

var errCatalogNotLoaded = errors.New("the winget catalog is not loaded")

// vulnFixDeployment deploys the newest version of a program: a browser
// update, or the library's winget package (added when missing).
func (s *Server) vulnFixDeployment(ctx context.Context, ref string, devices []string, by string) (vulnFixDeployment, error) {
	if s.winget.Status().Packages == 0 {
		return vulnFixDeployment{}, errCatalogNotLoaded
	}
	d := store.NewDeployment{Deployment: store.Deployment{Ongoing: false, ExpireDays: 7, RequestedBy: by, StartAt: s.now(),
		Platform: "windows"}, DeviceIDs: devices}
	if key, ok := strings.CutPrefix(ref, "browser:"); ok {
		def, ok := browserDefByKey(key)
		if !ok {
			return vulnFixDeployment{}, fmt.Errorf("unknown browser %q", key)
		}
		d.Action, d.Browsers, d.Name = agentproto.ActionBrowsers, []string{key}, "Vulnerability fix: "+def.Name
	} else {
		p, err := s.st.SoftwarePackageByWinget(ctx, ref)
		if errors.Is(err, store.ErrNotFound) {
			p, err = s.addWingetPackage(ctx, ref, by)
		}
		if err != nil {
			return vulnFixDeployment{}, err
		}
		d.Action, d.PackageID, d.Name = agentproto.ActionInstall, &p.ID, "Vulnerability fix: "+p.Name
	}
	id, jobs, err := s.st.CreateDeployment(ctx, d)
	if err != nil {
		return vulnFixDeployment{}, err
	}
	return vulnFixDeployment{ID: id, Name: d.Name, Devices: jobs}, nil
}

// addWingetPackage adds a winget package to the library (newest version).
func (s *Server) addWingetPackage(ctx context.Context, wingetID, by string) (store.SoftwarePackage, error) {
	cp, err := s.winget.Get(ctx, wingetID)
	if err != nil {
		return store.SoftwarePackage{}, fmt.Errorf("winget package %s: %w", wingetID, err)
	}
	p := store.SoftwarePackage{Name: cp.Name, Source: store.SourceWinget, WingetID: cp.ID, CreatedBy: by,
		Description: "Added to roll out a vulnerability fix."}
	if bad := s.validPackage(ctx, &p); bad != "" {
		return p, errors.New(bad)
	}
	created, err := s.st.CreateSoftwarePackage(ctx, p)
	if errors.Is(err, store.ErrConflict) {
		p.Name = cleanText(cp.Name+" ("+cp.ID+")", 200)
		created, err = s.st.CreateSoftwarePackage(ctx, p)
	}
	return created, err
}

// ---- accepted risks ----

func (s *Server) handleListVulnExceptions(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListVulnExceptions(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type vulnExceptionReq struct {
	CVE       string  `json:"cve"`
	DeviceID  string  `json:"deviceId"`
	Reason    string  `json:"reason"`
	ExpiresAt *string `json:"expiresAt"` // YYYY-MM-DD or RFC 3339
}

func (s *Server) handleCreateVulnException(w http.ResponseWriter, r *http.Request) {
	var req vulnExceptionReq
	if !decode(w, r, &req) {
		return
	}
	x := store.VulnException{CVE: strings.ToUpper(strings.TrimSpace(req.CVE)), Reason: cleanText(req.Reason, 1000)}
	if !vulndb.ValidCVE(x.CVE) {
		apiError(w, http.StatusBadRequest, "invalid_cve")
		return
	}
	if x.Reason == "" {
		apiError(w, http.StatusBadRequest, "reason_required")
		return
	}
	sc := s.scope(r)
	if req.DeviceID != "" {
		found, err := s.st.DevicesInScope(r.Context(), sc, []string{req.DeviceID})
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if _, ok := found[req.DeviceID]; !ok {
			apiError(w, http.StatusBadRequest, "unknown_device")
			return
		}
		x.DeviceID = &req.DeviceID
	} else if !sc.All {
		apiError(w, http.StatusForbidden, "forbidden") // all devices is beyond a scoped user
		return
	}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			t, err = time.Parse("2006-01-02", *req.ExpiresAt)
			t = t.Add(24*time.Hour - time.Second)
		}
		if err != nil || !t.After(s.now()) || t.After(s.now().AddDate(3, 0, 0)) {
			apiError(w, http.StatusBadRequest, "invalid_expiry")
			return
		}
		x.ExpiresAt = &t
	}
	u, _ := currentUser(r)
	x.CreatedBy = u.Username
	id, err := s.st.CreateVulnException(r.Context(), x)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "exception_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "vulnerabilities.accept", x.CVE, true, map[string]any{"device": req.DeviceID, "reason": x.Reason, "expires": x.ExpiresAt})
	created, err := s.st.VulnExceptionByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDeleteVulnException(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	x, err := s.st.VulnExceptionByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sc := s.scope(r)
	if !sc.All {
		if x.DeviceID == nil {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		found, err := s.st.DevicesInScope(r.Context(), sc, []string{*x.DeviceID})
		if err != nil || len(found) == 0 {
			apiError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	if err := s.st.DeleteVulnException(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "vulnerabilities.accept.remove", x.CVE, true, map[string]any{"device": x.DeviceID})
	w.WriteHeader(http.StatusNoContent)
}

// ---- settings ----

func (s *Server) handleGetVulnSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.vulnStatus(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	set := s.loadVulnSettings(r.Context())
	st.Settings = &set
	for _, p := range vulndb.Products {
		st.Products = append(st.Products, vulnProductStatus{CPE: p.CPE, Name: p.Name, Winget: p.WingetID})
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handlePutVulnSettings(w http.ResponseWriter, r *http.Request) {
	var req vulnSettings
	if !decode(w, r, &req) {
		return
	}
	if req.MSRCMonths < 3 || req.MSRCMonths > 36 {
		apiError(w, http.StatusBadRequest, "invalid_months")
		return
	}
	old := s.loadVulnSettings(r.Context())
	if err := s.st.PutSetting(r.Context(), "vulnerabilities", req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "vulnerabilities.settings", "", true, map[string]any{"enabled": req.Enabled, "nvd": req.NVD, "kev": req.KEV,
		"debian": req.Debian, "msrc": req.MSRC, "msrcMonths": req.MSRCMonths})
	if req.Enabled && (!old.Enabled || req.MSRCMonths > old.MSRCMonths) {
		s.startVulnSync(false)
	}
	s.handleGetVulnSettings(w, r)
}

func (s *Server) handleVulnSync(w http.ResponseWriter, r *http.Request) {
	if !s.loadVulnSettings(r.Context()).Enabled {
		apiError(w, http.StatusConflict, "disabled")
		return
	}
	if !s.startVulnSync(true) {
		apiError(w, http.StatusConflict, "sync_running")
		return
	}
	s.audit(r, "vulnerabilities.sync", "", true, nil)
	w.WriteHeader(http.StatusAccepted)
}
