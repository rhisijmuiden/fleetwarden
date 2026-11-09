package agent

// Parsing of the Windows security script output. It lives outside the
// windows build so the tests run on every platform.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type winSecurity struct {
	Errors  []string `json:"errors"`
	Volumes []struct {
		Mount      string   `json:"mount"`
		OS         bool     `json:"os"`
		Protection string   `json:"protection"`
		Status     string   `json:"status"`
		Percent    int      `json:"percent"`
		Cipher     string   `json:"cipher"`
		Protectors []string `json:"protectors"`
		Keys       []struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		} `json:"keys"`
	} `json:"volumes"`
	Defender *struct {
		Mode    string  `json:"mode"`
		Service bool    `json:"service"`
		AV      bool    `json:"av"`
		RT      bool    `json:"rt"`
		Tamper  bool    `json:"tamper"`
		Sig     string  `json:"sig"`
		SigAt   *string `json:"sigAt"`
		Engine  string  `json:"engine"`
		Quick   *string `json:"quick"`
		Full    *string `json:"full"`
	} `json:"defender"`
	Threats []struct {
		Name      string   `json:"name"`
		Severity  int      `json:"severity"`
		At        *string  `json:"at"`
		Status    int      `json:"status"`
		Active    bool     `json:"active"`
		Resources []string `json:"resources"`
	} `json:"threats"`
	AV []struct {
		Name  string `json:"name"`
		State uint32 `json:"state"`
	} `json:"av"`
	Firewall []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Inbound string `json:"inbound"`
		Active  bool   `json:"active"`
	} `json:"firewall"`
	Admins []struct {
		Path string `json:"path"`
		SID  string `json:"sid"`
		Cls  string `json:"cls"`
	} `json:"admins"`
	SecureBoot *bool `json:"secureBoot"`
	TPM        *struct {
		Present bool   `json:"present"`
		Ready   bool   `json:"ready"`
		Version string `json:"version"`
	} `json:"tpm"`
}

func isoTime(v *string) *time.Time {
	if v == nil || *v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *v)
	if err != nil || t.Year() < 2000 {
		return nil
	}
	t = t.UTC()
	return &t
}

// bitLockerStatus maps VolumeStatus.
func bitLockerStatus(s string) string {
	switch s {
	case "FullyEncrypted":
		return "encrypted"
	case "EncryptionInProgress":
		return "encrypting"
	case "DecryptionInProgress":
		return "decrypting"
	case "EncryptionPaused", "DecryptionPaused":
		return "paused"
	default:
		return "decrypted"
	}
}

// Defender threat severities and states.
var (
	threatSeverity = map[int]string{1: "Low", 2: "Moderate", 4: "High", 5: "Severe"}
	threatAction   = map[int]string{1: "Detected", 2: "Cleaned", 3: "Quarantined", 4: "Removed", 5: "Allowed", 6: "Blocked",
		102: "QuarantineFailed", 103: "RemoveFailed", 104: "AllowFailed", 105: "Abandoned", 107: "BlockFailed"}
)

func threatActive(status int, active bool) bool {
	switch status {
	case 1, 102, 103, 105, 107:
		return true
	case 2, 3, 4, 5, 6:
		return false
	}
	return active
}

