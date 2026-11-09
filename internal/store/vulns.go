package store

// Vulnerabilities (phase 13): the public source data, the matches per
// device and accepted risks.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- source data ----

// VulnCVE is a CVE from the NVD feeds.
type VulnCVE struct {
	ID        string
	Summary   string
	Score     float64
	Severity  string
	Vector    string
	Published *time.Time
	Modified  *time.Time
	Rejected  bool
	Ranges    []VulnRange
}

// VulnRange is an affected version range of a Windows program.
type VulnRange struct {
	CVE       string `json:"-"`
	Product   string `json:"product"`
	Version   string `json:"version,omitempty"`
	StartIncl string `json:"startIncl,omitempty"`
	StartExcl string `json:"startExcl,omitempty"`
	EndIncl   string `json:"endIncl,omitempty"`
	EndExcl   string `json:"endExcl,omitempty"`
}

// SaveNVD stores a chunk of CVEs from the NVD with their program ranges.
func (s *Store) SaveNVD(ctx context.Context, cves []VulnCVE) error {
	if len(cves) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE nvd_in (cve_id text, summary text, score real, severity text, vector text,
		published_at timestamptz, modified_at timestamptz, rejected boolean) ON COMMIT DROP`); err != nil {
		return err
	}
	rows := make([][]any, 0, len(cves))
	ids := make([]string, 0, len(cves))
	var ranges [][]any
	seen := map[string]bool{}
	for _, c := range cves {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		ids = append(ids, c.ID)
		rows = append(rows, []any{c.ID, c.Summary, float32(c.Score), c.Severity, c.Vector, c.Published, c.Modified, c.Rejected})
		for _, r := range c.Ranges {
			ranges = append(ranges, []any{c.ID, r.Product, r.Version, r.StartIncl, r.StartExcl, r.EndIncl, r.EndExcl})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"nvd_in"}, []string{"cve_id", "summary", "score", "severity", "vector",
		"published_at", "modified_at", "rejected"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO vulnerabilities (cve_id, summary, score, severity, vector, published_at, modified_at, rejected)
		SELECT cve_id, summary, score, severity, vector, published_at, modified_at, rejected FROM nvd_in
		ON CONFLICT (cve_id) DO UPDATE SET summary = EXCLUDED.summary, score = EXCLUDED.score, severity = EXCLUDED.severity,
		vector = EXCLUDED.vector, published_at = EXCLUDED.published_at, modified_at = EXCLUDED.modified_at,
		rejected = EXCLUDED.rejected`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM vuln_ranges WHERE cve_id = ANY($1)", ids); err != nil {
		return err
	}
	if len(ranges) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"vuln_ranges"}, []string{"cve_id", "product", "version", "start_incl",
			"start_excl", "end_incl", "end_excl"}, pgx.CopyFromRows(ranges)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// VulnKEV is an entry of CISA's KEV catalog.
type VulnKEV struct {
	CVE        string
	Name       string
	Added, Due *time.Time
	Ransomware bool
}

// SaveKEV replaces the KEV flags.
func (s *Store) SaveKEV(ctx context.Context, list []VulnKEV) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE kev_in (cve_id text, name text, added date, due date, ransomware boolean)
		ON COMMIT DROP`); err != nil {
		return err
	}
	rows := make([][]any, 0, len(list))
	seen := map[string]bool{}
	for _, k := range list {
		if seen[k.CVE] {
			continue
		}
		seen[k.CVE] = true
		rows = append(rows, []any{k.CVE, k.Name, k.Added, k.Due, k.Ransomware})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"kev_in"}, []string{"cve_id", "name", "added", "due", "ransomware"},
		pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE vulnerabilities SET kev = false, kev_name = '', kev_added = NULL, kev_due = NULL,
		kev_ransomware = false WHERE kev AND cve_id NOT IN (SELECT cve_id FROM kev_in)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO vulnerabilities (cve_id, title, kev, kev_name, kev_added, kev_due, kev_ransomware)
		SELECT cve_id, name, true, name, added, due, ransomware FROM kev_in
		ON CONFLICT (cve_id) DO UPDATE SET kev = true, kev_name = EXCLUDED.kev_name, kev_added = EXCLUDED.kev_added,
		kev_due = EXCLUDED.kev_due, kev_ransomware = EXCLUDED.kev_ransomware`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VulnDebian is a security tracker entry.
type VulnDebian struct {
	Release, Package, CVE, Status, FixedVersion, Urgency string
	NoDSA                                                bool
}

