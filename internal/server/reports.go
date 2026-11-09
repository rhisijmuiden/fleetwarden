package server

// Reports: compliance per group, missing patches and outdated software,
// shown in the web interface, exported as PDF or CSV and mailed on a
// schedule.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/reports"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const keyReports = "reports"

// Report kinds.
const (
	reportCompliance = "compliance"
	reportPatches    = "patches"
	reportSoftware   = "software"
	reportAssets     = "assets"
)

var reportKinds = []string{reportCompliance, reportPatches, reportSoftware, reportAssets}

// reportSettings define compliance and which software the reports skip.
type reportSettings struct {
	OfflineDays     int      `json:"offlineDays"`     // a device not seen this long is not compliant
	PatchGraceDays  int      `json:"patchGraceDays"`  // security updates may be missing this long
	DiskFreePercent int      `json:"diskFreePercent"` // minimum free space on every volume
	IgnoredSoftware []string `json:"ignoredSoftware"` // winget package IDs left out of the reports
	// ExpiringDays: warranties and licences ending within this many days
	// count as ending soon.
	ExpiringDays int `json:"expiringDays"`
}

func defaultReportSettings() reportSettings {
	return reportSettings{OfflineDays: 7, PatchGraceDays: 14, DiskFreePercent: 10, IgnoredSoftware: []string{}, ExpiringDays: 90}
}

func (s *Server) loadReportSettings(ctx context.Context) reportSettings {
	rs := defaultReportSettings()
	_ = s.st.GetSetting(ctx, keyReports, &rs)
	if rs.IgnoredSoftware == nil {
		rs.IgnoredSoftware = []string{}
	}
	if rs.ExpiringDays == 0 {
		rs.ExpiringDays = 90
	}
	return rs
}

func validReportSettings(rs reportSettings) bool {
	return rs.OfflineDays >= 1 && rs.OfflineDays <= 365 && rs.PatchGraceDays >= 0 && rs.PatchGraceDays <= 365 &&
		rs.DiskFreePercent >= 1 && rs.DiskFreePercent <= 90 && len(rs.IgnoredSoftware) <= 1000 &&
		rs.ExpiringDays >= 1 && rs.ExpiringDays <= 730
}

// minVolumeBytes keeps tiny partitions (EFI, recovery) out of the disk check.
const minVolumeBytes = 2 << 30

type lowVolume struct {
	Mount       string `json:"mount"`
	FreePercent int    `json:"freePercent"`
	FreeBytes   int64  `json:"freeBytes"`
}

func lowVolumes(vols []store.ReportVolume, minPercent int) (low []lowVolume, lowest *lowVolume) {
	for _, v := range vols {
		if v.SizeBytes < minVolumeBytes || v.FreeBytes < 0 {
			continue
		}
		lv := lowVolume{Mount: v.Mount, FreePercent: int(v.FreeBytes * 100 / v.SizeBytes), FreeBytes: v.FreeBytes}
		if lowest == nil || lv.FreePercent < lowest.FreePercent {
			c := lv
			lowest = &c
		}
		if lv.FreePercent < minPercent {
			low = append(low, lv)
		}
	}
	return low, lowest
}

// reportInput is the data every report starts from.
type reportInput struct {
	settings reportSettings
	devices  []store.ReportDevice
	scope    string // "All groups" or the group names
	now      time.Time
}

func (s *Server) reportInput(ctx context.Context, sc store.Scope, groups []int64, lang string) (reportInput, error) {
	rs := s.loadReportSettings(ctx)
	now := s.now()
	devs, err := s.st.ReportDevices(ctx, sc, groups, now.Add(-time.Duration(rs.PatchGraceDays)*24*time.Hour))
	if err != nil {
		return reportInput{}, err
	}
	in := reportInput{settings: rs, devices: devs, now: now, scope: reports.T(lang, "scope.all")}
	if len(groups) > 0 {
		all, err := s.st.ListGroups(ctx, sc)
		if err != nil {
			return in, err
		}
		var names []string
		for _, g := range all {
			if slices.Contains(groups, g.ID) {
				names = append(names, g.Name)
			}
		}
		in.scope = reports.T(lang, "scope.groups", strings.Join(names, ", "))
	} else if !sc.All {
		all, _ := s.st.ListGroups(ctx, sc)
		var names []string
		for _, g := range all {
			names = append(names, g.Name)
		}
		in.scope = reports.T(lang, "scope.groups", strings.Join(names, ", "))
	}
	return in, nil
}

func deviceIDs(devs []store.ReportDevice) []string {
	ids := make([]string, 0, len(devs))
	for _, d := range devs {
		ids = append(ids, d.ID)
	}
	return ids
}

// ---- outdated software ----

type outdatedProgram struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
	PackageID string `json:"packageId"`
	Package   string `json:"package"`
	Latest    string `json:"latest"`
}

