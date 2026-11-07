package store

import (
	"context"
	"time"
)

// Mail is a queued or sent message.
type Mail struct {
	ID            int64      `json:"id"`
	CreatedAt     time.Time  `json:"createdAt"`
	Kind          string     `json:"kind"`
	To            string     `json:"to"`
	Subject       string     `json:"subject"`
	BodyText      string     `json:"-"`
	BodyHTML      string     `json:"-"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"nextAttemptAt"`
	LastError     string     `json:"lastError"`
	SentAt        *time.Time `json:"sentAt"`
}

// EnqueueMail adds a message to the queue.
func (s *Store) EnqueueMail(ctx context.Context, m Mail) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO mail_queue (kind, to_addr, subject, body_text, body_html)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, m.Kind, m.To, m.Subject, m.BodyText, m.BodyHTML).Scan(&id)
	return id, err
}

// ProcessMail locks up to limit due messages and calls send for each one inside
// a transaction, recording the result. Locked rows are skipped by other workers.
func (s *Store) ProcessMail(ctx context.Context, limit, maxAttempts int, backoff func(attempt int) time.Duration,
	send func(Mail) error) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `SELECT id, kind, to_addr, subject, body_text, body_html, attempts FROM mail_queue
		WHERE status = 'queued' AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var batch []Mail
	for rows.Next() {
		var m Mail
		if err := rows.Scan(&m.ID, &m.Kind, &m.To, &m.Subject, &m.BodyText, &m.BodyHTML, &m.Attempts); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, m)
	}
	rows.Close()
	for _, m := range batch {
		attempt := m.Attempts + 1
		if err := send(m); err != nil {
			status := "queued"
			if attempt >= maxAttempts {
				status = "failed"
			}
			msg := err.Error()
			if len(msg) > 1000 {
				msg = msg[:1000]
			}
			_, err = tx.Exec(ctx, `UPDATE mail_queue SET attempts = $2, status = $3, last_error = $4, next_attempt_at = $5 WHERE id = $1`,
				m.ID, attempt, status, msg, time.Now().Add(backoff(attempt)))
		} else {
			_, err = tx.Exec(ctx, `UPDATE mail_queue SET attempts = $2, status = 'sent', last_error = '', sent_at = now() WHERE id = $1`,
				m.ID, attempt)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit(ctx)
}

// RecordMail stores a message that was sent (or failed) synchronously, such as a test mail.
func (s *Store) RecordMail(ctx context.Context, m Mail, sendErr error) error {
	status, msg := "sent", ""
	if sendErr != nil {
		status, msg = "failed", sendErr.Error()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO mail_queue (kind, to_addr, subject, body_text, body_html, status, attempts, last_error, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, CASE WHEN $6 = 'sent' THEN now() END)`,
		m.Kind, m.To, m.Subject, m.BodyText, m.BodyHTML, status, msg)
	return err
}

// ListMail returns the most recent messages.
func (s *Store) ListMail(ctx context.Context, limit int) ([]Mail, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, created_at, kind, to_addr, subject, status, attempts, next_attempt_at, last_error, sent_at
		FROM mail_queue ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mail{}
	for rows.Next() {
		var m Mail
		if err := rows.Scan(&m.ID, &m.CreatedAt, &m.Kind, &m.To, &m.Subject, &m.Status, &m.Attempts, &m.NextAttemptAt, &m.LastError, &m.SentAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RetryMail puts a failed message back in the queue.
func (s *Store) RetryMail(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE mail_queue SET status = 'queued', attempts = 0, next_attempt_at = now() WHERE id = $1 AND status = 'failed'`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PurgeMail deletes sent and failed messages older than age.
func (s *Store) PurgeMail(ctx context.Context, age time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM mail_queue WHERE status <> 'queued' AND created_at < now() - make_interval(secs => $1)", age.Seconds())
	return err
}
