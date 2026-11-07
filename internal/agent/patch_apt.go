//go:build linux

package agent

// apt backend for Debian and Proxmox VE. Scans simulate a dist-upgrade, so
// the list matches what Proxmox wants (never a plain "apt upgrade"); an
// install upgrades exactly the approved packages and lets apt pull in new
// dependencies. FleetWarden's own packages are left to the agent and server
// updaters.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type aptBackend struct {
	// run executes a command; tests replace it.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// root prefixes the files read (/boot, /run, /etc/pve); "" is "/".
	root string
}

func newAptBackend() *aptBackend { return &aptBackend{run: runApt} }

var aptEnv = []string{
	"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "NEEDRESTART_SUSPEND=1", "APT_LISTCHANGES_FRONTEND=none",
	"LC_ALL=C", "LANG=C", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
}

func runApt(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = aptEnv
	cmd.WaitDelay = 30 * time.Second
	return cmd.CombinedOutput()
}

// selfPackages are updated by FleetWarden's own updaters, never by patching:
// upgrading the agent in the middle of a job would stop the job.
var selfPackages = map[string]bool{"fleetwarden-agent": true, "fleetwarden-server": true}

var aptLock = []string{"-o", "DPkg::Lock::Timeout=600"}

func (b *aptBackend) path(p string) string {
	if b.root == "" {
		return p
	}
	return filepath.Join(b.root, p)
}

func (b *aptBackend) Configure(agentproto.PatchConfig) error { return nil }

func (b *aptBackend) Scan(ctx context.Context) (agentproto.PatchScanResult, error) {
	res := agentproto.PatchScanResult{Source: "apt", Cluster: b.cluster()}
	var scanErr error
	if out, err := b.run(ctx, "apt-get", append([]string{"update", "-q"}, aptLock...)...); err != nil {
		// Old package lists still give a useful answer; report the error too.
		scanErr = fmt.Errorf("apt-get update: %w: %s", err, tail(out, 400))
	}
	ups, err := b.simulate(ctx)
	if err != nil {
		return res, err
	}
	res.Updates = ups
	res.RebootPending = b.RebootPending(ctx)
	res.ScannedAt = time.Now().UTC()
	return res, scanErr
}

func (b *aptBackend) simulate(ctx context.Context) ([]agentproto.PatchUpdate, error) {
	out, err := b.run(ctx, "apt-get", "-s", "-q", "-o", "Debug::NoLocking=1", "dist-upgrade")
	if err != nil {
		return nil, fmt.Errorf("apt-get -s dist-upgrade: %w: %s", err, tail(out, 400))
	}
	return parseAptSimulation(out), nil
}

