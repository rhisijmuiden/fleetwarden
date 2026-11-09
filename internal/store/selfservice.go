package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- self-service catalog ----

// SelfServiceItem is a library package published in the self-service catalog.
type SelfServiceItem struct {
	ID          int64      `json:"id"`
	PackageID   int64      `json:"packageId"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	GroupIDs    []int64    `json:"groupIds"`
	Approval    bool       `json:"approval"`
	Enabled     bool       `json:"enabled"`
	CreatedBy   string     `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	Groups      []GroupRef `json:"groups"`
	// From the package.
	PackageName        string `json:"packageName"`
	PackageDescription string `json:"packageDescription"`
	Publisher          string `json:"publisher"`
	Platform           string `json:"platform"`
	Source             string `json:"source"`
	Version            string `json:"version"`
	// Requests counts the requests of the last 30 days, Installs the
	// completed installs among them.
	Requests int `json:"requests"`
	Installs int `json:"installs"`
}

// Name is what the user sees.
func (i SelfServiceItem) Name() string {
	if i.DisplayName != "" {
		return i.DisplayName
	}
	return i.PackageName
}

// Text is the description the user sees.
func (i SelfServiceItem) Text() string {
	if i.Description != "" {
		return i.Description
	}
	return i.PackageDescription
}

const ssItemCols = `i.id, i.package_id, i.display_name, i.description, i.category, i.group_ids, i.approval, i.enabled, i.created_by,
	i.created_at, i.updated_at,
	COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY lower(g.name)) FROM device_groups g
		WHERE g.id = ANY(i.group_ids)), '[]'),
	p.name, p.description, p.publisher, p.platform, p.source, p.version,
	(SELECT count(*) FROM selfservice_requests r WHERE r.item_id = i.id AND r.created_at > now() - interval '30 days')::int,
	(SELECT count(*) FROM selfservice_requests r JOIN software_jobs j ON j.deployment_id = r.deployment_id
		WHERE r.item_id = i.id AND r.created_at > now() - interval '30 days' AND j.state = 'completed')::int`

func scanSSItem(row pgx.Row) (SelfServiceItem, error) {
	var i SelfServiceItem
	var groups []byte
	err := row.Scan(&i.ID, &i.PackageID, &i.DisplayName, &i.Description, &i.Category, &i.GroupIDs, &i.Approval, &i.Enabled, &i.CreatedBy,
		&i.CreatedAt, &i.UpdatedAt, &groups, &i.PackageName, &i.PackageDescription, &i.Publisher, &i.Platform, &i.Source, &i.Version,
		&i.Requests, &i.Installs)
	if err != nil {
		return i, mapErr(err)
	}
	if i.GroupIDs == nil {
		i.GroupIDs = []int64{}
	}
	i.Groups = []GroupRef{}
	_ = json.Unmarshal(groups, &i.Groups)
	return i, nil
}

// ListSelfServiceItems returns the catalog, sorted by category and name.
func (s *Store) ListSelfServiceItems(ctx context.Context) ([]SelfServiceItem, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+ssItemCols+` FROM selfservice_items i JOIN software_packages p ON p.id = i.package_id
		ORDER BY lower(i.category), lower(COALESCE(NULLIF(i.display_name, ''), p.name))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SelfServiceItem{}
	for rows.Next() {
		i, err := scanSSItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// SelfServiceItemByID returns one catalog item.
func (s *Store) SelfServiceItemByID(ctx context.Context, id int64) (SelfServiceItem, error) {
	return scanSSItem(s.pool.QueryRow(ctx, "SELECT "+ssItemCols+` FROM selfservice_items i JOIN software_packages p ON p.id = i.package_id
		WHERE i.id = $1`, id))
}

// CreateSelfServiceItem publishes a package; ErrConflict when it already is.
func (s *Store) CreateSelfServiceItem(ctx context.Context, i SelfServiceItem) (int64, error) {
	if i.GroupIDs == nil {
		i.GroupIDs = []int64{}
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO selfservice_items (package_id, display_name, description, category, group_ids, approval, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		i.PackageID, i.DisplayName, i.Description, i.Category, i.GroupIDs, i.Approval, i.Enabled, i.CreatedBy).Scan(&id)
	return id, mapErr(err)
}

// UpdateSelfServiceItem changes how an item shows and who sees it.
func (s *Store) UpdateSelfServiceItem(ctx context.Context, i SelfServiceItem) error {
	if i.GroupIDs == nil {
		i.GroupIDs = []int64{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE selfservice_items SET display_name = $2, description = $3, category = $4, group_ids = $5, approval = $6,
		enabled = $7, updated_at = now() WHERE id = $1`, i.ID, i.DisplayName, i.Description, i.Category, i.GroupIDs, i.Approval, i.Enabled)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSelfServiceItem removes an item from the catalog and cancels its
