package agent

import "github.com/rhisijmuiden/fleetwarden/internal/agentproto"

// ChatNotifier shows a chat with the IT department to the signed-on user.
// Chat returns how many trays show it.
type ChatNotifier interface {
	Chat(c agentproto.ChatState) int
}

// handleChat passes the chat to the tray and tells the server whether a
// user sees it.
func (a *Agent) handleChat(e *agentproto.Envelope, send func(string, any)) {
	var c agentproto.ChatState
	if e.Decode(&c) != nil || c.ID == 0 {
		return
	}
	n := 0
	if cn, ok := a.Notify.(ChatNotifier); ok {
		n = cn.Chat(c)
	}
	send(agentproto.TypeChatStatus, agentproto.ChatStatus{ID: c.ID, Trays: n})
}
