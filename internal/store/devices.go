package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scope limits which device groups a user sees. All means no limit.
type Scope struct {
	All    bool
	Groups []int64
}

// Allows reports whether the scope includes a group.
func (s Scope) Allows(groupID int64) bool {
	if s.All {
		return true
	}
	for _, g := range s.Groups {
		if g == groupID {
			return true
		}
	}
	return false
}

func (s Scope) groups() []int64 {
	if s.Groups == nil {
		return []int64{}
	}
	return s.Groups
}

// ---- device groups ----

// DeviceGroup groups devices for policies, update rings and role scoping.
type DeviceGroup struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	UpdateRing   int     `json:"updateRing"`
	AgentChannel *string `json:"agentChannel"`
	WindowStart  *int    `json:"windowStart"`
	WindowEnd    *int    `json:"windowEnd"`
	PatchPolicy  *int64  `json:"patchPolicyId"`
	// RemoteConsent is auto (servers direct, workstations ask), direct,
	// notify or ask.
	RemoteConsent string `json:"remoteConsent"`
	// InventoryMinutes and PatchScanMinutes override the server-wide scan
	// intervals for the group; nil uses the default.
	InventoryMinutes *int      `json:"inventoryMinutes"`
	PatchScanMinutes *int      `json:"patchScanMinutes"`
	CreatedAt        time.Time `json:"createdAt"`
	DeviceCount      int       `json:"deviceCount"`
	OnlineCount      int       `json:"onlineCount"` // filled in by the server
}

const groupCols = `g.id, g.name, g.description, g.update_ring, g.agent_channel, g.window_start, g.window_end, g.patch_policy_id, g.remote_consent,
	g.inventory_minutes, g.patch_scan_minutes, g.created_at,
	(SELECT count(*) FROM devices d WHERE d.group_id = g.id)::int`

func scanGroup(row pgx.Row) (DeviceGroup, error) {
	var g DeviceGroup
	var ring int16
	var ws, we *int16
	err := row.Scan(&g.ID, &g.Name, &g.Description, &ring, &g.AgentChannel, &ws, &we, &g.PatchPolicy, &g.RemoteConsent,
		&g.InventoryMinutes, &g.PatchScanMinutes, &g.CreatedAt, &g.DeviceCount)
	g.UpdateRing = int(ring)
	if ws != nil {
		v := int(*ws)
		g.WindowStart = &v
	}
	if we != nil {
		v := int(*we)
		g.WindowEnd = &v
	}
	return g, mapErr(err)
}

