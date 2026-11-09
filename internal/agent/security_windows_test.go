//go:build windows

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// TestSecurityStatusWindows runs the real security script (CI runs it on
// GitHub's Windows runners, as administrator).
func TestSecurityStatusWindows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rep, err := collectSecurity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	normalizeSecurity(&rep)
	t.Logf("errors: %v", rep.Errors)
	t.Logf("volumes: %+v", rep.Volumes)
	if rep.Defender != nil {
		d := *rep.Defender
		d.Threats = nil
		t.Logf("defender: %+v (%d threats)", d, len(rep.Defender.Threats))
	}
	t.Logf("antivirus: %+v", rep.Antivirus)
	t.Logf("firewall: %+v", rep.Firewall)
	t.Logf("admins: %+v", rep.Admins)
	t.Logf("secure boot: %v, tpm: %+v", rep.SecureBoot, rep.TPM)
	sys := false
	for _, v := range rep.Volumes {
		sys = sys || v.System
		for _, k := range v.Keys {
			if !strings.HasPrefix(k.ID, "{") || len(k.Password) != 55 {
				t.Errorf("recovery key of %s: %q", v.Mount, k.ID)
			}
		}
	}
	if !sys {
		t.Errorf("no system volume in %+v", rep.Volumes)
	}
	if len(rep.Firewall) != 3 {
		t.Errorf("firewall profiles: %+v", rep.Firewall)
	}
	builtin := false
	for _, a := range rep.Admins {
		builtin = builtin || (a.Builtin && strings.HasSuffix(a.SID, "-500"))
	}
	if len(rep.Admins) == 0 || !builtin {
		t.Errorf("administrators: %+v", rep.Admins)
	}
	for _, e := range rep.Errors {
		if strings.HasPrefix(e, "Firewall:") || strings.HasPrefix(e, "Administrators:") {
			t.Errorf("part failed: %s", e)
		}
	}
}

// TestSecurityActionScripts checks that the action scripts parse (the
// Defender ones only run when Defender is active on the runner).
func TestSecurityActionScripts(t *testing.T) {
	for _, a := range []string{agentproto.SecurityQuickScan, agentproto.SecurityFullScan, agentproto.SecuritySignatures, agentproto.SecurityRotateKey} {
		script := securityActionScript(agentproto.SecurityAction{Action: a, Mount: "C:"})
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		check := `$e=$null; $null=[System.Management.Automation.Language.Parser]::ParseInput(@'` + "\n" + script + "\n" + `'@, [ref]$null, [ref]$e); if($e){ $e | ForEach-Object { $_.Message }; exit 1 }`
		out, err := runPowerShell(ctx, check)
		cancel()
		if err != nil {
			t.Errorf("%s does not parse: %v %s", a, err, out)
		}
	}
	if _, err := runSecurityAction(context.Background(), agentproto.SecurityAction{Action: "nope"}); err == nil {
		t.Error("unknown action ran")
	}
}
