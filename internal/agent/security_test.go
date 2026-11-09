package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// A trimmed answer of the security script on a Windows 11 laptop.
const winSecuritySample = `{"errors":[],
"volumes":[{"mount":"C:","os":true,"protection":"On","status":"FullyEncrypted","percent":100,"cipher":"XtsAes128","protectors":["Tpm","RecoveryPassword"],
  "keys":[{"id":"{1f0e5c7a-3b2d-4c1e-9a8b-7d6c5b4a3f21}","password":"123456-234567-345678-456789-567890-678901-789012-890123"}]},
 {"mount":"D:","os":false,"protection":"Off","status":"FullyDecrypted","percent":0,"cipher":"None","protectors":[],"keys":[]},
 {"mount":"\\\\?\\Volume{abc}\\","os":false,"protection":"Off","status":"FullyDecrypted","percent":0,"cipher":"None","protectors":[],"keys":[]},
 {"mount":"E:","os":false,"protection":"Off","status":"FullyEncrypted","percent":100,"cipher":"XtsAes256","protectors":["RecoveryPassword"],"keys":[]}],
"defender":{"mode":"Normal","service":true,"av":true,"rt":true,"tamper":true,"sig":"1.421.95.0","sigAt":"2026-11-08T06:12:00.0000000Z","engine":"1.1.24090.11",
  "quick":"2026-11-07T12:00:00.0000000Z","full":null},
"threats":[{"name":"Trojan:Win32/Wacatac.B!ml","severity":5,"at":"2026-11-08T09:00:00.0000000Z","status":1,"active":true,"resources":["file:_C:\\Users\\jan\\Downloads\\x.exe"]},
  {"name":"PUA:Win32/Presenoker","severity":1,"at":"2026-11-01T09:00:00.0000000Z","status":3,"active":true,"resources":[]}],
"av":[{"name":"Windows Defender","state":397568}],
"firewall":[{"name":"Domain","enabled":true,"inbound":"NotConfigured","active":false},{"name":"Private","enabled":true,"inbound":"Block","active":true},
  {"name":"Public","enabled":false,"inbound":"Allow","active":false}],
"admins":[{"path":"WinNT://PC-01/Administrator","sid":"S-1-5-21-1-2-3-500","cls":"User"},{"path":"WinNT://CORP/Domain Admins","sid":"S-1-5-21-9-8-7-512","cls":"Group"},
  {"path":"WinNT://CORP/jan","sid":"S-1-5-21-9-8-7-1104","cls":"User"},{"path":"S-1-5-21-5-5-5-1001","sid":"S-1-5-21-5-5-5-1001","cls":""}],
"secureBoot":true,"tpm":{"present":true,"ready":true,"version":"2.0"}}`