// ListGroups returns the groups within a scope, ordered by name.
func (s *Store) ListGroups(ctx context.Context, sc Scope) ([]DeviceGroup, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+groupCols+" FROM device_groups g WHERE $1 OR g.id = ANY($2) ORDER BY lower(g.name)",
		sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceGroup{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GroupByID returns a group.
func (s *Store) GroupByID(ctx context.Context, id int64) (DeviceGroup, error) {
	return scanGroup(s.pool.QueryRow(ctx, "SELECT "+groupCols+" FROM device_groups g WHERE g.id = $1", id))
}

// GroupByName returns a group by case-insensitive name.
func (s *Store) GroupByName(ctx context.Context, name string) (DeviceGroup, error) {
	return scanGroup(s.pool.QueryRow(ctx, "SELECT "+groupCols+" FROM device_groups g WHERE lower(g.name) = lower($1)", strings.TrimSpace(name)))
}

func consentOrAuto(v string) string {
	if v == "" {
		return "auto"
	}
	return v
}

func smallint(v *int) any {
	if v == nil {
		return nil
	}
	return int16(*v)
}

// CreateGroup inserts a group.
func (s *Store) CreateGroup(ctx context.Context, g DeviceGroup) (DeviceGroup, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO device_groups (name, description, update_ring, agent_channel, window_start, window_end, patch_policy_id, remote_consent,
		inventory_minutes, patch_scan_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`, g.Name, g.Description, int16(g.UpdateRing), g.AgentChannel,
		smallint(g.WindowStart), smallint(g.WindowEnd), g.PatchPolicy, consentOrAuto(g.RemoteConsent), g.InventoryMinutes, g.PatchScanMinutes).Scan(&id)
	if err != nil {
		return g, mapErr(err)
	}
	return s.GroupByID(ctx, id)
}

// UpdateGroup changes a group.
func (s *Store) UpdateGroup(ctx context.Context, g DeviceGroup) error {
	tag, err := s.pool.Exec(ctx, `UPDATE device_groups SET name = $2, description = $3, update_ring = $4, agent_channel = $5,
		window_start = $6, window_end = $7, patch_policy_id = $8, remote_consent = $9,
		inventory_minutes = $10, patch_scan_minutes = $11 WHERE id = $1`, g.ID, g.Name, g.Description, int16(g.UpdateRing), g.AgentChannel,
		smallint(g.WindowStart), smallint(g.WindowEnd), g.PatchPolicy, consentOrAuto(g.RemoteConsent), g.InventoryMinutes, g.PatchScanMinutes)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// DeleteGroup removes an empty group. It returns ErrConflict when devices are still in it.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM device_groups WHERE id = $1", id)
	if err != nil {
		if strings.Contains(err.Error(), "23503") { // devices still reference the group
			return ErrConflict
		}
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- enrollment tokens ----

// EnrollmentToken lets new agents enroll into a group.
type EnrollmentToken struct {
	ID          int64      `json:"id"`
	TokenEnc    string     `json:"-"`
	Prefix      string     `json:"prefix"`
	Description string     `json:"description"`
	GroupID     int64      `json:"groupId"`
	GroupName   string     `json:"groupName"`
	CreatedBy   string     `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	MaxUses     *int       `json:"maxUses"`
	Uses        int        `json:"uses"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
}

// Usable reports whether the token may enroll a device now.
func (t EnrollmentToken) Usable(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt)) && (t.MaxUses == nil || t.Uses < *t.MaxUses)
}

const tokenCols = `t.id, t.token_enc, t.prefix, t.description, t.group_id, g.name, t.created_by, t.created_at, t.expires_at,
	t.revoked_at, t.max_uses, t.uses, t.last_used_at`

func scanToken(row pgx.Row) (EnrollmentToken, error) {
	var t EnrollmentToken
	err := row.Scan(&t.ID, &t.TokenEnc, &t.Prefix, &t.Description, &t.GroupID, &t.GroupName, &t.CreatedBy, &t.CreatedAt,
		&t.ExpiresAt, &t.RevokedAt, &t.MaxUses, &t.Uses, &t.LastUsedAt)
	return t, mapErr(err)
}

// CreateToken stores a new enrollment token (hash plus encrypted copy).
func (s *Store) CreateToken(ctx context.Context, t EnrollmentToken, plain string) (EnrollmentToken, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO enrollment_tokens (token_hash, token_enc, prefix, description, group_id, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, HashToken(plain), t.TokenEnc, t.Prefix, t.Description, t.GroupID,
		t.CreatedBy, t.ExpiresAt, t.MaxUses).Scan(&id)
	if err != nil {
		return t, mapErr(err)
	}
	return s.TokenByID(ctx, id)
}

// TokenByID returns a token.
func (s *Store) TokenByID(ctx context.Context, id int64) (EnrollmentToken, error) {
	return scanToken(s.pool.QueryRow(ctx, "SELECT "+tokenCols+" FROM enrollment_tokens t JOIN device_groups g ON g.id = t.group_id WHERE t.id = $1", id))
}

// TokenByValue looks a token up by its plain value.
func (s *Store) TokenByValue(ctx context.Context, plain string) (EnrollmentToken, error) {
	return scanToken(s.pool.QueryRow(ctx, "SELECT "+tokenCols+" FROM enrollment_tokens t JOIN device_groups g ON g.id = t.group_id WHERE t.token_hash = $1", HashToken(plain)))
}

// ListTokens returns tokens within a scope, newest first.
func (s *Store) ListTokens(ctx context.Context, sc Scope) ([]EnrollmentToken, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+tokenCols+` FROM enrollment_tokens t JOIN device_groups g ON g.id = t.group_id
		WHERE $1 OR t.group_id = ANY($2) ORDER BY t.created_at DESC`, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnrollmentToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken marks a token revoked.
func (s *Store) RevokeToken(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "UPDATE enrollment_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---- devices ----

// Device is an enrolled endpoint.
type Device struct {
	ID           string          `json:"id"`
	GroupID      int64           `json:"groupId"`
	GroupName    string          `json:"groupName"`
	TokenID      *int64          `json:"-"`
	PublicKey    []byte          `json:"-"`
	MachineID    string          `json:"-"`
	Hostname     string          `json:"hostname"`
	Platform     string          `json:"platform"`
	Arch         string          `json:"arch"`
	AgentVersion string          `json:"agentVersion"`
	OSName       string          `json:"osName"`
	OSVersion    string          `json:"osVersion"`
	OSBuild      string          `json:"osBuild"`
	Kind         string          `json:"kind"`
	Domain       string          `json:"domain"`
	DomainJoined bool            `json:"domainJoined"`
	OU           string          `json:"ou"`
	Manufacturer string          `json:"manufacturer"`
	Model        string          `json:"model"`
	Serial       string          `json:"serial"`
	CPU          string          `json:"cpu"`
	CPUCores     int             `json:"cpuCores"`
	MemoryBytes  int64           `json:"memoryBytes"`
	LoggedInUser string          `json:"loggedInUser"`
	RemoteIP     string          `json:"remoteIp"`
	LocalIPs     []string        `json:"localIps"`
	BootTime     *time.Time      `json:"bootTime"`
	Hardware     json.RawMessage `json:"hardware,omitempty"`
	EnrolledAt   time.Time       `json:"enrolledAt"`
	LastSeenAt   time.Time       `json:"lastSeenAt"`
	InventoryAt  *time.Time      `json:"inventoryAt"`
	Notes        string          `json:"notes"`
	Online       bool            `json:"online"` // filled in by the server
	// ConnectedVia names the secure gateway of the current connection;
	// empty for a direct connection (filled in by the server).
	ConnectedVia string `json:"connectedVia,omitempty"`

	UpdateVersion string     `json:"updateVersion"` // agent update: target version
	UpdateState   string     `json:"updateState"`
	UpdateMessage string     `json:"updateMessage"`
	UpdateAt      *time.Time `json:"updateAt"`

	PatchScanAt        *time.Time `json:"patchScanAt"`
	PatchScanError     string     `json:"patchScanError"`
	PatchSource        string     `json:"patchSource"`
	PatchCluster       string     `json:"patchCluster"`
	PatchRebootPending bool       `json:"patchRebootPending"`
	PatchMissing       int        `json:"patchMissing"`
}

const deviceCols = `d.id::text, d.group_id, g.name, d.token_id, d.public_key, d.machine_id, d.hostname, d.platform, d.arch,
	d.agent_version, d.os_name, d.os_version, d.os_build, d.kind, d.domain, d.domain_joined, d.ou, d.manufacturer, d.model,
	d.serial, d.cpu, d.cpu_cores, d.memory_bytes, d.logged_in_user, d.remote_ip, d.local_ips, d.boot_time, d.hardware,
	d.enrolled_at, d.last_seen_at, d.inventory_at, d.notes, d.update_version, d.update_state, d.update_message, d.update_at,
	d.patch_scan_at, d.patch_scan_error, d.patch_source, d.patch_cluster, d.patch_reboot_pending,
	(SELECT count(*) FROM device_patches p WHERE p.device_id = d.id)::int`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.GroupID, &d.GroupName, &d.TokenID, &d.PublicKey, &d.MachineID, &d.Hostname, &d.Platform, &d.Arch,
		&d.AgentVersion, &d.OSName, &d.OSVersion, &d.OSBuild, &d.Kind, &d.Domain, &d.DomainJoined, &d.OU, &d.Manufacturer,
		&d.Model, &d.Serial, &d.CPU, &d.CPUCores, &d.MemoryBytes, &d.LoggedInUser, &d.RemoteIP, &d.LocalIPs, &d.BootTime,
		&d.Hardware, &d.EnrolledAt, &d.LastSeenAt, &d.InventoryAt, &d.Notes, &d.UpdateVersion, &d.UpdateState, &d.UpdateMessage, &d.UpdateAt,
		&d.PatchScanAt, &d.PatchScanError, &d.PatchSource, &d.PatchCluster, &d.PatchRebootPending, &d.PatchMissing)
	return d, mapErr(err)
}

const deviceFrom = " FROM devices d JOIN device_groups g ON g.id = d.group_id "

// DeviceByID returns a device.
func (s *Store) DeviceByID(ctx context.Context, id string) (Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, "SELECT "+deviceCols+deviceFrom+"WHERE d.id::text = $1", id))
}

// DeviceByMachine finds an earlier enrollment of the same machine.
func (s *Store) DeviceByMachine(ctx context.Context, machineID string) (Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, "SELECT "+deviceCols+deviceFrom+"WHERE d.machine_id = $1 ORDER BY d.enrolled_at DESC LIMIT 1", machineID))
}

// DeviceQuery filters the device list.
type DeviceQuery struct {
	Scope      Scope
	Search     string // hostname, user, IP, serial, OS
	GroupID    int64
	Platform   string
	Software   string   // devices with this program installed (substring)
	IDs        []string // only these devices
	ExcludeIDs []string // not these devices
	Limit      int
	Offset     int
}

// ListDevices returns devices without the hardware blob.
func (s *Store) ListDevices(ctx context.Context, q DeviceQuery) ([]Device, int, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q.Search != "" {
		add(`(d.hostname ILIKE ? OR d.logged_in_user ILIKE ? OR d.serial ILIKE ? OR d.os_name ILIKE ? OR d.remote_ip ILIKE ?
			OR array_to_string(d.local_ips, ' ') ILIKE ?)`, "%"+likeEscape(q.Search)+"%")
	}
	if q.GroupID != 0 {
		add("d.group_id = ?", q.GroupID)
	}
	if q.Platform != "" {
		add("d.platform = ?", q.Platform)
	}
	if q.Software != "" {
		add("EXISTS (SELECT 1 FROM device_software s WHERE s.device_id = d.id AND s.name ILIKE ?)", "%"+likeEscape(q.Software)+"%")
	}
	if q.IDs != nil {
		add("d.id::text = ANY(?)", q.IDs)
	}
	if len(q.ExcludeIDs) > 0 {
		add("NOT (d.id::text = ANY(?))", q.ExcludeIDs)
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*)"+deviceFrom+"WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	args = append(args, limit, q.Offset)
	rows, err := s.pool.Query(ctx, "SELECT "+deviceCols+deviceFrom+"WHERE "+cond+" ORDER BY lower(d.hostname), d.id LIMIT $"+
		itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		d.Hardware = nil
		out = append(out, d)
	}
	return out, total, rows.Err()
}

func itoa(i int) string { return strconv.Itoa(i) }

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// CreateDevice registers a new enrollment and counts the token use.
func (s *Store) CreateDevice(ctx context.Context, d Device) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if d.TokenID != nil {
		if _, err := tx.Exec(ctx, "UPDATE enrollment_tokens SET uses = uses + 1, last_used_at = now() WHERE id = $1", *d.TokenID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO devices (id, group_id, token_id, public_key, machine_id, hostname, platform, arch, agent_version, remote_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, d.ID, d.GroupID, d.TokenID, d.PublicKey, d.MachineID, d.Hostname,
		d.Platform, d.Arch, d.AgentVersion, d.RemoteIP); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

// ReEnrollDevice gives an existing device (same machine, reinstalled agent) a new key.
func (s *Store) ReEnrollDevice(ctx context.Context, d Device) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if d.TokenID != nil {
		if _, err := tx.Exec(ctx, "UPDATE enrollment_tokens SET uses = uses + 1, last_used_at = now() WHERE id = $1", *d.TokenID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET public_key = $2, token_id = $3, hostname = $4, platform = $5, arch = $6,
		agent_version = $7, remote_ip = $8, group_id = $9, enrolled_at = now(), last_seen_at = now() WHERE id = $1`,
		d.ID, d.PublicKey, d.TokenID, d.Hostname, d.Platform, d.Arch, d.AgentVersion, d.RemoteIP, d.GroupID); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

// TouchDevice records that a device was seen, with heartbeat values.
func (s *Store) TouchDevice(ctx context.Context, id, remoteIP, agentVersion, user string, localIPs []string, boot *time.Time) error {
	if localIPs == nil {
		localIPs = []string{}
	}
	_, err := s.pool.Exec(ctx, `UPDATE devices SET last_seen_at = now(), remote_ip = $2,
		agent_version = CASE WHEN $3 = '' THEN agent_version ELSE $3 END, logged_in_user = $4, local_ips = $5,
		boot_time = COALESCE($6, boot_time) WHERE id::text = $1`, id, remoteIP, agentVersion, user, localIPs, boot)
	return err
}

// MarkSeen only updates last_seen_at.
func (s *Store) MarkSeen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, "UPDATE devices SET last_seen_at = now() WHERE id::text = $1", id)
	return err
}

// SoftwareRow is one stored program.
type SoftwareRow struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	Publisher   string  `json:"publisher"`
	InstalledOn *string `json:"installedOn"`
	Source      string  `json:"source"`
	SizeBytes   int64   `json:"sizeBytes"`
	// The Debian source package, when it differs (not read back).
	SrcPackage string `json:"-" db:"-"`
	SrcVersion string `json:"-" db:"-"`
}

