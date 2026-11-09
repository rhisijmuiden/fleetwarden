package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SecuritySummary is the summary of one security report.
type SecuritySummary struct {
	Encryption string `json:"encryption"`
	Antivirus  string `json:"antivirus"`
	Firewall   string `json:"firewall"`
	Threats    int    `json:"threats"`
	Admins     int    `json:"admins"`
	SecureBoot *bool  `json:"secureBoot"`
	TPM        *bool  `json:"tpm"`
}

// SeenKey is a recovery key in a report, its password already sealed.
type SeenKey struct {
	KeyID    string
	Volume   string
	Password string
}

// SecurityUpdate is what a report changes.
type SecurityUpdate struct {
	DeviceID    string
	CollectedAt time.Time
	Summary     SecuritySummary
	Report      json.RawMessage
	Keys        []SeenKey
	// KeysComplete is false when BitLocker could not be read: keys that
	// are missing from the report then stay as they are.
	KeysComplete bool
}

// SaveSecurity stores a report and the recovery keys in it.
func (s *Store) SaveSecurity(ctx context.Context, u SecurityUpdate) error {
	return s.tx(ctx, func(tx txx) error {
		var hostname, serial string
		if err := tx.QueryRow(ctx, "SELECT hostname, serial FROM devices WHERE id::text = $1", u.DeviceID).Scan(&hostname, &serial); err != nil {
			return mapErr(err)
		}
		sm := u.Summary
		if _, err := tx.Exec(ctx, `INSERT INTO device_security (device_id, collected_at, received_at, encryption, antivirus, firewall, threats,
			admins, secure_boot, tpm, report) VALUES ($1::uuid, $2, now(), $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (device_id) DO UPDATE SET collected_at = EXCLUDED.collected_at, received_at = now(), encryption = EXCLUDED.encryption,
			antivirus = EXCLUDED.antivirus, firewall = EXCLUDED.firewall, threats = EXCLUDED.threats, admins = EXCLUDED.admins,
			secure_boot = EXCLUDED.secure_boot, tpm = EXCLUDED.tpm, report = EXCLUDED.report`,
			u.DeviceID, u.CollectedAt, sm.Encryption, sm.Antivirus, sm.Firewall, sm.Threats, sm.Admins, sm.SecureBoot, sm.TPM, u.Report); err != nil {
			return err
		}
		ids := make([]string, 0, len(u.Keys))
		for _, k := range u.Keys {
			ids = append(ids, k.KeyID)
			// The sealed value differs every time; only a changed password
			// (never for the same protector) would need a new one, so the
			// first sealed value stays.
			if _, err := tx.Exec(ctx, `INSERT INTO recovery_keys (key_id, device_id, hostname, serial, volume, password)
				VALUES ($1, $2::uuid, $3, $4, $5, $6)
				ON CONFLICT (key_id) DO UPDATE SET device_id = EXCLUDED.device_id, hostname = EXCLUDED.hostname, serial = EXCLUDED.serial,
				volume = EXCLUDED.volume, last_seen_at = now(), removed_at = NULL`,
				k.KeyID, u.DeviceID, hostname, serial, k.Volume, k.Password); err != nil {
				return err
			}
		}
		if u.KeysComplete {
			if _, err := tx.Exec(ctx, `UPDATE recovery_keys SET removed_at = now()
				WHERE device_id::text = $1 AND removed_at IS NULL AND NOT (key_id = ANY($2))`, u.DeviceID, ids); err != nil {
				return err
			}
		}
		return nil
	})
}

// SecurityStats counts the devices per state.
type SecurityStats struct {
	Devices    int            `json:"devices"`  // devices in scope
	Reported   int            `json:"reported"` // with a security report
	Encryption map[string]int `json:"encryption"`
	Antivirus  map[string]int `json:"antivirus"`
	Firewall   map[string]int `json:"firewall"`
	Threats    int            `json:"threats"` // devices with active threats
	SecureBoot int            `json:"secureBootOff"`
	Keys       int            `json:"keys"` // stored recovery keys in use
}

