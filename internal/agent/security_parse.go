package agent

// Parsers for the Linux security status. They live outside the linux build
// so the tests run on every platform.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type lsblkNode struct {
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	FSType     string      `json:"fstype"`
	MountPoint string      `json:"mountpoint"`
	Children   []lsblkNode `json:"children"`
}

// skipMount leaves out what is no data volume.
func skipMount(m string) bool {
	return m == "" || m == "[SWAP]" || strings.HasPrefix(m, "/snap/") || strings.HasPrefix(m, "/boot/efi") || strings.HasPrefix(m, "/run/") ||
		strings.HasPrefix(m, "/sys/") || strings.HasPrefix(m, "/proc/")
}

// parseLsblkSecurity finds the mounted file systems and whether a LUKS (or
// other dm-crypt) layer lies under them.
func parseLsblkSecurity(raw []byte) ([]agentproto.SecureVolume, error) {
	var doc struct {
		Devices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var out []agentproto.SecureVolume
	seen := map[string]bool{}
	var walk func(n lsblkNode, crypt bool)
	walk = func(n lsblkNode, crypt bool) {
		crypt = crypt || n.Type == "crypt"
		if !skipMount(n.MountPoint) && !seen[n.MountPoint] && n.Type != "rom" && n.Type != "loop" {
			seen[n.MountPoint] = true
			v := agentproto.SecureVolume{Mount: n.MountPoint, System: n.MountPoint == "/", Protection: "off", Status: "decrypted"}
			if crypt {
				v.Method, v.Protection, v.Status, v.Percent = agentproto.EncryptionLUKS, "on", "encrypted", 100
			}
			out = append(out, v)
		}
		for _, c := range n.Children {
			walk(c, crypt)
		}
	}
	for _, d := range doc.Devices {
		walk(d, false)
	}
	return out, nil
}

// parseLinuxAdmins lists the members of sudo, wheel and admin (also by
// primary group) and the accounts with UID 0.
func parseLinuxAdmins(group, passwd string) []agentproto.LocalAdmin {
	gids := map[string]bool{}
	names := map[string]bool{}
	for _, line := range strings.Split(group, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 4 || !slices.Contains([]string{"sudo", "wheel", "admin"}, f[0]) {
			continue
		}
		gids[f[2]] = true
		for _, m := range strings.Split(f[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				names[m] = true
			}
		}
	}
	root := map[string]bool{}
	for _, line := range strings.Split(passwd, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 7 {
			continue
		}
		if f[2] == "0" {
			names[f[0]] = true
			root[f[0]] = true
		} else if gids[f[3]] {
			names[f[0]] = true
		}
	}
	out := []agentproto.LocalAdmin{}
	for n := range names {
		out = append(out, agentproto.LocalAdmin{Name: n, Kind: "user", Builtin: root[n]})
	}
	slices.SortFunc(out, func(a, b agentproto.LocalAdmin) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// linuxFirewall reports the firewall in use: the Proxmox firewall, ufw,
// firewalld, or a ruleset in nftables or iptables.
func linuxFirewall(run func(string, ...string) ([]byte, error)) []agentproto.FirewallState {
	var out []agentproto.FirewallState
	if b, err := run("pve-firewall", "status"); err == nil {
		out = append(out, agentproto.FirewallState{Name: "Proxmox VE firewall", Enabled: bytes.Contains(b, []byte("enabled/running"))})
	}
	if b, err := run("ufw", "status", "verbose"); err == nil {
		st := agentproto.FirewallState{Name: "ufw", Enabled: bytes.Contains(b, []byte("Status: active"))}
		if st.Enabled {
			st.Inbound = "allow"
			if bytes.Contains(b, []byte("deny (incoming)")) || bytes.Contains(b, []byte("reject (incoming)")) {
				st.Inbound = "block"
			}
		}
		out = append(out, st)
	}
	if b, err := run("systemctl", "is-active", "firewalld"); err == nil || len(b) > 0 {
		if strings.TrimSpace(string(b)) == "active" {
			out = append(out, agentproto.FirewallState{Name: "firewalld", Enabled: true, Inbound: "block"})
		}
	}
	if len(out) > 0 {
		return out
	}
	if b, err := run("nft", "list", "ruleset"); err == nil {
		if nftFilters(string(b)) {
			return []agentproto.FirewallState{{Name: "nftables", Enabled: true}}
		}
	}
	if b, err := run("iptables", "-S", "INPUT"); err == nil {
		if iptablesFilters(string(b)) {
			return []agentproto.FirewallState{{Name: "iptables", Enabled: true}}
		}
	}
	return []agentproto.FirewallState{{Name: "none", Enabled: false}}
}

// nftFilters reports whether a ruleset has an input chain that filters.
func nftFilters(ruleset string) bool {
	for _, line := range strings.Split(ruleset, "\n") {
		l := strings.TrimSpace(line)
		if strings.Contains(l, "hook input") && (strings.Contains(l, "policy drop") || strings.Contains(l, "policy reject")) {
			return true
		}
	}
	return strings.Contains(ruleset, "hook input") && (strings.Contains(ruleset, " drop") || strings.Contains(ruleset, " reject"))
}

// iptablesFilters reports whether INPUT drops by default or has rules.
func iptablesFilters(rules string) bool {
	n := 0
	for _, line := range strings.Split(rules, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "-P INPUT DROP") || strings.HasPrefix(l, "-P INPUT REJECT") {
			return true
		}
		if strings.HasPrefix(l, "-A INPUT") {
			n++
		}
	}
	return n > 0
}

// linuxAntivirus finds ClamAV and Microsoft Defender for Endpoint.
func linuxAntivirus(run func(string, ...string) ([]byte, error)) []agentproto.Antivirus {
	var out []agentproto.Antivirus
	if b, err := run("mdatp", "health", "--field", "real_time_protection_enabled"); err == nil {
		on := strings.Contains(string(b), "true")
		up := true
		if d, err := run("mdatp", "health", "--field", "definitions_status"); err == nil {
			up = strings.Contains(string(d), "up_to_date")
		}
		out = append(out, agentproto.Antivirus{Name: "Microsoft Defender for Endpoint", Enabled: on, UpToDate: up, Defender: true})
	}
	active := func(unit string) bool {
		b, _ := run("systemctl", "is-active", unit)
		return strings.TrimSpace(string(b)) == "active"
	}
	if _, err := run("clamscan", "--version"); err == nil {
		out = append(out, agentproto.Antivirus{Name: "ClamAV", Enabled: active("clamav-daemon") || active("clamd@scan"),
			UpToDate: active("clamav-freshclam")})
	}
	return out
}

const secureBootVar = "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"

// linuxSecureBoot reads the SecureBoot EFI variable: four attribute bytes,
// then 1 when Secure Boot is on. No EFI means legacy boot: off.
func linuxSecureBoot(efiDir string) bool {
	b, err := os.ReadFile(filepath.Join(efiDir, "efivars", secureBootVar))
	return err == nil && len(b) >= 5 && b[4] == 1
}

func linuxTPM(dir string) *agentproto.TPMStatus {
	if _, err := os.Stat(filepath.Join(dir, "tpm0")); err != nil {
		return &agentproto.TPMStatus{}
	}
	t := &agentproto.TPMStatus{Present: true, Ready: true}
	if b, err := os.ReadFile(filepath.Join(dir, "tpm0", "tpm_version_major")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "2":
			t.Version = "2.0"
		case "1":
			t.Version = "1.2"
		}
	}
	return t
}