// outdatedSoftware matches the Windows programs of the devices to the
// winget catalog. It returns the outdated programs per device and how many
// installations were recognised.
func (s *Server) outdatedSoftware(ctx context.Context, devs []store.ReportDevice, ignored []string) (map[string][]outdatedProgram, int, error) {
	out := map[string][]outdatedProgram{}
	var win []string
	for _, d := range devs {
		if d.Platform == "windows" {
			win = append(win, d.ID)
		}
	}
	if len(win) == 0 || s.winget.Status().Packages == 0 {
		return out, 0, nil
	}
	progs, err := s.st.WindowsPrograms(ctx, win)
	if err != nil {
		return nil, 0, err
	}
	matched := 0
	for _, p := range progs {
		m, ok := s.winget.MatchInstalled(ctx, p.Name, p.Publisher, p.Version)
		if !ok {
			continue
		}
		matched++
		if slices.ContainsFunc(ignored, func(id string) bool { return strings.EqualFold(id, m.ID) }) || !m.Outdated(p.Version) {
			continue
		}
		list := out[p.DeviceID]
		// The same package can show up twice (32- and 64-bit entries); keep the oldest.
		if i := slices.IndexFunc(list, func(o outdatedProgram) bool { return o.PackageID == m.ID }); i >= 0 {
			if catalog.CompareVersions(p.Version, list[i].Version) < 0 {
				list[i].Version, list[i].Name = p.Version, p.Name
			}
			continue
		}
		out[p.DeviceID] = append(list, outdatedProgram{Name: p.Name, Version: p.Version, Publisher: p.Publisher,
			PackageID: m.ID, Package: m.Name, Latest: m.Latest})
	}
	return out, matched, nil
}

// ---- compliance ----

type complianceDevice struct {
	ID          string      `json:"id"`
	Hostname    string      `json:"hostname"`
	GroupID     int64       `json:"groupId"`
	GroupName   string      `json:"groupName"`
	Platform    string      `json:"platform"`
	Online      bool        `json:"online"`
	LastSeenAt  time.Time   `json:"lastSeenAt"`
	Stale       bool        `json:"stale"`
	NotScanned  bool        `json:"notScanned"`
	Security    int         `json:"security"` // security updates missing longer than the grace period
	Outdated    int         `json:"outdated"`
	LowDisk     []lowVolume `json:"lowDisk"`
	LowestDisk  *lowVolume  `json:"lowestDisk"`
	Compliant   bool        `json:"compliant"`
	SoftwareTop []string    `json:"software"` // outdated program names
}

type complianceGroup struct {
	GroupID    int64  `json:"groupId"`
	Name       string `json:"name"`
	Devices    int    `json:"devices"`
	Compliant  int    `json:"compliant"`
	Stale      int    `json:"stale"`
	Patches    int    `json:"patches"` // missing security updates or no scan
	Software   int    `json:"software"`
	Disk       int    `json:"disk"`
	Score      int    `json:"score"`
	NotScanned int    `json:"notScanned"`
}

type complianceReport struct {
	Generated  time.Time          `json:"generated"`
	Scope      string             `json:"scope"`
	Settings   reportSettings     `json:"settings"`
	Catalog    bool               `json:"catalog"` // outdated software could be checked
	Devices    int                `json:"devices"`
	Compliant  int                `json:"compliant"`
	Score      int                `json:"score"`
	Stale      int                `json:"stale"`
	Patches    int                `json:"patches"`
	NotScanned int                `json:"notScanned"`
	Software   int                `json:"software"`
	Disk       int                `json:"disk"`
	Groups     []complianceGroup  `json:"groups"`
	List       []complianceDevice `json:"rows"`
}

func percent(part, total int) int {
	if total == 0 {
		return 100
	}
	return part * 100 / total
}

func (s *Server) complianceReport(ctx context.Context, in reportInput) (complianceReport, error) {
	rs := in.settings
	sw, _, err := s.outdatedSoftware(ctx, in.devices, rs.IgnoredSoftware)
	if err != nil {
		return complianceReport{}, err
	}
	rep := complianceReport{Generated: in.now, Scope: in.scope, Settings: rs, Catalog: s.winget.Status().Packages > 0,
		Groups: []complianceGroup{}, List: []complianceDevice{}}
	groups := map[int64]*complianceGroup{}
	var order []int64
	staleBefore := in.now.Add(-time.Duration(rs.OfflineDays) * 24 * time.Hour)
	for _, d := range in.devices {
		online := s.hub.online(d.ID)
		cd := complianceDevice{ID: d.ID, Hostname: d.Hostname, GroupID: d.GroupID, GroupName: d.GroupName, Platform: d.Platform,
			Online: online, LastSeenAt: d.LastSeenAt, Stale: !online && d.LastSeenAt.Before(staleBefore),
			NotScanned: d.PatchScanAt == nil, Security: d.SecurityOld, Outdated: len(sw[d.ID]), LowDisk: []lowVolume{}}
		low, lowest := lowVolumes(d.Volumes, rs.DiskFreePercent)
		if low != nil {
			cd.LowDisk = low
		}
		cd.LowestDisk = lowest
		for _, p := range sw[d.ID] {
			cd.SoftwareTop = append(cd.SoftwareTop, p.Name)
		}
		cd.Compliant = !cd.Stale && !cd.NotScanned && cd.Security == 0 && cd.Outdated == 0 && len(cd.LowDisk) == 0
		g := groups[d.GroupID]
		if g == nil {
			g = &complianceGroup{GroupID: d.GroupID, Name: d.GroupName}
			groups[d.GroupID] = g
			order = append(order, d.GroupID)
		}
		g.Devices++
		rep.Devices++
		count := func(cond bool, a, b *int) {
			if cond {
				*a++
				*b++
			}
		}
		count(cd.Compliant, &g.Compliant, &rep.Compliant)
		count(cd.Stale, &g.Stale, &rep.Stale)
		count(cd.Security > 0 || cd.NotScanned, &g.Patches, &rep.Patches)
		count(cd.NotScanned, &g.NotScanned, &rep.NotScanned)
		count(cd.Outdated > 0, &g.Software, &rep.Software)
		count(len(cd.LowDisk) > 0, &g.Disk, &rep.Disk)
		rep.List = append(rep.List, cd)
	}
	for _, id := range order {
		g := groups[id]
		g.Score = percent(g.Compliant, g.Devices)
		rep.Groups = append(rep.Groups, *g)
	}
	sort.SliceStable(rep.Groups, func(i, j int) bool { return strings.ToLower(rep.Groups[i].Name) < strings.ToLower(rep.Groups[j].Name) })
	rep.Score = percent(rep.Compliant, rep.Devices)
	return rep, nil
}

