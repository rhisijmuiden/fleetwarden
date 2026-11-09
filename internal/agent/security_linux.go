//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func collectSecurity(ctx context.Context) (agentproto.SecurityReport, error) {
	var rep agentproto.SecurityReport
	run := func(name string, args ...string) ([]byte, error) {
		if _, err := exec.LookPath(name); err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, name, args...).Output()
	}
	if out, err := run("lsblk", "-J", "-p", "-o", "NAME,TYPE,FSTYPE,MOUNTPOINT"); err == nil {
		vols, err := parseLsblkSecurity(out)
		if err != nil {
			rep.Errors = append(rep.Errors, "encryption: "+err.Error())
		}
		rep.Volumes = vols
	} else {
		rep.Errors = append(rep.Errors, "encryption: lsblk: "+err.Error())
	}
	rep.Firewall = linuxFirewall(run)
	rep.Antivirus = linuxAntivirus(run)
	group, _ := os.ReadFile("/etc/group")
	passwd, _ := os.ReadFile("/etc/passwd")
	rep.Admins = parseLinuxAdmins(string(group), string(passwd))
	sb := linuxSecureBoot("/sys/firmware/efi")
	rep.SecureBoot = &sb
	rep.TPM = linuxTPM("/sys/class/tpm")
	return rep, nil
}

func runSecurityAction(_ context.Context, act agentproto.SecurityAction) (string, error) {
	return "", errors.New(act.Action + " is not available on Linux")
}