// SaveDebian replaces the tracker data; summaries fill in CVEs the NVD
// does not know (yet).
func (s *Store) SaveDebian(ctx context.Context, entries []VulnDebian, summaries map[string]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "TRUNCATE vuln_debian"); err != nil {
		return err
	}
	rows := make([][]any, 0, len(entries))
	seen := map[[3]string]bool{}
	for _, e := range entries {
		k := [3]string{e.Release, e.Package, e.CVE}
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, []any{e.Release, e.Package, e.CVE, e.Status, e.FixedVersion, e.Urgency, e.NoDSA})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"vuln_debian"}, []string{"release", "package", "cve_id", "status",
		"fixed_version", "urgency", "nodsa"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE deb_in (cve_id text, summary text) ON COMMIT DROP"); err != nil {
		return err
	}
	srows := make([][]any, 0, len(summaries))
	for id, sum := range summaries {
		srows = append(srows, []any{id, sum})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"deb_in"}, []string{"cve_id", "summary"}, pgx.CopyFromRows(srows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO vulnerabilities (cve_id, summary) SELECT cve_id, summary FROM deb_in
		ON CONFLICT (cve_id) DO UPDATE SET summary = EXCLUDED.summary WHERE vulnerabilities.summary = ''`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VulnMSRC is a CVE of a Microsoft monthly release.
type VulnMSRC struct {
	CVE       string
	Title     string
	Score     float64
	Severity  string
	Exploited bool
	Fixes     []VulnMSRCFix
}

// VulnMSRCFix is a security update that fixes a CVE.
type VulnMSRCFix struct {
	Product, KB, FixedBuild, Supersedes string
}

// SaveMSRCMonth replaces the data of one monthly release.
func (s *Store) SaveMSRCMonth(ctx context.Context, month string, vulns []VulnMSRC) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "DELETE FROM vuln_msrc WHERE month = $1", month); err != nil {
		return err
	}
	var fixes [][]any
	meta := make([][]any, 0, len(vulns))
	seen := map[string]bool{}
	for _, v := range vulns {
		for _, f := range v.Fixes {
			fixes = append(fixes, []any{v.CVE, f.Product, f.KB, f.FixedBuild, f.Supersedes, month})
		}
		if !seen[v.CVE] {
			seen[v.CVE] = true
			meta = append(meta, []any{v.CVE, v.Title, float32(v.Score), v.Severity, v.Exploited})
		}
	}
	if len(fixes) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"vuln_msrc"}, []string{"cve_id", "product", "kb", "fixed_build",
			"supersedes", "month"}, pgx.CopyFromRows(fixes)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE msrc_in (cve_id text, title text, score real, severity text, exploited boolean)
		ON COMMIT DROP`); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"msrc_in"}, []string{"cve_id", "title", "score", "severity", "exploited"},
		pgx.CopyFromRows(meta)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO vulnerabilities (cve_id, title, msrc_score, msrc_severity, exploited)
		SELECT cve_id, title, score, severity, exploited FROM msrc_in
		ON CONFLICT (cve_id) DO UPDATE SET title = EXCLUDED.title, msrc_score = EXCLUDED.msrc_score,
		msrc_severity = EXCLUDED.msrc_severity, exploited = EXCLUDED.exploited`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PruneMSRC drops the months that are no longer read.
func (s *Store) PruneMSRC(ctx context.Context, keep []string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM vuln_msrc WHERE NOT (month = ANY($1))", keep)
	return err
}

// VulnSource is the state of one source.
type VulnSource struct {
	Source    string          `json:"source"`
	SyncedAt  *time.Time      `json:"syncedAt"`
	CheckedAt *time.Time      `json:"checkedAt"`
	Error     string          `json:"error"`
	Items     int             `json:"items"`
	State     json.RawMessage `json:"-"`
}

// VulnSources returns the state of every source read so far.
func (s *Store) VulnSources(ctx context.Context) (map[string]VulnSource, error) {
	rows, err := s.pool.Query(ctx, "SELECT source, synced_at, checked_at, error, items, state FROM vuln_sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]VulnSource{}
	for rows.Next() {
		var v VulnSource
		if err := rows.Scan(&v.Source, &v.SyncedAt, &v.CheckedAt, &v.Error, &v.Items, &v.State); err != nil {
			return nil, err
		}
		out[v.Source] = v
	}
	return out, rows.Err()
}

// SetVulnSourceOK records a successful read; state may be nil to keep it.
func (s *Store) SetVulnSourceOK(ctx context.Context, source string, items int, state any) error {
	var raw []byte
	if state != nil {
		var err error
		if raw, err = json.Marshal(state); err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO vuln_sources (source, synced_at, checked_at, error, items, state)
		VALUES ($1, now(), now(), '', $2, COALESCE($3, '{}'::jsonb))
		ON CONFLICT (source) DO UPDATE SET synced_at = now(), checked_at = now(), error = '', items = $2,
		state = COALESCE($3, vuln_sources.state)`, source, items, raw)
	return err
}