func scoreTone(score int) string {
	switch {
	case score >= 90:
		return reports.ToneSuccess
	case score >= 70:
		return reports.ToneWarning
	}
	return reports.ToneDanger
}

func countTone(n int) string {
	if n > 0 {
		return reports.ToneWarning
	}
	return ""
}

func (c complianceDevice) reasons(lang string, offlineDays int) []string {
	var out []string
	if c.Stale {
		if c.LastSeenAt.IsZero() {
			out = append(out, reports.T(lang, "compliance.reason.never"))
		} else {
			out = append(out, reports.T(lang, "compliance.reason.stale", reports.ShortDate(&c.LastSeenAt)))
		}
	}
	if c.NotScanned {
		out = append(out, reports.T(lang, "compliance.reason.scan"))
	}
	if c.Security > 0 {
		out = append(out, reports.T(lang, "compliance.reason.patch", c.Security))
	}
	if c.Outdated > 0 {
		out = append(out, reports.T(lang, "compliance.reason.sw", c.Outdated))
	}
	for _, v := range c.LowDisk {
		out = append(out, reports.T(lang, "compliance.reason.disk", v.Mount, v.FreePercent))
	}
	return out
}

func (r complianceReport) output(lang string) reportOutput {
	rs := r.Settings
	o := reportOutput{Kind: reportCompliance, Title: reports.T(lang, "compliance.title")}
	sum := reports.Section{Title: reports.T(lang, "compliance.summary"),
		Text: reports.T(lang, "compliance.text", rs.OfflineDays, rs.PatchGraceDays, rs.DiskFreePercent)}
	if !r.Catalog {
		sum.Text += "\n" + reports.T(lang, "software.noCatalog")
	}
	sum.Stats = []reports.Stat{
		{Label: reports.T(lang, "compliance.score"), Value: fmt.Sprintf("%d%%", r.Score), Sub: reports.T(lang, "compliance.ofDevices", r.Compliant, r.Devices), Tone: scoreTone(r.Score)},
		{Label: reports.T(lang, "compliance.patches"), Value: strconv.Itoa(r.Patches), Tone: countTone(r.Patches)},
		{Label: reports.T(lang, "compliance.software"), Value: strconv.Itoa(r.Software), Tone: countTone(r.Software)},
		{Label: reports.T(lang, "compliance.disk"), Value: strconv.Itoa(r.Disk), Tone: countTone(r.Disk)},
		{Label: reports.T(lang, "compliance.stale", rs.OfflineDays), Value: strconv.Itoa(r.Stale), Tone: countTone(r.Stale)},
	}
	groups := reports.Table{Title: reports.T(lang, "compliance.byGroup"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.group"), Width: 3}, {Label: reports.T(lang, "col.devices"), Width: 1, Right: true},
		{Label: reports.T(lang, "col.score"), Width: 1.2, Right: true}, {Label: reports.T(lang, "col.patches"), Width: 1.5, Right: true},
		{Label: reports.T(lang, "col.software"), Width: 1.5, Right: true}, {Label: reports.T(lang, "col.disk"), Width: 1.2, Right: true},
		{Label: reports.T(lang, "col.stale"), Width: 1.2, Right: true}}}
	for _, g := range r.Groups {
		groups.Rows = append(groups.Rows, []reports.Cell{{Text: g.Name}, {Text: strconv.Itoa(g.Devices)},
			{Text: fmt.Sprintf("%d%%", g.Score), Tone: scoreTone(g.Score)}, {Text: strconv.Itoa(g.Patches), Tone: countTone(g.Patches)},
			{Text: strconv.Itoa(g.Software), Tone: countTone(g.Software)}, {Text: strconv.Itoa(g.Disk), Tone: countTone(g.Disk)},
			{Text: strconv.Itoa(g.Stale), Tone: countTone(g.Stale)}})
	}
	bad := reports.Table{Title: reports.T(lang, "compliance.nonCompliant"), Empty: reports.T(lang, "compliance.allCompliant"),
		Columns: []reports.Column{{Label: reports.T(lang, "col.device"), Width: 2}, {Label: reports.T(lang, "col.group"), Width: 1.6},
			{Label: reports.T(lang, "col.lastSeen"), Width: 1.1}, {Label: reports.T(lang, "col.reasons"), Width: 5}}}
	o.CSVHeader = []string{reports.T(lang, "col.device"), reports.T(lang, "col.group"), reports.T(lang, "col.platform"),
		reports.T(lang, "col.lastSeen"), reports.T(lang, "col.compliantYesNo"), reports.T(lang, "col.patches"),
		reports.T(lang, "col.software"), reports.T(lang, "col.freeSpace"), reports.T(lang, "col.reasons")}
	for _, d := range r.List {
		reasons := d.reasons(lang, rs.OfflineDays)
		if !d.Compliant {
			bad.Rows = append(bad.Rows, []reports.Cell{{Text: d.Hostname}, {Text: d.GroupName}, {Text: reports.ShortDate(&d.LastSeenAt)},
				{Text: strings.Join(reasons, "; "), Tone: reports.ToneDanger}})
		}
		yes, free := reports.T(lang, "no"), ""
		if d.Compliant {
			yes = reports.T(lang, "yes")
		}
		if d.LowestDisk != nil {
			free = fmt.Sprintf("%s %d%%", d.LowestDisk.Mount, d.LowestDisk.FreePercent)
		}
		o.CSVRows = append(o.CSVRows, []string{d.Hostname, d.GroupName, d.Platform, d.LastSeenAt.UTC().Format(time.RFC3339), yes,
			strconv.Itoa(d.Security), strconv.Itoa(d.Outdated), free, strings.Join(reasons, "; ")})
	}
	sum.Tables = []reports.Table{groups}
	o.Sections = []reports.Section{sum, {Tables: []reports.Table{bad}}}
	o.Facts = []mail.Fact{
		{Label: reports.T(lang, "compliance.score"), Value: fmt.Sprintf("%d%% (%s)", r.Score, reports.T(lang, "compliance.ofDevices", r.Compliant, r.Devices)), Tone: scoreTone(r.Score)},
	}
	return o
}

