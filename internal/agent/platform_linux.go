//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func defaultConfigPath() string { return "/etc/fleetwarden-agent/agent.json" }

func restrictDir(dir string) error { return os.Chmod(dir, 0o700) }

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func machineID() string {
	if id := readTrim("/etc/machine-id"); id != "" {
		return id
	}
	return readTrim("/var/lib/dbus/machine-id")
}

func bootTime() time.Time {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0).UTC()
			}
		}
	}
	return time.Time{}
}

// loggedInUser lists the users with an interactive session.
func loggedInUser() string {
	out, err := exec.Command("who").Output()
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	var users []string
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) > 0 && !seen[f[0]] {
			seen[f[0]] = true
			users = append(users, f[0])
		}
	}
	return strings.Join(users, ", ")
}

func osRelease() map[string]string {
	out := map[string]string{}
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			out[k] = strings.Trim(v, `"'`)
		}
	}
	return out
}

func collectOS(ctx context.Context) agentproto.OSInfo {
	rel := osRelease()
	info := agentproto.OSInfo{Name: rel["PRETTY_NAME"], Version: rel["VERSION_ID"], Build: readTrim("/proc/sys/kernel/osrelease"), Kind: "server"}
	if _, err := os.Stat("/usr/bin/pveversion"); err == nil {
		info.Kind = "hypervisor"
		if out, err := exec.CommandContext(ctx, "pveversion").Output(); err == nil {
			// "pve-manager/8.2.4/faa83925c9641325 (running kernel: 6.8.8-2-pve)"
			if parts := strings.Split(strings.TrimSpace(string(out)), "/"); len(parts) >= 2 {
				info.Name = "Proxmox VE " + parts[1] + " (" + rel["PRETTY_NAME"] + ")"
				info.Version = parts[1]
			}
		}
	} else if hasDesktop() {
		info.Kind = "workstation"
	}
	if out, err := exec.CommandContext(ctx, "realm", "list", "--name-only").Output(); err == nil {
		if d := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); d != "" {
			info.Domain, info.DomainJoined = d, true
		}
	}
	return info
}

func hasDesktop() bool {
	for _, d := range []string{"/usr/share/xsessions", "/usr/share/wayland-sessions"} {
		if entries, err := os.ReadDir(d); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

func collectHardware() agentproto.Hardware {
	dmi := func(n string) string { return readTrim("/sys/class/dmi/id/" + n) }
	hw := agentproto.Hardware{
		Manufacturer: dmi("sys_vendor"), Model: dmi("product_name"), Serial: dmi("product_serial"),
		BIOS:      strings.TrimSpace(dmi("bios_vendor") + " " + dmi("bios_version")),
		SystemSKU: dmi("product_sku"), Baseboard: dmi("board_name"), BIOSVersion: dmi("bios_version"),
	}
	cpuinfo, _ := os.ReadFile("/proc/cpuinfo")
	cores := map[string]bool{}
	var phys, core string
	for _, l := range strings.Split(string(cpuinfo), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "model name", "Model":
			if hw.CPU == "" {
				hw.CPU = v
			}
		case "processor":
			hw.CPUThreads++
		case "physical id":
			phys = v
		case "core id":
			core = v
			cores[phys+"/"+core] = true
		case "flags":
			if strings.Contains(" "+v+" ", " hypervisor ") {
				hw.Virtual = true
			}
		}
	}
	hw.CPUCores = len(cores)
	if hw.CPUCores == 0 {
		hw.CPUCores = hw.CPUThreads
	}
	if mem := readTrim("/proc/meminfo"); mem != "" {
		for _, l := range strings.Split(mem, "\n") {
			if v, ok := strings.CutPrefix(l, "MemTotal:"); ok {
				kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
				hw.MemoryBytes = kb * 1024
			}
		}
	}
	for _, v := range []string{"kvm", "qemu", "vmware", "virtualbox", "hyper-v", "xen", "bochs"} {
		if strings.Contains(strings.ToLower(hw.Manufacturer+" "+hw.Model), v) {
			hw.Virtual = true
		}
	}
	hw.Disks = linuxDisks()
	hw.Volumes = linuxVolumes()
	hw.Network = networkInterfaces()
	return hw
}

func linuxDisks() []agentproto.Disk {
	entries, _ := os.ReadDir("/sys/block")
	var out []agentproto.Disk
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "dm-") || strings.HasPrefix(n, "sr") ||
			strings.HasPrefix(n, "zram") || strings.HasPrefix(n, "zd") || strings.HasPrefix(n, "md") || strings.HasPrefix(n, "nbd") {
			continue
		}
		base := filepath.Join("/sys/block", n)
		sectors, _ := strconv.ParseInt(readTrim(base+"/size"), 10, 64)
		if sectors == 0 {
			continue
		}
		d := agentproto.Disk{Name: n, Model: readTrim(base + "/device/model"), SizeBytes: sectors * 512, Media: "hdd"}
		switch {
		case strings.HasPrefix(n, "nvme"):
			d.Media = "nvme"
		case readTrim(base+"/queue/rotational") == "0":
			d.Media = "ssd"
		}
		if strings.HasPrefix(n, "vd") || strings.HasPrefix(n, "xvd") {
			d.Media = "virtual"
		}
		out = append(out, d)
	}
	return out
}

var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "vfat": true,
	"exfat": true, "ntfs": true, "ntfs3": true, "f2fs": true, "jfs": true, "reiserfs": true}

// fixedVolumes is linuxVolumes; it is cheap enough for every heartbeat.
func fixedVolumes() []agentproto.Volume { return linuxVolumes() }

func linuxVolumes() []agentproto.Volume {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []agentproto.Volume
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !realFS[fields[2]] || seen[fields[0]] {
			continue
		}
		mount := strings.ReplaceAll(fields[1], `\040`, " ")
		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			continue
		}
		seen[fields[0]] = true
		out = append(out, agentproto.Volume{Mount: mount, FileSystem: fields[2],
			SizeBytes: int64(st.Blocks) * int64(st.Bsize), FreeBytes: int64(st.Bavail) * int64(st.Bsize)})
	}
	return out
}

func collectSoftware(ctx context.Context) ([]agentproto.Software, error) {
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		return nil, errors.New("dpkg-query not found; software inventory skipped")
	}
	out, err := exec.CommandContext(ctx, "dpkg-query", "-W", "-f", "${db:Status-Abbrev}\t${Package}\t${Version}\t${Maintainer}\t${Installed-Size}\t${source:Package}\t${source:Version}\n").Output()
	if err != nil {
		return nil, err
	}
	var sw []agentproto.Software
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 7 || !strings.HasPrefix(f[0], "ii") {
			continue
		}
		pub := f[3]
		if i := strings.Index(pub, "<"); i > 0 {
			pub = strings.TrimSpace(pub[:i])
		}
		kb, _ := strconv.ParseInt(f[4], 10, 64)
		s := agentproto.Software{Name: f[1], Version: f[2], Publisher: pub, Source: "dpkg", SizeBytes: kb * 1024}
		// The source package, for the Debian security tracker (phase 13).
		if f[5] != f[1] || f[6] != f[2] {
			s.SourcePackage, s.SourceVersion = f[5], f[6]
		}
		sw = append(sw, s)
	}
	return sortSoftware(sw), nil
}

func collectInventory(ctx context.Context) (agentproto.Inventory, error) {
	inv := agentproto.Inventory{OS: collectOS(ctx), Hardware: collectHardware()}
	inv.Hostname, _ = os.Hostname()
	sw, err := collectSoftware(ctx)
	inv.Software = sw
	return inv, err
}