// InventoryUpdate holds the device columns written from a full inventory.
type InventoryUpdate struct {
	Hostname, OSName, OSVersion, OSBuild, Kind, Domain, OU string
	DomainJoined                                           bool
	Manufacturer, Model, Serial, CPU, LoggedInUser         string
	CPUCores                                               int
	MemoryBytes                                            int64
	LocalIPs                                               []string
	BootTime                                               *time.Time
	Hardware                                               []byte
	Software                                               []SoftwareRow
}

// SaveInventory replaces a device's inventory.
func (s *Store) SaveInventory(ctx context.Context, id string, in InventoryUpdate) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if in.LocalIPs == nil {
		in.LocalIPs = []string{}
	}
	tag, err := tx.Exec(ctx, `UPDATE devices SET hostname = $2, os_name = $3, os_version = $4, os_build = $5, kind = $6,
		domain = $7, ou = $8, domain_joined = $9, manufacturer = $10, model = $11, serial = $12, cpu = $13, logged_in_user = $14,
		cpu_cores = $15, memory_bytes = $16, local_ips = $17, boot_time = COALESCE($18, boot_time), hardware = $19,
		inventory_at = now(), last_seen_at = now(), vuln_dirty = true WHERE id::text = $1`, id, in.Hostname, in.OSName, in.OSVersion, in.OSBuild,
		in.Kind, in.Domain, in.OU, in.DomainJoined, in.Manufacturer, in.Model, in.Serial, in.CPU, in.LoggedInUser, in.CPUCores,
		in.MemoryBytes, in.LocalIPs, in.BootTime, in.Hardware)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, "DELETE FROM device_software WHERE device_id::text = $1", id); err != nil {
		return err
	}
	rows := make([][]any, 0, len(in.Software))
	for _, sw := range in.Software {
		var on any
		if sw.InstalledOn != nil {
			if t, err := time.Parse("2006-01-02", *sw.InstalledOn); err == nil {
				on = t
			}
		}
		rows = append(rows, []any{id, sw.Name, sw.Version, sw.Publisher, on, sw.Source, sw.SizeBytes, sw.SrcPackage, sw.SrcVersion})
	}
	if len(rows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"device_software"},
			[]string{"device_id", "name", "version", "publisher", "installed_on", "source", "size_bytes", "src_package", "src_version"}, pgx.CopyFromRows(castUUID(rows))); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// castUUID turns the text device id into a [16]byte for COPY into a uuid column.
