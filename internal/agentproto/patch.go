package agentproto

import "time"

// Patch management messages.
//
// The agent scans for missing updates (Windows Update, apt) when the server
// asks and reports them. The server decides, from the device group's patch
// policy, which updates are approved and when the maintenance window is
// open, and then sends one install job. The agent asks the logged-on user
// first when the policy wants that (the tray pop-up on Windows), installs,
// and restarts the device according to the reboot policy.
const (
	// server -> agent
	TypePatchConfig  = "patch.config"  // settings the agent applies itself
	TypePatchScan    = "patch.scan"    // scan for missing updates now
	TypePatchInstall = "patch.install" // install updates (one job at a time)
	// agent -> server
	TypePatchScanResult = "patch.scan.result"
	TypePatchStatus     = "patch.status"
)

// Update categories. Windows classifications map onto these; on Linux an
// update is "security" when it comes from a security suite, else "other".
const (
	CategorySecurity   = "security"
	CategoryCritical   = "critical"
	CategoryCumulative = "cumulative"
	CategoryDefinition = "definition"
	CategoryDriver     = "driver"
	CategoryFeature    = "feature"
	CategoryOther      = "other"
)

// Categories lists the update categories in display order.
var Categories = []string{CategorySecurity, CategoryCritical, CategoryCumulative, CategoryDefinition, CategoryDriver, CategoryFeature, CategoryOther}

// ValidCategory reports whether c is a known category.
func ValidCategory(c string) bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// Reboot policies.
const (
	RebootNever     = "never"     // report that a restart is needed, never restart
	RebootAutomatic = "automatic" // restart right after installing (with a short notice to the user)
	RebootPrompt    = "prompt"    // ask the user; restart after the last postpone
)

// ValidReboot reports whether r is a known reboot policy.
func ValidReboot(r string) bool { return r == RebootNever || r == RebootAutomatic || r == RebootPrompt }

// PatchConfig carries the policy settings the agent applies on its own.
type PatchConfig struct {
	// Managed is true when a patch policy applies to the device.
	Managed bool `json:"managed"`
	// TakeOverWindowsUpdate turns off Windows' own automatic installs, so
	// updates only arrive through FleetWarden.
	TakeOverWindowsUpdate bool `json:"takeOverWindowsUpdate"`
	// ManageDrivers stops Windows from fetching drivers by itself (also when
	// a new device is plugged in): drivers come from the repository only.
	ManageDrivers bool `json:"manageDrivers,omitempty"`
	// Branding for the user pop-up (white label).
	ProductName string `json:"productName"`
	AccentColor string `json:"accentColor"`
}

// PatchUpdate is one missing update.
type PatchUpdate struct {
	// ID is the Windows Update GUID or, on Linux, the package name.
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	KB             string    `json:"kb,omitempty"`
	Category       string    `json:"category"`
	Severity       string    `json:"severity,omitempty"` // Critical, Important, Moderate, Low
	SizeBytes      int64     `json:"sizeBytes,omitempty"`
	ReleasedAt     time.Time `json:"releasedAt,omitzero"`
	CurrentVersion string    `json:"currentVersion,omitempty"`
	NewVersion     string    `json:"newVersion,omitempty"`
	// RebootRequired is true when the update is known to need a restart.
	RebootRequired bool `json:"rebootRequired,omitempty"`
}

// PatchScanResult answers a scan.
type PatchScanResult struct {
	ScannedAt     time.Time     `json:"scannedAt"`
	Updates       []PatchUpdate `json:"updates"`
	RebootPending bool          `json:"rebootPending"`
	// Source names the update source: windows-update, wsus, apt.
	Source string `json:"source"`
	// Cluster is the Proxmox VE cluster the node belongs to; the server
	// patches one node of a cluster at a time.
	Cluster string `json:"cluster,omitempty"`
	Error   string `json:"error,omitempty"`
	// Drivers lists the driver and firmware updates on offer; DriversScanned
	// is false for agents that do not look for them (older versions), so
	// the server keeps what it knew. DriverError is a failed driver scan.
	Drivers        []DriverUpdate `json:"drivers,omitempty"`
	DriversScanned bool           `json:"driversScanned,omitempty"`
	DriverError    string         `json:"driverError,omitempty"`
}

// PatchInstall asks the agent to install updates.
type PatchInstall struct {
	RunID int64 `json:"runId"`
	// Updates lists the IDs to install. Empty means: only restart when a
	// restart is pending.
	Updates []string `json:"updates"`
	Reboot  string   `json:"reboot"`
	// Notify asks the logged-on user before installing (Windows pop-up).
	Notify           bool `json:"notify"`
	MaxPostpones     int  `json:"maxPostpones"`
	PostponeMinutes  int  `json:"postponeMinutes"`
	CountdownSeconds int  `json:"countdownSeconds"`
	// Drivers are repository driver and firmware updates, installed after
	// the Windows or apt updates.
	Drivers []DriverInstall `json:"drivers,omitempty"`
}

// Patch job states reported in PatchStatus.
const (
	PatchSent        = "sent" // server side only: sent, no answer yet
	PatchWaitingUser = "waiting-user"
	PatchPostponed   = "postponed"
	PatchInstalling  = "installing"
	PatchRebooting   = "rebooting"
	PatchCompleted   = "completed"
	PatchFailed      = "failed"
)

// PatchFinished reports whether a job state is final.
func PatchFinished(state string) bool { return state == PatchCompleted || state == PatchFailed }

// PatchResult is the outcome of one update.
type PatchResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// PatchStatus reports the progress of a PatchInstall job.
type PatchStatus struct {
	RunID          int64         `json:"runId"`
	State          string        `json:"state"`
	Message        string        `json:"message,omitempty"`
	Results        []PatchResult `json:"results,omitempty"`
	RebootRequired bool          `json:"rebootRequired"`
	Postpones      int           `json:"postpones"`
	PostponedUntil time.Time     `json:"postponedUntil,omitzero"`
}
