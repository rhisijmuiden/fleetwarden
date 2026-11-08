package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- driver and firmware repository ----

// Driver update states.
const (
	DriverAvailable   = "available"   // offered to devices, not in the repository
	DriverQueued      = "queued"      // added to the repository, waiting for the download
	DriverDownloading = "downloading" // the server is fetching the files
	DriverReady       = "ready"       // in the repository: devices may install it
	DriverFailed      = "failed"      // the download failed
	DriverRejected    = "rejected"    // hidden: never added
)

// DriverFile is one file of a driver update: where the vendor offers it and,
// once stored, its SHA-256 in the repository.
type DriverFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"` // the vendor's checksum, when known
	SHA1   string `json:"sha1,omitempty"`
	Stored string `json:"stored,omitempty"` // SHA-256 of the stored file
}

// DriverUpdate is a driver or firmware update in the catalog.
type DriverUpdate struct {
	ID          int64        `json:"id"`
	Source      string       `json:"source"`
	SourceID    string       `json:"sourceId"`
	Kind        string       `json:"kind"`
	Platform    string       `json:"platform"`
	Title       string       `json:"title"`
	Vendor      string       `json:"vendor"`
	Class       string       `json:"class"`
	Version     string       `json:"version"`
	ReleasedAt  *time.Time   `json:"releasedAt"`
	SizeBytes   int64        `json:"sizeBytes"`
	HardwareID  string       `json:"hardwareId"`
	Description string       `json:"description"`
	Reboot      bool         `json:"reboot"`
	Files       []DriverFile `json:"files"`
	State       string       `json:"state"`
	Error       string       `json:"error"`
	StoredBytes int64        `json:"storedBytes"`
	Attempts    int          `json:"attempts"`
	DecidedBy   string       `json:"decidedBy"`
	DecidedAt   *time.Time   `json:"decidedAt"`
	StoredAt    *time.Time   `json:"storedAt"`
	FirstSeenAt time.Time    `json:"firstSeenAt"`
	LastSeenAt  time.Time    `json:"lastSeenAt"`
	// Devices counts the devices in the caller's scope that can install it.
	Devices int `json:"devices"`
}

const driverCols = `u.id, u.source, u.source_id, u.kind, u.platform, u.title, u.vendor, u.class, u.version, u.released_at,
	u.size_bytes, u.hardware_id, u.description, u.reboot, u.files, u.state, u.error, u.stored_bytes, u.attempts, u.decided_by,
	u.decided_at, u.stored_at, u.first_seen_at, u.last_seen_at`

func scanDriver(row pgx.Row, extra ...any) (DriverUpdate, error) {
	var u DriverUpdate
	var files []byte
	dst := []any{&u.ID, &u.Source, &u.SourceID, &u.Kind, &u.Platform, &u.Title, &u.Vendor, &u.Class, &u.Version, &u.ReleasedAt,
		&u.SizeBytes, &u.HardwareID, &u.Description, &u.Reboot, &files, &u.State, &u.Error, &u.StoredBytes, &u.Attempts, &u.DecidedBy,
		&u.DecidedAt, &u.StoredAt, &u.FirstSeenAt, &u.LastSeenAt}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return u, mapErr(err)
	}
	u.Files = []DriverFile{}
	_ = json.Unmarshal(files, &u.Files)
	return u, nil
}

// DeviceDriverRow is a driver update offered to one device.
type DeviceDriverRow struct {
	DriverUpdate
	DeviceName     string    `json:"deviceName"`
	FwDeviceID     string    `json:"fwDeviceId"`
	CurrentVersion string    `json:"currentVersion"`
	SeenAt         time.Time `json:"seenAt"`
}

// DriverScan is what one device reported.
type DriverScan struct {
	Platform string
	Error    string
	Updates  []DriverScanItem
}

// DriverScanItem is one offered update with the device-specific details.
type DriverScanItem struct {
	Update         DriverUpdate
	DeviceName     string
	FwDeviceID     string
	CurrentVersion string
}