// SecurityStats counts the devices in scope per state. Linux devices
// without antivirus are counted under "none" like Windows ones; the UI
// decides how serious that is.
func (s *Store) SecurityStats(ctx context.Context, sc Scope) (SecurityStats, error) {
	st := SecurityStats{Encryption: map[string]int{}, Antivirus: map[string]int{}, Firewall: map[string]int{}}
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(ds.device_id), count(*) FILTER (WHERE ds.threats > 0),
		count(*) FILTER (WHERE ds.secure_boot = false)
		FROM devices d LEFT JOIN device_security ds ON ds.device_id = d.id WHERE ($1 OR d.group_id = ANY($2))`,
		sc.All, sc.groups()).Scan(&st.Devices, &st.Reported, &st.Threats, &st.SecureBoot); err != nil {
		return st, err
	}
	rows, err := s.pool.Query(ctx, `SELECT 'e', ds.encryption, count(*) FROM device_security ds JOIN devices d ON d.id = ds.device_id
		WHERE ($1 OR d.group_id = ANY($2)) GROUP BY ds.encryption
		UNION ALL SELECT 'a', ds.antivirus, count(*) FROM device_security ds JOIN devices d ON d.id = ds.device_id
		WHERE ($1 OR d.group_id = ANY($2)) GROUP BY ds.antivirus
		UNION ALL SELECT 'f', ds.firewall, count(*) FROM device_security ds JOIN devices d ON d.id = ds.device_id
		WHERE ($1 OR d.group_id = ANY($2)) GROUP BY ds.firewall`, sc.All, sc.groups())
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, state string
		var n int
		if err := rows.Scan(&kind, &state, &n); err != nil {
			return st, err
		}
		map[string]map[string]int{"e": st.Encryption, "a": st.Antivirus, "f": st.Firewall}[kind][state] = n
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM recovery_keys k JOIN devices d ON d.id = k.device_id
		WHERE k.removed_at IS NULL AND ($1 OR d.group_id = ANY($2))`, sc.All, sc.groups()).Scan(&st.Keys)
	return st, err
}

// SecurityRow is one device in the security overview.
type SecurityRow struct {
	DeviceID    string     `json:"deviceId"`
	Hostname    string     `json:"hostname"`
	GroupID     int64      `json:"groupId"`
	GroupName   string     `json:"groupName"`
	Platform    string     `json:"platform"`
	Kind        string     `json:"kind"`
	OSName      string     `json:"osName"`
	LastSeenAt  *time.Time `json:"lastSeenAt"`
	CollectedAt *time.Time `json:"collectedAt"`
	SecuritySummary
	Keys int `json:"keys"`
}

// SecurityQuery filters the overview.
type SecurityQuery struct {
	Scope   Scope
	Search  string
	GroupID int64
	// Filter: unencrypted (not encrypted, partial or suspended), antivirus
	// (not ok), firewall (not on), threats, secureboot (off), unreported.
	Filter string
}