func castUUID(rows [][]any) [][]any {
	for _, r := range rows {
		if u, ok := parseUUID(r[0].(string)); ok {
			r[0] = u
		}
	}
	return rows
}

func parseUUID(s string) ([16]byte, bool) {
	var u [16]byte
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return u, false
	}
	for i := 0; i < 16; i++ {
		hi, ok1 := fromHex(s[2*i])
		lo, ok2 := fromHex(s[2*i+1])
		if !ok1 || !ok2 {
			return u, false
		}
		u[i] = hi<<4 | lo
	}
	return u, true
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// DeviceSoftware returns the programs of a device.
func (s *Store) DeviceSoftware(ctx context.Context, id string) ([]SoftwareRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, version, publisher, to_char(installed_on, 'YYYY-MM-DD'), source, size_bytes
		FROM device_software WHERE device_id::text = $1 ORDER BY lower(name), version`, id)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[SoftwareRow])
	if out == nil {
		out = []SoftwareRow{}
	}
	return out, err
}

// MoveDevices puts devices into another group.
func (s *Store) MoveDevices(ctx context.Context, ids []string, groupID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, "UPDATE devices SET group_id = $2 WHERE id::text = ANY($1)", ids, groupID)
	return tag.RowsAffected(), mapErr(err)
}

// SetDeviceNotes stores the admin notes of a device.
func (s *Store) SetDeviceNotes(ctx context.Context, id, notes string) error {
	_, err := s.pool.Exec(ctx, "UPDATE devices SET notes = $2 WHERE id::text = $1", id, notes)
	return err
}

// DeleteDevice removes a device and its inventory.
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM devices WHERE id::text = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DevicesEnrolledSince returns devices enrolled with a token after a time
// (the Add client wizard waits for them).
func (s *Store) DevicesEnrolledSince(ctx context.Context, tokenID int64, since time.Time) ([]Device, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+deviceCols+deviceFrom+"WHERE d.token_id = $1 AND d.enrolled_at >= $2 ORDER BY d.enrolled_at", tokenID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		d.Hardware = nil
		out = append(out, d)
	}
	return out, rows.Err()
}

// OnlineCounts counts the given (connected) devices per group.
func (s *Store) OnlineCounts(ctx context.Context, ids []string) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, "SELECT group_id, count(*) FROM devices WHERE id::text = ANY($1) GROUP BY group_id", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g int64
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, err
		}
		out[g] = n
	}
	return out, rows.Err()
}

// DeviceStats counts devices per platform within a scope.
type DeviceStats struct {
	Total    int            `json:"total"`
	Platform map[string]int `json:"platform"`
}

// CountDevices returns device counts within a scope.
func (s *Store) CountDevices(ctx context.Context, sc Scope) (DeviceStats, error) {
	st := DeviceStats{Platform: map[string]int{}}
	rows, err := s.pool.Query(ctx, "SELECT platform, count(*) FROM devices WHERE $1 OR group_id = ANY($2) GROUP BY platform", sc.All, sc.groups())
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return st, err
		}
		st.Platform[p] = n
		st.Total += n
	}
	return st, rows.Err()
}

// ---- scopes ----

// UserScope returns the device-group scope of a user. Administrators are never scoped.
func UserScope(u User) Scope {
	if !u.Scoped || u.Role == "administrator" {
		return Scope{All: true}
	}
	g := u.ScopeGroups
	if g == nil {
		g = []int64{}
	}
	return Scope{Groups: g}
}

// SetUserScope replaces the device-group scope of a user. scoped=false gives access to all groups.
func (s *Store) SetUserScope(ctx context.Context, userID int64, scoped bool, groups []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "UPDATE users SET scoped = $2, updated_at = now() WHERE id = $1", userID, scoped); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM user_group_scopes WHERE user_id = $1", userID); err != nil {
		return err
	}
	if scoped && len(groups) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO user_group_scopes (user_id, group_id)
			SELECT $1, g.id FROM device_groups g WHERE g.id = ANY($2) ON CONFLICT DO NOTHING`, userID, groups); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetMappingScope replaces the device-group scope of a role mapping.
func (s *Store) SetMappingScope(ctx context.Context, mappingID int64, scoped bool, groups []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "UPDATE role_mappings SET scoped = $2 WHERE id = $1", mappingID, scoped); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM role_mapping_scopes WHERE mapping_id = $1", mappingID); err != nil {
		return err
	}
	if scoped && len(groups) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO role_mapping_scopes (mapping_id, group_id)
			SELECT $1, g.id FROM device_groups g WHERE g.id = ANY($2) ON CONFLICT DO NOTHING`, mappingID, groups); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
