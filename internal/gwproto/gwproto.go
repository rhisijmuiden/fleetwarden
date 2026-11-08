// Package gwproto is the tunnel protocol between the FleetWarden management
// server and a secure gateway in the DMZ.
//
// The management server dials out to the gateway (LAN to DMZ), so the
// gateway never needs a route into the internal network:
//
//	agent --443--> gateway (DMZ) <--tunnel-- management server (LAN)
//
// The tunnel is a WebSocket over TLS to PathTunnel on the gateway's tunnel
// port. The gateway's TLS certificate may be self-signed: both sides prove
// their Ed25519 keys over a transcript that includes the SHA-256 of the
// certificate the server saw, so a TLS man-in-the-middle cannot complete the
// handshake. The first connection pairs the two with a one-time code that
// the gateway prints at installation; both sides then pin each other's key.
//
// After the handshake the WebSocket carries a yamux session. The gateway
// opens one stream per agent HTTP request (the management server answers
// them with its normal agent endpoints); the server opens streams for
// control requests (status). Agent traffic itself is signed end to end
// (see agentproto), so the gateway only relays it.
package gwproto

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

// Version is the tunnel protocol version.
const Version = 1

// PathTunnel is where the gateway accepts the server's tunnel.
const PathTunnel = "/gateway/v1/tunnel"

// DefaultTunnelPort is the gateway's default tunnel port.
const DefaultTunnelPort = 8444

// Control paths the server requests from the gateway over the tunnel.
const (
	PathStatus = "/status"
)

// AgentPrefix is the only path prefix the gateway forwards.
const AgentPrefix = "/agent/"

// GatewayHello is the gateway's first message.
type GatewayHello struct {
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
	PublicKey []byte `json:"publicKey"`
	Nonce     []byte `json:"nonce"`
	Paired    bool   `json:"paired"`
}

// ServerAuth is the server's answer: its key, a signature over the
// transcript and, when pairing, a proof that it knows the pairing code.
type ServerAuth struct {
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
	PublicKey []byte `json:"publicKey"`
	Nonce     []byte `json:"nonce"`
	Signature []byte `json:"signature"`
	Pairing   []byte `json:"pairing,omitempty"`
}

// GatewayAuth completes the handshake. Error is set (and the rest empty)
// when the gateway refuses the server.
type GatewayAuth struct {
	Signature []byte `json:"signature,omitempty"`
	Pairing   []byte `json:"pairing,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Handshake errors the gateway reports in GatewayAuth.Error.
const (
	ErrCodeBadPairing   = "bad_pairing_code"
	ErrCodeOtherServer  = "paired_with_other_server"
	ErrCodeNotPaired    = "not_paired"
	ErrCodeBadSignature = "bad_signature"
	ErrCodeRateLimited  = "rate_limited"
	ErrCodeProtocol     = "protocol"
)

// Transcript binds both nonces, both keys and the TLS certificate.
func Transcript(gwNonce, gwKey, serverKey, certHash, serverNonce []byte) []byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte("fleetwarden-gateway-tunnel-v1"), gwNonce, gwKey, serverKey, certHash, serverNonce} {
		var l [2]byte
		l[0], l[1] = byte(len(part)>>8), byte(len(part))
		h.Write(l[:])
		h.Write(part)
	}
	return h.Sum(nil)
}

// Roles in signatures and pairing proofs, so one side's proof cannot be
// reflected back.
const (
	RoleServer  = "server"
	RoleGateway = "gateway"
)

// Sign signs the transcript for a role.
func Sign(key ed25519.PrivateKey, role string, transcript []byte) []byte {
	return ed25519.Sign(key, append([]byte(role+"\x00"), transcript...))
}

// Verify checks a transcript signature.
func Verify(pub []byte, role string, transcript, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, append([]byte(role+"\x00"), transcript...), sig)
}

// PairingProof proves knowledge of the pairing code.
func PairingProof(code, role string, transcript []byte) []byte {
	m := hmac.New(sha256.New, []byte(NormalizeCode(code)))
	m.Write([]byte(role + "\x00"))
	m.Write(transcript)
	return m.Sum(nil)
}

// CheckPairingProof compares a proof in constant time.
func CheckPairingProof(code, role string, transcript, proof []byte) bool {
	return subtle.ConstantTimeCompare(PairingProof(code, role, transcript), proof) == 1
}

var codeEnc = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// NewPairingCode returns a random code like "K7QF-2MXA-PZ4R-H9TD" (80 bits).
func NewPairingCode() string {
	b := make([]byte, 10)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	s := codeEnc.EncodeToString(b)
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// NormalizeCode removes separators and spaces and upper-cases the code, so
// it may be typed in any form.
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return -1
	}, code)
}

// NewNonce returns 32 random bytes.
func NewNonce() []byte {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return b
}

// CertHash is the SHA-256 of a DER certificate.
func CertHash(der []byte) []byte {
	h := sha256.Sum256(der)
	return h[:]
}

// Fingerprint formats a public key for people: the first 16 bytes of its
// SHA-256 in hex groups.
func Fingerprint(pub []byte) string {
	if len(pub) == 0 {
		return ""
	}
	h := sha256.Sum256(pub)
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < 16; i++ {
		if i > 0 && i%2 == 0 {
			b.WriteByte(':')
		}
		b.WriteByte(hexd[h[i]>>4])
		b.WriteByte(hexd[h[i]&0xf])
	}
	return b.String()
}

// MuxConfig is the yamux configuration of both ends.
func MuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.KeepAliveInterval = 20 * time.Second
	c.ConnectionWriteTimeout = 30 * time.Second
	c.MaxStreamWindowSize = 4 << 20
	c.StreamOpenTimeout = 30 * time.Second
	c.StreamCloseTimeout = 2 * time.Minute
	c.AcceptBacklog = 1024
	c.LogOutput = io.Discard
	return c
}

// ErrHandshake is returned for a failed handshake.
var ErrHandshake = errors.New("gateway handshake failed")

// Status is what the gateway reports on PathStatus.
type Status struct {
	Version      string    `json:"version"`
	Protocol     int       `json:"protocol"`
	StartedAt    time.Time `json:"startedAt"`
	ConnectedAt  time.Time `json:"connectedAt"`
	Hostname     string    `json:"hostname"`
	Listen       []string  `json:"listen"`
	TLSMode      string    `json:"tlsMode"` // acme, files, proxy
	Requests     int64     `json:"requests"`
	ActiveSocket int64     `json:"activeSockets"`
	Rejected     int64     `json:"rejected"`
}
