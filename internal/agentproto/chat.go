package agentproto

import "time"

// Chat between a technician in the web interface and the user signed on to
// a device. The technician starts a chat; the server sends its state to the
// agent, which shows it in the tray of the signed-on user. The user answers
// with a self-service request (op "chat"); the reply carries the new state.
const (
	TypeChat       = "chat"        // server -> agent: ChatState
	TypeChatStatus = "chat.status" // agent -> server: ChatStatus
)

// SelfServiceOpChat sends the user's message in the open chat.
const SelfServiceOpChat = "chat"

// Senders of a chat line.
const (
	ChatFromTech = "tech"
	ChatFromUser = "user"
)

// ChatState is a chat as the tray shows it.
type ChatState struct {
	ID       int64  `json:"id"`
	Operator string `json:"operator"` // the technician who started it
	// Open is false once the technician ended the chat.
	Open        bool       `json:"open"`
	Lines       []ChatLine `json:"lines"`
	ProductName string     `json:"productName,omitempty"`
	AccentColor string     `json:"accentColor,omitempty"`
}

// ChatLine is one message.
type ChatLine struct {
	ID   int64     `json:"id"`
	From string    `json:"from"` // ChatFromTech or ChatFromUser
	Name string    `json:"name"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// ChatStatus tells the server how many trays show the chat (0: nobody is
// signed on, or the tray app does not run).
type ChatStatus struct {
	ID    int64 `json:"id"`
	Trays int   `json:"trays"`
}