// SaveDriverScan adds new updates to the catalog (as available) and
// replaces the list of updates offered to the device. A failed scan without
// results keeps the previous list. Catalog details are only refreshed while
// an update is not in the repository, so what was approved stays as it was.
func (s *Store) SaveDriverScan(ctx context.Context, deviceID string, r DriverScan) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if r.Error == "" || len(r.Updates) > 0 {
		keep := make([]int64, 0, len(r.Updates))
		for _, it := range r.Updates {
			u := it.Update
			files, _ := json.Marshal(u.Files)
			var id int64
			err := tx.QueryRow(ctx, `INSERT INTO driver_updates (source, source_id, kind, platform, title, vendor, class, version,
				released_at, size_bytes, hardware_id, description, reboot, files)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				ON CONFLICT (source, source_id) DO UPDATE SET last_seen_at = now(),
					title = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $5 ELSE driver_updates.title END,
					vendor = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $6 ELSE driver_updates.vendor END,
					class = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $7 ELSE driver_updates.class END,
					version = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $8 ELSE driver_updates.version END,
					released_at = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $9 ELSE driver_updates.released_at END,
					size_bytes = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $10 ELSE driver_updates.size_bytes END,
					description = CASE WHEN driver_updates.state IN ('available', 'rejected', 'failed') THEN $12 ELSE driver_updates.description END,
					files = CASE WHEN driver_updates.state IN ('available', 'rejected') OR
						(driver_updates.state = 'failed' AND jsonb_array_length($14::jsonb) > 0) THEN $14::jsonb ELSE driver_updates.files END
				RETURNING id`,
				u.Source, u.SourceID, u.Kind, r.Platform, u.Title, u.Vendor, u.Class, u.Version, u.ReleasedAt, u.SizeBytes, u.HardwareID,
				u.Description, u.Reboot, files).Scan(&id)
			if err != nil {
				return err
			}
			keep = append(keep, id)
			if _, err := tx.Exec(ctx, `INSERT INTO device_drivers (device_id, driver_update_id, device_name, fw_device_id, current_version)
				VALUES ($1::uuid, $2, $3, $4, $5) ON CONFLICT (device_id, driver_update_id)
				DO UPDATE SET device_name = $3, fw_device_id = $4, current_version = $5`,
				deviceID, id, it.DeviceName, it.FwDeviceID, it.CurrentVersion); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM device_drivers WHERE device_id::text = $1 AND NOT (driver_update_id = ANY($2))",
			deviceID, keep); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE devices SET driver_scan_at = now(), driver_scan_error = $2 WHERE id::text = $1",
		deviceID, r.Error); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DriverQuery filters the catalog.
type DriverQuery struct {
	Scope  Scope
	Search string
	Kind   string
	State  string // a state, or "repository" for queued, downloading and ready
	// Offered limits the list to updates a device in the scope can install;
	// the repository view shows every stored update.
	Offered bool
}

