package store

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEntry is one audit log record.
type AuditEntry struct {
	ID      int64          `json:"id"`
	At      time.Time      `json:"at"`
	ActorID *int64         `json:"actorId"`
	Actor   string         `json:"actor"`
	IP      string         `json:"ip"`
	Action  string         `json:"action"`
	Target  string         `json:"target"`
	Success bool           `json:"success"`
	Details map[string]any `json:"details"`
}

// Audit appends an entry.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	raw, err := json.Marshal(e.Details)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_log (actor_id, actor, ip, action, target, success, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, e.ActorID, e.Actor, e.IP, e.Action, e.Target, e.Success, raw)
	return err
}

// AuditQuery filters the audit log.
type AuditQuery struct {
	Before int64  // only entries with a lower ID (paging); 0 for the newest
	Search string // matched against actor, action, target and IP
	Limit  int
}

// ListAudit returns entries newest first.
func (s *Store) ListAudit(ctx context.Context, q AuditQuery) ([]AuditEntry, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, at, actor_id, actor, ip, action, target, success, details FROM audit_log
		WHERE ($1 = 0 OR id < $1)
		  AND ($2 = '' OR actor ILIKE '%' || $2 || '%' OR action ILIKE '%' || $2 || '%'
		       OR target ILIKE '%' || $2 || '%' OR ip ILIKE '%' || $2 || '%')
		ORDER BY id DESC LIMIT $3`, q.Before, q.Search, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var raw []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.Actor, &e.IP, &e.Action, &e.Target, &e.Success, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &e.Details)
		out = append(out, e)
	}
	return out, rows.Err()
}