// SetVulnSourceState stores the source's state without marking a sync.
func (s *Store) SetVulnSourceState(ctx context.Context, source string, state any) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO vuln_sources (source, state) VALUES ($1, $2)
		ON CONFLICT (source) DO UPDATE SET state = $2`, source, raw)
	return err
}

// SetVulnSourceError records a failed read.
func (s *Store) SetVulnSourceError(ctx context.Context, source, msg string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO vuln_sources (source, checked_at, error) VALUES ($1, now(), $2)
		ON CONFLICT (source) DO UPDATE SET checked_at = now(), error = $2`, source, msg)
	return err
}

// CountVulnRows counts the stored rows of a source table.
func (s *Store) CountVulnRows(ctx context.Context, table string) (int, error) {
	switch table {
	case "vulnerabilities", "vuln_debian", "vuln_msrc", "vuln_ranges":
	default:
		return 0, fmt.Errorf("unknown table %q", table)
	}
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*)::int FROM "+table).Scan(&n)
	return n, err
}

// CountKEV counts the CVEs in the KEV catalog.
func (s *Store) CountKEV(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*)::int FROM vulnerabilities WHERE kev").Scan(&n)
	return n, err
}

// ---- matching ----

// VulnDevice is what the matcher needs to know of a device.
type VulnDevice struct {
	ID, Platform, Arch, OSName, OSVersion, OSBuild string
}

// VulnDirtyDevices returns devices whose inventory or updates changed since
// they were last matched.
func (s *Store) VulnDirtyDevices(ctx context.Context, limit int) ([]VulnDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, platform, arch, os_name, os_version, os_build FROM devices
		WHERE vuln_dirty AND inventory_at IS NOT NULL ORDER BY vuln_checked_at NULLS FIRST LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[VulnDevice])
}

// MarkAllVulnDirty asks for every device to be matched again.
func (s *Store) MarkAllVulnDirty(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "UPDATE devices SET vuln_dirty = true WHERE NOT vuln_dirty")
	return err
}

// VulnSoftware is an installed program or package.
type VulnSoftware struct {
	Name, Version, Publisher, Source, SrcPackage, SrcVersion string
}

