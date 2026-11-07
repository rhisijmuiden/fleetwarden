package agentproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sync"
)

// Remote control.
//
// The server asks the agent for a session over the signed control channel
// (TypeRemoteStart). The agent asks the user for consent when the group wants
// that, then opens a second WebSocket for the session to
// PathRemote+sessionID+"/"+base64url(agent key). That request is signed like
// a file download, so the server knows the device and the agent's ephemeral
// X25519 key; the server's key came in the signed TypeRemoteStart. Both ends
// derive AES-256-GCM keys from the X25519 secret, and every binary message on
// the session socket is sealed with a per-direction counter as nonce. The
// stream between agent and server therefore does not depend on TLS either.
// The server relays the plain messages to the operator's browser.
const (
	TypeRemoteStart  = "remote.start"  // server -> agent
	TypeRemoteStop   = "remote.stop"   // server -> agent
	TypeRemoteStatus = "remote.status" // agent -> server

	PathRemote = "/agent/v1/remote/" // + session id + "/" + agent key
)

// Remote session kinds.
const (
	RemoteDesktop  = "desktop"
	RemoteTerminal = "terminal"
)

// Consent modes: take over directly, show a notice while connected, or ask
// the logged-on user first.
const (
	ConsentDirect = "direct"
	ConsentNotify = "notify"
	ConsentAsk    = "ask"
)

// RemoteStart asks the agent to open a remote session.
type RemoteStart struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Operator  string `json:"operator"` // shown to the user
	Consent   string `json:"consent"`
	// ConsentSeconds is how long the user has to answer an "ask" prompt.
	ConsentSeconds int    `json:"consentSeconds"`
	ServerKey      []byte `json:"serverKey"` // X25519 public key
	ProductName    string `json:"productName,omitempty"`
	AccentColor    string `json:"accentColor,omitempty"`
}

// RemoteStop ends a session (the operator closed it, or it was cancelled
// before the agent connected).
type RemoteStop struct {
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

// Remote session states reported by the agent.
const (
	RemoteStateConsent    = "consent"    // waiting for the user
	RemoteStateConnecting = "connecting" // consent given (or not needed)
	RemoteStateDenied     = "denied"     // the user said no or did not answer
	RemoteStateFailed     = "failed"
	RemoteStateEnded      = "ended"
)

// RemoteStatus reports the progress of a session.
type RemoteStatus struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	// Reason is a stable code: user_denied, no_answer, user_ended,
	// unsupported, no_console, helper_failed, ...
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// RemoteKey is an ephemeral X25519 key for one session.
type RemoteKey struct{ priv *ecdh.PrivateKey }

// NewRemoteKey creates a key.
func NewRemoteKey() (*RemoteKey, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &RemoteKey{priv: k}, nil
}

// Public returns the public key.
func (k *RemoteKey) Public() []byte { return k.priv.PublicKey().Bytes() }

// EncodeRemoteKey encodes a public key for the session URL.
func EncodeRemoteKey(pub []byte) string { return base64.RawURLEncoding.EncodeToString(pub) }

// DecodeRemoteKey decodes a public key from the session URL.
func DecodeRemoteKey(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("malformed session key")
	}
	return b, nil
}

// RemoteCipher seals and opens the messages of one session socket. Seal and
// Open may run concurrently with each other.
type RemoteCipher struct {
	smu, rmu   sync.Mutex
	send, recv cipher.AEAD
	sendN      uint64
	recvN      uint64
}

// ErrRemoteMessage is returned for a message that fails authentication.
var ErrRemoteMessage = errors.New("agentproto: remote message failed authentication")

// Cipher derives the session cipher from this key and the peer's public key.
// agentSide selects the direction.
func (k *RemoteKey) Cipher(peer []byte, sessionID string, agentSide bool) (*RemoteCipher, error) {
	pub, err := ecdh.X25519().NewPublicKey(peer)
	if err != nil {
		return nil, err
	}
	secret, err := k.priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	keys, err := hkdf.Key(sha256.New, secret, []byte(sessionID), "fleetwarden-remote-v1", 64)
	if err != nil {
		return nil, err
	}
	toServer, toAgent := keys[:32], keys[32:]
	mk := func(key []byte) (cipher.AEAD, error) {
		b, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(b)
	}
	c := &RemoteCipher{}
	sk, rk := toAgent, toServer
	if agentSide {
		sk, rk = toServer, toAgent
	}
	if c.send, err = mk(sk); err != nil {
		return nil, err
	}
	if c.recv, err = mk(rk); err != nil {
		return nil, err
	}
	return c, nil
}

func nonce(n uint64) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b[4:], n)
	return b
}

// Seal encrypts the next outgoing message.
func (c *RemoteCipher) Seal(plain []byte) []byte {
	c.smu.Lock()
	defer c.smu.Unlock()
	c.sendN++
	return c.send.Seal(nil, nonce(c.sendN), plain, nil)
}

// Open decrypts the next incoming message. Messages must arrive in order.
func (c *RemoteCipher) Open(sealed []byte) ([]byte, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	out, err := c.recv.Open(nil, nonce(c.recvN+1), sealed, nil)
	if err != nil {
		return nil, ErrRemoteMessage
	}
	c.recvN++
	return out, nil
}
