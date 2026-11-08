package gwproto

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

// HandshakeTimeout bounds the whole handshake.
const HandshakeTimeout = 15 * time.Second

// RemoteError is a refusal reported by the other side.
type RemoteError struct{ Code string }

func (e *RemoteError) Error() string { return "gateway refused the server: " + e.Code }

// LocalError is a check that failed on this side.
type LocalError struct{ Code, Detail string }

func (e *LocalError) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

// Local error codes.
const (
	ErrCodeKeyChanged    = "gateway_key_changed"
	ErrCodeNeedsPairing  = "pairing_code_required"
	ErrCodeAlreadyPaired = "gateway_already_paired"
	ErrCodeGatewayProof  = "gateway_proof_invalid"
	ErrCodeProtocolLocal = "protocol_mismatch"
)

func writeJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, raw)
}

func readJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	typ, raw, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return fmt.Errorf("%w: unexpected binary message", ErrHandshake)
	}
	return json.Unmarshal(raw, v)
}

// ServerSide are the inputs of the management server's handshake.
type ServerSide struct {
	Key     ed25519.PrivateKey
	Version string
	// Pinned is the gateway key stored at pairing; nil to pair now.
	Pinned []byte
	// PairingCode proves the server may pair; only used when Pinned is nil.
	PairingCode string
	// CertHash is the SHA-256 of the TLS certificate the gateway presented.
	CertHash []byte
}

// ServerResult describes the gateway after a successful handshake.
type ServerResult struct {
	PublicKey []byte
	Version   string
	Paired    bool // this handshake paired the gateway
}

// ServerHandshake authenticates the gateway and the server to each other.
func ServerHandshake(ctx context.Context, ws *websocket.Conn, p ServerSide) (ServerResult, error) {
	ctx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	var res ServerResult
	var hello GatewayHello
	if err := readJSON(ctx, ws, &hello); err != nil {
		return res, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	if hello.Protocol != Version {
		return res, &LocalError{Code: ErrCodeProtocolLocal, Detail: fmt.Sprintf("gateway speaks protocol %d, server %d", hello.Protocol, Version)}
	}
	if len(hello.PublicKey) != ed25519.PublicKeySize || len(hello.Nonce) < 16 {
		return res, fmt.Errorf("%w: malformed hello", ErrHandshake)
	}
	pairing := len(p.Pinned) == 0
	if !pairing && !bytes.Equal(hello.PublicKey, p.Pinned) {
		return res, &LocalError{Code: ErrCodeKeyChanged, Detail: "fingerprint " + Fingerprint(hello.PublicKey)}
	}
	if pairing && hello.Paired {
		return res, &LocalError{Code: ErrCodeAlreadyPaired}
	}
	if pairing && NormalizeCode(p.PairingCode) == "" {
		return res, &LocalError{Code: ErrCodeNeedsPairing}
	}
	pub := p.Key.Public().(ed25519.PublicKey)
	nonce := NewNonce()
	t := Transcript(hello.Nonce, hello.PublicKey, pub, p.CertHash, nonce)
	auth := ServerAuth{Protocol: Version, Version: p.Version, PublicKey: pub, Nonce: nonce, Signature: Sign(p.Key, RoleServer, t)}
	if pairing {
		auth.Pairing = PairingProof(p.PairingCode, RoleServer, t)
	}
	if err := writeJSON(ctx, ws, auth); err != nil {
		return res, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	var ga GatewayAuth
	if err := readJSON(ctx, ws, &ga); err != nil {
		return res, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	if ga.Error != "" {
		return res, &RemoteError{Code: ga.Error}
	}
	if !Verify(hello.PublicKey, RoleGateway, t, ga.Signature) {
		// A TLS interceptor changes the certificate hash and thus the transcript.
		return res, &LocalError{Code: ErrCodeGatewayProof, Detail: "signature does not match (TLS interception?)"}
	}
	if pairing && !CheckPairingProof(p.PairingCode, RoleGateway, t, ga.Pairing) {
		return res, &LocalError{Code: ErrCodeGatewayProof, Detail: "the gateway could not prove the pairing code"}
	}
	return ServerResult{PublicKey: hello.PublicKey, Version: hello.Version, Paired: pairing}, nil
}

// GatewaySide are the inputs of the gateway's handshake.
type GatewaySide struct {
	Key     ed25519.PrivateKey
	Version string
	// Pinned is the paired server's key; nil while unpaired.
	Pinned []byte
	// PairingCode is the one-time code while unpaired.
	PairingCode string
	// CertHash is the SHA-256 of this gateway's tunnel certificate.
	CertHash []byte
	// AllowAttempt is asked before a pairing code is checked (rate limit);
	// Failed is told about a wrong code.
	AllowAttempt func() bool
	Failed       func()
}

// GatewayResult describes the authenticated server.
type GatewayResult struct {
	PublicKey []byte
	Version   string
	Paired    bool // this handshake paired the server: pin PublicKey
}

// GatewayHandshake authenticates the server and the gateway to each other.
func GatewayHandshake(ctx context.Context, ws *websocket.Conn, p GatewaySide) (GatewayResult, error) {
	ctx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	var res GatewayResult
	pub := p.Key.Public().(ed25519.PublicKey)
	nonce := NewNonce()
	if err := writeJSON(ctx, ws, GatewayHello{Protocol: Version, Version: p.Version, PublicKey: pub, Nonce: nonce, Paired: len(p.Pinned) > 0}); err != nil {
		return res, err
	}
	refuse := func(code string) (GatewayResult, error) {
		_ = writeJSON(ctx, ws, GatewayAuth{Error: code})
		return res, &LocalError{Code: code}
	}
	var sa ServerAuth
	if err := readJSON(ctx, ws, &sa); err != nil {
		return res, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	if sa.Protocol != Version {
		return refuse(ErrCodeProtocol)
	}
	if len(sa.PublicKey) != ed25519.PublicKeySize || len(sa.Nonce) < 16 {
		return refuse(ErrCodeProtocol)
	}
	t := Transcript(nonce, pub, sa.PublicKey, p.CertHash, sa.Nonce)
	if !Verify(sa.PublicKey, RoleServer, t, sa.Signature) {
		return refuse(ErrCodeBadSignature)
	}
	pairing := len(p.Pinned) == 0
	if !pairing && !bytes.Equal(sa.PublicKey, p.Pinned) {
		return refuse(ErrCodeOtherServer)
	}
	if pairing {
		if len(sa.Pairing) == 0 {
			return refuse(ErrCodeNotPaired)
		}
		if p.AllowAttempt != nil && !p.AllowAttempt() {
			return refuse(ErrCodeRateLimited)
		}
		if NormalizeCode(p.PairingCode) == "" || !CheckPairingProof(p.PairingCode, RoleServer, t, sa.Pairing) {
			if p.Failed != nil {
				p.Failed()
			}
			return refuse(ErrCodeBadPairing)
		}
	}
	ga := GatewayAuth{Signature: Sign(p.Key, RoleGateway, t)}
	if pairing {
		ga.Pairing = PairingProof(p.PairingCode, RoleGateway, t)
	}
	if err := writeJSON(ctx, ws, ga); err != nil {
		return res, err
	}
	return GatewayResult{PublicKey: sa.PublicKey, Version: sa.Version, Paired: pairing}, nil
}