// ---- missing patches ----

type patchUpdateRow struct {
	UpdateID   string     `json:"updateId"`
	Title      string     `json:"title"`
	KB         string     `json:"kb"`
	Category   string     `json:"category"`
	Severity   string     `json:"severity"`
	ReleasedAt *time.Time `json:"releasedAt"`
	FirstSeen  time.Time  `json:"firstSeenAt"`
	Devices    int        `json:"devices"`
}

type patchDeviceRow struct {
	ID            string     `json:"id"`
	Hostname      string     `json:"hostname"`
	GroupName     string     `json:"groupName"`
	Platform      string     `json:"platform"`
	Online        bool       `json:"online"`
	Missing       int        `json:"missing"`
	Security      int        `json:"security"`
	RebootPending bool       `json:"rebootPending"`
	PatchScanAt   *time.Time `json:"patchScanAt"`
}

type patchesReport struct {
	Generated       time.Time        `json:"generated"`
	Scope           string           `json:"scope"`
	Devices         int              `json:"devices"`
	Scanned         int              `json:"scanned"`
	UpToDate        int              `json:"upToDate"`
	DevicesMissing  int              `json:"devicesMissing"`
	DevicesSecurity int              `json:"devicesSecurity"`
	RebootPending   int              `json:"rebootPending"`
	Updates         []patchUpdateRow `json:"updates"`
	List            []patchDeviceRow `json:"rows"`
	missing         []store.MissingPatch
	hosts           map[string]store.ReportDevice
}

