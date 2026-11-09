// Package wol sends Wake-on-LAN magic packets. The agent uses it to wake
// other devices in its network, the server to wake devices in its own.
package wol

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Limits on one wake job.
const (
	MaxTargets    = 32
	MaxBroadcasts = 4
	repeats       = 3 // packets get lost; wake-up is idempotent
)

// ValidPort reports whether p is a port Wake-on-LAN listens on.
func ValidPort(p int) bool { return p == 0 || p == 7 || p == 9 }

// ValidBroadcast reports whether a is an IPv4 address the agent may send
// a wake packet to: the limited broadcast or a directed broadcast (the last
// address of a network, which ends in 255 for /24 and larger networks; any
// IPv4 address is accepted, since smaller networks end differently).
func ValidBroadcast(a string) bool {
	ip := net.ParseIP(a)
	return ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsUnspecified()
}

// Broadcast returns the directed broadcast address of an IPv4 network
// ("192.168.1.20/24" -> "192.168.1.255").
func Broadcast(cidr string) (string, bool) {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", false
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 8 || ones > 30 {
		return "", false
	}
	b := make(net.IP, 4)
	base := n.IP.To4()
	for i := range 4 {
		b[i] = base[i] | ^n.Mask[i]
	}
	return b.String(), true
}

// Send sends the magic packets of the targets to their broadcast
// addresses and returns how many it sent.
func Send(targets []agentproto.WakeTarget, port int) (int, error) {
	if port == 0 {
		port = agentproto.WakePort
	}
	if !ValidPort(port) {
		return 0, fmt.Errorf("invalid wake port %d", port)
	}
	if len(targets) == 0 || len(targets) > MaxTargets {
		return 0, errors.New("no or too many wake targets")
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	sent := 0
	var last error
	for r := range repeats {
		if r > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		for _, t := range targets {
			pkt, err := agentproto.MagicPacket(t.MAC)
			if err != nil {
				last = err
				continue
			}
			bcs := t.Broadcasts
			if len(bcs) > MaxBroadcasts {
				bcs = bcs[:MaxBroadcasts]
			}
			for _, bc := range bcs {
				if !ValidBroadcast(bc) {
					last = fmt.Errorf("invalid broadcast address %q", bc)
					continue
				}
				addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(bc, strconv.Itoa(port)))
				if err != nil {
					last = err
					continue
				}
				_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if _, err := conn.WriteToUDP(pkt, addr); err != nil {
					last = err
					continue
				}
				if r == 0 {
					sent++
				}
			}
		}
	}
	if sent == 0 {
		if last == nil {
			last = errors.New("nothing to send")
		}
		return 0, last
	}
	return sent, nil
}
