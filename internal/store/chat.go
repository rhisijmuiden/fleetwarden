package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Chat is a conversation between a technician and the user of a device.
type Chat struct {
	ID            int64         `json:"id"`
	DeviceID      string        `json:"deviceId"`
	HelpRequestID *int64        `json:"helpRequestId"`
	StartedBy     string        `json:"startedBy"`
	Operator      string        `json:"operator"`
	Username      string        `json:"username"`
	StartedAt     time.Time     `json:"startedAt"`
	EndedAt       *time.Time    `json:"endedAt"`
	EndedBy       string        `json:"endedBy"`
	DeliveredAt   *time.Time    `json:"deliveredAt"`
	Trays         int           `json:"trays"`
	Messages      []ChatMessage `json:"messages"`
}

// ChatMessage is one line of a chat.
type ChatMessage struct {
	ID        int64     `json:"id"`
	Sender    string    `json:"sender"` // tech or user
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrChatOpen is returned when a device already has an open chat.
var ErrChatOpen = errors.New("store: chat already open")

const chatCols = `id, device_id::text, help_request_id, started_by, operator, username, started_at, ended_at, ended_by, delivered_at, trays`

func scanChat(row pgx.Row) (Chat, error) {
	var c Chat
	err := row.Scan(&c.ID, &c.DeviceID, &c.HelpRequestID, &c.StartedBy, &c.Operator, &c.Username, &c.StartedAt, &c.EndedAt, &c.EndedBy,
		&c.DeliveredAt, &c.Trays)
	return c, mapErr(err)
}

// StartChat opens a chat on a device; ErrChatOpen when one is open.
func (s *Store) StartChat(ctx context.Context, c Chat) (Chat, error) {
	out, err := scanChat(s.pool.QueryRow(ctx, `INSERT INTO chats (device_id, help_request_id, started_by, operator)
		VALUES ($1, $2, $3, $4) RETURNING `+chatCols, c.DeviceID, c.HelpRequestID, c.StartedBy, c.Operator))
	if errors.Is(err, ErrConflict) {
		return out, ErrChatOpen
	}
	return out, err
}

// OpenChat returns the open chat of a device with its messages.
func (s *Store) OpenChat(ctx context.Context, deviceID string) (Chat, error) {
	c, err := scanChat(s.pool.QueryRow(ctx, "SELECT "+chatCols+" FROM chats WHERE device_id::text = $1 AND ended_at IS NULL", deviceID))
	if err != nil {
		return c, err
	}
	c.Messages, err = s.ChatMessages(ctx, c.ID)
	return c, err
}

// LastChat returns the newest chat of a device (open or ended) with its
// messages.
func (s *Store) LastChat(ctx context.Context, deviceID string) (Chat, error) {
	c, err := scanChat(s.pool.QueryRow(ctx, "SELECT "+chatCols+" FROM chats WHERE device_id::text = $1 ORDER BY started_at DESC, id DESC LIMIT 1", deviceID))
	if err != nil {
		return c, err
	}
	c.Messages, err = s.ChatMessages(ctx, c.ID)
	return c, err
}

// ChatByID returns a chat with its messages.
func (s *Store) ChatByID(ctx context.Context, id int64) (Chat, error) {
	c, err := scanChat(s.pool.QueryRow(ctx, "SELECT "+chatCols+" FROM chats WHERE id = $1", id))
	if err != nil {
		return c, err
	}
	c.Messages, err = s.ChatMessages(ctx, c.ID)
	return c, err
}

// ChatMessages returns the messages of a chat, oldest first.
func (s *Store) ChatMessages(ctx context.Context, chatID int64) ([]ChatMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, sender, name, body, created_at FROM chat_messages WHERE chat_id = $1 ORDER BY id`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.Sender, &m.Name, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddChatMessage adds a line to an open chat; ErrNotFound when the chat is
// ended. A user's message also records the user's account.
func (s *Store) AddChatMessage(ctx context.Context, chatID int64, m ChatMessage) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO chat_messages (chat_id, sender, name, body)
		SELECT id, $2, $3, $4 FROM chats WHERE id = $1 AND ended_at IS NULL RETURNING id`, chatID, m.Sender, m.Name, m.Body).Scan(&id)
	if err != nil {
		return 0, mapErr(err)
	}
	if m.Sender == "user" {
		_, err = s.pool.Exec(ctx, `UPDATE chats SET username = $2 WHERE id = $1 AND username = ''`, chatID, m.Name)
	}
	return id, err
}

// EndChat ends an open chat.
func (s *Store) EndChat(ctx context.Context, chatID int64, by string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE chats SET ended_at = now(), ended_by = $2 WHERE id = $1 AND ended_at IS NULL`, chatID, by)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetChatDelivery records how many trays show a chat.
func (s *Store) SetChatDelivery(ctx context.Context, chatID int64, trays int) error {
	_, err := s.pool.Exec(ctx, `UPDATE chats SET trays = $2, delivered_at = CASE WHEN $2 > 0 THEN coalesce(delivered_at, now()) ELSE delivered_at END
		WHERE id = $1`, chatID, trays)
	return err
}

// EndIdleChats ends open chats without a message since before.
func (s *Store) EndIdleChats(ctx context.Context, before time.Time) ([]Chat, error) {
	rows, err := s.pool.Query(ctx, `UPDATE chats c SET ended_at = now(), ended_by = 'timeout'
		WHERE ended_at IS NULL AND greatest(started_at, coalesce((SELECT max(created_at) FROM chat_messages m WHERE m.chat_id = c.id), started_at)) < $1
		RETURNING `+chatCols, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