// VulnDeviceSoftware returns the programs of a device.
func (s *Store) VulnDeviceSoftware(ctx context.Context, id string) ([]VulnSoftware, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, version, publisher, source, src_package, src_version FROM device_software
		WHERE device_id::text = $1`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[VulnSoftware])
}

// DebianVulns returns the tracker entries of source packages in a release.
func (s *Store) DebianVulns(ctx context.Context, release string, packages []string) ([]VulnDebian, error) {
	rows, err := s.pool.Query(ctx, `SELECT release, package, cve_id, status, fixed_version, urgency, nodsa FROM vuln_debian
		WHERE release = $1 AND package = ANY($2)`, release, packages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VulnDebian
	for rows.Next() {
		var e VulnDebian
		if err := rows.Scan(&e.Release, &e.Package, &e.CVE, &e.Status, &e.FixedVersion, &e.Urgency, &e.NoDSA); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MSRCRow is one stored Microsoft fix.
type MSRCRow struct {
	CVE, Product, KB, FixedBuild, Supersedes string
}

func (s *Store) msrcRows(ctx context.Context, where string, arg any) ([]MSRCRow, error) {
	rows, err := s.pool.Query(ctx, "SELECT cve_id, product, kb, fixed_build, supersedes FROM vuln_msrc WHERE "+where, arg)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[MSRCRow])
}

// MSRCForProduct returns the fixes of a Windows release.
func (s *Store) MSRCForProduct(ctx context.Context, product string) ([]MSRCRow, error) {
	return s.msrcRows(ctx, "product = $1", product)
}

// MSRCForKBs returns the CVEs that updates fix.
func (s *Store) MSRCForKBs(ctx context.Context, kbs []string) ([]MSRCRow, error) {
	return s.msrcRows(ctx, "kb = ANY($1)", kbs)
}

// RangesForProducts returns the affected ranges of programs.
func (s *Store) RangesForProducts(ctx context.Context, products []string) ([]VulnRange, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.cve_id, r.product, r.version, r.start_incl, r.start_excl, r.end_incl, r.end_excl
		FROM vuln_ranges r JOIN vulnerabilities v ON v.cve_id = r.cve_id WHERE r.product = ANY($1) AND NOT v.rejected`, products)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VulnRange
	for rows.Next() {
		var r VulnRange
		if err := rows.Scan(&r.CVE, &r.Product, &r.Version, &r.StartIncl, &r.StartExcl, &r.EndIncl, &r.EndExcl); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RejectedCVEs returns which of the CVEs the NVD rejected.
func (s *Store) RejectedCVEs(ctx context.Context, ids []string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, "SELECT cve_id FROM vulnerabilities WHERE rejected AND cve_id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// DeviceVuln is a vulnerability found on a device.
type DeviceVuln struct {
	CVE       string   `json:"cve"`
	Kind      string   `json:"kind"`
	Product   string   `json:"product"`
	Installed string   `json:"installedVersion"`
	Fixed     string   `json:"fixedVersion"`
	Fix       string   `json:"fix"`
	FixRef    []string `json:"fixRef"`
}

// SaveDeviceVulns replaces the matches of a device, keeping when each was
// first seen.
func (s *Store) SaveDeviceVulns(ctx context.Context, id string, list []DeviceVuln) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE dv_in (cve_id text, kind text, product text, installed_version text,
		fixed_version text, fix text, fix_ref text[]) ON COMMIT DROP`); err != nil {
		return err
	}
	rows := make([][]any, 0, len(list))
	seen := map[[2]string]bool{}
	for _, v := range list {
		k := [2]string{v.CVE, v.Product}
		if seen[k] {
			continue
		}
		seen[k] = true
		ref := v.FixRef
		if ref == nil {
			ref = []string{}
		}
		rows = append(rows, []any{v.CVE, v.Kind, v.Product, v.Installed, v.Fixed, v.Fix, ref})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"dv_in"}, []string{"cve_id", "kind", "product", "installed_version",
		"fixed_version", "fix", "fix_ref"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM device_vulnerabilities dv WHERE dv.device_id::text = $1 AND NOT EXISTS
		(SELECT 1 FROM dv_in i WHERE i.cve_id = dv.cve_id AND i.product = dv.product)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO device_vulnerabilities (device_id, cve_id, kind, product, installed_version,
		fixed_version, fix, fix_ref) SELECT $1::uuid, cve_id, kind, product, installed_version, fixed_version, fix, fix_ref FROM dv_in
		ON CONFLICT (device_id, cve_id, product) DO UPDATE SET kind = EXCLUDED.kind, installed_version = EXCLUDED.installed_version,
		fixed_version = EXCLUDED.fixed_version, fix = EXCLUDED.fix, fix_ref = EXCLUDED.fix_ref`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE devices SET vuln_dirty = false, vuln_checked_at = now() WHERE id::text = $1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- views ----

// vulnSeverity is the severity shown: NVD's, else Microsoft's, else the
// Debian urgency.
const vulnSeverity = `(CASE WHEN v.severity <> 'none' THEN v.severity WHEN v.msrc_severity <> '' THEN v.msrc_severity
	ELSE 'none' END)`

const vulnScore = `(CASE WHEN v.score > 0 THEN v.score ELSE v.msrc_score END)`

// vulnOpen is true for a device match no accepted risk covers.
const vulnOpen = `NOT EXISTS (SELECT 1 FROM vuln_exceptions x WHERE x.cve_id = dv.cve_id
	AND (x.device_id IS NULL OR x.device_id = dv.device_id) AND (x.expires_at IS NULL OR x.expires_at > now()))`

// VulnStats sums up the open vulnerabilities in a scope.
type VulnStats struct {
	CVEs          int            `json:"cves"`
	BySeverity    map[string]int `json:"bySeverity"`
	KEV           int            `json:"kev"`
	Devices       int            `json:"devices"`
	CriticalHosts int            `json:"criticalDevices"`
	Fixable       int            `json:"fixable"` // CVEs with a fix on offer for at least one device
	Accepted      int            `json:"accepted"`
	Checked       int            `json:"checked"` // devices matched at least once
}

// VulnStats counts the open vulnerabilities of the devices in scope.
func (s *Store) VulnStats(ctx context.Context, sc Scope) (VulnStats, error) {
	st := VulnStats{BySeverity: map[string]int{}}
	q := `WITH m AS (SELECT dv.device_id, dv.cve_id, dv.fix, ` + vulnSeverity + ` AS sev, v.kev
		FROM device_vulnerabilities dv JOIN devices d ON d.id = dv.device_id LEFT JOIN vulnerabilities v ON v.cve_id = dv.cve_id
		WHERE ($1 OR d.group_id = ANY($2)) AND ` + vulnOpen + `)
		SELECT sev, count(DISTINCT cve_id)::int FROM m GROUP BY sev`
	rows, err := s.pool.Query(ctx, q, sc.All, sc.groups())
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			rows.Close()
			return st, err
		}
		if sev == "" {
			sev = "none"
		}
		st.BySeverity[sev] += n
		st.CVEs += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	err = s.pool.QueryRow(ctx, `WITH m AS (SELECT dv.device_id, dv.cve_id, dv.fix, `+vulnSeverity+` AS sev, COALESCE(v.kev, false) AS kev
		FROM device_vulnerabilities dv JOIN devices d ON d.id = dv.device_id LEFT JOIN vulnerabilities v ON v.cve_id = dv.cve_id
		WHERE ($1 OR d.group_id = ANY($2)) AND `+vulnOpen+`)
		SELECT (SELECT count(DISTINCT cve_id)::int FROM m WHERE kev),
			(SELECT count(DISTINCT device_id)::int FROM m),
			(SELECT count(DISTINCT device_id)::int FROM m WHERE sev = 'critical' OR kev),
			(SELECT count(DISTINCT cve_id)::int FROM m WHERE fix IN ('patch', 'software')),
			(SELECT count(*)::int FROM vuln_exceptions x WHERE x.expires_at IS NULL OR x.expires_at > now()),
			(SELECT count(*)::int FROM devices d WHERE d.vuln_checked_at IS NOT NULL AND ($1 OR d.group_id = ANY($2)))`,
		sc.All, sc.groups()).Scan(&st.KEV, &st.Devices, &st.CriticalHosts, &st.Fixable, &st.Accepted, &st.Checked)
	return st, err
}

// VulnQuery filters the vulnerability list.
type VulnQuery struct {
	Scope    Scope
	Search   string // CVE id, title, product
	Severity string
	KEV      bool
	Fixable  bool
	Kind     string
	GroupID  int64
	DeviceID string
	Accepted bool // the accepted risks instead of the open ones
	Limit    int
}

// VulnSummary is a CVE with the devices it affects.
type VulnSummary struct {
	CVE        string     `json:"cve"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Severity   string     `json:"severity"`
	Score      float64    `json:"score"`
	KEV        bool       `json:"kev"`
	Exploited  bool       `json:"exploited"`
	Ransomware bool       `json:"ransomware"`
	Published  *time.Time `json:"published"`
	Devices    int        `json:"devices"`
	Fixable    int        `json:"fixable"` // devices with a fix on offer
	Products   []string   `json:"products"`
	Kinds      []string   `json:"kinds"`
	FirstSeen  time.Time  `json:"firstSeen"`
}

// ListVulns returns the CVEs found on devices in scope, worst first.
func (s *Store) ListVulns(ctx context.Context, q VulnQuery) ([]VulnSummary, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if q.Accepted {
		where = append(where, "NOT "+vulnOpen)
	} else {
		where = append(where, vulnOpen)
	}
	if q.Search != "" {
		add(`(dv.cve_id ILIKE ? OR v.title ILIKE ? OR dv.product ILIKE ? OR v.summary ILIKE ?)`, "%"+likeEscape(q.Search)+"%")
	}
	if q.Severity != "" {
		add(vulnSeverity+" = ?", q.Severity)
	}
	if q.KEV {
		where = append(where, "COALESCE(v.kev, false)")
	}
	if q.Fixable {
		where = append(where, "dv.fix IN ('patch', 'software')")
	}
	if q.Kind != "" {
		add("dv.kind = ?", q.Kind)
	}
	if q.GroupID > 0 {
		add("d.group_id = ?", q.GroupID)
	}
	if q.DeviceID != "" {
		add("dv.device_id::text = ?", q.DeviceID)
	}
	limit := q.Limit
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `SELECT dv.cve_id, COALESCE(NULLIF(max(v.title), ''), max(v.kev_name), ''), COALESCE(max(v.summary), ''),
		COALESCE(max(`+vulnSeverity+`), 'none'), COALESCE(max(`+vulnScore+`), 0)::float8, COALESCE(bool_or(v.kev), false),
		COALESCE(bool_or(v.exploited), false), COALESCE(bool_or(v.kev_ransomware), false), max(v.published_at),
		count(DISTINCT dv.device_id)::int, count(DISTINCT dv.device_id) FILTER (WHERE dv.fix IN ('patch', 'software'))::int,
		(array_agg(DISTINCT dv.product))[1:5], array_agg(DISTINCT dv.kind), min(dv.first_seen_at)
		FROM device_vulnerabilities dv JOIN devices d ON d.id = dv.device_id LEFT JOIN vulnerabilities v ON v.cve_id = dv.cve_id
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY dv.cve_id
		ORDER BY bool_or(COALESCE(v.kev, false)) DESC, array_position(ARRAY['critical', 'high', 'medium', 'low', 'none'],
			COALESCE(max(`+vulnSeverity+`), 'none')), COALESCE(max(`+vulnScore+`), 0) DESC, count(DISTINCT dv.device_id) DESC, dv.cve_id DESC
		LIMIT `+fmt.Sprint(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnSummary{}
	for rows.Next() {
		var v VulnSummary
		if err := rows.Scan(&v.CVE, &v.Title, &v.Summary, &v.Severity, &v.Score, &v.KEV, &v.Exploited, &v.Ransomware, &v.Published,
			&v.Devices, &v.Fixable, &v.Products, &v.Kinds, &v.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VulnDetail is a CVE with everything the sources say.
type VulnDetail struct {
	CVE          string       `json:"cve"`
	Title        string       `json:"title"`
	Summary      string       `json:"summary"`
	Severity     string       `json:"severity"`
	Score        float64      `json:"score"`
	Vector       string       `json:"vector"`
	Published    *time.Time   `json:"published"`
	Modified     *time.Time   `json:"modified"`
	Exploited    bool         `json:"exploited"`
	KEV          bool         `json:"kev"`
	KEVName      string       `json:"kevName"`
	KEVAdded     *time.Time   `json:"kevAdded"`
	KEVDue       *time.Time   `json:"kevDue"`
	Ransomware   bool         `json:"ransomware"`
	MSRCSeverity string       `json:"msrcSeverity"`
	Updates      []string     `json:"updates"` // Microsoft KBs
	Debian       []VulnDebian `json:"-"`
	Ranges       []VulnRange  `json:"ranges"`
}

// VulnByID returns a CVE; ErrNotFound when no source knows it.
func (s *Store) VulnByID(ctx context.Context, id string) (VulnDetail, error) {
	var v VulnDetail
	err := s.pool.QueryRow(ctx, `SELECT v.cve_id, COALESCE(NULLIF(v.title, ''), v.kev_name), v.summary, `+vulnSeverity+`, `+vulnScore+`::float8,
		v.vector, v.published_at, v.modified_at, v.exploited, v.kev, v.kev_name, v.kev_added, v.kev_due, v.kev_ransomware, v.msrc_severity,
		ARRAY(SELECT DISTINCT kb FROM vuln_msrc m WHERE m.cve_id = v.cve_id ORDER BY kb)
		FROM vulnerabilities v WHERE v.cve_id = $1`, id).Scan(&v.CVE, &v.Title, &v.Summary, &v.Severity, &v.Score, &v.Vector,
		&v.Published, &v.Modified, &v.Exploited, &v.KEV, &v.KEVName, &v.KEVAdded, &v.KEVDue, &v.Ransomware, &v.MSRCSeverity, &v.Updates)
	if err != nil {
		return v, mapErr(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT product, version, start_incl, start_excl, end_incl, end_excl FROM vuln_ranges
		WHERE cve_id = $1 ORDER BY product`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	v.Ranges = []VulnRange{}
	for rows.Next() {
		var r VulnRange
		if err := rows.Scan(&r.Product, &r.Version, &r.StartIncl, &r.StartExcl, &r.EndIncl, &r.EndExcl); err != nil {
			return v, err
		}
		v.Ranges = append(v.Ranges, r)
	}
	return v, rows.Err()
}

// VulnDeviceRow is a device a CVE affects.
type VulnDeviceRow struct {
	DeviceID  string    `json:"deviceId"`
	Hostname  string    `json:"hostname"`
	GroupID   int64     `json:"groupId"`
	GroupName string    `json:"groupName"`
	Platform  string    `json:"platform"`
	OSName    string    `json:"osName"`
	LastSeen  time.Time `json:"lastSeen"`
	Online    bool      `json:"online"` // filled in by the server
	Accepted  bool      `json:"accepted"`
	DeviceVuln
	FirstSeen time.Time `json:"firstSeen"`
}

// VulnAffected returns the devices in scope that a CVE affects.
func (s *Store) VulnAffected(ctx context.Context, sc Scope, cve string, deviceIDs []string) ([]VulnDeviceRow, error) {
	if deviceIDs == nil {
		deviceIDs = []string{}
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, g.name, d.platform, d.os_name, d.last_seen_at, NOT (`+vulnOpen+`),
		dv.cve_id, dv.kind, dv.product, dv.installed_version, dv.fixed_version, dv.fix, dv.fix_ref, dv.first_seen_at
		FROM device_vulnerabilities dv JOIN devices d ON d.id = dv.device_id JOIN device_groups g ON g.id = d.group_id
		WHERE dv.cve_id = $1 AND ($2 OR d.group_id = ANY($3)) AND (cardinality($4::text[]) = 0 OR d.id::text = ANY($4))
		ORDER BY lower(d.hostname), dv.product`, cve, sc.All, sc.groups(), deviceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnDeviceRow{}
	for rows.Next() {
		var r VulnDeviceRow
		if err := rows.Scan(&r.DeviceID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Platform, &r.OSName, &r.LastSeen, &r.Accepted,
			&r.CVE, &r.Kind, &r.Product, &r.Installed, &r.Fixed, &r.Fix, &r.FixRef, &r.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeviceVulnRow is a vulnerability of one device with the CVE's data.
type DeviceVulnRow struct {
	DeviceVuln
	Title     string     `json:"title"`
	Summary   string     `json:"summary"`
	Severity  string     `json:"severity"`
	Score     float64    `json:"score"`
	KEV       bool       `json:"kev"`
	Exploited bool       `json:"exploited"`
	Published *time.Time `json:"published"`
	FirstSeen time.Time  `json:"firstSeen"`
	Accepted  bool       `json:"accepted"`
}

// DeviceVulns returns the vulnerabilities of a device, worst first.
func (s *Store) DeviceVulns(ctx context.Context, id string) ([]DeviceVulnRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT dv.cve_id, dv.kind, dv.product, dv.installed_version, dv.fixed_version, dv.fix, dv.fix_ref,
		COALESCE(NULLIF(v.title, ''), v.kev_name, ''), COALESCE(v.summary, ''), COALESCE(`+vulnSeverity+`, 'none'),
		COALESCE(`+vulnScore+`, 0)::float8, COALESCE(v.kev, false), COALESCE(v.exploited, false), v.published_at, dv.first_seen_at,
		NOT (`+vulnOpen+`)
		FROM device_vulnerabilities dv LEFT JOIN vulnerabilities v ON v.cve_id = dv.cve_id WHERE dv.device_id::text = $1
		ORDER BY COALESCE(v.kev, false) DESC, array_position(ARRAY['critical', 'high', 'medium', 'low', 'none'],
			COALESCE(`+vulnSeverity+`, 'none')), COALESCE(`+vulnScore+`, 0) DESC, dv.cve_id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceVulnRow{}
	for rows.Next() {
		var r DeviceVulnRow
		if err := rows.Scan(&r.CVE, &r.Kind, &r.Product, &r.Installed, &r.Fixed, &r.Fix, &r.FixRef, &r.Title, &r.Summary, &r.Severity,
			&r.Score, &r.KEV, &r.Exploited, &r.Published, &r.FirstSeen, &r.Accepted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeviceVulnCheck says when a device was last matched.
func (s *Store) DeviceVulnCheck(ctx context.Context, id string) (*time.Time, bool, error) {
	var at *time.Time
	var dirty bool
	err := s.pool.QueryRow(ctx, "SELECT vuln_checked_at, vuln_dirty FROM devices WHERE id::text = $1", id).Scan(&at, &dirty)
	return at, dirty, mapErr(err)
}

// VulnDeviceSummary counts the open vulnerabilities of a device.
type VulnDeviceSummary struct {
	DeviceID  string         `json:"deviceId"`
	Hostname  string         `json:"hostname"`
	GroupID   int64          `json:"groupId"`
	GroupName string         `json:"groupName"`
	Platform  string         `json:"platform"`
	OSName    string         `json:"osName"`
	LastSeen  time.Time      `json:"lastSeen"`
	CheckedAt *time.Time     `json:"checkedAt"`
	Total     int            `json:"total"`
	Counts    map[string]int `json:"counts"`
	KEV       int            `json:"kev"`
	Fixable   int            `json:"fixable"`
	MaxScore  float64        `json:"maxScore"`
}

// VulnDeviceSummaries lists the devices in scope with their open
// vulnerabilities, most exposed first.
func (s *Store) VulnDeviceSummaries(ctx context.Context, sc Scope, search string, groupID int64) ([]VulnDeviceSummary, error) {
	rows, err := s.pool.Query(ctx, `WITH m AS (SELECT dv.device_id, dv.cve_id, bool_or(dv.fix IN ('patch', 'software')) AS fixable,
			max(`+vulnSeverity+`) FILTER (WHERE true) AS sev, COALESCE(bool_or(v.kev), false) AS kev, COALESCE(max(`+vulnScore+`), 0) AS score
			FROM device_vulnerabilities dv LEFT JOIN vulnerabilities v ON v.cve_id = dv.cve_id WHERE `+vulnOpen+`
			GROUP BY dv.device_id, dv.cve_id)
		SELECT d.id::text, d.hostname, d.group_id, g.name, d.platform, d.os_name, d.last_seen_at, d.vuln_checked_at,
			count(m.cve_id)::int,
			count(*) FILTER (WHERE m.sev = 'critical')::int, count(*) FILTER (WHERE m.sev = 'high')::int,
			count(*) FILTER (WHERE m.sev = 'medium')::int, count(*) FILTER (WHERE m.sev = 'low')::int,
			count(*) FILTER (WHERE m.sev = 'none')::int, count(*) FILTER (WHERE m.kev)::int, count(*) FILTER (WHERE m.fixable)::int,
			COALESCE(max(m.score), 0)::float8
		FROM devices d JOIN device_groups g ON g.id = d.group_id LEFT JOIN m ON m.device_id = d.id
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = '' OR d.hostname ILIKE $4) AND ($5 = 0 OR d.group_id = $5)
		GROUP BY d.id, g.name
		ORDER BY count(*) FILTER (WHERE m.kev) DESC, count(*) FILTER (WHERE m.sev = 'critical') DESC,
			count(*) FILTER (WHERE m.sev = 'high') DESC, count(m.cve_id) DESC, lower(d.hostname)
		LIMIT 2000`, sc.All, sc.groups(), search, "%"+likeEscape(search)+"%", groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnDeviceSummary{}
	for rows.Next() {
		var r VulnDeviceSummary
		var c, h, m, l, n int
		if err := rows.Scan(&r.DeviceID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Platform, &r.OSName, &r.LastSeen, &r.CheckedAt,
			&r.Total, &c, &h, &m, &l, &n, &r.KEV, &r.Fixable, &r.MaxScore); err != nil {
			return nil, err
		}
		r.Counts = map[string]int{"critical": c, "high": h, "medium": m, "low": l, "none": n}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- accepted risks ----

// VulnException accepts the risk of a CVE.
type VulnException struct {
	ID        int64      `json:"id"`
	CVE       string     `json:"cve"`
	DeviceID  *string    `json:"deviceId"`
	Hostname  string     `json:"hostname"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	Title     string     `json:"title"`
	Severity  string     `json:"severity"`
}

// ListVulnExceptions returns the accepted risks; scoped users see the ones
// for all devices and for devices in their groups.
func (s *Store) ListVulnExceptions(ctx context.Context, sc Scope) ([]VulnException, error) {
	rows, err := s.pool.Query(ctx, `SELECT x.id, x.cve_id, x.device_id::text, COALESCE(d.hostname, ''), x.reason, x.expires_at,
		x.created_by, x.created_at, COALESCE(NULLIF(v.title, ''), v.kev_name, ''), COALESCE(`+vulnSeverity+`, 'none')
		FROM vuln_exceptions x LEFT JOIN devices d ON d.id = x.device_id LEFT JOIN vulnerabilities v ON v.cve_id = x.cve_id
		WHERE x.device_id IS NULL OR $1 OR d.group_id = ANY($2) ORDER BY x.created_at DESC`, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnException{}
	for rows.Next() {
		var x VulnException
		if err := rows.Scan(&x.ID, &x.CVE, &x.DeviceID, &x.Hostname, &x.Reason, &x.ExpiresAt, &x.CreatedBy, &x.CreatedAt, &x.Title,
			&x.Severity); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// CreateVulnException stores an accepted risk; ErrConflict when one exists.
func (s *Store) CreateVulnException(ctx context.Context, x VulnException) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO vuln_exceptions (cve_id, device_id, reason, expires_at, created_by)
		VALUES ($1, $2::uuid, $3, $4, $5) RETURNING id`, x.CVE, x.DeviceID, x.Reason, x.ExpiresAt, x.CreatedBy).Scan(&id)
	return id, mapErr(err)
}

// VulnExceptionByID returns an accepted risk.
func (s *Store) VulnExceptionByID(ctx context.Context, id int64) (VulnException, error) {
	var x VulnException
	err := s.pool.QueryRow(ctx, `SELECT x.id, x.cve_id, x.device_id::text, COALESCE(d.hostname, ''), x.reason, x.expires_at,
		x.created_by, x.created_at FROM vuln_exceptions x LEFT JOIN devices d ON d.id = x.device_id WHERE x.id = $1`, id).
		Scan(&x.ID, &x.CVE, &x.DeviceID, &x.Hostname, &x.Reason, &x.ExpiresAt, &x.CreatedBy, &x.CreatedAt)
	return x, mapErr(err)
}

// DeleteVulnException removes an accepted risk.
func (s *Store) DeleteVulnException(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM vuln_exceptions WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PurgeVulnExceptions drops accepted risks that expired a month ago.
func (s *Store) PurgeVulnExceptions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM vuln_exceptions WHERE expires_at < now() - interval '30 days'")
	return err
}
