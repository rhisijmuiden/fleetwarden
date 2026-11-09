//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aptSim = `NOTE: This is only a simulation!
Reading package lists...
Building dependency tree...
The following packages will be upgraded:
  base-files libssl3t64 linux-image-amd64 pve-manager fleetwarden-agent
Inst base-files [13.8] (13.8+deb13u1 Debian:13.1/stable [amd64])
Inst libssl3t64 [3.5.1-1] (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64]) []
Inst linux-image-6.12.48+deb13-amd64 (6.12.48-1 Debian-Security:13/stable-security [amd64])
Inst linux-image-amd64 [6.12.43-1] (6.12.48-1 Debian:13.1/stable, Debian-Security:13/stable-security [amd64])
Inst pve-manager [9.0.10] (9.0.11 Proxmox:9.0/trixie [all])
Inst fleetwarden-agent [0.1.0] (0.2.0 FleetWarden:stable [amd64])
Conf base-files (13.8+deb13u1 Debian:13.1/stable [amd64])
`

func TestParseAptSimulation(t *testing.T) {
	ups := parseAptSimulation([]byte(aptSim))
	if len(ups) != 4 {
		t.Fatalf("updates: %+v", ups)
	}
	byID := map[string]int{}
	for i, u := range ups {
		byID[u.ID] = i
	}
	if _, ok := byID["fleetwarden-agent"]; ok {
		t.Error("the agent's own package is listed")
	}
	if _, ok := byID["linux-image-6.12.48+deb13-amd64"]; ok {
		t.Error("a new dependency is listed as an update")
	}
	ssl := ups[byID["libssl3t64"]]
	if ssl.Category != "security" || ssl.CurrentVersion != "3.5.1-1" || ssl.NewVersion != "3.5.1-1+deb13u1" {
		t.Errorf("libssl: %+v", ssl)
	}
	if k := ups[byID["linux-image-amd64"]]; k.Category != "security" || !k.RebootRequired {
		t.Errorf("kernel: %+v", k)
	}
	if b := ups[byID["base-files"]]; b.Category != "other" || b.RebootRequired {
		t.Errorf("base-files: %+v", b)
	}
	if p := ups[byID["pve-manager"]]; p.NewVersion != "9.0.11" {
		t.Errorf("pve-manager: %+v", p)
	}
}

func TestCompareNatural(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"6.12.48+deb13-amd64", "6.12.9+deb13-amd64", 1},
		{"6.8.12-4-pve", "6.8.12-10-pve", -1},
		{"6.1.0-28-amd64", "6.1.0-28-amd64", 0},
		{"6.10", "6.9.1", 1},
	}
	for _, c := range cases {
		if got := compareNatural(c.a, c.b); got != c.want {
			t.Errorf("compareNatural(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func fakeRoot(t *testing.T, running string, kernels ...string) string {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "proc/sys/kernel"), 0o755)
	os.MkdirAll(filepath.Join(root, "boot"), 0o755)
	os.MkdirAll(filepath.Join(root, "run"), 0o755)
	os.WriteFile(filepath.Join(root, "proc/sys/kernel/osrelease"), []byte(running+"\n"), 0o644)
	for _, k := range kernels {
		os.WriteFile(filepath.Join(root, "boot", "vmlinuz-"+k), nil, 0o644)
	}
	return root
}

func TestAptRebootPendingAndCluster(t *testing.T) {
	b := &aptBackend{root: fakeRoot(t, "6.12.43+deb13-amd64", "6.12.43+deb13-amd64")}
	if b.RebootPending(context.Background()) {
		t.Error("reboot pending with only the running kernel")
	}
	b.root = fakeRoot(t, "6.12.43+deb13-amd64", "6.12.43+deb13-amd64", "6.12.48+deb13-amd64")
	if !b.RebootPending(context.Background()) {
		t.Error("newer kernel installed but no reboot pending")
	}
	b.root = fakeRoot(t, "6.12.48+deb13-amd64", "6.12.43+deb13-amd64", "6.12.48+deb13-amd64")
	os.WriteFile(filepath.Join(b.root, "run/reboot-required"), nil, 0o644)
	if !b.RebootPending(context.Background()) {
		t.Error("/run/reboot-required ignored")
	}
	os.MkdirAll(filepath.Join(b.root, "etc/pve"), 0o755)
	os.WriteFile(filepath.Join(b.root, "etc/pve/corosync.conf"), []byte("totem {\n  cluster_name: lab-cluster\n  version: 2\n}\n"), 0o644)
	if c := b.cluster(); c != "lab-cluster" {
		t.Errorf("cluster: %q", c)
	}
}

func TestAptInstall(t *testing.T) {
	var calls []string
	installed := false
	b := &aptBackend{root: fakeRoot(t, "6.12.43+deb13-amd64", "6.12.43+deb13-amd64"), run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "apt-mark" && args[0] == "showauto":
			return []byte("libssl3t64\nzlib1g\n"), nil
		case name == "apt-get" && args[0] == "install":
			installed = true
			return []byte("ok"), nil
		case name == "apt-get" && args[0] == "-s":
			if installed { // base-files was held back
				return []byte("Inst base-files [13.8] (13.8+deb13u1 Debian:13.1/stable [amd64])\n"), nil
			}
			return []byte(aptSim), nil
		}
		return nil, nil
	}}
	res, reboot, err := b.Install(context.Background(), []string{"libssl3t64", "base-files", "fleetwarden-agent"}, func(string) {})
	if err != nil || reboot {
		t.Fatalf("install: %v %v", err, reboot)
	}
	if len(res) != 2 || !res[0].OK || res[1].OK {
		t.Errorf("results: %+v", res)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "apt-get install -y -q --no-remove") || strings.Contains(joined, "install -y -q --no-remove -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold libssl3t64 base-files fleetwarden-agent") {
		t.Errorf("install command: %s", joined)
	}
	if !strings.Contains(joined, "apt-mark auto libssl3t64") {
		t.Errorf("automatic marks not restored: %s", joined)
	}

	b.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "apt-get" && args[0] == "install" {
			return []byte("E: Could not get lock"), errors.New("exit status 100")
		}
		if name == "apt-get" && args[0] == "-s" {
			return []byte(aptSim), nil
		}
		return nil, nil
	}
	res, _, err = b.Install(context.Background(), []string{"libssl3t64"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Could not get lock") || res[0].OK {
		t.Errorf("failed install: %v %+v", err, res)
	}
}
