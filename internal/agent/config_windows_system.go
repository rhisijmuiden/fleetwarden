//go:build windows

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Settings of the device on Windows: power timeouts, members of the local
// Administrators group and firewall rules.

func (w *winConfig) SystemStore(kind string) (PolicyStore, error) {
	switch kind {
	case SystemPower:
		root, prefix := registry.LOCAL_MACHINE, ""
		if w.AsCurrentUser {
			root, prefix = registry.CURRENT_USER, w.PolicyRoot+`\Machine\`
		}
		return &powerStore{root: root, prefix: prefix}, nil
	case SystemAdmins:
		return w.openAdminStore()
	case SystemFirewall:
		return w.openFirewallStore()
	}
	return nil, nil
}

// output runs a PowerShell script for a store and returns what it printed.
func (w *winConfig) output(script string) (string, error) {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; [Console]::OutputEncoding = [Text.Encoding]::UTF8\n"+script).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, tailString(strings.TrimSpace(string(out)), 400))
	}
	return string(out), nil
}

// Power settings are written as the Windows power policy (what group policy
// sets: the user cannot change them) and into the active power plan, so they
// apply at once. Removing one removes the policy; the plan keeps the value
// until the user changes it.

const powerPolicyKey = `SOFTWARE\Policies\Microsoft\Power\PowerSettings\`

type powerStore struct {
	root   registry.Key
	prefix string
}

func (s *powerStore) key(ps PowerSetting) string { return s.prefix + powerPolicyKey + ps.Setting }

func powerValueName(ac bool) string {
	if ac {
		return "ACSettingIndex"
	}
	return "DCSettingIndex"
}

func (s *powerStore) Matches(name string, v any) (bool, bool, error) {
	ps, ac, ok := ParsePowerSettingName(name)
	if !ok {
		return false, false, fmt.Errorf("unknown power setting %q", name)
	}
	k, err := registry.OpenKey(s.root, s.key(ps), registry.QUERY_VALUE)
	if err != nil {
		return false, false, nil
	}
	defer k.Close()
	cur, _, err := k.GetIntegerValue(powerValueName(ac))
	if err != nil {
		return false, false, nil
	}
	return int64(cur) == toInt64(v), true, nil
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return -1
}

func (s *powerStore) Set(name string, v any) error {
	ps, ac, ok := ParsePowerSettingName(name)
	if !ok {
		return fmt.Errorf("unknown power setting %q", name)
	}
	secs := toInt64(v)
	k, _, err := registry.CreateKey(s.root, s.key(ps), registry.SET_VALUE)
	if err != nil {
		return err
	}
	err = k.SetDWordValue(powerValueName(ac), uint32(secs))
	k.Close()
	if err != nil {
		return err
	}
	// The active plan too, so it applies now. A PC without a battery has no
	// DC values to set; that is not an error.
	flag := "/setdcvalueindex"
	if ac {
		flag = "/setacvalueindex"
	}
	if out, err := exec.Command("powercfg.exe", flag, "SCHEME_CURRENT", ps.Sub, ps.Setting, fmt.Sprint(secs)).CombinedOutput(); err != nil {
		return fmt.Errorf("powercfg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("powercfg.exe", "/setactive", "SCHEME_CURRENT").Run()
	return nil
}

func (s *powerStore) Delete(name string) error {
	ps, ac, ok := ParsePowerSettingName(name)
	if !ok {
		return nil
	}
	k, err := registry.OpenKey(s.root, s.key(ps), registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	_ = k.DeleteValue(powerValueName(ac))
	left, _ := k.ReadValueNames(-1)
	k.Close()
	if len(left) == 0 {
		_ = registry.DeleteKey(s.root, s.key(ps))
	}
	return nil
}

func (s *powerStore) Close() error { return nil }

// The local Administrators group is found by its SID, because its name
// depends on the language of Windows.
const adminsSID = "S-1-5-32-544"

type adminStore struct {
	w     *winConfig
	sids  map[string]bool // members by SID
	names map[string]bool // members by lower-case DOMAIN\name
}

// adminMembersScript lists the members of the group, one "SID<tab>path" per
// line. It reads them through ADSI, which also lists members whose account
// is gone (Get-LocalGroupMember fails on those).
const adminMembersScript = `$name = (New-Object Security.Principal.SecurityIdentifier '` + adminsSID + `').Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
$grp = [ADSI]("WinNT://$env:COMPUTERNAME/$name,group")
foreach ($m in @($grp.psbase.Invoke('Members'))) {
  $t = $m.GetType()
  $sid = New-Object Security.Principal.SecurityIdentifier([byte[]]$t.InvokeMember('objectSid', 'GetProperty', $null, $m, $null), 0)
  $path = $t.InvokeMember('ADsPath', 'GetProperty', $null, $m, $null)
  "$($sid.Value)` + "`t" + `$path"
}`

func (w *winConfig) openAdminStore() (PolicyStore, error) {
	out, err := w.output(adminMembersScript)
	if err != nil {
		return nil, fmt.Errorf("listing the local administrators: %w", err)
	}
	s := &adminStore{w: w, sids: map[string]bool{}, names: map[string]bool{}}
	for _, line := range strings.Split(out, "\n") {
		sid, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		s.sids[strings.ToUpper(sid)] = true
		// WinNT://DOMAIN/name or WinNT://WORKGROUP/PC/name
		parts := strings.Split(strings.TrimPrefix(path, "WinNT://"), "/")
		if n := len(parts); n >= 2 {
			s.names[strings.ToLower(parts[n-2]+`\`+parts[n-1])] = true
		}
	}
	return s, nil
}

// memberSID resolves an account to its SID; "" when Windows does not know it
// (yet), for example an Entra ID account that never signed in.
func memberSID(member string) string {
	if strings.HasPrefix(strings.ToUpper(member), "S-1-") {
		if sid, err := windows.StringToSid(member); err == nil {
			return sid.String()
		}
		return ""
	}
	sid, _, _, err := windows.LookupSID("", member)
	if err != nil {
		return ""
	}
	return sid.String()
}

func (s *adminStore) Matches(_ string, v any) (bool, bool, error) {
	member, _ := v.(string)
	if sid := memberSID(member); sid != "" {
		ok := s.sids[strings.ToUpper(sid)]
		return ok, ok, nil
	}
	m := strings.ToLower(member)
	if !strings.Contains(m, `\`) {
		m = strings.ToLower(windowsComputerName()) + `\` + m
	}
	ok := s.names[m]
	return ok, ok, nil
}

func windowsComputerName() string {
	n, _ := windows.ComputerName()
	return n
}

func (s *adminStore) Set(_ string, v any) error {
	member, _ := v.(string)
	return s.w.run(agentproto.ShellPowerShell, fmt.Sprintf(`try { Add-LocalGroupMember -SID '%s' -Member %s }
catch { if ($_.FullyQualifiedErrorId -notlike 'MemberExists*') { throw } }`, adminsSID, psq(member)), nil)
}

func (s *adminStore) Delete(name string) error {
	member := strings.TrimPrefix(name, "admin:")
	if sid := memberSID(member); sid != "" {
		if !s.sids[strings.ToUpper(sid)] {
			return nil
		}
		member = sid
	}
	return s.w.run(agentproto.ShellPowerShell, fmt.Sprintf(`try { Remove-LocalGroupMember -SID '%s' -Member %s }
catch { if ($_.FullyQualifiedErrorId -notlike 'MemberNotFound*' -and $_.FullyQualifiedErrorId -notlike 'PrincipalNotFound*') { throw } }`, adminsSID, psq(member)), nil)
}

func (s *adminStore) Close() error { return nil }

// Firewall rules live in the group "FleetWarden". A rule's description holds
// a hash of its configuration, so a changed configuration replaces the rule;
// the agent also checks the fields someone is likely to change by hand.

type firewallStore struct {
	w     *winConfig
	rules map[string]liveRule // by Windows rule name
}

type liveRule struct {
	Name        string
	Description string
	Enabled     string
	Direction   string
	Action      string
	Profile     string
	Protocol    string
	LocalPort   string
	RemotePort  string
	Program     string
}

const firewallListScript = `@(Get-NetFirewallRule -Group '` + agentproto.FirewallGroup + `' -ErrorAction SilentlyContinue | ForEach-Object {
  $p = $_ | Get-NetFirewallPortFilter
  $a = $_ | Get-NetFirewallApplicationFilter
  [pscustomobject]@{ Name = $_.Name; Description = [string]$_.Description; Enabled = [string]$_.Enabled
    Direction = [string]$_.Direction; Action = [string]$_.Action; Profile = [string]$_.Profile
    Protocol = [string]$p.Protocol; LocalPort = (@($p.LocalPort) -join ','); RemotePort = (@($p.RemotePort) -join ',')
    Program = [string]$a.Program }
}) | ConvertTo-Json -Compress -Depth 2`

func (w *winConfig) openFirewallStore() (PolicyStore, error) {
	out, err := w.output(firewallListScript)
	if err != nil {
		return nil, fmt.Errorf("listing the firewall rules: %w", err)
	}
	s := &firewallStore{w: w, rules: map[string]liveRule{}}
	out = strings.TrimSpace(out)
	if out == "" {
		return s, nil
	}
	var list []liveRule
	if strings.HasPrefix(out, "{") {
		var one liveRule
		err = json.Unmarshal([]byte(out), &one)
		list = []liveRule{one}
	} else {
		err = json.Unmarshal([]byte(out), &list)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the firewall rules: %w", err)
	}
	for _, r := range list {
		s.rules[strings.ToLower(r.Name)] = r
	}
	return s, nil
}

// firewallRuleName is the Windows name (ID) of a rule.
func firewallRuleName(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(name)))
	return "FleetWarden-" + hex.EncodeToString(sum[:8])
}

func toFirewallRule(v any) agentproto.FirewallRule {
	switch r := v.(type) {
	case agentproto.FirewallRule:
		return r
	case *agentproto.FirewallRule:
		return *r
	}
	var r agentproto.FirewallRule
	raw, _ := json.Marshal(v)
	_ = json.Unmarshal(raw, &r)
	return r
}

func firewallHash(r agentproto.FirewallRule) string {
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return "FleetWarden " + hex.EncodeToString(sum[:8])
}

// firewallProtocol is the protocol as New-NetFirewallRule takes it.
func firewallProtocol(p string) string {
	switch p {
	case "tcp":
		return "TCP"
	case "udp":
		return "UDP"
	case "icmpv4":
		return "ICMPv4"
	case "icmpv6":
		return "ICMPv6"
	}
	return "Any"
}

func firewallPorts(p string) string {
	if p == "" {
		return "Any"
	}
	return strings.ReplaceAll(p, " ", "")
}

func (s *firewallStore) Matches(_ string, v any) (bool, bool, error) {
	r := toFirewallRule(v)
	live, ok := s.rules[strings.ToLower(firewallRuleName(r.Name))]
	if !ok {
		return false, false, nil
	}
	dir := "Inbound"
	if r.Direction == agentproto.FirewallOut {
		dir = "Outbound"
	}
	action := "Allow"
	if r.Action == agentproto.FirewallBlock {
		action = "Block"
	}
	match := live.Description == firewallHash(r) && live.Enabled == "True" &&
		strings.EqualFold(live.Direction, dir) && strings.EqualFold(live.Action, action) &&
		strings.EqualFold(live.Protocol, firewallProtocol(r.Protocol))
	if r.Protocol == "tcp" || r.Protocol == "udp" {
		match = match && strings.EqualFold(live.LocalPort, firewallPorts(r.LocalPorts)) && strings.EqualFold(live.RemotePort, firewallPorts(r.RemotePorts))
	}
	return match, true, nil
}

func (s *firewallStore) Set(_ string, v any) error {
	r := toFirewallRule(v)
	id := firewallRuleName(r.Name)
	dir := "Inbound"
	if r.Direction == agentproto.FirewallOut {
		dir = "Outbound"
	}
	action := "Allow"
	if r.Action == agentproto.FirewallBlock {
		action = "Block"
	}
	profile := "Any"
	if len(r.Profiles) > 0 {
		var ps []string
		for _, p := range r.Profiles {
			ps = append(ps, strings.ToUpper(p[:1])+p[1:])
		}
		profile = strings.Join(ps, ",")
	}
	args := fmt.Sprintf("-Name %s -DisplayName %s -Group %s -Description %s -Direction %s -Action %s -Profile %s -Protocol %s -Enabled True",
		psq(id), psq(r.Name), psq(agentproto.FirewallGroup), psq(firewallHash(r)), dir, action, profile, firewallProtocol(r.Protocol))
	list := func(s string) string {
		var q []string
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				q = append(q, psq(p))
			}
		}
		return "@(" + strings.Join(q, ",") + ")"
	}
	if r.Protocol == "tcp" || r.Protocol == "udp" {
		if r.LocalPorts != "" {
			args += " -LocalPort " + list(r.LocalPorts)
		}
		if r.RemotePorts != "" {
			args += " -RemotePort " + list(r.RemotePorts)
		}
	}
	if r.RemoteAddresses != "" {
		args += " -RemoteAddress " + list(r.RemoteAddresses)
	}
	if r.Program != "" {
		args += " -Program " + psq(r.Program)
	}
	script := fmt.Sprintf("Remove-NetFirewallRule -Name %s -ErrorAction SilentlyContinue\nNew-NetFirewallRule %s | Out-Null\n", psq(id), args)
	return s.w.run(agentproto.ShellPowerShell, script, nil)
}

func (s *firewallStore) Delete(name string) error {
	// The setting name holds the lower-case rule name, which is what the
	// Windows name is made from.
	id := firewallRuleName(strings.TrimPrefix(name, "rule:"))
	if _, ok := s.rules[strings.ToLower(id)]; !ok {
		return nil
	}
	return s.w.run(agentproto.ShellPowerShell, "Remove-NetFirewallRule -Name "+psq(id), nil)
}

func (s *firewallStore) Close() error { return nil }