// requests that wait for approval.
func (s *Store) DeleteSelfServiceItem(ctx context.Context, id int64, by string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE selfservice_requests SET state = 'cancelled', decided_by = $2, decided_at = now(),
		note = 'removed from the catalog' WHERE item_id = $1 AND state = 'pending'`, id, by); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM selfservice_items WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// CatalogEntry is a catalog item as one device sees it.
type CatalogEntry struct {
	Item SelfServiceItem
	// Installed: the inventory has the program.
	Installed bool
	// The newest request of the device for the item.
	RequestID    int64
	RequestState string // pending, approved, rejected, cancelled; '' without a request
	RequestNote  string
	JobState     string // the install job of an approved request
	JobMessage   string
	RequestedAt  time.Time
}

// DeviceCatalog returns the enabled items for a device's platform and
// group, with the device's newest request per item.
func (s *Store) DeviceCatalog(ctx context.Context, deviceID string) ([]CatalogEntry, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+ssItemCols+`,
		EXISTS (SELECT 1 FROM device_software w WHERE w.device_id = d.id
			AND starts_with(lower(w.name), lower(COALESCE(NULLIF(p.detect_name, ''), NULLIF(p.apt_package, ''), p.name)))),
		COALESCE(r.id, 0), COALESCE(r.state, ''), COALESCE(r.note, ''), COALESCE(j.state, ''), COALESCE(j.message, ''),
		COALESCE(r.created_at, 'epoch'::timestamptz)
		FROM devices d
		JOIN selfservice_items i ON i.enabled AND (cardinality(i.group_ids) = 0 OR d.group_id = ANY(i.group_ids))
		JOIN software_packages p ON p.id = i.package_id AND p.platform = d.platform
		LEFT JOIN LATERAL (SELECT * FROM selfservice_requests r WHERE r.device_id = d.id AND r.item_id = i.id ORDER BY r.id DESC LIMIT 1) r ON true
		LEFT JOIN software_jobs j ON j.deployment_id = r.deployment_id AND j.device_id = d.id
		WHERE d.id::text = $1
		ORDER BY lower(i.category), lower(COALESCE(NULLIF(i.display_name, ''), p.name))`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogEntry{}
	for rows.Next() {
		var e CatalogEntry
		var groups []byte
		i := &e.Item
		if err := rows.Scan(&i.ID, &i.PackageID, &i.DisplayName, &i.Description, &i.Category, &i.GroupIDs, &i.Approval, &i.Enabled, &i.CreatedBy,
			&i.CreatedAt, &i.UpdatedAt, &groups, &i.PackageName, &i.PackageDescription, &i.Publisher, &i.Platform, &i.Source, &i.Version,
			&i.Requests, &i.Installs, &e.Installed, &e.RequestID, &e.RequestState, &e.RequestNote, &e.JobState, &e.JobMessage, &e.RequestedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- install requests ----

// SelfServiceRequest is a user's request to install a catalog item.
type SelfServiceRequest struct {
	ID           int64      `json:"id"`
	ItemID       *int64     `json:"itemId"`
	PackageID    *int64     `json:"packageId"`
	Name         string     `json:"name"`
	DeviceID     string     `json:"deviceId"`
	Hostname     string     `json:"hostname"`
	GroupID      int64      `json:"groupId"`
	GroupName    string     `json:"groupName"`
	Username     string     `json:"username"`
	Reason       string     `json:"reason"`
	State        string     `json:"state"`
	DeploymentID *int64     `json:"deploymentId"`
	DecidedBy    string     `json:"decidedBy"`
	DecidedAt    *time.Time `json:"decidedAt"`
	Note         string     `json:"note"`
	CreatedAt    time.Time  `json:"createdAt"`
	// The install job of an approved request.
	JobState   string `json:"jobState"`
	JobMessage string `json:"jobMessage"`
}

const ssRequestCols = `r.id, r.item_id, r.package_id, r.name, r.device_id::text, d.hostname, d.group_id, g.name, r.username, r.reason, r.state,
	r.deployment_id, r.decided_by, r.decided_at, r.note, r.created_at, COALESCE(j.state, ''), COALESCE(j.message, '')`

const ssRequestFrom = ` FROM selfservice_requests r JOIN devices d ON d.id = r.device_id JOIN device_groups g ON g.id = d.group_id
	LEFT JOIN software_jobs j ON j.deployment_id = r.deployment_id AND j.device_id = r.device_id`

func scanSSRequest(row pgx.Row) (SelfServiceRequest, error) {
	var r SelfServiceRequest
	err := row.Scan(&r.ID, &r.ItemID, &r.PackageID, &r.Name, &r.DeviceID, &r.Hostname, &r.GroupID, &r.GroupName, &r.Username, &r.Reason, &r.State,
		&r.DeploymentID, &r.DecidedBy, &r.DecidedAt, &r.Note, &r.CreatedAt, &r.JobState, &r.JobMessage)
	return r, mapErr(err)
}

// CreateSelfServiceRequest stores a request and returns its ID.
func (s *Store) CreateSelfServiceRequest(ctx context.Context, r SelfServiceRequest) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO selfservice_requests (item_id, package_id, name, device_id, username, reason, state, deployment_id,
		decided_by, decided_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		r.ItemID, r.PackageID, r.Name, r.DeviceID, r.Username, r.Reason, r.State, r.DeploymentID, r.DecidedBy, r.DecidedAt).Scan(&id)
	return id, mapErr(err)
}

// SelfServiceRequestQuery filters requests.
type SelfServiceRequestQuery struct {
	Scope Scope
	State string // '' is every state
	Limit int
}

// ListSelfServiceRequests returns requests of devices in the scope, newest first.
func (s *Store) ListSelfServiceRequests(ctx context.Context, q SelfServiceRequestQuery) ([]SelfServiceRequest, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, "SELECT "+ssRequestCols+ssRequestFrom+`
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = '' OR r.state = $3)
		ORDER BY r.id DESC LIMIT $4`, q.Scope.All, q.Scope.groups(), q.State, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SelfServiceRequest{}
	for rows.Next() {
		r, err := scanSSRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SelfServiceRequestByID returns one request.
func (s *Store) SelfServiceRequestByID(ctx context.Context, id int64) (SelfServiceRequest, error) {
	return scanSSRequest(s.pool.QueryRow(ctx, "SELECT "+ssRequestCols+ssRequestFrom+" WHERE r.id = $1", id))
}

// DecideSelfServiceRequest approves or rejects a pending request; false when
// it was no longer pending.
func (s *Store) DecideSelfServiceRequest(ctx context.Context, id int64, state, by, note string, deploymentID *int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE selfservice_requests SET state = $2, decided_by = $3, decided_at = now(), note = $4, deployment_id = $5
		WHERE id = $1 AND state = 'pending'`, id, state, by, note, deploymentID)
	return tag.RowsAffected() == 1, err
}