// ListSecurity returns the devices with their security summary.
func (s *Store) ListSecurity(ctx context.Context, q SecurityQuery) ([]SecurityRow, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v ...any) {
		for _, x := range v {
			args = append(args, x)
			cond = strings.Replace(cond, "?", "$"+itoa(len(args)), 1)
		}
		where = append(where, cond)
	}
	if q.Search != "" {
		add("(d.hostname ILIKE ? OR d.serial ILIKE ? OR d.logged_in_user ILIKE ?)", "%"+likeEscape(q.Search)+"%", "%"+likeEscape(q.Search)+"%",
			"%"+likeEscape(q.Search)+"%")
	}
	if q.GroupID != 0 {
		add("d.group_id = ?", q.GroupID)
	}
	switch q.Filter {
	case "unencrypted":
		add("ds.encryption IN ('none', 'partial', 'suspended')")
	case "antivirus":
		add("ds.antivirus <> 'ok' AND NOT (d.platform = 'linux' AND ds.antivirus = 'none')")
	case "firewall":
		add("ds.firewall IN ('off', 'partial')")
	case "threats":
		add("ds.threats > 0")
	case "secureboot":
		add("ds.secure_boot = false")
	case "unreported":
		add("ds.device_id IS NULL")
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, g.name, d.platform, d.kind, d.os_name, d.last_seen_at,
		ds.collected_at, COALESCE(ds.encryption, ''), COALESCE(ds.antivirus, ''), COALESCE(ds.firewall, ''), COALESCE(ds.threats, 0),
		COALESCE(ds.admins, 0), ds.secure_boot, ds.tpm,
		(SELECT count(*) FROM recovery_keys k WHERE k.device_id = d.id AND k.removed_at IS NULL)::int
		FROM devices d JOIN device_groups g ON g.id = d.group_id LEFT JOIN device_security ds ON ds.device_id = d.id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY lower(d.hostname), d.id LIMIT 5000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityRow{}
	for rows.Next() {
		var r SecurityRow
		if err := rows.Scan(&r.DeviceID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Platform, &r.Kind, &r.OSName, &r.LastSeenAt,
			&r.CollectedAt, &r.Encryption, &r.Antivirus, &r.Firewall, &r.Threats, &r.Admins, &r.SecureBoot, &r.TPM, &r.Keys); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeviceSecurity is the stored report of one device.
type DeviceSecurity struct {
	CollectedAt time.Time       `json:"collectedAt"`
	ReceivedAt  time.Time       `json:"receivedAt"`
	Summary     SecuritySummary `json:"summary"`
	Report      json.RawMessage `json:"report"`
}

// DeviceSecurity returns the last report of a device.
func (s *Store) DeviceSecurity(ctx context.Context, deviceID string) (DeviceSecurity, error) {
	var d DeviceSecurity
	sm := &d.Summary
	err := s.pool.QueryRow(ctx, `SELECT collected_at, received_at, encryption, antivirus, firewall, threats, admins, secure_boot, tpm, report
		FROM device_security WHERE device_id::text = $1`, deviceID).Scan(&d.CollectedAt, &d.ReceivedAt, &sm.Encryption, &sm.Antivirus,
		&sm.Firewall, &sm.Threats, &sm.Admins, &sm.SecureBoot, &sm.TPM, &d.Report)
	return d, mapErr(err)
}

// RecoveryKey is a stored BitLocker recovery key, without its password.
type RecoveryKey struct {
	ID          int64      `json:"id"`
	KeyID       string     `json:"keyId"`
	DeviceID    *string    `json:"deviceId"`
	Hostname    string     `json:"hostname"`
	Serial      string     `json:"serial"`
	GroupName   string     `json:"groupName"`
	Volume      string     `json:"volume"`
	FirstSeenAt time.Time  `json:"firstSeenAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	RemovedAt   *time.Time `json:"removedAt"`
	RevealedAt  *time.Time `json:"revealedAt"`
	RevealedBy  string     `json:"revealedBy"`
	groupID     *int64
	password    string
}

// Password returns the sealed password.
func (k RecoveryKey) Password() string { return k.password }

// InScope reports whether a user with this scope may see the key. Keys of
// deleted devices are for unscoped users only.
func (k RecoveryKey) InScope(sc Scope) bool {
	if sc.All {
		return true
	}
	return k.groupID != nil && sc.Allows(*k.groupID)
}

const recoveryKeyCols = `k.id, k.key_id, k.device_id::text, k.hostname, k.serial, COALESCE(g.name, ''), k.volume, k.first_seen_at,
	k.last_seen_at, k.removed_at, k.revealed_at, k.revealed_by, d.group_id, k.password`

const recoveryKeyFrom = ` FROM recovery_keys k LEFT JOIN devices d ON d.id = k.device_id LEFT JOIN device_groups g ON g.id = d.group_id `

func scanRecoveryKey(row interface{ Scan(...any) error }) (RecoveryKey, error) {
	var k RecoveryKey
	err := row.Scan(&k.ID, &k.KeyID, &k.DeviceID, &k.Hostname, &k.Serial, &k.GroupName, &k.Volume, &k.FirstSeenAt, &k.LastSeenAt,
		&k.RemovedAt, &k.RevealedAt, &k.RevealedBy, &k.groupID, &k.password)
	return k, err
}

// RecoveryKeyQuery finds recovery keys.
type RecoveryKeyQuery struct {
	Scope    Scope
	Search   string // the key ID (or its first characters), hostname or serial number
	DeviceID string
}

// ListRecoveryKeys returns matching keys, the ones in use first.
func (s *Store) ListRecoveryKeys(ctx context.Context, q RecoveryKeyQuery) ([]RecoveryKey, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	if q.Search != "" {
		// The recovery screen shows the key ID without braces.
		id := strings.ToUpper(strings.Trim(strings.TrimSpace(q.Search), "{}"))
		args = append(args, likeEscape(id)+"%", "%"+likeEscape(q.Search)+"%")
		where = append(where, "(upper(trim(both '{}' from k.key_id)) LIKE $3 OR k.hostname ILIKE $4 OR k.serial ILIKE $4)")
	}
	if q.DeviceID != "" {
		args = append(args, q.DeviceID)
		where = append(where, "k.device_id::text = $"+itoa(len(args)))
	}
	rows, err := s.pool.Query(ctx, "SELECT "+recoveryKeyCols+recoveryKeyFrom+"WHERE "+strings.Join(where, " AND ")+
		" ORDER BY (k.removed_at IS NULL) DESC, lower(k.hostname), k.volume, k.last_seen_at DESC LIMIT 200", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecoveryKey{}
	for rows.Next() {
		k, err := scanRecoveryKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RecoveryKeyByID returns one key with its sealed password.
func (s *Store) RecoveryKeyByID(ctx context.Context, id int64) (RecoveryKey, error) {
	k, err := scanRecoveryKey(s.pool.QueryRow(ctx, "SELECT "+recoveryKeyCols+recoveryKeyFrom+"WHERE k.id = $1", id))
	return k, mapErr(err)
}

// MarkKeyRevealed records who looked at a key last.
func (s *Store) MarkKeyRevealed(ctx context.Context, id int64, by string) error {
	_, err := s.pool.Exec(ctx, "UPDATE recovery_keys SET revealed_at = now(), revealed_by = $2 WHERE id = $1", id, by)
	return err
}

// ---- actions ----

// SecurityAction is a Defender scan, signature update or key rotation.
type SecurityAction struct {
	ID          int64      `json:"id"`
	DeviceID    string     `json:"deviceId"`
	Action      string     `json:"action"`
	Mount       string     `json:"mount"`
	State       string     `json:"state"`
	Message     string     `json:"message"`
	RequestedBy string     `json:"requestedBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

const securityActionCols = "id, device_id::text, action, mount, state, message, requested_by, created_at, finished_at"

func scanSecurityAction(row interface{ Scan(...any) error }) (SecurityAction, error) {
	var a SecurityAction
	err := row.Scan(&a.ID, &a.DeviceID, &a.Action, &a.Mount, &a.State, &a.Message, &a.RequestedBy, &a.CreatedAt, &a.FinishedAt)
	return a, err
}

// CreateSecurityAction records a new action. It answers ErrConflict when the
// same action is still open on the device.
func (s *Store) CreateSecurityAction(ctx context.Context, a SecurityAction) (SecurityAction, error) {
	var out SecurityAction
	err := s.tx(ctx, func(tx txx) error {
		// The device row serialises actions of one device.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM devices WHERE id::text = $1 FOR UPDATE", a.DeviceID); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM security_actions WHERE device_id::text = $1 AND action = $2
			AND state IN ('queued', 'running')`, a.DeviceID, a.Action).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return ErrConflict
		}
		var err error
		out, err = scanSecurityAction(tx.QueryRow(ctx, `INSERT INTO security_actions (device_id, action, mount, requested_by)
			VALUES ($1::uuid, $2, $3, $4) RETURNING `+securityActionCols, a.DeviceID, a.Action, a.Mount, a.RequestedBy))
		return err
	})
	return out, err
}

// UpdateSecurityAction stores the state an agent reported. Finished actions
// do not change any more.
func (s *Store) UpdateSecurityAction(ctx context.Context, deviceID string, id int64, state, msg string) (SecurityAction, bool, error) {
	a, err := scanSecurityAction(s.pool.QueryRow(ctx, `UPDATE security_actions SET state = $3, message = $4,
		finished_at = CASE WHEN $3 IN ('completed', 'failed') THEN now() END
		WHERE id = $1 AND device_id::text = $2 AND state IN ('queued', 'running') RETURNING `+securityActionCols, id, deviceID, state, msg))
	if err != nil {
		if mapErr(err) == ErrNotFound {
			return a, false, nil
		}
		return a, false, err
	}
	return a, true, nil
}

// ListSecurityActions returns the latest actions of a device.
func (s *Store) ListSecurityActions(ctx context.Context, deviceID string, limit int) ([]SecurityAction, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+securityActionCols+" FROM security_actions WHERE device_id::text = $1 ORDER BY created_at DESC, id DESC LIMIT $2",
		deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityAction{}
	for rows.Next() {
		a, err := scanSecurityAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenSecurityActions returns the queued actions of a device, to send again
// when it connects.
func (s *Store) OpenSecurityActions(ctx context.Context, deviceID string) ([]SecurityAction, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+securityActionCols+" FROM security_actions WHERE device_id::text = $1 AND state = 'queued' ORDER BY id",
		deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecurityAction
	for rows.Next() {
		a, err := scanSecurityAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ExpireSecurityActions fails actions that got no answer: queued ones after
// a day (the device stayed offline), running ones after 13 hours (a full
// scan may take 12).
func (s *Store) ExpireSecurityActions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE security_actions SET state = 'failed', message = 'no answer from the device', finished_at = now()
		WHERE (state = 'queued' AND created_at < now() - interval '1 day') OR (state = 'running' AND created_at < now() - interval '13 hours')`)
	return err
}

// SecurityAlertDevice is what the security alert rules look at.
type SecurityAlertDevice struct {
	DeviceID   string
	Platform   string
	Summary    SecuritySummary
	Report     json.RawMessage
	ReceivedAt time.Time
}

// SecurityAlertDevices returns the summaries of every device with a report.
func (s *Store) SecurityAlertDevices(ctx context.Context) (map[string]SecurityAlertDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT ds.device_id::text, d.platform, ds.encryption, ds.antivirus, ds.firewall, ds.threats, ds.received_at,
		ds.report FROM device_security ds JOIN devices d ON d.id = ds.device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SecurityAlertDevice{}
	for rows.Next() {
		var d SecurityAlertDevice
		if err := rows.Scan(&d.DeviceID, &d.Platform, &d.Summary.Encryption, &d.Summary.Antivirus, &d.Summary.Firewall, &d.Summary.Threats,
			&d.ReceivedAt, &d.Report); err != nil {
			return nil, err
		}
		out[d.DeviceID] = d
	}
	return out, rows.Err()
}

type txx = pgx.Tx

// tx runs f in a transaction.
func (s *Store) tx(ctx context.Context, f func(tx txx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