func parseWinSecurity(raw []byte) (agentproto.SecurityReport, error) {
	var w winSecurity
	if err := json.Unmarshal(raw, &w); err != nil {
		return agentproto.SecurityReport{}, fmt.Errorf("security status: %w", err)
	}
	rep := agentproto.SecurityReport{Errors: w.Errors, SecureBoot: w.SecureBoot}
	sysDrive := strings.ToUpper(os.Getenv("SystemDrive"))
	for _, v := range w.Volumes {
		sv := agentproto.SecureVolume{Mount: strings.ToUpper(strings.TrimSpace(v.Mount)), System: v.OS || strings.EqualFold(v.Mount, sysDrive),
			Status: bitLockerStatus(v.Status), Percent: v.Percent, Protectors: v.Protectors}
		if !driveLetterRE.MatchString(sv.Mount) {
			continue // volumes without a drive letter (recovery partitions)
		}
		if sv.Status != "decrypted" || v.Percent > 0 {
			sv.Method = agentproto.EncryptionBitLocker
			if v.Cipher != "" && v.Cipher != "None" {
				sv.Cipher = v.Cipher
			}
		}
		switch {
		case v.Protection == "On":
			sv.Protection = "on"
		case sv.Method != "" && sv.Status == "encrypted":
			// Encrypted but the key is in the clear: suspended (a BIOS
			// update, or protectors never added).
			sv.Protection = "suspended"
		default:
			sv.Protection = "off"
		}
		for _, k := range v.Keys {
			if k.ID != "" && k.Password != "" {
				sv.Keys = append(sv.Keys, agentproto.RecoveryKey{ID: strings.ToUpper(k.ID), Password: k.Password})
			}
		}
		rep.Volumes = append(rep.Volumes, sv)
	}
	if len(rep.Volumes) == 0 {
		// No BitLocker (the feature is missing): the fixed drives, unencrypted.
		for _, v := range fixedVolumes() {
			rep.Volumes = append(rep.Volumes, agentproto.SecureVolume{Mount: v.Mount, System: strings.EqualFold(v.Mount, sysDrive),
				Protection: "off", Status: "decrypted"})
		}
	}
	if d := w.Defender; d != nil {
		ds := &agentproto.DefenderStatus{Mode: d.Mode, ServiceEnabled: d.Service, AntivirusEnabled: d.AV, RealTime: d.RT,
			TamperProtected: d.Tamper, SignatureVersion: d.Sig, SignatureUpdatedAt: isoTime(d.SigAt), EngineVersion: d.Engine,
			QuickScanAt: isoTime(d.Quick), FullScanAt: isoTime(d.Full)}
		if ds.Mode == "" {
			ds.Mode = "Not running"
		}
		for _, t := range w.Threats {
			at := isoTime(t.At)
			if at == nil {
				continue
			}
			sev := threatSeverity[t.Severity]
			if sev == "" {
				sev = "Unknown"
			}
			ds.Threats = append(ds.Threats, agentproto.Threat{Name: t.Name, Severity: sev, DetectedAt: *at,
				Active: threatActive(t.Status, t.Active), Action: threatAction[t.Status], Resources: t.Resources})
		}
		rep.Defender = ds
	}
	defenderListed := false
	for _, p := range w.AV {
		en, up := avProductState(p.State)
		isDef := strings.Contains(strings.ToLower(p.Name), "defender")
		defenderListed = defenderListed || isDef
		rep.Antivirus = append(rep.Antivirus, agentproto.Antivirus{Name: p.Name, Enabled: en, UpToDate: up, Defender: isDef})
	}
	if d := rep.Defender; d != nil && !defenderListed {
		// Windows Server has no Security Center: Defender speaks for itself.
		upToDate := d.SignatureUpdatedAt != nil && time.Since(*d.SignatureUpdatedAt) < 7*24*time.Hour
		rep.Antivirus = append(rep.Antivirus, agentproto.Antivirus{Name: "Microsoft Defender Antivirus",
			Enabled: d.AntivirusEnabled && d.RealTime && d.Mode != "Passive", UpToDate: upToDate, Defender: true})
	}
	for _, f := range w.Firewall {
		in := strings.ToLower(f.Inbound)
		if in == "notconfigured" || in == "" {
			in = "block" // the Windows default
		}
		rep.Firewall = append(rep.Firewall, agentproto.FirewallState{Name: f.Name, Enabled: f.Enabled, Inbound: in, Active: f.Active})
	}
	for _, m := range w.Admins {
		rep.Admins = append(rep.Admins, windowsAdmin(m.Path, m.SID, m.Cls))
	}
	if t := w.TPM; t != nil {
		rep.TPM = &agentproto.TPMStatus{Present: t.Present, Ready: t.Ready, Version: t.Version}
	}
	return rep, nil
}

// windowsAdmin turns an ADSI member (WinNT://DOMAIN/name or
// WinNT://WORKGROUP/PC/name, or the bare SID of a deleted account) into a
// LocalAdmin.
func windowsAdmin(path, sid, cls string) agentproto.LocalAdmin {
	parts := strings.Split(strings.TrimPrefix(path, "WinNT://"), "/")
	name := path
	if n := len(parts); n >= 2 {
		name = parts[n-2] + `\` + parts[n-1]
	} else if n == 1 {
		name = parts[0]
	}
	a := agentproto.LocalAdmin{Name: name, SID: strings.ToUpper(sid), Kind: strings.ToLower(cls)}
	if a.Kind != "user" && a.Kind != "group" {
		a.Kind = "unknown"
	}
	if strings.HasPrefix(name, "S-1-") {
		a.Kind = "unknown" // the account no longer resolves
	}
	if strings.HasSuffix(a.SID, "-500") || strings.HasSuffix(a.SID, "-512") {
		a.Builtin = true
	}
	return a
}