// RecentSelfServiceActions counts the install and help requests of a
// device since a time (a limit against a runaway tray).
func (s *Store) RecentSelfServiceActions(ctx context.Context, deviceID string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM selfservice_requests WHERE device_id::text = $1 AND created_at > $2)
		+ (SELECT count(*) FROM help_requests WHERE device_id::text = $1 AND created_at > $2)`, deviceID, since).Scan(&n)
	return n, err
}

// ---- help requests ----

// HelpRequest is a request for help from the tray.
type HelpRequest struct {
	ID         int64      `json:"id"`
	DeviceID   string     `json:"deviceId"`
	Hostname   string     `json:"hostname"`
	GroupID    int64      `json:"groupId"`
	GroupName  string     `json:"groupName"`
	Online     bool       `json:"online"` // filled in by the server
	Username   string     `json:"username"`
	Subject    string     `json:"subject"`
	Message    string     `json:"message"`
	Contact    string     `json:"contact"`
	State      string     `json:"state"`
	AssignedTo string     `json:"assignedTo"`
	Reply      string     `json:"reply"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ClosedAt   *time.Time `json:"closedAt"`
}

const helpCols = `h.id, h.device_id::text, d.hostname, d.group_id, g.name, h.username, h.subject, h.message, h.contact, h.state, h.assigned_to,
	h.reply, h.created_at, h.updated_at, h.closed_at`

