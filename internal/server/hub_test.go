package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Messages queued from several goroutines at once must reach the agent in
// sequence order: the agent drops the session on a lower sequence number.
func TestHubSendKeepsSequenceOrder(t *testing.T) {
	_, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	agentPub, _, _ := ed25519.GenerateKey(rand.Reader)
	nonce := make([]byte, 32)
	h := newAgentHub()
	c := &agentConn{deviceID: "dev", sess: agentproto.NewSession(nonce, serverKey, agentPub, agentproto.ToAgent),
		out: make(chan *agentproto.Envelope, 4000), cancel: func() {}}
	h.add(c)
	var wg sync.WaitGroup
	for g := 0; g < 40; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !h.send("dev", agentproto.TypeAgentConfig, agentproto.AgentConfig{InventoryMinutes: i}) {
					t.Error("send failed")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(c.out)
	var last uint64
	n := 0
	for e := range c.out {
		if e.Seq <= last {
			t.Fatalf("message %d has sequence %d after %d", n, e.Seq, last)
		}
		last = e.Seq
		n++
	}
	if n != 2000 {
		t.Fatalf("got %d messages", n)
	}
}
