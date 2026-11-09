package agentproto

import (
	"strconv"
	"strings"
	"time"
)

// Driver and firmware updates.
//
// The agent reports which driver and firmware updates its platform offers
// (Windows Update drivers and firmware, fwupd/LVFS on Linux) together with
// a patch scan, but never downloads them itself. An administrator adds an
// update to FleetWarden's own repository; only then does the server fetch
// the files from the vendor, check them and keep them under their SHA-256.
// Agents install repository files only, fetched from the server with a
// signed request (PathRepository + SHA-256), as part of a patch job.
//
// The content-addressed path is deliberate: a later caching node (a
// separate repository server) can serve the same paths.

// PathRepository serves repository files to agents: + the file's SHA-256.
const PathRepository = "/agent/v1/repository/"

// Driver update sources.
const (
	DriverSourceWindowsUpdate = "windows-update"
	DriverSourceFwupd         = "fwupd"
	// Vendor BIOS catalogs, matched by the server on the device's model.
	DriverSourceDell   = "dell"
	DriverSourceHP     = "hp"
	DriverSourceLenovo = "lenovo"
	// A package an administrator uploaded.
	DriverSourceUpload = "upload"
)

// How the agent installs a repository update (DriverInstall.Installer).
const (
	// InstallerINF unpacks the files (.cab, .zip) and adds the drivers
	// with pnputil. The default on Windows.
	InstallerINF = "inf"
	// InstallerFwupd installs a firmware cabinet with fwupdmgr. The default
	// on Linux.
	InstallerFwupd = "fwupd"
	// InstallerDell runs a Dell Update Package silently (/s).
	InstallerDell = "dell"
	// InstallerHP extracts an HP SoftPaq and runs its silent command.
	InstallerHP = "hp"
	// InstallerLenovo extracts a Lenovo package and runs its command.
	InstallerLenovo = "lenovo"
	// InstallerExe runs an uploaded program with the given arguments.
	InstallerExe = "exe"
)

// Driver update kinds.
const (
	DriverKindDriver   = "driver"
	DriverKindFirmware = "firmware" // BIOS/UEFI and device firmware
)

// DriverFile is one file of a driver update, where the vendor offers it.
type DriverFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size,omitempty"`
	// SHA256 and SHA1 are the vendor's checksums when it publishes them.
	SHA256 string `json:"sha256,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
}

// DriverUpdate is a driver or firmware update a device can install.
type DriverUpdate struct {
	Source string `json:"source"`
	// SourceID identifies the update at its source: the Windows Update
	// GUID, or the checksum of the fwupd release.
	SourceID string `json:"sourceId"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Vendor   string `json:"vendor,omitempty"`
	// Class is the device class (Display, Net, Firmware, System, ...).
	Class      string    `json:"class,omitempty"`
	Version    string    `json:"version,omitempty"`
	ReleasedAt time.Time `json:"releasedAt,omitzero"`
	SizeBytes  int64     `json:"sizeBytes,omitempty"`
	// HardwareID is the device it is meant for (PnP hardware ID, fwupd GUID).
	HardwareID string `json:"hardwareId,omitempty"`
	// Device and CurrentVersion describe the device on this machine.
	Device         string       `json:"device,omitempty"`
	DeviceID       string       `json:"deviceId,omitempty"` // fwupd device ID
	CurrentVersion string       `json:"currentVersion,omitempty"`
	Description    string       `json:"description,omitempty"`
	RebootRequired bool         `json:"rebootRequired,omitempty"`
	Files          []DriverFile `json:"files,omitempty"`
}

// RepoFile is a repository file the agent fetches from the server.
type RepoFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// DriverInstall is one driver or firmware update in a patch job.
type DriverInstall struct {
	// ID is how the result is reported ("driver:" + repository ID).
	ID       string     `json:"id"`
	Source   string     `json:"source"`
	SourceID string     `json:"sourceId"`
	Kind     string     `json:"kind"`
	Title    string     `json:"title"`
	DeviceID string     `json:"deviceId,omitempty"` // fwupd device to flash
	Files    []RepoFile `json:"files"`
	// Installer is how to install it (Installer* constants); empty means
	// the platform default (inf on Windows, fwupd on Linux).
	Installer string `json:"installer,omitempty"`
	// Command is the vendor's silent install command inside the extracted
	// package (HP, Lenovo), or the arguments for an uploaded program.
	Command string `json:"command,omitempty"`
	// ExitCodes are the exit codes that mean success (besides the usual
	// restart codes); empty means 0.
	ExitCodes []int `json:"exitCodes,omitempty"`
}

// DriverResultID is the result ID of a repository update in a patch job.
func DriverResultID(repoID int64) string { return "driver:" + strconv.FormatInt(repoID, 10) }

// SplitCommand splits a command line on spaces outside double quotes and
// removes the quotes: `"Hp Upd.exe" -s` -> [Hp Upd.exe, -s].
func SplitCommand(cmd string) []string {
	var out []string
	var cur strings.Builder
	quoted, has := false, false
	for _, r := range cmd {
		switch {
		case r == '"':
			quoted = !quoted
			has = true
		case (r == ' ' || r == '\t') && !quoted:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// JoinCommand quotes the arguments that need it and joins them.
func JoinCommand(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		q[i] = a
	}
	return strings.Join(q, " ")
}