const helpFrom = ` FROM help_requests h JOIN devices d ON d.id = h.device_id JOIN device_groups g ON g.id = d.group_id`

func scanHelp(row pgx.Row) (HelpRequest, error) {
	var h HelpRequest
	err := row.Scan(&h.ID, &h.DeviceID, &h.Hostname, &h.GroupID, &h.GroupName, &h.Username, &h.Subject, &h.Message, &h.Contact, &h.State,
		&h.AssignedTo, &h.Reply, &h.CreatedAt, &h.UpdatedAt, &h.ClosedAt)
	return h, mapErr(err)
}

// CreateHelpRequest stores a help request and returns its ID.
func (s *Store) CreateHelpRequest(ctx context.Context, h HelpRequest) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO help_requests (device_id, username, subject, message, contact) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		h.DeviceID, h.Username, h.Subject, h.Message, h.Contact).Scan(&id)
	return id, mapErr(err)
}

// HelpQuery filters help requests.
type HelpQuery struct {
	Scope    Scope
	State    string // '' every state, "active" open and in progress
	DeviceID string
	Username string // with DeviceID: only this user's requests
	Limit    int
}

// ListHelpRequests returns help requests, newest first.
func (s *Store) ListHelpRequests(ctx context.Context, q HelpQuery) ([]HelpRequest, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, "SELECT "+helpCols+helpFrom+`
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = '' OR h.state = $3 OR ($3 = 'active' AND h.state <> 'closed'))
		AND ($4 = '' OR h.device_id::text = $4) AND ($5 = '' OR lower(h.username) = lower($5))
		ORDER BY h.id DESC LIMIT $6`, q.Scope.All, q.Scope.groups(), q.State, q.DeviceID, q.Username, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HelpRequest{}
	for rows.Next() {
		h, err := scanHelp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HelpRequestByID returns one help request.
func (s *Store) HelpRequestByID(ctx context.Context, id int64) (HelpRequest, error) {
	return scanHelp(s.pool.QueryRow(ctx, "SELECT "+helpCols+helpFrom+" WHERE h.id = $1", id))
}

// UpdateHelpRequest changes the state, the technician and the reply.
func (s *Store) UpdateHelpRequest(ctx context.Context, id int64, state, assignedTo, reply string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE help_requests SET state = $2, assigned_to = $3, reply = $4, updated_at = now(),
		closed_at = CASE WHEN $2 = 'closed' THEN COALESCE(closed_at, now()) ELSE NULL END WHERE id = $1`, id, state, assignedTo, reply)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SelfServiceCounts counts what waits for a technician in the scope.
type SelfServiceCounts struct {
	PendingRequests int `json:"pendingRequests"`
	OpenHelp        int `json:"openHelp"`
}

// CountSelfService counts pending install requests and open help requests.
func (s *Store) CountSelfService(ctx context.Context, sc Scope) (SelfServiceCounts, error) {
	var c SelfServiceCounts
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM selfservice_requests r JOIN devices d ON d.id = r.device_id WHERE r.state = 'pending' AND ($1 OR d.group_id = ANY($2))),
		(SELECT count(*) FROM help_requests h JOIN devices d ON d.id = h.device_id WHERE h.state = 'open' AND ($1 OR d.group_id = ANY($2)))`,
		sc.All, sc.groups()).Scan(&c.PendingRequests, &c.OpenHelp)
	return c, err
}

// PurgeSelfService removes closed help requests and decided install
// requests older than the retention.
func (s *Store) PurgeSelfService(ctx context.Context, olderThan time.Duration) error {
	cut := time.Now().Add(-olderThan)
	if _, err := s.pool.Exec(ctx, "DELETE FROM help_requests WHERE state = 'closed' AND closed_at < $1", cut); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM selfservice_requests WHERE state <> 'pending' AND created_at < $1", cut)
	return err
}