func (s *Server) patchesReport(ctx context.Context, in reportInput) (patchesReport, error) {
	rep := patchesReport{Generated: in.now, Scope: in.scope, Updates: []patchUpdateRow{}, List: []patchDeviceRow{},
		hosts: map[string]store.ReportDevice{}}
	missing, err := s.st.MissingPatches(ctx, deviceIDs(in.devices))
	if err != nil {
		return rep, err
	}
	rep.missing = missing
	for _, d := range in.devices {
		rep.hosts[d.ID] = d
		rep.Devices++
		if d.PatchScanAt != nil {
			rep.Scanned++
			if d.Missing == 0 {
				rep.UpToDate++
			}
		}
		if d.Missing > 0 {
			rep.DevicesMissing++
		}
		if d.Security > 0 {
			rep.DevicesSecurity++
		}
		if d.RebootPending {
			rep.RebootPending++
		}
		if d.Missing > 0 || d.RebootPending {
			rep.List = append(rep.List, patchDeviceRow{ID: d.ID, Hostname: d.Hostname, GroupName: d.GroupName, Platform: d.Platform,
				Online: s.hub.online(d.ID), Missing: d.Missing, Security: d.Security, RebootPending: d.RebootPending, PatchScanAt: d.PatchScanAt})
		}
	}
	idx := map[string]int{}
	for _, m := range missing {
		i, ok := idx[m.UpdateID]
		if !ok {
			i = len(rep.Updates)
			idx[m.UpdateID] = i
			rep.Updates = append(rep.Updates, patchUpdateRow{UpdateID: m.UpdateID, Title: m.Title, KB: m.KB, Category: m.Category,
				Severity: m.Severity, ReleasedAt: m.ReleasedAt, FirstSeen: m.FirstSeen})
		}
		u := &rep.Updates[i]
		u.Devices++
		if m.FirstSeen.Before(u.FirstSeen) {
			u.FirstSeen = m.FirstSeen
		}
	}
	catRank := func(c string) int {
		i := slices.Index([]string{"security", "critical", "cumulative", "definition", "driver", "feature", "other"}, c)
		if i < 0 {
			return 99
		}
		return i
	}
	sort.SliceStable(rep.Updates, func(i, j int) bool {
		a, b := rep.Updates[i], rep.Updates[j]
		if catRank(a.Category) != catRank(b.Category) {
			return catRank(a.Category) < catRank(b.Category)
		}
		return a.Devices > b.Devices
	})
	sort.SliceStable(rep.List, func(i, j int) bool {
		a, b := rep.List[i], rep.List[j]
		if a.Security != b.Security {
			return a.Security > b.Security
		}
		return a.Missing > b.Missing
	})
	return rep, nil
}

func categoryText(lang, c string) string {
	t := reports.T(lang, "category."+c)
	if t == "category."+c {
		return c
	}
	return t
}