// Inst libssl3t64 [3.5.1-1] (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64]) []
var aptInstRE = regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]*)\] )?\((\S+) (.*?) ?\[([^\]]+)\]\)`)

// parseAptSimulation lists the upgrades in "apt-get -s" output. New packages
// (dependencies without an installed version) are pulled in by the upgrade
// that needs them, so they are not listed on their own.
func parseAptSimulation(out []byte) []agentproto.PatchUpdate {
	ups := []agentproto.PatchUpdate{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := aptInstRE.FindStringSubmatch(sc.Text())
		if m == nil || m[2] == "" || selfPackages[m[1]] {
			continue
		}
		name, suites := m[1], strings.ToLower(m[4])
		cat := agentproto.CategoryOther
		if strings.Contains(suites, "security") {
			cat = agentproto.CategorySecurity
		}
		ups = append(ups, agentproto.PatchUpdate{
			ID: name, Title: name, Category: cat, CurrentVersion: m[2], NewVersion: m[3], RebootRequired: kernelPackage(name),
		})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].ID < ups[j].ID })
	return ups
}

func kernelPackage(name string) bool {
	for _, p := range []string{"linux-image-", "proxmox-kernel-", "pve-kernel-", "intel-microcode", "amd64-microcode", "proxmox-default-kernel"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (b *aptBackend) Install(ctx context.Context, ids []string, progress func(string)) ([]agentproto.PatchResult, bool, error) {
	pkgs := make([]string, 0, len(ids))
	for _, id := range ids {
		if !selfPackages[id] && !strings.HasPrefix(id, "-") {
			pkgs = append(pkgs, id)
		}
	}
	if len(pkgs) == 0 {
		return nil, b.RebootPending(ctx), nil
	}
	// "apt-get install" marks packages as installed by hand; keep the marks
	// of packages that were installed as dependencies.
	autoOut, _ := b.run(ctx, "apt-mark", "showauto")
	auto := map[string]bool{}
	for _, l := range strings.Fields(string(autoOut)) {
		auto[l] = true
	}
	progress(fmt.Sprintf("apt: upgrading %d package(s)", len(pkgs)))
	args := append([]string{"install", "-y", "-q", "--no-remove"}, aptLock...)
	args = append(args, "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold")
	out, installErr := b.run(ctx, "apt-get", append(args, pkgs...)...)
	var reauto []string
	for _, p := range pkgs {
		if auto[p] {
			reauto = append(reauto, p)
		}
	}
	if len(reauto) > 0 {
		_, _ = b.run(ctx, "apt-mark", append([]string{"auto"}, reauto...)...)
	}
	// What is still upgradable failed.
	left := map[string]bool{}
	after, simErr := b.simulate(ctx)
	for _, u := range after {
		left[u.ID] = true
	}
	results := make([]agentproto.PatchResult, 0, len(pkgs))
	for _, p := range pkgs {
		r := agentproto.PatchResult{ID: p, OK: true}
		if left[p] || (simErr != nil && installErr != nil) {
			r.OK, r.Error = false, "not upgraded"
			if installErr != nil {
				r.Error = "apt-get install failed"
			}
		}
		results = append(results, r)
	}
	if installErr == nil && simErr != nil {
		installErr = simErr
	}
	if installErr != nil {
		return results, b.RebootPending(ctx), fmt.Errorf("apt-get install: %w: %s", installErr, tail(out, 600))
	}
	return results, b.RebootPending(ctx), nil
}

// RebootPending reports whether the system asked for a restart, or a newer
// kernel than the running one is installed.
func (b *aptBackend) RebootPending(context.Context) bool {
	if _, err := os.Stat(b.path("/run/reboot-required")); err == nil {
		return true
	}
	running := strings.TrimSpace(readTrim(b.path("/proc/sys/kernel/osrelease")))
	if running == "" {
		return false
	}
	files, _ := filepath.Glob(b.path("/boot/vmlinuz-*"))
	newest := ""
	for _, f := range files {
		v := strings.TrimPrefix(filepath.Base(f), "vmlinuz-")
		if newest == "" || compareNatural(v, newest) > 0 {
			newest = v
		}
	}
	return newest != "" && newest != running && compareNatural(newest, running) > 0
}

// Reboot restarts in one minute, so the agent can report first and logged-on
// users see the broadcast.
func (b *aptBackend) Reboot(ctx context.Context, message string) error {
	out, err := b.run(ctx, "shutdown", "-r", "+1", message)
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(out, 200))
	}
	return nil
}

// cluster returns the Proxmox VE cluster name of this node.
func (b *aptBackend) cluster() string {
	raw, err := os.ReadFile(b.path("/etc/pve/corosync.conf"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if ok && strings.TrimSpace(k) == "cluster_name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// compareNatural compares version-like strings: digit runs as numbers,
// everything else as text.
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		da, ra := splitRun(a)
		db, rb := splitRun(b)
		if isDigit(da[0]) && isDigit(db[0]) {
			da, db = strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(da) != len(db) {
				return cmpInt(len(da), len(db))
			}
		}
		if c := strings.Compare(da, db); c != 0 {
			return c
		}
		a, b = ra, rb
	}
	return cmpInt(len(a), len(b))
}

func splitRun(s string) (string, string) {
	d := isDigit(s[0])
	i := 1
	for i < len(s) && isDigit(s[i]) == d {
		i++
	}
	return s[:i], s[i:]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
