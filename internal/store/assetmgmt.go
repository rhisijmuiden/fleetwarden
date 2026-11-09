package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- asset management: owner, location and warranty per device ----

// DeviceAsset is what an organisation records about a device beyond what
// the agent reports. Dates are YYYY-MM-DD, empty when unknown.
type DeviceAsset struct {
	AssetTag     string     `json:"assetTag"`
	Owner        string     `json:"owner"`
	Location     string     `json:"location"`
	Department   string     `json:"department"`
	Supplier     string     `json:"supplier"`
	OrderNumber  string     `json:"orderNumber"`
	PurchaseDate string     `json:"purchaseDate"`
	WarrantyEnd  string     `json:"warrantyEnd"`
	UpdatedBy    string     `json:"updatedBy"`
	UpdatedAt    *time.Time `json:"updatedAt"`
}

// AssetRow is a device in the asset register.
type AssetRow struct {
	DeviceAsset
	ID           string    `json:"id"`
	Hostname     string    `json:"hostname"`
	GroupID      int64     `json:"groupId"`
	GroupName    string    `json:"groupName"`
	Platform     string    `json:"platform"`
	Kind         string    `json:"kind"`
	Manufacturer string    `json:"manufacturer"`
	Model        string    `json:"model"`
	Serial       string    `json:"serial"`
	OSName       string    `json:"osName"`
	LoggedInUser string    `json:"loggedInUser"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	EnrolledAt   time.Time `json:"enrolledAt"`
	Online       bool      `json:"online"` // filled in by the server
}

const assetCols = `COALESCE(a.asset_tag, ''), COALESCE(a.owner, ''), COALESCE(a.location, ''), COALESCE(a.department, ''),
	COALESCE(a.supplier, ''), COALESCE(a.order_number, ''), COALESCE(to_char(a.purchase_date, 'YYYY-MM-DD'), ''),
	COALESCE(to_char(a.warranty_end, 'YYYY-MM-DD'), ''), COALESCE(a.updated_by, ''), a.updated_at`

func scanAsset(dst *DeviceAsset) []any {
	return []any{&dst.AssetTag, &dst.Owner, &dst.Location, &dst.Department, &dst.Supplier, &dst.OrderNumber,
		&dst.PurchaseDate, &dst.WarrantyEnd, &dst.UpdatedBy, &dst.UpdatedAt}
}

// DeviceAssetOf returns the asset details of a device; a device without
// any has empty ones.
func (s *Store) DeviceAssetOf(ctx context.Context, deviceID string) (DeviceAsset, error) {
	var a DeviceAsset
	err := s.pool.QueryRow(ctx, "SELECT "+assetCols+` FROM devices d LEFT JOIN device_assets a ON a.device_id = d.id
		WHERE d.id::text = $1`, deviceID).Scan(scanAsset(&a)...)
	return a, mapErr(err)
}

// AssetQuery filters the asset register.
type AssetQuery struct {
	Scope   Scope
	Search  string // host name, serial, model, asset tag, owner, location, supplier, order
	GroupID int64
	// Warranty: expired, expiring (within WarrantyDays), valid or unknown.
	Warranty     string
	WarrantyDays int
	Today        string // YYYY-MM-DD, the server's date
	IDs          []string
}

// ListAssets returns the asset register, ordered by host name.
func (s *Store) ListAssets(ctx context.Context, q AssetQuery) ([]AssetRow, error) {
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
		p := "%" + likeEscape(q.Search) + "%"
		add(`(d.hostname ILIKE ? OR d.serial ILIKE ? OR d.model ILIKE ? OR d.manufacturer ILIKE ? OR a.asset_tag ILIKE ?
			OR a.owner ILIKE ? OR a.location ILIKE ? OR a.department ILIKE ? OR a.supplier ILIKE ? OR a.order_number ILIKE ?
			OR d.logged_in_user ILIKE ?)`, p, p, p, p, p, p, p, p, p, p, p)
	}
	if q.GroupID != 0 {
		add("d.group_id = ?", q.GroupID)
	}
	if q.IDs != nil {
		add("d.id::text = ANY(?)", q.IDs)
	}
	today := q.Today
	if today == "" {
		today = time.Now().Format(time.DateOnly)
	}
	switch q.Warranty {
	case "expired":
		add("a.warranty_end < ?::date", today)
	case "expiring":
		add("a.warranty_end >= ?::date AND a.warranty_end <= ?::date + ?::int", today, today, q.WarrantyDays)
	case "valid":
		add("a.warranty_end > ?::date + ?::int", today, q.WarrantyDays)
	case "unknown":
		where = append(where, "a.warranty_end IS NULL")
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, g.name, d.platform, d.kind, d.manufacturer, d.model,
		d.serial, d.os_name, d.logged_in_user, d.last_seen_at, d.enrolled_at, `+assetCols+`
		FROM devices d JOIN device_groups g ON g.id = d.group_id LEFT JOIN device_assets a ON a.device_id = d.id
		WHERE `+strings.Join(where, " AND ")+" ORDER BY lower(d.hostname), d.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetRow{}
	for rows.Next() {
		var r AssetRow
		dst := append([]any{&r.ID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Platform, &r.Kind, &r.Manufacturer, &r.Model,
			&r.Serial, &r.OSName, &r.LoggedInUser, &r.LastSeenAt, &r.EnrolledAt}, scanAsset(&r.DeviceAsset)...)
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AssetFields says which fields of a DeviceAsset an update sets; the others
// keep their value.
type AssetFields map[string]bool

// Asset field names, as the API and the CSV import know them.
var AssetFieldNames = []string{"assetTag", "owner", "location", "department", "supplier", "orderNumber", "purchaseDate", "warrantyEnd"}

var assetColumns = map[string]string{"assetTag": "asset_tag", "owner": "owner", "location": "location", "department": "department",
	"supplier": "supplier", "orderNumber": "order_number", "purchaseDate": "purchase_date", "warrantyEnd": "warranty_end"}

func assetValue(a DeviceAsset, field string) any {
	switch field {
	case "assetTag":
		return a.AssetTag
	case "owner":
		return a.Owner
	case "location":
		return a.Location
	case "department":
		return a.Department
	case "supplier":
		return a.Supplier
	case "orderNumber":
		return a.OrderNumber
	case "purchaseDate":
		return dateOrNil(a.PurchaseDate)
	case "warrantyEnd":
		return dateOrNil(a.WarrantyEnd)
	}
	return nil
}

func dateOrNil(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// SetDeviceAssets sets fields of the asset details of devices, in one
// transaction. It returns how many devices were changed.
func (s *Store) SetDeviceAssets(ctx context.Context, updates map[string]DeviceAsset, fields map[string]AssetFields, by string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n := 0
	for id, a := range updates {
		f := fields[id]
		cols := []string{"device_id", "updated_by"}
		vals := []string{"$1::uuid", "$2"}
		sets := []string{"updated_by = EXCLUDED.updated_by", "updated_at = now()"}
		args := []any{id, by}
		for _, name := range AssetFieldNames {
			if !f[name] {
				continue
			}
			col := assetColumns[name]
			args = append(args, assetValue(a, name))
			ph := "$" + itoa(len(args))
			if name == "purchaseDate" || name == "warrantyEnd" {
				ph += "::date"
			}
			cols = append(cols, col)
			vals = append(vals, ph)
			sets = append(sets, col+" = EXCLUDED."+col)
		}
		if len(cols) == 2 {
			continue
		}
		tag, err := tx.Exec(ctx, `INSERT INTO device_assets (`+strings.Join(cols, ", ")+`)
			SELECT `+strings.Join(vals, ", ")+` WHERE EXISTS (SELECT 1 FROM devices WHERE id = $1::uuid)
			ON CONFLICT (device_id) DO UPDATE SET `+strings.Join(sets, ", "), args...)
		if err != nil {
			return 0, mapErr(err)
		}
		n += int(tag.RowsAffected())
	}
	return n, tx.Commit(ctx)
}

// ---- software licences ----

// SoftwareLicense is a number of seats bought for a program.
type SoftwareLicense struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Vendor         string    `json:"vendor"`
	MatchName      string    `json:"matchName"`
	MatchPublisher string    `json:"matchPublisher"`
	Seats          int       `json:"seats"`
	KeyEnc         string    `json:"-"`
	HasKey         bool      `json:"hasKey"`
	ExpiresOn      string    `json:"expiresOn"` // YYYY-MM-DD, empty for perpetual
	Cost           *float64  `json:"cost"`
	Notes          string    `json:"notes"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	InUse          int       `json:"inUse"` // devices with a matching program
}

// LikePattern turns a match with * wildcards into an ILIKE pattern.
func LikePattern(match string) string {
	return strings.ReplaceAll(likeEscape(strings.TrimSpace(match)), "*", "%")
}

// licenseMatch is the condition for a device_software row s of licence l.
const licenseMatch = `s.name ILIKE l.match_like AND (l.pub_like = '' OR s.publisher ILIKE l.pub_like)`

const licenseCols = `l.id, l.name, l.vendor, l.match_name, l.match_publisher, l.seats, l.key_enc, COALESCE(to_char(l.expires_on, 'YYYY-MM-DD'), ''),
	l.cost::float8, l.notes, l.created_by, l.created_at, l.updated_at`

func scanLicense(row pgx.Row) (SoftwareLicense, error) {
	var l SoftwareLicense
	err := row.Scan(&l.ID, &l.Name, &l.Vendor, &l.MatchName, &l.MatchPublisher, &l.Seats, &l.KeyEnc, &l.ExpiresOn, &l.Cost,
		&l.Notes, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt, &l.InUse)
	l.HasKey = l.KeyEnc != ""
	return l, mapErr(err)
}

// licenseSelect counts the devices (every device, licences are
// organisation-wide) with a matching program.
func licenseSelect(where string) string {
	return `SELECT ` + licenseCols + `, (SELECT count(DISTINCT s.device_id) FROM device_software s WHERE ` + licenseMatch + `)::int
		FROM (SELECT x.*, ` + likeSQL("x.match_name") + ` AS match_like, ` + likeSQL("x.match_publisher") + ` AS pub_like
			FROM software_licenses x) l ` + where
}

// likeSQL is LikePattern in SQL.
func likeSQL(col string) string {
	return `replace(replace(replace(replace(btrim(` + col + `), '\', '\\'), '%', '\%'), '_', '\_'), '*', '%')`
}

// ListLicenses returns every licence with its use, ordered by name.
func (s *Store) ListLicenses(ctx context.Context) ([]SoftwareLicense, error) {
	rows, err := s.pool.Query(ctx, licenseSelect("ORDER BY lower(l.name)"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SoftwareLicense{}
	for rows.Next() {
		l, err := scanLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LicenseByID returns a licence.
func (s *Store) LicenseByID(ctx context.Context, id int64) (SoftwareLicense, error) {
	return scanLicense(s.pool.QueryRow(ctx, licenseSelect("WHERE l.id = $1"), id))
}

// CreateLicense inserts a licence.
func (s *Store) CreateLicense(ctx context.Context, l SoftwareLicense) (SoftwareLicense, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO software_licenses (name, vendor, match_name, match_publisher, seats, key_enc, expires_on, cost, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, $9, $10) RETURNING id`, l.Name, l.Vendor, l.MatchName, l.MatchPublisher, l.Seats, l.KeyEnc,
		dateOrNil(l.ExpiresOn), l.Cost, l.Notes, l.CreatedBy).Scan(&id)
	if err != nil {
		return l, mapErr(err)
	}
	return s.LicenseByID(ctx, id)
}

// UpdateLicense changes a licence; the key only when setKey is true.
func (s *Store) UpdateLicense(ctx context.Context, l SoftwareLicense, setKey bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE software_licenses SET name = $2, vendor = $3, match_name = $4, match_publisher = $5, seats = $6,
		key_enc = CASE WHEN $7 THEN $8 ELSE key_enc END, expires_on = $9::date, cost = $10, notes = $11, updated_at = now() WHERE id = $1`,
		l.ID, l.Name, l.Vendor, l.MatchName, l.MatchPublisher, l.Seats, setKey, l.KeyEnc, dateOrNil(l.ExpiresOn), l.Cost, l.Notes)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// DeleteLicense removes a licence.
func (s *Store) DeleteLicense(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM software_licenses WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// LicenseInstall is a device that uses a seat of a licence.
type LicenseInstall struct {
	DeviceID   string    `json:"deviceId"`
	Hostname   string    `json:"hostname"`
	GroupID    int64     `json:"groupId"`
	GroupName  string    `json:"groupName"`
	Owner      string    `json:"owner"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Program    string    `json:"program"`
	Version    string    `json:"version"`
	Publisher  string    `json:"publisher"`
}

// LicenseInstalls returns the devices within a scope that use a seat of a
// licence, with the (first) matching program.
func (s *Store) LicenseInstalls(ctx context.Context, id int64, sc Scope) ([]LicenseInstall, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (lower(d.hostname), d.id) d.id::text, d.hostname, d.group_id, g.name,
		COALESCE(a.owner, ''), d.last_seen_at, s.name, s.version, s.publisher
		FROM (SELECT x.id, `+likeSQL("x.match_name")+` AS match_like, `+likeSQL("x.match_publisher")+` AS pub_like
			FROM software_licenses x WHERE x.id = $1) l
		JOIN device_software s ON `+licenseMatch+`
		JOIN devices d ON d.id = s.device_id JOIN device_groups g ON g.id = d.group_id
		LEFT JOIN device_assets a ON a.device_id = d.id
		WHERE ($2 OR d.group_id = ANY($3))
		ORDER BY lower(d.hostname), d.id, s.name`, id, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LicenseInstall{}
	for rows.Next() {
		var r LicenseInstall
		if err := rows.Scan(&r.DeviceID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Owner, &r.LastSeenAt, &r.Program, &r.Version, &r.Publisher); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LicensePreview counts the devices whose programs a match would count,
// with a few of the program names, before a licence is saved.
func (s *Store) LicensePreview(ctx context.Context, matchName, matchPublisher string) (int, []string, error) {
	var n int
	name, pub := LikePattern(matchName), LikePattern(matchPublisher)
	err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT device_id) FROM device_software s
		WHERE s.name ILIKE $1 AND ($2 = '' OR s.publisher ILIKE $2)`, name, pub).Scan(&n)
	if err != nil {
		return 0, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT s.name FROM device_software s WHERE s.name ILIKE $1 AND ($2 = '' OR s.publisher ILIKE $2)
		GROUP BY s.name ORDER BY count(*) DESC, s.name LIMIT 10`, name, pub)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return 0, nil, err
		}
		names = append(names, v)
	}
	return n, names, rows.Err()
}