func (r patchesReport) output(lang string) reportOutput {
	o := reportOutput{Kind: reportPatches, Title: reports.T(lang, "patches.title")}
	sum := reports.Section{Title: reports.T(lang, "patches.summary"), Stats: []reports.Stat{
		{Label: reports.T(lang, "patches.devicesSecurity"), Value: strconv.Itoa(r.DevicesSecurity), Tone: countTone(r.DevicesSecurity),
			Sub: reports.T(lang, "compliance.ofDevices", r.DevicesSecurity, r.Devices)},
		{Label: reports.T(lang, "patches.devicesMissing"), Value: strconv.Itoa(r.DevicesMissing), Tone: countTone(r.DevicesMissing)},
		{Label: reports.T(lang, "patches.updates"), Value: strconv.Itoa(len(r.Updates))},
		{Label: reports.T(lang, "patches.reboot"), Value: strconv.Itoa(r.RebootPending), Tone: countTone(r.RebootPending)},
		{Label: reports.T(lang, "patches.upToDate"), Value: strconv.Itoa(r.UpToDate), Tone: reports.ToneSuccess,
			Sub: reports.T(lang, "compliance.ofDevices", r.UpToDate, r.Devices)},
	}}
	ups := reports.Table{Title: reports.T(lang, "patches.byUpdate"), Empty: reports.T(lang, "patches.none"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.update"), Width: 6}, {Label: reports.T(lang, "col.kb"), Width: 1.2},
		{Label: reports.T(lang, "col.category"), Width: 1.4}, {Label: reports.T(lang, "col.released"), Width: 1.2},
		{Label: reports.T(lang, "col.devices"), Width: 1, Right: true}}}
	for _, u := range r.Updates {
		tone := ""
		if u.Category == "security" || u.Category == "critical" {
			tone = reports.ToneDanger
		}
		ups.Rows = append(ups.Rows, []reports.Cell{{Text: u.Title}, {Text: u.KB}, {Text: categoryText(lang, u.Category), Tone: tone},
			{Text: reports.ShortDate(u.ReleasedAt)}, {Text: strconv.Itoa(u.Devices)}})
	}
	devs := reports.Table{Title: reports.T(lang, "patches.byDevice"), Empty: reports.T(lang, "patches.none"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.device"), Width: 2.4}, {Label: reports.T(lang, "col.group"), Width: 2},
		{Label: reports.T(lang, "col.missing"), Width: 1, Right: true}, {Label: reports.T(lang, "col.security"), Width: 1, Right: true},
		{Label: reports.T(lang, "col.reboot"), Width: 1}, {Label: reports.T(lang, "col.lastScan"), Width: 1.3}}}
	for _, d := range r.List {
		reboot := ""
		if d.RebootPending {
			reboot = reports.T(lang, "yes")
		}
		devs.Rows = append(devs.Rows, []reports.Cell{{Text: d.Hostname}, {Text: d.GroupName}, {Text: strconv.Itoa(d.Missing)},
			{Text: strconv.Itoa(d.Security), Tone: countToneDanger(d.Security)}, {Text: reboot, Tone: countTone(boolInt(d.RebootPending))},
			{Text: reports.ShortDate(d.PatchScanAt)}})
	}
	o.Sections = []reports.Section{sum, {Tables: []reports.Table{ups, devs}}}
	o.CSVHeader = []string{reports.T(lang, "col.device"), reports.T(lang, "col.group"), reports.T(lang, "col.platform"),
		reports.T(lang, "col.update"), reports.T(lang, "col.kb"), reports.T(lang, "col.category"), reports.T(lang, "col.severity"),
		reports.T(lang, "col.released"), reports.T(lang, "col.firstSeen")}
	for _, m := range r.missing {
		d := r.hosts[m.DeviceID]
		o.CSVRows = append(o.CSVRows, []string{d.Hostname, d.GroupName, d.Platform, m.Title, m.KB, categoryText(lang, m.Category),
			m.Severity, reports.ShortDate(m.ReleasedAt), reports.ShortDate(&m.FirstSeen)})
	}
	o.Facts = []mail.Fact{{Label: reports.T(lang, "patches.devicesSecurity"), Value: reports.T(lang, "compliance.ofDevices", r.DevicesSecurity, r.Devices),
		Tone: countToneDanger(r.DevicesSecurity)}}
	return o
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func countToneDanger(n int) string {
	if n > 0 {
		return reports.ToneDanger
	}
	return ""
}

// ---- outdated software ----

type softwarePackageRow struct {
	PackageID string   `json:"packageId"`
	LibraryID *int64   `json:"libraryId"` // library package that installs the newest version
	Name      string   `json:"name"`
	Latest    string   `json:"latest"`
	Versions  []string `json:"versions"`
	Devices   int      `json:"devices"`
}

type softwareDeviceRow struct {
	ID        string            `json:"id"`
	Hostname  string            `json:"hostname"`
	GroupName string            `json:"groupName"`
	Online    bool              `json:"online"`
	Programs  []outdatedProgram `json:"programs"`
}

type softwareReport struct {
	Generated       time.Time            `json:"generated"`
	Scope           string               `json:"scope"`
	Catalog         catalog.Status       `json:"catalog"`
	Devices         int                  `json:"devices"` // Windows devices
	DevicesOutdated int                  `json:"devicesOutdated"`
	Installs        int                  `json:"installs"`
	Matched         int                  `json:"matched"`
	Ignored         []string             `json:"ignored"`
	Packages        []softwarePackageRow `json:"packages"`
	List            []softwareDeviceRow  `json:"rows"`
}

func (s *Server) softwareReport(ctx context.Context, in reportInput) (softwareReport, error) {
	rep := softwareReport{Generated: in.now, Scope: in.scope, Catalog: s.winget.Status(), Ignored: in.settings.IgnoredSoftware,
		Packages: []softwarePackageRow{}, List: []softwareDeviceRow{}}
	sw, matched, err := s.outdatedSoftware(ctx, in.devices, in.settings.IgnoredSoftware)
	if err != nil {
		return rep, err
	}
	rep.Matched = matched
	idx := map[string]int{}
	for _, d := range in.devices {
		if d.Platform != "windows" {
			continue
		}
		rep.Devices++
		progs := sw[d.ID]
		if len(progs) == 0 {
			continue
		}
		rep.DevicesOutdated++
		rep.Installs += len(progs)
		rep.List = append(rep.List, softwareDeviceRow{ID: d.ID, Hostname: d.Hostname, GroupName: d.GroupName, Online: s.hub.online(d.ID), Programs: progs})
		for _, p := range progs {
			i, ok := idx[p.PackageID]
			if !ok {
				i = len(rep.Packages)
				idx[p.PackageID] = i
				rep.Packages = append(rep.Packages, softwarePackageRow{PackageID: p.PackageID, Name: p.Package, Latest: p.Latest})
			}
			row := &rep.Packages[i]
			row.Devices++
			if !slices.Contains(row.Versions, p.Version) {
				row.Versions = append(row.Versions, p.Version)
			}
		}
	}
	lib := map[string]int64{}
	if pkgs, err := s.st.ListSoftwarePackages(ctx, store.Scope{All: true}); err == nil {
		for _, p := range pkgs {
			if p.Source == "winget" && p.Platform == "windows" && p.Version == "" {
				lib[strings.ToLower(p.WingetID)] = p.ID
			}
		}
	}
	for i := range rep.Packages {
		slices.SortFunc(rep.Packages[i].Versions, catalog.CompareVersions)
		if id, ok := lib[strings.ToLower(rep.Packages[i].PackageID)]; ok {
			rep.Packages[i].LibraryID = &id
		}
	}
	sort.SliceStable(rep.Packages, func(i, j int) bool {
		if rep.Packages[i].Devices != rep.Packages[j].Devices {
			return rep.Packages[i].Devices > rep.Packages[j].Devices
		}
		return strings.ToLower(rep.Packages[i].Name) < strings.ToLower(rep.Packages[j].Name)
	})
	sort.SliceStable(rep.List, func(i, j int) bool { return len(rep.List[i].Programs) > len(rep.List[j].Programs) })
	return rep, nil
}

func (r softwareReport) output(lang string) reportOutput {
	o := reportOutput{Kind: reportSoftware, Title: reports.T(lang, "software.title")}
	text := reports.T(lang, "software.note")
	if r.Catalog.Packages == 0 {
		text = reports.T(lang, "software.noCatalog")
	}
	sum := reports.Section{Title: reports.T(lang, "software.summary"), Text: text, Stats: []reports.Stat{
		{Label: reports.T(lang, "software.devices"), Value: strconv.Itoa(r.DevicesOutdated), Tone: countTone(r.DevicesOutdated),
			Sub: reports.T(lang, "compliance.ofDevices", r.DevicesOutdated, r.Devices)},
		{Label: reports.T(lang, "software.programs"), Value: strconv.Itoa(len(r.Packages)), Tone: countTone(len(r.Packages))},
		{Label: reports.T(lang, "software.installs"), Value: strconv.Itoa(r.Installs), Tone: countTone(r.Installs)},
		{Label: reports.T(lang, "software.matched"), Value: strconv.Itoa(r.Matched)},
	}}
	pk := reports.Table{Title: reports.T(lang, "software.byProgram"), Empty: reports.T(lang, "software.none"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.program"), Width: 3}, {Label: reports.T(lang, "col.package"), Width: 2.6},
		{Label: reports.T(lang, "col.installed"), Width: 2.4}, {Label: reports.T(lang, "col.latest"), Width: 1.4},
		{Label: reports.T(lang, "col.devices"), Width: 1, Right: true}}}
	for _, p := range r.Packages {
		pk.Rows = append(pk.Rows, []reports.Cell{{Text: p.Name}, {Text: p.PackageID, Tone: reports.ToneMuted},
			{Text: strings.Join(p.Versions, ", "), Tone: reports.ToneWarning}, {Text: p.Latest}, {Text: strconv.Itoa(p.Devices)}})
	}
	dv := reports.Table{Title: reports.T(lang, "software.byDevice"), Empty: reports.T(lang, "software.none"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.device"), Width: 2}, {Label: reports.T(lang, "col.group"), Width: 1.6},
		{Label: reports.T(lang, "col.program"), Width: 3.4}, {Label: reports.T(lang, "col.installed"), Width: 1.3, Right: true},
		{Label: reports.T(lang, "col.latest"), Width: 1.3, Right: true}}}
	o.CSVHeader = []string{reports.T(lang, "col.device"), reports.T(lang, "col.group"), reports.T(lang, "col.program"),
		reports.T(lang, "col.publisher"), reports.T(lang, "col.installed"), reports.T(lang, "col.latest"), reports.T(lang, "col.package")}
	for _, d := range r.List {
		for i, p := range d.Programs {
			host, group := "", ""
			if i == 0 {
				host, group = d.Hostname, d.GroupName
			}
			dv.Rows = append(dv.Rows, []reports.Cell{{Text: host}, {Text: group}, {Text: p.Name}, {Text: p.Version, Tone: reports.ToneWarning}, {Text: p.Latest}})
			o.CSVRows = append(o.CSVRows, []string{d.Hostname, d.GroupName, p.Name, p.Publisher, p.Version, p.Latest, p.PackageID})
		}
	}
	o.Sections = []reports.Section{sum, {Tables: []reports.Table{pk, dv}}}
	o.Facts = []mail.Fact{{Label: reports.T(lang, "software.devices"), Value: reports.T(lang, "compliance.ofDevices", r.DevicesOutdated, r.Devices),
		Tone: countTone(r.DevicesOutdated)}}
	return o
}

// ---- output ----

// reportOutput is a report ready for PDF, CSV and mail.
type reportOutput struct {
	Kind      string
	Title     string
	Sections  []reports.Section
	CSVHeader []string
	CSVRows   [][]string
	Facts     []mail.Fact
}

// buildReport collects one report.
func (s *Server) buildReport(ctx context.Context, kind string, in reportInput, lang string) (any, reportOutput, error) {
	switch kind {
	case reportCompliance:
		r, err := s.complianceReport(ctx, in)
		return r, r.output(lang), err
	case reportPatches:
		r, err := s.patchesReport(ctx, in)
		return r, r.output(lang), err
	case reportSoftware:
		r, err := s.softwareReport(ctx, in)
		return r, r.output(lang), err
	case reportAssets:
		r, err := s.assetsReport(ctx, in)
		return r, r.output(lang), err
	}
	return nil, reportOutput{}, errors.New("unknown report")
}

// document turns report outputs into one PDF document. With several
// reports each starts with its own heading.
func (s *Server) document(ctx context.Context, title, scope, lang string, outs []reportOutput) reports.Document {
	b := s.loadBranding(ctx)
	doc := reports.Document{Title: title, Subtitle: scope, Product: b.ProductName, Color: b.PrimaryColor, Lang: lang, Generated: s.now().In(time.Local)}
	if a, err := s.st.GetAsset(ctx, "logo"); err == nil {
		doc.Logo = a.Data
	}
	for _, o := range outs {
		secs := o.Sections
		if len(outs) > 1 {
			secs = append([]reports.Section{{Title: o.Title, Heading: true}}, secs...)
		}
		doc.Sections = append(doc.Sections, secs...)
	}
	return doc
}

func reportFileName(kind string, t time.Time, ext string) string {
	return fmt.Sprintf("fleetwarden-%s-%s.%s", kind, t.Format("2006-01-02"), ext)
}

// ---- handlers ----

func (s *Server) reportRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermReportsManage, 0, h) }
	mux.Handle("GET /api/v1/reports/settings", view(s.handleGetReportSettings))
	mux.Handle("PUT /api/v1/reports/settings", manage(s.handlePutReportSettings))
	mux.Handle("POST /api/v1/reports/software/ignore", manage(s.handleIgnoreSoftware))
	mux.Handle("GET /api/v1/reports/schedules", manage(s.handleListReportSchedules))
	mux.Handle("POST /api/v1/reports/schedules", manage(s.handleCreateReportSchedule))
	mux.Handle("PUT /api/v1/reports/schedules/{id}", manage(s.handleUpdateReportSchedule))
	mux.Handle("DELETE /api/v1/reports/schedules/{id}", manage(s.handleDeleteReportSchedule))
	mux.Handle("POST /api/v1/reports/schedules/{id}/send", manage(s.handleSendReportSchedule))
	mux.Handle("GET /api/v1/reports/{kind}", view(s.handleReport))
	mux.Handle("GET /api/v1/reports/{kind}/export", view(s.handleExportReport))
}

