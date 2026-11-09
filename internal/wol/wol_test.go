package wol

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func TestMagicPacket(t *testing.T) {
	p, err := agentproto.MagicPacket("00:11:22:aa:bb:cc")
	if err != nil || len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) ||
		!bytes.Equal(p[96:], []byte{0x00, 0x11, 0x22, 0xaa, 0xbb, 0xcc}) {
		t.Fatalf("magic packet: %x %v", p, err)
	}
	if _, err := agentproto.MagicPacket("00:11:22:33:44:55:66:77"); err == nil {
		t.Error("EUI-64 accepted")
	}
	for mac, ok := range map[string]bool{"00:11:22:aa:bb:cc": true, "00:00:00:00:00:00": false, "01:00:5e:00:00:01": false, "": false, "zz": false} {
		if agentproto.WakeableMAC(mac) != ok {
			t.Errorf("wakeable %q", mac)
		}
	}
}

func TestBroadcast(t *testing.T) {
	cases := map[string]string{"192.168.1.20/24": "192.168.1.255", "10.0.5.9/16": "10.0.255.255", "172.16.0.10/28": "172.16.0.15"}
	for in, want := range cases {
		if got, ok := Broadcast(in); !ok || got != want {
			t.Errorf("%s: %s %v", in, got, ok)
		}
	}
	for _, bad := range []string{"fe80::1/64", "10.0.0.1/32", "10.0.0.1/31", "nonsense"} {
		if _, ok := Broadcast(bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
	if ValidBroadcast("127.0.0.1") || ValidBroadcast("::1") || ValidBroadcast("224.0.0.1") || !ValidBroadcast("255.255.255.255") {
		t.Error("ValidBroadcast")
	}
}

// TestSend sends to a local listener (127.0.0.1 is refused as broadcast, so
// the test listens on all addresses and sends to the limited broadcast or,
// where that is not possible, skips).
func TestSend(t *testing.T) {
	if _, err := Send(nil, 9); err == nil {
		t.Error("empty job accepted")
	}
	if _, err := Send([]agentproto.WakeTarget{{MAC: "00:11:22:aa:bb:cc", Broadcasts: []string{"255.255.255.255"}}}, 80); err == nil {
		t.Error("port 80 accepted")
	}
	l, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 9})
	if err != nil {
		t.Skip("cannot listen on port 9:", err)
	}
	defer l.Close()
	n, err := Send([]agentproto.WakeTarget{{MAC: "00:11:22:aa:bb:cc", Broadcasts: []string{"255.255.255.255"}}}, 9)
	if err != nil {
		t.Skip("no broadcast in this environment:", err)
	}
	if n != 1 {
		t.Fatalf("sent %d", n)
	}
	_ = l.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 200)
	m, _, err := l.ReadFromUDP(buf)
	if err != nil {
		t.Skip("broadcast not delivered locally:", err)
	}
	if m != 102 {
		t.Errorf("received %d bytes", m)
	}
}
