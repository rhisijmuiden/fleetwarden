//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func programData() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return p
	}
	return `C:\ProgramData`
}

func defaultConfigPath() string { return filepath.Join(programData(), "FleetWarden", "agent.json") }

// restrictDir limits the folder to SYSTEM and Administrators (ProgramData is
// readable by all users by default, and the folder holds the device key).
func restrictDir(dir string) error {
	cmd := exec.Command("icacls", dir, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func machineID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("MachineGuid")
	return v
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procTickCount64  = kernel32.NewProc("GetTickCount64")
	wtsapi32         = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySess = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFree      = wtsapi32.NewProc("WTSFreeMemory")
)

func bootTime() time.Time {
	ms, _, _ := procTickCount64.Call()
	return time.Now().Add(-time.Duration(ms) * time.Millisecond).UTC().Truncate(time.Minute)
}

func wtsString(session uint32, class uint32) string {
	var buf *uint16
	var n uint32
	r, _, _ := procWTSQuerySess.Call(0, uintptr(session), uintptr(class), uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return ""
	}
	defer procWTSFree.Call(uintptr(unsafe.Pointer(buf))) //nolint:errcheck
	return windows.UTF16PtrToString(buf)
}

// loggedInUser returns the users of active sessions (console and RDP).
func loggedInUser() string {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	list := unsafe.Slice(sessions, count)
	seen := map[string]bool{}
	var users []string
	for _, s := range list {
		if s.State != windows.WTSActive {
			continue
		}
		user := wtsString(s.SessionID, 5) // WTSUserName
		if user == "" {
			continue
		}
		if dom := wtsString(s.SessionID, 7); dom != "" { // WTSDomainName
			user = dom + `\` + user
		}
		if !seen[user] {
			seen[user] = true
			users = append(users, user)
		}
	}
	return strings.Join(users, ", ")
}

// inventoryScript asks CIM for the hardware in one PowerShell run.
const inventoryScript = `$ErrorActionPreference='SilentlyContinue'
$cs=Get-CimInstance Win32_ComputerSystem; $os=Get-CimInstance Win32_OperatingSystem; $bios=Get-CimInstance Win32_BIOS
$cpu=@(Get-CimInstance Win32_Processor); $pd=@{}; foreach($d in @(Get-PhysicalDisk)){ $pd[[string]$d.DeviceId]=[string]$d.MediaType }
[pscustomobject]@{
 manufacturer=$cs.Manufacturer; model=$cs.Model; domain=$cs.Domain; partOfDomain=[bool]$cs.PartOfDomain; memory=[int64]$cs.TotalPhysicalMemory
 hypervisor=[bool]$cs.HypervisorPresent; osName=$os.Caption; osBuild=$os.BuildNumber; productType=[int]$os.ProductType
 serial=$bios.SerialNumber; bios=($bios.Manufacturer+' '+$bios.SMBIOSBIOSVersion)
 cpu=$cpu[0].Name; cores=($cpu|Measure-Object NumberOfCores -Sum).Sum; threads=($cpu|Measure-Object NumberOfLogicalProcessors -Sum).Sum
 disks=@(Get-CimInstance Win32_DiskDrive|ForEach-Object{[pscustomobject]@{name=$_.DeviceID;model=$_.Model;size=[int64]$_.Size;media=$pd[[string]$_.Index];iface=$_.InterfaceType}})
 volumes=@(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3'|ForEach-Object{[pscustomobject]@{mount=$_.DeviceID;fs=$_.FileSystem;size=[int64]$_.Size;free=[int64]$_.FreeSpace}})
 gpus=@(Get-CimInstance Win32_VideoController|ForEach-Object{$_.Name})
}|ConvertTo-Json -Depth 4 -Compress`

type cimInventory struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Domain       string `json:"domain"`
	PartOfDomain bool   `json:"partOfDomain"`
	Memory       int64  `json:"memory"`
	Hypervisor   bool   `json:"hypervisor"`
	OSName       string `json:"osName"`
	OSBuild      string `json:"osBuild"`
	ProductType  int    `json:"productType"`
	Serial       string `json:"serial"`
	BIOS         string `json:"bios"`
	CPU          string `json:"cpu"`
	Cores        int    `json:"cores"`
	Threads      int    `json:"threads"`
	Disks        []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
		Size  int64  `json:"size"`
		Media string `json:"media"`
		Iface string `json:"iface"`
	} `json:"disks"`
	Volumes []struct {
		Mount string `json:"mount"`
		FS    string `json:"fs"`
		Size  int64  `json:"size"`
		Free  int64  `json:"free"`
	} `json:"volumes"`
	GPUs []string `json:"gpus"`
}

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Output()
}

func regString(k registry.Key, name string) string {
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func collectOS(cim cimInventory) agentproto.OSInfo {
	info := agentproto.OSInfo{Name: strings.TrimSpace(cim.OSName), Build: cim.OSBuild, Kind: "workstation"}
	if cim.ProductType == 2 || cim.ProductType == 3 {
		info.Kind = "server"
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		info.Version = regString(k, "DisplayVersion")
		if info.Version == "" {
			info.Version = regString(k, "ReleaseId")
		}
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil && info.Build != "" {
			info.Build += "." + strconv.FormatUint(ubr, 10)
		}
		if info.Name == "" {
			info.Name = regString(k, "ProductName")
		}
		k.Close()
	}
	info.DomainJoined = cim.PartOfDomain
	if cim.PartOfDomain {
		info.Domain = cim.Domain
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Group Policy\State\Machine`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		info.OU = regString(k, "Distinguished-Name")
		k.Close()
	}
	return info
}

func collectHardware(cim cimInventory) agentproto.Hardware {
	hw := agentproto.Hardware{Manufacturer: strings.TrimSpace(cim.Manufacturer), Model: strings.TrimSpace(cim.Model), Serial: strings.TrimSpace(cim.Serial),
		BIOS: strings.TrimSpace(cim.BIOS), CPU: strings.TrimSpace(cim.CPU), CPUCores: cim.Cores, CPUThreads: cim.Threads, MemoryBytes: cim.Memory,
		GPUs: cim.GPUs, Network: networkInterfaces()}
	for _, v := range []string{"virtual", "vmware", "kvm", "qemu", "xen", "parallels"} {
		if strings.Contains(strings.ToLower(hw.Manufacturer+" "+hw.Model), v) {
			hw.Virtual = true
		}
	}
	for _, d := range cim.Disks {
		media := map[string]string{"3": "hdd", "4": "ssd", "HDD": "hdd", "SSD": "ssd"}[d.Media]
		if strings.EqualFold(d.Iface, "NVMe") || strings.Contains(strings.ToUpper(d.Model), "NVME") {
			media = "nvme"
		}
		if hw.Virtual && media == "" {
			media = "virtual"
		}
		hw.Disks = append(hw.Disks, agentproto.Disk{Name: d.Name, Model: strings.TrimSpace(d.Model), SizeBytes: d.Size, Media: media})
	}
	for _, v := range cim.Volumes {
		hw.Volumes = append(hw.Volumes, agentproto.Volume{Mount: v.Mount, FileSystem: v.FS, SizeBytes: v.Size, FreeBytes: v.Free})
	}
	return hw
}

// collectSoftware reads the uninstall registry keys (64-bit, 32-bit and the
// per-user keys of loaded profiles), like "Apps & features".
func collectSoftware() []agentproto.Software {
	var sw []agentproto.Software
	const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	read := func(root registry.Key, path string, access uint32, source string) {
		k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS|access)
		if err != nil {
			return
		}
		defer k.Close()
		names, _ := k.ReadSubKeyNames(-1)
		for _, n := range names {
			sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE|access)
			if err != nil {
				continue
			}
			name := regString(sk, "DisplayName")
			sysComp, _, _ := sk.GetIntegerValue("SystemComponent")
			parent := regString(sk, "ParentKeyName")
			if name != "" && sysComp != 1 && parent == "" {
				s := agentproto.Software{Name: name, Version: regString(sk, "DisplayVersion"), Publisher: regString(sk, "Publisher"), Source: source}
				if d := regString(sk, "InstallDate"); len(d) == 8 {
					s.InstalledOn = d[0:4] + "-" + d[4:6] + "-" + d[6:8]
				}
				if size, _, err := sk.GetIntegerValue("EstimatedSize"); err == nil {
					s.SizeBytes = int64(size) * 1024
				}
				if wi, _, _ := sk.GetIntegerValue("WindowsInstaller"); wi == 1 {
					s.Source = "msi"
				}
				sw = append(sw, s)
			}
			sk.Close()
		}
	}
	read(registry.LOCAL_MACHINE, uninstall, registry.WOW64_64KEY, "registry")
	read(registry.LOCAL_MACHINE, uninstall, registry.WOW64_32KEY, "registry")
	if users, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS); err == nil {
		sids, _ := users.ReadSubKeyNames(-1)
		users.Close()
		for _, sid := range sids {
			if strings.HasPrefix(sid, "S-1-5-21-") && !strings.HasSuffix(sid, "_Classes") {
				read(registry.USERS, sid+`\`+uninstall, 0, "user")
			}
		}
	}
	return sortSoftware(sw)
}

func collectInventory(ctx context.Context) (agentproto.Inventory, error) {
	var cim cimInventory
	out, err := runPowerShell(ctx, inventoryScript)
	if err == nil {
		err = json.Unmarshal(out, &cim)
	}
	inv := agentproto.Inventory{OS: collectOS(cim), Hardware: collectHardware(cim), Software: collectSoftware()}
	inv.Hostname, _ = os.Hostname()
	if err != nil {
		return inv, fmt.Errorf("hardware inventory: %w", err)
	}
	return inv, nil
}