// reportGroups reads ?groups=1,2 and keeps groups within the scope.
func (s *Server) reportGroups(r *http.Request) ([]int64, bool) {
	var out []int64
	sc := s.scope(r)
	for _, p := range strings.Split(r.URL.Query().Get("groups"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil || !sc.Allows(id) {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !slices.Contains(reportKinds, kind) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	groups, ok := s.reportGroups(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_group")
		return
	}
	u, _ := currentUser(r)
	lang := reports.Lang(u.Language)
	in, err := s.reportInput(r.Context(), s.scope(r), groups, lang)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rep, _, err := s.buildReport(r.Context(), kind, in, lang)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleExportReport(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	format := r.URL.Query().Get("format")
	if !slices.Contains(reportKinds, kind) || (format != "pdf" && format != "csv") {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	groups, ok := s.reportGroups(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_group")
		return
	}
	u, _ := currentUser(r)
	lang := reports.Lang(u.Language)
	if l := r.URL.Query().Get("lang"); l == "en" || l == "nl" {
		lang = l
	}
	in, err := s.reportInput(r.Context(), s.scope(r), groups, lang)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	_, out, err := s.buildReport(r.Context(), kind, in, lang)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var body []byte
	ctype := "text/csv; charset=utf-8"
	if format == "pdf" {
		ctype = "application/pdf"
		body, err = s.document(r.Context(), out.Title, in.scope, lang, []reportOutput{out}).PDF()
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	} else {
		body = reports.CSV(lang, out.CSVHeader, out.CSVRows)
	}
	s.audit(r, "report.export", kind, true, map[string]any{"format": format, "groups": groups})
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, reportFileName(kind, in.now, format)))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func (s *Server) handleGetReportSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadReportSettings(r.Context()))
}

func (s *Server) handlePutReportSettings(w http.ResponseWriter, r *http.Request) {
	var rs reportSettings
	if !decode(w, r, &rs) {
		return
	}
	if rs.IgnoredSoftware == nil {
		rs.IgnoredSoftware = []string{}
	}
	if rs.ExpiringDays == 0 {
		rs.ExpiringDays = 90
	}
	if !validReportSettings(rs) {
		apiError(w, http.StatusBadRequest, "invalid_report_settings")
		return
	}
	if err := s.st.PutSetting(r.Context(), keyReports, rs); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "report.settings", "", true, map[string]any{"offlineDays": rs.OfflineDays, "patchGraceDays": rs.PatchGraceDays,
		"diskFreePercent": rs.DiskFreePercent, "ignored": len(rs.IgnoredSoftware)})
	writeJSON(w, http.StatusOK, rs)
}

func (s *Server) handleIgnoreSoftware(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID string `json:"packageId"`
		Ignore    bool   `json:"ignore"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.PackageID = strings.TrimSpace(req.PackageID)
	if req.PackageID == "" || len(req.PackageID) > 200 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	rs := s.loadReportSettings(r.Context())
	rs.IgnoredSoftware = slices.DeleteFunc(rs.IgnoredSoftware, func(id string) bool { return strings.EqualFold(id, req.PackageID) })
	if req.Ignore {
		rs.IgnoredSoftware = append(rs.IgnoredSoftware, req.PackageID)
		slices.Sort(rs.IgnoredSoftware)
	}
	if !validReportSettings(rs) {
		apiError(w, http.StatusBadRequest, "invalid_report_settings")
		return
	}
	if err := s.st.PutSetting(r.Context(), keyReports, rs); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "report.ignore_software", req.PackageID, true, map[string]any{"ignore": req.Ignore})
	writeJSON(w, http.StatusOK, rs)
}