func TestParseWindowsSecurity(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	rep, err := parseWinSecurity([]byte(winSecuritySample))
	if err != nil {
		t.Fatal(err)
	}
	normalizeSecurity(&rep)
	if len(rep.Volumes) != 3 {
		t.Fatalf("volumes: %+v", rep.Volumes)
	}
	c, d, e := rep.Volumes[0], rep.Volumes[1], rep.Volumes[2]
	if !c.System || c.Method != agentproto.EncryptionBitLocker || c.Protection != "on" || c.Status != "encrypted" || c.Cipher != "XtsAes128" ||
		len(c.Keys) != 1 || c.Keys[0].ID != "{1F0E5C7A-3B2D-4C1E-9A8B-7D6C5B4A3F21}" {
		t.Errorf("C: %+v", c)
	}
	if d.Mount != "D:" || d.Method != "" || d.Protection != "off" || d.Status != "decrypted" {
		t.Errorf("D: %+v", d)
	}
	if e.Mount != "E:" || e.Protection != "suspended" || e.Method != agentproto.EncryptionBitLocker {
		t.Errorf("E: %+v", e)
	}
	df := rep.Defender
	if df == nil || !df.RealTime || df.SignatureUpdatedAt == nil || df.SignatureUpdatedAt.Hour() != 6 || df.FullScanAt != nil || df.QuickScanAt == nil {
		t.Fatalf("defender: %+v", df)
	}
	if len(df.Threats) != 2 || !df.Threats[0].Active || df.Threats[0].Severity != "Severe" || df.Threats[1].Active || df.Threats[1].Action != "Quarantined" {
		t.Errorf("threats: %+v", df.Threats)
	}
	// productState 0x061100: on, up to date.
	if len(rep.Antivirus) != 1 || !rep.Antivirus[0].Enabled || !rep.Antivirus[0].UpToDate || !rep.Antivirus[0].Defender {
		t.Errorf("antivirus: %+v", rep.Antivirus)
	}
	if len(rep.Firewall) != 3 || rep.Firewall[0].Inbound != "block" || !rep.Firewall[1].Active || rep.Firewall[2].Enabled {
		t.Errorf("firewall: %+v", rep.Firewall)
	}
	if len(rep.Admins) != 4 {
		t.Fatalf("admins: %+v", rep.Admins)
	}
	byName := map[string]agentproto.LocalAdmin{}
	for _, a := range rep.Admins {
		byName[a.Name] = a
	}
	if a := byName[`PC-01\Administrator`]; !a.Builtin || a.Kind != "user" {
		t.Errorf("administrator: %+v", a)
	}
	if a := byName[`CORP\Domain Admins`]; !a.Builtin || a.Kind != "group" {
		t.Errorf("domain admins: %+v", a)
	}
	if a := byName[`CORP\jan`]; a.Builtin || a.Kind != "user" {
		t.Errorf("jan: %+v", a)
	}
	if a := byName["S-1-5-21-5-5-5-1001"]; a.Kind != "unknown" {
		t.Errorf("orphan: %+v", a)
	}
	if rep.SecureBoot == nil || !*rep.SecureBoot || rep.TPM == nil || rep.TPM.Version != "2.0" {
		t.Errorf("secure boot / tpm: %v %+v", rep.SecureBoot, rep.TPM)
	}
}

