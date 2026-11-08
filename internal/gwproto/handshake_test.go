package gwproto

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// pair runs both handshakes over a real WebSocket.
func pair(t *testing.T, srv ServerSide, gw GatewaySide) (ServerResult, error, GatewayResult, error) {
	t.Helper()
	type gwOut struct {
		res GatewayResult
		err error
	}
	ch := make(chan gwOut, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			ch <- gwOut{err: err}
			return
		}
		defer ws.CloseNow() //nolint:errcheck
		res, err := GatewayHandshake(r.Context(), ws, gw)
		ch <- gwOut{res, err}
	}))
	defer ts.Close()
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow() //nolint:errcheck
	sres, serr := ServerHandshake(context.Background(), ws, srv)
	if serr != nil {
		ws.CloseNow() //nolint:errcheck // a refusing server hangs up
	}
	g := <-ch
	return sres, serr, g.res, g.err
}

func keys(t *testing.T) (ed25519.PrivateKey, ed25519.PrivateKey) {
	_, a, _ := ed25519.GenerateKey(rand.Reader)
	_, b, _ := ed25519.GenerateKey(rand.Reader)
	return a, b
}

func TestPairingAndReconnect(t *testing.T) {
	sk, gk := keys(t)
	code := NewPairingCode()
	hash := CertHash([]byte("cert"))
	sres, serr, gres, gerr := pair(t, ServerSide{Key: sk, PairingCode: strings.ToLower(strings.ReplaceAll(code, "-", " ")), CertHash: hash},
		GatewaySide{Key: gk, PairingCode: code, CertHash: hash})
	if serr != nil || gerr != nil || !sres.Paired || !gres.Paired {
		t.Fatalf("pairing: %v %v", serr, gerr)
	}
	if string(sres.PublicKey) != string(gk.Public().(ed25519.PublicKey)) || string(gres.PublicKey) != string(sk.Public().(ed25519.PublicKey)) {
		t.Fatal("keys not exchanged")
	}
	// Reconnect with pinned keys and no code.
	_, serr, gres, gerr = pair(t, ServerSide{Key: sk, Pinned: sres.PublicKey, CertHash: hash}, GatewaySide{Key: gk, Pinned: gres.PublicKey, CertHash: hash})
	if serr != nil || gerr != nil || gres.Paired {
		t.Fatalf("reconnect: %v %v", serr, gerr)
	}
}

func TestHandshakeRefusals(t *testing.T) {
	sk, gk := keys(t)
	other, _ := keys(t)
	hash := CertHash([]byte("cert"))
	code := NewPairingCode()
	var re *RemoteError
	var le *LocalError

	// Wrong code.
	_, serr, _, _ := pair(t, ServerSide{Key: sk, PairingCode: "AAAA-AAAA-AAAA-AAAA", CertHash: hash}, GatewaySide{Key: gk, PairingCode: code, CertHash: hash})
	if !errors.As(serr, &re) || re.Code != ErrCodeBadPairing {
		t.Errorf("wrong code: %v", serr)
	}
	// A TLS interceptor shows the server another certificate.
	_, serr, _, _ = pair(t, ServerSide{Key: sk, PairingCode: code, CertHash: CertHash([]byte("mitm"))}, GatewaySide{Key: gk, PairingCode: code, CertHash: hash})
	if !errors.As(serr, &re) || re.Code != ErrCodeBadSignature && re.Code != ErrCodeBadPairing {
		// The gateway's transcript differs, so the server's signature fails there.
		t.Errorf("interception: %v", serr)
	}
	// Another server cannot use a paired gateway.
	_, serr, _, _ = pair(t, ServerSide{Key: other, Pinned: gk.Public().(ed25519.PublicKey), CertHash: hash}, GatewaySide{Key: gk, Pinned: sk.Public().(ed25519.PublicKey), CertHash: hash})
	if !errors.As(serr, &re) || re.Code != ErrCodeOtherServer {
		t.Errorf("other server: %v", serr)
	}
	// The server notices a gateway with a new key.
	_, serr, _, _ = pair(t, ServerSide{Key: sk, Pinned: other.Public().(ed25519.PublicKey), CertHash: hash}, GatewaySide{Key: gk, Pinned: sk.Public().(ed25519.PublicKey), CertHash: hash})
	if !errors.As(serr, &le) || le.Code != ErrCodeKeyChanged {
		t.Errorf("key changed: %v", serr)
	}
	// Pairing with an already paired gateway.
	_, serr, _, _ = pair(t, ServerSide{Key: sk, PairingCode: code, CertHash: hash}, GatewaySide{Key: gk, Pinned: sk.Public().(ed25519.PublicKey), CertHash: hash})
	if !errors.As(serr, &le) || le.Code != ErrCodeAlreadyPaired {
		t.Errorf("already paired: %v", serr)
	}
	// Rate limit.
	_, serr, _, _ = pair(t, ServerSide{Key: sk, PairingCode: code, CertHash: hash}, GatewaySide{Key: gk, PairingCode: code, CertHash: hash, AllowAttempt: func() bool { return false }})
	if !errors.As(serr, &re) || re.Code != ErrCodeRateLimited {
		t.Errorf("rate limit: %v", serr)
	}
}

func TestPairingCode(t *testing.T) {
	c := NewPairingCode()
	if len(c) != 19 || strings.Count(c, "-") != 3 || NormalizeCode(c) != strings.ReplaceAll(c, "-", "") {
		t.Errorf("code %q", c)
	}
	if NormalizeCode(" k7qf-2mxa ") != "K7QF2MXA" {
		t.Error("normalize")
	}
	if Fingerprint(make([]byte, 32)) == "" || Fingerprint(nil) != "" {
		t.Error("fingerprint")
	}
}