// ListDrivers returns catalog entries with the number of devices in the
// scope that can install them.
func (s *Store) ListDrivers(ctx context.Context, q DriverQuery) ([]DriverUpdate, error) {
	where := []string{"true"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q.Search != "" {
		add("(u.title ILIKE ? OR u.vendor ILIKE ? OR u.class ILIKE ? OR u.hardware_id ILIKE ?)", "%"+likeEscape(q.Search)+"%")
	}
	if q.Kind != "" {
		add("u.kind = ?", q.Kind)
	}
	switch q.State {
	case "":
	case "repository":
		where = append(where, "u.state IN ('queued', 'downloading', 'ready')")
	default:
		add("u.state = ?", q.State)
	}
	counted := `(SELECT count(*)::int FROM device_drivers dd JOIN devices d ON d.id = dd.device_id
		WHERE dd.driver_update_id = u.id AND ($1 OR d.group_id = ANY($2)))`
	if q.Offered {
		where = append(where, counted+" > 0")
	}
	rows, err := s.pool.Query(ctx, "SELECT "+driverCols+", "+counted+" FROM driver_updates u WHERE "+strings.Join(where, " AND ")+
		" ORDER BY "+counted+" DESC, lower(u.title) LIMIT 2000", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DriverUpdate{}
	for rows.Next() {
		var n int
		u, err := scanDriver(rows, &n)
		if err != nil {
			return nil, err
		}
		u.Devices = n
		out = append(out, u)
	}
	return out, rows.Err()
}

// DriverByID returns a catalog entry.
func (s *Store) DriverByID(ctx context.Context, id int64) (DriverUpdate, error) {
	return scanDriver(s.pool.QueryRow(ctx, "SELECT "+driverCols+" FROM driver_updates u WHERE u.id = $1", id))
}

// DriverDevice is a device that can install a catalog entry.
type DriverDevice struct {
	DeviceID       string     `json:"deviceId"`
	Hostname       string     `json:"hostname"`
	GroupName      string     `json:"groupName"`
	Online         bool       `json:"online"`
	DeviceName     string     `json:"deviceName"`
	CurrentVersion string     `json:"currentVersion"`
	SeenAt         time.Time  `json:"seenAt"`
	LastSeen       *time.Time `json:"lastSeen"`
}

// DriverDevices lists the devices in the scope that can install an update.
func (s *Store) DriverDevices(ctx context.Context, id int64, sc Scope) ([]DriverDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, g.name, dd.device_name, dd.current_version, dd.first_seen_at, d.last_seen_at
		FROM device_drivers dd JOIN devices d ON d.id = dd.device_id JOIN device_groups g ON g.id = d.group_id
		WHERE dd.driver_update_id = $1 AND ($2 OR d.group_id = ANY($3)) ORDER BY lower(d.hostname) LIMIT 5000`, id, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DriverDevice{}
	for rows.Next() {
		var d DriverDevice
		if err := rows.Scan(&d.DeviceID, &d.Hostname, &d.GroupName, &d.DeviceName, &d.CurrentVersion, &d.SeenAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceDrivers returns the driver updates offered to a device.
func (s *Store) DeviceDrivers(ctx context.Context, deviceID string) ([]DeviceDriverRow, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+driverCols+`, dd.device_name, dd.fw_device_id, dd.current_version, dd.first_seen_at
		FROM device_drivers dd JOIN driver_updates u ON u.id = dd.driver_update_id
		WHERE dd.device_id::text = $1 ORDER BY u.kind, lower(u.title)`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceDriverRow{}
	for rows.Next() {
		var r DeviceDriverRow
		u, err := scanDriver(rows, &r.DeviceName, &r.FwDeviceID, &r.CurrentVersion, &r.SeenAt)
		if err != nil {
			return nil, err
		}
		r.DriverUpdate = u
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetDriverState changes the state of catalog entries by hand: add to the
// repository (queued), reject, or back to available (which also forgets the
// stored files). It returns the entries that changed.
func (s *Store) SetDriverState(ctx context.Context, ids []int64, state, by string) ([]DriverUpdate, error) {
	var from string
	switch state {
	case DriverQueued:
		from = "('available', 'rejected', 'failed')"
	case DriverRejected:
		from = "('available', 'failed')"
	case DriverAvailable:
		from = "('queued', 'downloading', 'ready', 'failed', 'rejected')"
	default:
		return nil, ErrConflict
	}
	rows, err := s.pool.Query(ctx, `UPDATE driver_updates u SET state = $2, decided_by = $3, decided_at = now(), error = '', attempts = 0,
		stored_bytes = CASE WHEN $2 = 'available' THEN 0 ELSE stored_bytes END,
		stored_at = CASE WHEN $2 = 'available' THEN NULL ELSE stored_at END,
		files = CASE WHEN $2 = 'available' THEN (SELECT COALESCE(jsonb_agg(f - 'stored'), '[]') FROM jsonb_array_elements(files) f) ELSE files END
		WHERE u.id = ANY($1) AND u.state IN `+from+" RETURNING "+driverCols, ids, state, by)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DriverUpdate{}
	for rows.Next() {
		u, err := scanDriver(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// NextDriverDownload claims the oldest queued entry for download.
func (s *Store) NextDriverDownload(ctx context.Context) (DriverUpdate, error) {
	return scanDriver(s.pool.QueryRow(ctx, `UPDATE driver_updates u SET state = 'downloading', attempts = attempts + 1
		WHERE u.id = (SELECT id FROM driver_updates WHERE state = 'queued' ORDER BY decided_at NULLS FIRST, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+driverCols))
}

// FinishDriverDownload stores the outcome of a download: ready with the
// stored files, back to queued for another try, or failed.
func (s *Store) FinishDriverDownload(ctx context.Context, id int64, state string, files []DriverFile, stored int64, errText string) error {
	raw, _ := json.Marshal(files)
	_, err := s.pool.Exec(ctx, `UPDATE driver_updates SET state = $2, files = $3, stored_bytes = $4, error = $5,
		stored_at = CASE WHEN $2 = 'ready' THEN now() ELSE stored_at END WHERE id = $1 AND state = 'downloading'`,
		id, state, raw, stored, errText)
	return err
}

// RequeueDriver puts a ready entry whose files went missing (after a restore
// without the repository folder) back in the download queue.
func (s *Store) RequeueDriver(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE driver_updates SET state = 'queued', attempts = 0, error = '', stored_at = NULL, stored_bytes = 0
		WHERE id = $1 AND state = 'ready'`, id)
	return err
}

// ResetInterruptedDriverDownloads puts downloads that were running when the
// server stopped back in the queue.
func (s *Store) ResetInterruptedDriverDownloads(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "UPDATE driver_updates SET state = 'queued' WHERE state = 'downloading'")
	return err
}

// StoredDriverFiles returns every SHA-256 that a catalog entry refers to.
func (s *Store) StoredDriverFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT f->>'stored' FROM driver_updates u, jsonb_array_elements(u.files) f
		WHERE u.state IN ('downloading', 'ready') AND COALESCE(f->>'stored', '') <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sum string
		if err := rows.Scan(&sum); err != nil {
			return nil, err
		}
		out[sum] = true
	}
	return out, rows.Err()
}

// ReadyDrivers returns the repository entries a device can install.
func (s *Store) ReadyDrivers(ctx context.Context, deviceID string) ([]DeviceDriverRow, error) {
	all, err := s.DeviceDrivers(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, r := range all {
		if r.State == DriverReady {
			out = append(out, r)
		}
	}
	return out, nil
}

// ReadyDriverEntries returns every ready entry (for the file check).
func (s *Store) ReadyDriverEntries(ctx context.Context) ([]DriverUpdate, error) {
	return s.ListDrivers(ctx, DriverQuery{Scope: Scope{All: true}, State: DriverReady})
}

// DeviceMayDownloadDriver reports whether a device has an open patch job
// with a repository update that has this file.
func (s *Store) DeviceMayDownloadDriver(ctx context.Context, deviceID, sha string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM patch_runs r JOIN driver_updates u ON ('driver:' || u.id) = ANY(r.updates)
		WHERE r.device_id::text = $1 AND r.finished_at IS NULL AND u.state = 'ready'
		AND u.files @> jsonb_build_array(jsonb_build_object('stored', $2::text)))`, deviceID, sha).Scan(&ok)
	return ok, err
}

