package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"sort"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// localIPs returns the non-loopback, non-link-local addresses.
func localIPs() []string {
	var out []string
	for _, n := range networkInterfaces() {
		out = append(out, n.IPs...)
	}
	return out
}

func networkInterfaces() []agentproto.NetIface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []agentproto.NetIface
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		n := agentproto.NetIface{Name: i.Name, MAC: i.HardwareAddr.String()}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLoopback() {
				continue
			}
			n.IPs = append(n.IPs, ipn.IP.String())
			n.CIDRs = append(n.CIDRs, ipn.String())
		}
		if len(n.IPs) > 0 || n.MAC != "" {
			out = append(out, n)
		}
	}
	return out
}

func sortSoftware(sw []agentproto.Software) []agentproto.Software {
	seen := map[string]bool{}
	out := sw[:0]
	for _, s := range sw {
		k := strings.ToLower(s.Name) + "\x00" + s.Version
		if s.Name == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// CollectInventory gathers the full inventory of this machine.
func CollectInventory(ctx context.Context) (agentproto.Inventory, error) {
	return collectInventory(ctx)
}