func TestParseWindowsServerSecurity(t *testing.T) {
	// Windows Server: no Security Center, Defender in passive mode.
	raw := `{"errors":["BitLocker: The term 'Get-BitLockerVolume' is not recognized"],"defender":{"mode":"Passive","service":true,"av":true,"rt":false,
		"sig":"1.1","sigAt":"2026-01-01T00:00:00Z"},"firewall":[{"name":"Domain","enabled":true}]}`
	rep, err := parseWinSecurity([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Antivirus) != 1 || rep.Antivirus[0].Enabled || rep.Antivirus[0].UpToDate || !rep.Antivirus[0].Defender {
		t.Errorf("antivirus: %+v", rep.Antivirus)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("errors: %v", rep.Errors)
	}
}

func TestAVProductState(t *testing.T) {
	for _, c := range []struct {
		state   uint32
		on, new bool
	}{{0x061100, true, true}, {0x061000, true, true}, {0x060100, false, true}, {0x061110, true, false}, {0x041000, true, true}, {0x040100, false, true}} {
		on, up := avProductState(c.state)
		if on != c.on || up != c.new {
			t.Errorf("%#x: on=%v up=%v", c.state, on, up)
		}
	}
}

func TestParseLsblk(t *testing.T) {
	raw := `{"blockdevices":[
	 {"name":"/dev/nvme0n1","type":"disk","fstype":null,"mountpoint":null,"children":[
	  {"name":"/dev/nvme0n1p1","type":"part","fstype":"vfat","mountpoint":"/boot/efi"},
	  {"name":"/dev/nvme0n1p2","type":"part","fstype":"ext4","mountpoint":"/boot"},
	  {"name":"/dev/nvme0n1p3","type":"part","fstype":"crypto_LUKS","mountpoint":null,"children":[
	   {"name":"/dev/mapper/root","type":"crypt","fstype":"LVM2_member","mountpoint":null,"children":[
	    {"name":"/dev/mapper/vg-root","type":"lvm","fstype":"ext4","mountpoint":"/"},
	    {"name":"/dev/mapper/vg-swap","type":"lvm","fstype":"swap","mountpoint":"[SWAP]"}]}]}]},
	 {"name":"/dev/sda","type":"disk","fstype":null,"mountpoint":null,"children":[{"name":"/dev/sda1","type":"part","fstype":"xfs","mountpoint":"/srv"}]},
	 {"name":"/dev/sr0","type":"rom","fstype":"iso9660","mountpoint":"/media/cdrom"}]}`
	vols, err := parseLsblkSecurity([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]agentproto.SecureVolume{}
	for _, v := range vols {
		got[v.Mount] = v
	}
	if len(vols) != 3 || got["/"].Method != agentproto.EncryptionLUKS || !got["/"].System || got["/boot"].Method != "" || got["/srv"].Method != "" {
		t.Errorf("volumes: %+v", vols)
	}
}

func TestLinuxAdmins(t *testing.T) {
	group := "root:x:0:\nsudo:x:27:jan,piet\nwheel:x:10:\nusers:x:100:klaas\nbeheer:x:1001:\n"
	passwd := "root:x:0:0:root:/root:/bin/bash\njan:x:1000:1000::/home/jan:/bin/bash\nanna:x:1002:10::/home/anna:/bin/bash\nklaas:x:1003:100::/home/klaas:/bin/bash\n"
	admins := parseLinuxAdmins(group, passwd)
	var names []string
	for _, a := range admins {
		names = append(names, a.Name)
	}
	if len(admins) != 4 || names[0] != "anna" || names[1] != "jan" || names[2] != "piet" || names[3] != "root" || !admins[3].Builtin {
		t.Errorf("admins: %+v", admins)
	}
}

func TestLinuxFirewall(t *testing.T) {
	out := map[string]string{
		"ufw":       "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n",
		"systemctl": "inactive\n",
	}
	run := func(name string, _ ...string) ([]byte, error) {
		if v, ok := out[name]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	fw := linuxFirewall(run)
	if len(fw) != 1 || fw[0].Name != "ufw" || !fw[0].Enabled || fw[0].Inbound != "block" {
		t.Errorf("ufw: %+v", fw)
	}
	out = map[string]string{"nft": "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n"}
	if fw := linuxFirewall(run); len(fw) != 1 || fw[0].Name != "nftables" || !fw[0].Enabled {
		t.Errorf("nftables: %+v", fw)
	}
	out = map[string]string{"nft": "", "iptables": "-P INPUT ACCEPT\n"}
	if fw := linuxFirewall(run); len(fw) != 1 || fw[0].Enabled {
		t.Errorf("no firewall: %+v", fw)
	}
}

func TestLinuxSecureBootAndTPM(t *testing.T) {
	dir := t.TempDir()
	if linuxSecureBoot(filepath.Join(dir, "none")) {
		t.Error("secure boot without EFI")
	}
	efi := filepath.Join(dir, "efi")
	if err := os.MkdirAll(filepath.Join(efi, "efivars"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(efi, "efivars", secureBootVar), []byte{6, 0, 0, 0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}
	if !linuxSecureBoot(efi) {
		t.Error("secure boot on not seen")
	}
	tpm := filepath.Join(dir, "tpm")
	if st := linuxTPM(tpm); st.Present {
		t.Errorf("tpm: %+v", st)
	}
	if err := os.MkdirAll(filepath.Join(tpm, "tpm0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpm, "tpm0", "tpm_version_major"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := linuxTPM(tpm); !st.Present || st.Version != "2.0" {
		t.Errorf("tpm 2.0: %+v", st)
	}
}
