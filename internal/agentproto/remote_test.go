package agentproto

import "testing"

func TestRemoteCipher(t *testing.T) {
	server, _ := NewRemoteKey()
	agent, _ := NewRemoteKey()
	sc, err := server.Cipher(agent.Public(), "s1", false)
	if err != nil {
		t.Fatal(err)
	}
	ac, err := agent.Cipher(server.Public(), "s1", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"frame one", "frame two"} {
		got, err := sc.Open(ac.Seal([]byte(msg)))
		if err != nil || string(got) != msg {
			t.Fatalf("agent -> server: %q %v", got, err)
		}
	}
	got, err := ac.Open(sc.Seal([]byte("click")))
	if err != nil || string(got) != "click" {
		t.Fatalf("server -> agent: %q %v", got, err)
	}
	// Replayed, reflected and foreign messages fail.
	m := ac.Seal([]byte("x"))
	if _, err := sc.Open(m); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Open(m); err == nil {
		t.Error("replay accepted")
	}
	if _, err := ac.Open(ac.Seal([]byte("y"))); err == nil {
		t.Error("reflected message accepted")
	}
	other, _ := agent.Cipher(server.Public(), "s2", true)
	if _, err := sc.Open(other.Seal([]byte("z"))); err == nil {
		t.Error("message of another session accepted")
	}
	k, err := DecodeRemoteKey(EncodeRemoteKey(agent.Public()))
	if err != nil || string(k) != string(agent.Public()) {
		t.Fatal("key encoding")
	}
}