// DriverStats summarises the repository.
type DriverStats struct {
	Available   int   `json:"available"`   // offered to a device, not in the repository
	Queued      int   `json:"queued"`      // queued or downloading
	Ready       int   `json:"ready"`       // in the repository
	Failed      int   `json:"failed"`      // download failed
	StoredBytes int64 `json:"storedBytes"` // size of the repository
	Devices     int   `json:"devices"`     // devices in the scope with an update on offer
	Scanned     int   `json:"scanned"`     // devices in the scope with a driver scan
}

// DriverStats counts the repository and the devices in the scope.
func (s *Store) DriverStats(ctx context.Context, sc Scope) (DriverStats, error) {
	var st DriverStats
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM driver_updates u WHERE u.state = 'available' AND EXISTS (SELECT 1 FROM device_drivers dd
			JOIN devices d ON d.id = dd.device_id WHERE dd.driver_update_id = u.id AND ($1 OR d.group_id = ANY($2)))),
		(SELECT count(*)::int FROM driver_updates WHERE state IN ('queued', 'downloading')),
		(SELECT count(*)::int FROM driver_updates WHERE state = 'ready'),
		(SELECT count(*)::int FROM driver_updates WHERE state = 'failed'),
		(SELECT COALESCE(sum(stored_bytes), 0)::bigint FROM driver_updates WHERE state = 'ready'),
		(SELECT count(DISTINCT dd.device_id)::int FROM device_drivers dd JOIN devices d ON d.id = dd.device_id
			JOIN driver_updates u ON u.id = dd.driver_update_id WHERE u.state <> 'rejected' AND ($1 OR d.group_id = ANY($2))),
		(SELECT count(*)::int FROM devices d WHERE d.driver_scan_at IS NOT NULL AND ($1 OR d.group_id = ANY($2)))`,
		sc.All, sc.groups()).Scan(&st.Available, &st.Queued, &st.Ready, &st.Failed, &st.StoredBytes, &st.Devices, &st.Scanned)
	return st, err
}

// DriverScanInfo returns when the device last reported its driver updates.
func (s *Store) DriverScanInfo(ctx context.Context, deviceID string) (*time.Time, string, error) {
	var at *time.Time
	var errText string
	err := s.pool.QueryRow(ctx, "SELECT driver_scan_at, driver_scan_error FROM devices WHERE id::text = $1", deviceID).Scan(&at, &errText)
	return at, errText, mapErr(err)
}
