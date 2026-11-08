package server

import (
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Validation of the Windows settings: desktop, power, local administrators
// and firewall rules.

var (
	// A wallpaper is a local path (C:\...) or a share (\\server\...), to an
	// image file.
	wallpaperRE = regexp.MustCompile(`(?i)^(?:[a-z]:\\|\\\\[^\\/:*?"<>|\s]+\\)[^:*?"<>|\x00-\x1f]+\.(?:jpe?g|png|bmp|gif|tiff?|jfif)$`)
	// An account: DOMAIN\name, name, AzureAD\user@example.com or a SID.
	memberRE       = regexp.MustCompile(`^(?:S-1-[0-9-]+|(?:[^\\/:*?"<>|\x00-\x1f]{1,64}\\)?[^\\/:*?"<>|\[\]=;,+\x00-\x1f]{1,104})$`)
	ruleNameRE     = regexp.MustCompile(`^[^|\\'"\x00-\x1f]{1,100}$`)
	programRE      = regexp.MustCompile(`(?i)^(?:[a-z]:\\|%[a-z]+%\\)[^:*?"<>|\x00-\x1f]+\.exe$`)
	portItemRE     = regexp.MustCompile(`^\d{1,5}(?:-\d{1,5})?$`)
	maxLockMinutes = 9999 // Windows takes up to 599940 seconds
)

const maxPowerMinutes = 24 * 60 * 7

func validDesktop(d *agentproto.DesktopSettings) string {
	d.Wallpaper = strings.TrimSpace(strings.ReplaceAll(d.Wallpaper, "/", `\`))
	if d.Wallpaper == "" && d.LockMinutes == 0 {
		return "nothing_configured"
	}
	if d.Wallpaper != "" {
		if len(d.Wallpaper) > 260 || !wallpaperRE.MatchString(d.Wallpaper) {
			return "invalid_wallpaper"
		}
		if d.WallpaperStyle == "" {
			d.WallpaperStyle = "fill"
		}
		if !slices.Contains(agentproto.WallpaperStyles, d.WallpaperStyle) {
			return "invalid_wallpaper_style"
		}
	} else {
		d.WallpaperStyle = ""
	}
	if d.LockMinutes < 0 || d.LockMinutes > maxLockMinutes {
		return "invalid_lock_minutes"
	}
	return ""
}

func validPower(p *agentproto.PowerSettings) string {
	n := 0
	for _, v := range []*int{p.MonitorAC, p.MonitorDC, p.SleepAC, p.SleepDC, p.HibernateAC, p.HibernateDC} {
		if v == nil {
			continue
		}
		if *v < 0 || *v > maxPowerMinutes {
			return "invalid_power_minutes"
		}
		n++
	}
	if n == 0 {
		return "nothing_configured"
	}
	return ""
}

func validAdmins(a *agentproto.AdminSettings) string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range a.Members {
		m = strings.TrimSpace(strings.ReplaceAll(m, "/", `\`))
		if m == "" {
			continue
		}
		if !memberRE.MatchString(m) {
			return "invalid_member"
		}
		if seen[strings.ToLower(m)] {
			return "duplicate_member"
		}
		seen[strings.ToLower(m)] = true
		out = append(out, m)
	}
	if len(out) == 0 {
		return "nothing_configured"
	}
	if len(out) > 50 {
		return "too_many_members"
	}
	a.Members = out
	return ""
}

// cleanPorts checks a comma separated list of ports and ranges.
func cleanPorts(s string) (string, bool) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !portItemRE.MatchString(p) {
			return "", false
		}
		lo, hi, isRange := strings.Cut(p, "-")
		a, _ := strconv.Atoi(lo)
		b := a
		if isRange {
			b, _ = strconv.Atoi(hi)
		}
		if a < 1 || b > 65535 || b < a {
			return "", false
		}
		out = append(out, p)
	}
	if len(out) > 100 {
		return "", false
	}
	return strings.Join(out, ","), true
}

// cleanAddresses checks remote addresses: IPs, subnets, ranges and the
// Windows keywords.
func cleanAddresses(s string) (string, bool) {
	keywords := map[string]string{"localsubnet": "LocalSubnet", "dns": "DNS", "dhcp": "DHCP", "wins": "WINS", "defaultgateway": "DefaultGateway", "internet": "Internet", "intranet": "Intranet"}
	var out []string
	for _, a := range strings.Split(s, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if k, ok := keywords[strings.ToLower(a)]; ok {
			out = append(out, k)
			continue
		}
		if lo, hi, ok := strings.Cut(a, "-"); ok {
			x, err1 := netip.ParseAddr(lo)
			y, err2 := netip.ParseAddr(hi)
			if err1 != nil || err2 != nil || x.Is4() != y.Is4() || y.Less(x) {
				return "", false
			}
		} else if strings.Contains(a, "/") {
			if _, err := netip.ParsePrefix(a); err != nil {
				return "", false
			}
		} else if _, err := netip.ParseAddr(a); err != nil {
			return "", false
		}
		out = append(out, a)
	}
	if len(out) > 100 {
		return "", false
	}
	return strings.Join(out, ","), true
}

func validFirewall(f *agentproto.FirewallSettings) string {
	if len(f.Rules) == 0 {
		return "nothing_configured"
	}
	if len(f.Rules) > 50 {
		return "too_many_rules"
	}
	seen := map[string]bool{}
	for i := range f.Rules {
		r := &f.Rules[i]
		r.Name = strings.TrimSpace(r.Name)
		if !ruleNameRE.MatchString(r.Name) {
			return "invalid_rule_name"
		}
		if seen[strings.ToLower(r.Name)] {
			return "duplicate_rule"
		}
		seen[strings.ToLower(r.Name)] = true
		if r.Direction != agentproto.FirewallIn && r.Direction != agentproto.FirewallOut {
			return "invalid_direction"
		}
		if r.Action != agentproto.FirewallAllow && r.Action != agentproto.FirewallBlock {
			return "invalid_firewall_action"
		}
		if r.Protocol == "" {
			r.Protocol = "any"
		}
		if !slices.Contains(agentproto.FirewallProtocols, r.Protocol) {
			return "invalid_protocol"
		}
		var ok bool
		if r.LocalPorts, ok = cleanPorts(r.LocalPorts); !ok {
			return "invalid_ports"
		}
		if r.RemotePorts, ok = cleanPorts(r.RemotePorts); !ok {
			return "invalid_ports"
		}
		if (r.LocalPorts != "" || r.RemotePorts != "") && r.Protocol != "tcp" && r.Protocol != "udp" {
			return "ports_need_tcp_udp"
		}
		if r.RemoteAddresses, ok = cleanAddresses(r.RemoteAddresses); !ok {
			return "invalid_addresses"
		}
		r.Program = strings.TrimSpace(strings.ReplaceAll(r.Program, "/", `\`))
		if r.Program != "" && (len(r.Program) > 260 || !programRE.MatchString(r.Program)) {
			return "invalid_program"
		}
		profiles := []string{}
		for _, p := range agentproto.FirewallProfiles {
			if slices.Contains(r.Profiles, p) {
				profiles = append(profiles, p)
			}
		}
		if len(profiles) != len(r.Profiles) {
			return "invalid_profile"
		}
		if len(profiles) == len(agentproto.FirewallProfiles) {
			profiles = nil // all is the same as none
		}
		r.Profiles = profiles
	}
	return ""
}
