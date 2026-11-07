package agentproto

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSessionSignAndReplay(t *testing.T) {
	agentPub, agentKey := keys(t)
	serverPub, serverKey := keys(t)
	nonce := []byte("0123456789abcdef")
	agent := NewSession(nonce, agentKey, serverPub, ToServer)
	server := NewSession(nonce, serverKey, agentPub, ToAgent)

	e1, err := agent.Seal(TypeHeartbeat, "", Heartbeat{LoggedInUser: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Open(e1); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	if err := server.Open(e1); err == nil {
		t.Fatal("replayed message accepted")
	}
	e2, _ := agent.Seal(TypeHeartbeat, "", Heartbeat{LoggedInUser: "bob"})
	e2.Data = []byte(`{"loggedInUser":"mallory"}`)
	if err := server.Open(e2); err == nil {
		t.Fatal("tampered message accepted")
	}

	// A message signed for another session (other nonce) is rejected.
	other := NewSession([]byte("another-nonce..."), agentKey, serverPub, ToServer)
	e3, _ := other.Seal(TypeHeartbeat, "", nil)
	e3.Seq = 10
	e3.Sign(agentKey, []byte("another-nonce..."), ToServer)
	if err := server.Open(e3); err == nil {
		t.Fatal("message from another session accepted")
	}

	// A server message cannot be reflected back to the server.
	s1, _ := server.Seal(TypeInventoryRequest, "", nil)
	if err := agent.Open(s1); err != nil {
		t.Fatalf("server message rejected: %v", err)
	}
	reflected := *s1
	reflected.Seq = 99
	reflected.Sign(serverKey, nonce, ToAgent)
	if err := server.Open(&reflected); err == nil {
		t.Fatal("reflected message accepted")
	}
}

func TestRequestSignature(t *testing.T) {
	pub, key := keys(t)
	now := time.Now()
	h := SignRequest(key, "dev-1", "GET", "/agent/v1/files/x.deb", now)
	id, ts, sig, err := ParseRequestAuth(h)
	if err != nil || id != "dev-1" {
		t.Fatalf("parse: %v %q", err, id)
	}
	if !VerifyRequest(pub, id, "GET", "/agent/v1/files/x.deb", ts, sig, now, time.Minute) {
		t.Fatal("valid request rejected")
	}
	if VerifyRequest(pub, id, "GET", "/agent/v1/files/other.deb", ts, sig, now, time.Minute) {
		t.Fatal("signature valid for another path")
	}
	if VerifyRequest(pub, id, "GET", "/agent/v1/files/x.deb", ts, sig, now.Add(10*time.Minute), time.Minute) {
		t.Fatal("old signature accepted")
	}
}
