package agentproto

import (
	"strconv"
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
}

// DriverResultID is the result ID of a repository update in a patch job.
func DriverResultID(repoID int64) string { return "driver:" + strconv.FormatInt(repoID, 10) }
