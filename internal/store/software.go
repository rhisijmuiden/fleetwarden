package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- software library ----

// Package sources.
const (
	SourceWinget = "winget"
	SourceUpload = "upload"
	SourceApt    = "apt"
)

// SoftwarePackage is a program in the library.
type SoftwarePackage struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Publisher      string    `json:"publisher"`
	Description    string    `json:"description"`
	Platform       string    `json:"platform"`
	Source         string    `json:"source"`
	WingetID       string    `json:"wingetId"`
	Version        string    `json:"version"`
	FileSHA256     string    `json:"fileSha256"`
	FileName       string    `json:"fileName"`
	FileSize       int64     `json:"fileSize"`
	InstallerKind  string    `json:"installerKind"`
	InstallArgs    string    `json:"installArgs"`
	AptPackage     string    `json:"aptPackage"`
	DetectName     string    `json:"detectName"`
	ProductCode    string    `json:"productCode"`
	AnyHash        bool      `json:"anyHash"`
	TimeoutMinutes int       `json:"timeoutMinutes"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	// Installed counts the devices (in the caller's scope) that have it.
	Installed int `json:"installed"`
}

const packageCols = `p.id, p.name, p.publisher, p.description, p.platform, p.source, p.winget_id, p.version, p.file_sha256, p.file_name,
	p.file_size, p.installer_kind, p.install_args, p.apt_package, p.detect_name, p.product_code, p.any_hash, p.timeout_minutes,
	p.created_by, p.created_at, p.updated_at`

func scanPackage(row pgx.Row, extra ...any) (SoftwarePackage, error) {
	var p SoftwarePackage
	var timeout int16
	dst := []any{&p.ID, &p.Name, &p.Publisher, &p.Description, &p.Platform, &p.Source, &p.WingetID, &p.Version, &p.FileSHA256,
		&p.FileName, &p.FileSize, &p.InstallerKind, &p.InstallArgs, &p.AptPackage, &p.DetectName, &p.ProductCode, &p.AnyHash, &timeout,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt}
	err := row.Scan(append(dst, extra...)...)
	p.TimeoutMinutes = int(timeout)
	return p, mapErr(err)
}

// ListSoftwarePackages returns the library with install counts within a scope.
func (s *Store) ListSoftwarePackages(ctx context.Context, sc Scope) ([]SoftwarePackage, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+packageCols+`,
		(SELECT count(DISTINCT d.id) FROM device_software ds JOIN devices d ON d.id = ds.device_id
			WHERE d.platform = p.platform AND ($1 OR d.group_id = ANY($2))
			AND (CASE WHEN p.source = 'apt' THEN ds.name = p.apt_package
				ELSE lower(ds.name) LIKE lower(replace(replace(replace(COALESCE(NULLIF(p.detect_name, ''), p.name), '\', '\\'), '%', '\%'), '_', '\_')) || '%' END))::int
		FROM software_packages p ORDER BY lower(p.name), p.platform`, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SoftwarePackage{}
	for rows.Next() {
		var n int
		p, err := scanPackage(rows, &n)
		if err != nil {
			return nil, err
		}
		p.Installed = n
		out = append(out, p)
	}
	return out, rows.Err()
}

// SoftwarePackageByID returns a package.
func (s *Store) SoftwarePackageByID(ctx context.Context, id int64) (SoftwarePackage, error) {
	return scanPackage(s.pool.QueryRow(ctx, "SELECT "+packageCols+" FROM software_packages p WHERE p.id = $1", id))
}

func packageArgs(p SoftwarePackage) []any {
	return []any{p.Name, p.Publisher, p.Description, p.Platform, p.Source, p.WingetID, p.Version, p.FileSHA256, p.FileName, p.FileSize,
		p.InstallerKind, p.InstallArgs, p.AptPackage, p.DetectName, p.ProductCode, p.AnyHash, int16(p.TimeoutMinutes)}
}

// CreateSoftwarePackage adds a package; ErrConflict when the name exists for the platform.
func (s *Store) CreateSoftwarePackage(ctx context.Context, p SoftwarePackage) (SoftwarePackage, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO software_packages (name, publisher, description, platform, source, winget_id, version,
		file_sha256, file_name, file_size, installer_kind, install_args, apt_package, detect_name, product_code, any_hash, timeout_minutes,
		created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING id`,
		append(packageArgs(p), p.CreatedBy)...).Scan(&id)
	if err != nil {
		return p, mapErr(err)
	}
	return s.SoftwarePackageByID(ctx, id)
}

// UpdateSoftwarePackage changes a package.
func (s *Store) UpdateSoftwarePackage(ctx context.Context, p SoftwarePackage) error {
	tag, err := s.pool.Exec(ctx, `UPDATE software_packages SET name = $2, publisher = $3, description = $4, platform = $5, source = $6,
		winget_id = $7, version = $8, file_sha256 = $9, file_name = $10, file_size = $11, installer_kind = $12, install_args = $13,
		apt_package = $14, detect_name = $15, product_code = $16, any_hash = $17, timeout_minutes = $18, updated_at = now()
		WHERE id = $1`, append([]any{p.ID}, packageArgs(p)...)...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// DeleteSoftwarePackage removes a package; its jobs that did not start are cancelled.
func (s *Store) DeleteSoftwarePackage(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE software_jobs j SET state = 'cancelled', message = 'the package was deleted', updated_at = now(),
		finished_at = now() FROM software_deployments x WHERE x.id = j.deployment_id AND x.package_id = $1 AND j.state = 'pending'`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE software_deployments SET cancelled_at = now() WHERE package_id = $1 AND cancelled_at IS NULL", id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM software_packages WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// SoftwareFiles returns the SHA-256 of every uploaded file a package uses.
func (s *Store) SoftwareFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, "SELECT DISTINCT file_sha256 FROM software_packages WHERE file_sha256 <> ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}

// DeviceMayDownload reports whether the device has an unfinished job for a
// package with this uploaded file.
func (s *Store) DeviceMayDownload(ctx context.Context, deviceID, sha string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM software_jobs j JOIN software_deployments x ON x.id = j.deployment_id
		JOIN software_packages p ON p.id = x.package_id
		WHERE j.device_id::text = $1 AND j.finished_at IS NULL AND p.file_sha256 = $2)`, deviceID, sha).Scan(&ok)
	return ok, err
}

// ---- deployments ----

// Deployment is an install or uninstall on a set of devices.
type Deployment struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Action           string     `json:"action"`
	PackageID        *int64     `json:"packageId"`
	Platform         string     `json:"platform"`
	UninstallName    string     `json:"uninstallName"`
	UninstallVersion string     `json:"uninstallVersion"`
	UninstallArgs    string     `json:"uninstallArgs"`
	UninstallPurge   bool       `json:"uninstallPurge"`
	GroupIDs         []int64    `json:"groupIds"`
	Ongoing          bool       `json:"ongoing"`
	StartAt          time.Time  `json:"startAt"`
	ExpireDays       int        `json:"expireDays"`
	RequestedBy      string     `json:"requestedBy"`
	CreatedAt        time.Time  `json:"createdAt"`
	CancelledAt      *time.Time `json:"cancelledAt"`
	Groups           []GroupRef `json:"groups"`
	Counts           JobCounts  `json:"counts"`
}

// JobCounts counts the jobs of a deployment by outcome.
type JobCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"` // waiting for the device or the start time
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"` // failed or expired
	Cancelled int `json:"cancelled"`
	Reboot    int `json:"reboot"`
}

const deploymentCols = `x.id, x.name, x.action, x.package_id, x.platform, x.uninstall_name, x.uninstall_version, x.uninstall_args,
	x.uninstall_purge, x.group_ids, x.ongoing, x.start_at, x.expire_days, x.requested_by, x.created_at, x.cancelled_at,
	COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY lower(g.name)) FROM device_groups g
		WHERE g.id = ANY(x.group_ids)), '[]')`

// jobCountCols counts the deployment's jobs on devices in the scope ($1, $2).
const jobCountCols = `count(j.id)::int,
	count(j.id) FILTER (WHERE j.state = 'pending')::int,
	count(j.id) FILTER (WHERE j.finished_at IS NULL AND j.state <> 'pending')::int,
	count(j.id) FILTER (WHERE j.state = 'completed')::int,
	count(j.id) FILTER (WHERE j.state = 'skipped')::int,
	count(j.id) FILTER (WHERE j.state IN ('failed', 'expired'))::int,
	count(j.id) FILTER (WHERE j.state = 'cancelled')::int,
	count(j.id) FILTER (WHERE j.reboot_required)::int`

func scanDeployment(row pgx.Row) (Deployment, error) {
	var x Deployment
	var expire int16
	var groups []byte
	c := &x.Counts
	err := row.Scan(&x.ID, &x.Name, &x.Action, &x.PackageID, &x.Platform, &x.UninstallName, &x.UninstallVersion, &x.UninstallArgs,
		&x.UninstallPurge, &x.GroupIDs, &x.Ongoing, &x.StartAt, &expire, &x.RequestedBy, &x.CreatedAt, &x.CancelledAt, &groups,
		&c.Total, &c.Pending, &c.Running, &c.Completed, &c.Skipped, &c.Failed, &c.Cancelled, &c.Reboot)
	if err != nil {
		return x, mapErr(err)
	}
	x.ExpireDays = int(expire)
	x.Groups = []GroupRef{}
	_ = json.Unmarshal(groups, &x.Groups)
	if x.GroupIDs == nil {
		x.GroupIDs = []int64{}
	}
	return x, nil
}

// NewDeployment is a deployment to create with the devices it targets.
type NewDeployment struct {
	Deployment
	DeviceIDs []string
}

// CreateDeployment stores a deployment with a job for every listed device
// and every device of its groups on the deployment's platform. It returns
// the number of jobs.
func (s *Store) CreateDeployment(ctx context.Context, d NewDeployment) (int64, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if d.GroupIDs == nil {
		d.GroupIDs = []int64{}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO software_deployments (name, action, package_id, platform, uninstall_name, uninstall_version,
		uninstall_args, uninstall_purge, group_ids, ongoing, start_at, expire_days, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		d.Name, d.Action, d.PackageID, d.Platform, d.UninstallName, d.UninstallVersion, d.UninstallArgs, d.UninstallPurge, d.GroupIDs,
		d.Ongoing, d.StartAt, int16(d.ExpireDays), d.RequestedBy).Scan(&id)
	if err != nil {
		return 0, 0, mapErr(err)
	}
	if d.DeviceIDs == nil {
		d.DeviceIDs = []string{}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO software_jobs (deployment_id, device_id)
		SELECT $1, d.id FROM devices d WHERE d.platform = $2 AND (d.id::text = ANY($3) OR d.group_id = ANY($4))
		ON CONFLICT DO NOTHING`, id, d.Platform, d.DeviceIDs, d.GroupIDs)
	if err != nil {
		return 0, 0, err
	}
	return id, int(tag.RowsAffected()), tx.Commit(ctx)
}

// DeploymentQuery filters deployments.
type DeploymentQuery struct {
	Scope     Scope
	PackageID int64
	Limit     int
}

// ListDeployments returns deployments with at least one job in the scope
// (or, for a full scope, every deployment), newest first.
func (s *Store) ListDeployments(ctx context.Context, q DeploymentQuery) ([]Deployment, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, "SELECT "+deploymentCols+", "+jobCountCols+`
		FROM software_deployments x
		LEFT JOIN software_jobs j ON j.deployment_id = x.id AND ($1 OR j.device_id IN (SELECT id FROM devices WHERE group_id = ANY($2)))
		WHERE ($3 = 0 OR x.package_id = $3)
		GROUP BY x.id HAVING $1 OR count(j.id) > 0 OR x.group_ids && $2
		ORDER BY x.id DESC LIMIT $4`, q.Scope.All, q.Scope.groups(), q.PackageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		x, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeploymentByID returns a deployment with job counts within the scope.
func (s *Store) DeploymentByID(ctx context.Context, id int64, sc Scope) (Deployment, error) {
	return scanDeployment(s.pool.QueryRow(ctx, "SELECT "+deploymentCols+", "+jobCountCols+`
		FROM software_deployments x
		LEFT JOIN software_jobs j ON j.deployment_id = x.id AND ($1 OR j.device_id IN (SELECT id FROM devices WHERE group_id = ANY($2)))
		WHERE x.id = $3 GROUP BY x.id`, sc.All, sc.groups(), id))
}

// SoftwareJob is one device's part of a deployment.
type SoftwareJob struct {
	ID             int64      `json:"id"`
	DeploymentID   int64      `json:"deploymentId"`
	DeploymentName string     `json:"deploymentName"`
	Action         string     `json:"action"`
	DeviceID       string     `json:"deviceId"`
	Hostname       string     `json:"hostname"`
	GroupID        int64      `json:"groupId"`
	State          string     `json:"state"`
	Message        string     `json:"message"`
	ExitCode       *int       `json:"exitCode"`
	Output         string     `json:"output"`
	RebootRequired bool       `json:"rebootRequired"`
	Version        string     `json:"version"`
	Attempts       int        `json:"attempts"`
	CreatedAt      time.Time  `json:"createdAt"`
	SentAt         *time.Time `json:"sentAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
}

const jobCols = `j.id, j.deployment_id, x.name, x.action, j.device_id::text, d.hostname, d.group_id, j.state, j.message, j.exit_code, j.output,
	j.reboot_required, j.version, j.attempts, j.created_at, j.sent_at, j.updated_at, j.finished_at`

func scanJob(row pgx.Row) (SoftwareJob, error) {
	var j SoftwareJob
	err := row.Scan(&j.ID, &j.DeploymentID, &j.DeploymentName, &j.Action, &j.DeviceID, &j.Hostname, &j.GroupID, &j.State, &j.Message,
		&j.ExitCode, &j.Output, &j.RebootRequired, &j.Version, &j.Attempts, &j.CreatedAt, &j.SentAt, &j.UpdatedAt, &j.FinishedAt)
	return j, mapErr(err)
}

// JobQuery filters software jobs.
type JobQuery struct {
	Scope        Scope
	DeploymentID int64
	DeviceID     string
	Limit        int
}

// ListSoftwareJobs returns jobs, newest first.
func (s *Store) ListSoftwareJobs(ctx context.Context, q JobQuery) ([]SoftwareJob, error) {
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, "SELECT "+jobCols+` FROM software_jobs j JOIN software_deployments x ON x.id = j.deployment_id
		JOIN devices d ON d.id = j.device_id
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = 0 OR j.deployment_id = $3) AND ($4 = '' OR d.id::text = $4)
		ORDER BY CASE WHEN $3 = 0 THEN 0 ELSE array_position(ARRAY['failed', 'expired', 'downloading', 'installing', 'uninstalling',
			'sent', 'pending', 'completed', 'skipped', 'cancelled'], j.state) END, lower(d.hostname), j.id DESC LIMIT $5`,
		q.Scope.All, q.Scope.groups(), q.DeploymentID, q.DeviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SoftwareJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CancelDeployment stops a deployment: jobs that did not start are cancelled.
func (s *Store) CancelDeployment(ctx context.Context, id int64, by string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, "UPDATE software_deployments SET cancelled_at = COALESCE(cancelled_at, now()) WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE software_jobs SET state = 'cancelled', message = $2, updated_at = now(), finished_at = now()
		WHERE deployment_id = $1 AND state = 'pending'`, id, "cancelled by "+by); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RetrySoftwareJobs puts the failed and expired jobs of a deployment (in the
// scope) back in the queue. It returns how many.
func (s *Store) RetrySoftwareJobs(ctx context.Context, id int64, sc Scope) (int, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE software_jobs j SET state = 'pending', message = '', exit_code = NULL, output = '',
		created_at = now(), sent_at = NULL, updated_at = now(), finished_at = NULL
		FROM devices d, software_deployments x
		WHERE d.id = j.device_id AND x.id = j.deployment_id AND x.cancelled_at IS NULL AND j.deployment_id = $1
		AND j.state IN ('failed', 'expired') AND ($2 OR d.group_id = ANY($3))`, id, sc.All, sc.groups())
	return int(tag.RowsAffected()), err
}

// ---- scheduler ----

// ExpandDeployments adds jobs for devices that joined the groups of
// ongoing deployments.
func (s *Store) ExpandDeployments(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO software_jobs (deployment_id, device_id)
		SELECT x.id, d.id FROM software_deployments x JOIN devices d ON d.group_id = ANY(x.group_ids) AND d.platform = x.platform
		WHERE x.ongoing AND x.cancelled_at IS NULL AND (x.action = 'uninstall' OR x.package_id IS NOT NULL)
		ON CONFLICT DO NOTHING`)
	return int(tag.RowsAffected()), err
}

// ExpireSoftwareJobs closes jobs that waited too long for their device, and
// jobs that were sent but never finished.
func (s *Store) ExpireSoftwareJobs(ctx context.Context, staleAfter time.Duration) error {
	if _, err := s.pool.Exec(ctx, `UPDATE software_jobs j SET state = 'expired', message = 'the device was not online in time',
		updated_at = now(), finished_at = now() FROM software_deployments x
		WHERE x.id = j.deployment_id AND j.state = 'pending'
		AND GREATEST(j.created_at, x.start_at) + make_interval(days => x.expire_days) < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE software_jobs j SET state = 'failed', message = 'no answer from the agent',
		updated_at = now(), finished_at = now() FROM software_deployments x LEFT JOIN software_packages p ON p.id = x.package_id
		WHERE x.id = j.deployment_id AND j.finished_at IS NULL AND j.state <> 'pending'
		AND j.updated_at < now() - make_interval(mins => COALESCE(p.timeout_minutes, 60)) - $1::interval`, staleAfter)
	return err
}

// DueJob is a job to send with what the agent needs.
type DueJob struct {
	JobID      int64
	DeviceID   string
	Arch       string
	Deployment Deployment
	Package    *SoftwarePackage
}

// DueSoftwareJobs returns the next pending job of each listed device that
// has no running job, oldest first.
func (s *Store) DueSoftwareJobs(ctx context.Context, deviceIDs []string, limit int) ([]DueJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (j.device_id) j.id, j.device_id::text, d.arch, x.id
		FROM software_jobs j JOIN software_deployments x ON x.id = j.deployment_id JOIN devices d ON d.id = j.device_id
		WHERE j.state = 'pending' AND x.cancelled_at IS NULL AND x.start_at <= now() AND j.device_id::text = ANY($1)
		AND NOT EXISTS (SELECT 1 FROM software_jobs r WHERE r.device_id = j.device_id AND r.finished_at IS NULL AND r.state <> 'pending')
		ORDER BY j.device_id, j.id LIMIT $2`, deviceIDs, limit)
	if err != nil {
		return nil, err
	}
	type due struct {
		job          int64
		dev, arch    string
		deploymentID int64
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.job, &d.dev, &d.arch, &d.deploymentID); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DueJob, 0, len(list))
	deps := map[int64]Deployment{}
	pkgs := map[int64]*SoftwarePackage{}
	for _, d := range list {
		dep, ok := deps[d.deploymentID]
		if !ok {
			if dep, err = s.DeploymentByID(ctx, d.deploymentID, Scope{All: true}); err != nil {
				return nil, err
			}
			deps[d.deploymentID] = dep
		}
		j := DueJob{JobID: d.job, DeviceID: d.dev, Arch: d.arch, Deployment: dep}
		if dep.PackageID != nil {
			p, ok := pkgs[*dep.PackageID]
			if !ok {
				pk, err := s.SoftwarePackageByID(ctx, *dep.PackageID)
				if err != nil && err != ErrNotFound {
					return nil, err
				}
				if err == nil {
					p = &pk
				}
				pkgs[*dep.PackageID] = p
			}
			j.Package = p
		}
		out = append(out, j)
	}
	return out, nil
}

// MarkSoftwareJobSent moves a pending job to sent; false when it is no longer pending.
func (s *Store) MarkSoftwareJobSent(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE software_jobs SET state = 'sent', sent_at = now(), updated_at = now(), attempts = attempts + 1
		WHERE id = $1 AND state = 'pending'`, id)
	return tag.RowsAffected() == 1, err
}

// ResetSoftwareJob puts a sent job back (the device went offline before it arrived).
func (s *Store) ResetSoftwareJob(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "UPDATE software_jobs SET state = 'pending', sent_at = NULL, updated_at = now() WHERE id = $1 AND state = 'sent'", id)
	return err
}

// FinishSoftwareJob closes an open job with a state and message (server side).
func (s *Store) FinishSoftwareJob(ctx context.Context, id int64, state, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE software_jobs SET state = $2, message = $3, updated_at = now(), finished_at = now()
		WHERE id = $1 AND finished_at IS NULL`, id, state, message)
	return err
}

// SoftwareJobUpdate is a status report of a job.
type SoftwareJobUpdate struct {
	State          string
	Message        string
	ExitCode       *int
	Output         string
	RebootRequired bool
	Version        string
	Finished       bool
}

// UpdateSoftwareJob records a status of an open job of the device;
// ErrNotFound for a job that is closed already.
func (s *Store) UpdateSoftwareJob(ctx context.Context, id int64, deviceID string, u SoftwareJobUpdate) error {
	tag, err := s.pool.Exec(ctx, `UPDATE software_jobs SET state = $3, message = $4, exit_code = COALESCE($5, exit_code),
		output = CASE WHEN $6 = '' THEN output ELSE $6 END, reboot_required = $7, version = CASE WHEN $8 = '' THEN version ELSE $8 END,
		updated_at = now(), finished_at = CASE WHEN $9 THEN now() END
		WHERE id = $1 AND device_id::text = $2 AND finished_at IS NULL`,
		id, deviceID, u.State, u.Message, u.ExitCode, u.Output, u.RebootRequired, u.Version, u.Finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeSoftwareJobs removes finished jobs, and deployments without jobs, older than the cut-off.
func (s *Store) PurgeSoftwareJobs(ctx context.Context, olderThan time.Duration) error {
	cut := time.Now().Add(-olderThan)
	if _, err := s.pool.Exec(ctx, "DELETE FROM software_jobs WHERE finished_at < $1", cut); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM software_deployments x WHERE x.created_at < $1 AND NOT x.ongoing
		AND NOT EXISTS (SELECT 1 FROM software_jobs j WHERE j.deployment_id = x.id)`, cut)
	return err
}

// ---- installed software across devices ----

// InstalledSoftware is one program across the devices of a scope.
type InstalledSoftware struct {
	Name      string   `json:"name"`
	Publisher string   `json:"publisher"`
	Platform  string   `json:"platform"`
	Source    string   `json:"source"`
	Versions  []string `json:"versions"`
	Devices   int      `json:"devices"`
}

// InstalledQuery filters the installed software overview.
type InstalledQuery struct {
	Scope    Scope
	Search   string
	Platform string
	Limit    int
}

// ListInstalledSoftware aggregates the inventories of the devices in a scope.
func (s *Store) ListInstalledSoftware(ctx context.Context, q InstalledQuery) ([]InstalledSoftware, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q.Search != "" {
		add("(s.name ILIKE ? OR s.publisher ILIKE ?)", "%"+likeEscape(q.Search)+"%")
	}
	if q.Platform != "" {
		add("d.platform = ?", q.Platform)
	}
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT s.name, COALESCE(max(NULLIF(s.publisher, '')), ''), d.platform, max(s.source),
		array_agg(DISTINCT s.version ORDER BY s.version), count(DISTINCT d.id)::int
		FROM device_software s JOIN devices d ON d.id = s.device_id WHERE `+strings.Join(where, " AND ")+`
		GROUP BY s.name, d.platform ORDER BY count(DISTINCT d.id) DESC, lower(s.name) LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstalledSoftware{}
	for rows.Next() {
		var i InstalledSoftware
		if err := rows.Scan(&i.Name, &i.Publisher, &i.Platform, &i.Source, &i.Versions, &i.Devices); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// InstalledDevice is a device that has a program.
type InstalledDevice struct {
	DeviceID string `json:"deviceId"`
	Hostname string `json:"hostname"`
	GroupID  int64  `json:"groupId"`
	Version  string `json:"version"`
}

// DevicesWithSoftware lists the devices in a scope that have a program
// (with this exact name, and version when given).
func (s *Store) DevicesWithSoftware(ctx context.Context, sc Scope, name, version, platform string) ([]InstalledDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, s.version FROM device_software s JOIN devices d ON d.id = s.device_id
		WHERE ($1 OR d.group_id = ANY($2)) AND s.name = $3 AND ($4 = '' OR s.version = $4) AND ($5 = '' OR d.platform = $5)
		ORDER BY lower(d.hostname), s.version`, sc.All, sc.groups(), name, version, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstalledDevice{}
	for rows.Next() {
		var i InstalledDevice
		if err := rows.Scan(&i.DeviceID, &i.Hostname, &i.GroupID, &i.Version); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// DevicesInScope returns which of the listed devices exist within a scope,
// with their platform.
func (s *Store) DevicesInScope(ctx context.Context, sc Scope, ids []string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, platform FROM devices WHERE id::text = ANY($1) AND ($2 OR group_id = ANY($3))", ids, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}
