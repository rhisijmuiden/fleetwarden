package agentproto

import (
	"crypto/ed25519"
	"errors"
	"sync"
)

// Session signs outgoing and verifies incoming envelopes of one WebSocket
// session. It is safe for concurrent use.
type Session struct {
	Nonce []byte

	key     ed25519.PrivateKey
	peer    ed25519.PublicKey
	out, in Direction
	mu      sync.Mutex
	sendSeq uint64
	recvSeq uint64
}

// NewSession creates a session. own signs outgoing messages, peer verifies
// incoming ones; out is the direction of the messages this side sends.
func NewSession(nonce []byte, own ed25519.PrivateKey, peer ed25519.PublicKey, out Direction) *Session {
	in := ToServer
	if out == ToServer {
		in = ToAgent
	}
	return &Session{Nonce: nonce, key: own, peer: peer, out: out, in: in}
}

// Seal builds and signs the next outgoing message.
func (s *Session) Seal(typ, id string, v any) (*Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := NewEnvelope(typ, id, s.sendSeq+1, v)
	if err != nil {
		return nil, err
	}
	s.sendSeq++
	e.Sign(s.key, s.Nonce, s.out)
	return e, nil
}

// ErrBadSignature is returned for a message that fails verification.
var ErrBadSignature = errors.New("agentproto: bad signature or sequence")

// Open verifies an incoming message: a valid signature and a sequence number
// above the previous one.
func (s *Session) Open(e *Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Seq <= s.recvSeq || !e.Verify(s.peer, s.Nonce, s.in) {
		return ErrBadSignature
	}
	s.recvSeq = e.Seq
	return nil
}
