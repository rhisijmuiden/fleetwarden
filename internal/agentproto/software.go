package agentproto

import "time"

// Software deployment messages.
//
// The server sends one software job at a time per device: install a package
// or uninstall a program from the inventory. Installers from a public catalog
// (winget) are downloaded by the agent from the vendor's URL and checked
// against the catalog's SHA-256; uploaded installers come from the server
// with a signed request (PathSoftware + SHA-256). The agent reports its
// progress and sends a fresh inventory when the job is done.
const (
	// server -> agent
	TypeSoftwareJob = "software.job"
	// agent -> server
	TypeSoftwareStatus = "software.status"
)

// PathSoftware serves uploaded installers to agents: + the file's SHA-256.
const PathSoftware = "/agent/v1/software/"

// Software job actions.
const (
	ActionInstall   = "install"
	ActionUninstall = "uninstall"
	// ActionBrowsers updates web browsers: the agent asks the user, closes
	// the browsers, installs and opens them again with their tabs.
	ActionBrowsers = "browsers"
)

// Installer kinds. Windows: msi (also WiX), exe (needs its own silent
// arguments), inno, nullsoft, burn. Linux: apt (a package from the
// configured repositories) and deb (an uploaded package file).
const (
	KindMSI      = "msi"
	KindEXE      = "exe"
	KindInno     = "inno"
	KindNullsoft = "nullsoft"
	KindBurn     = "burn"
	KindApt      = "apt"
	KindDeb      = "deb"
)

// InstallerKinds lists the kinds per platform.
var InstallerKinds = map[string][]string{
	"windows": {KindMSI, KindEXE, KindInno, KindNullsoft, KindBurn},
	"linux":   {KindApt, KindDeb},
}

// ValidKind reports whether kind is an installer kind of the platform.
func ValidKind(platform, kind string) bool {
	for _, k := range InstallerKinds[platform] {
		if k == kind {
			return true
		}
	}
	return false
}

// DefaultSilentArgs are the unattended arguments per installer kind. For
// msi they are msiexec options (the agent adds /i and the file).
var DefaultSilentArgs = map[string]string{
	KindMSI:      "/qn /norestart",
	KindInno:     "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP-",
	KindNullsoft: "/S",
	KindBurn:     "/quiet /norestart",
}

// Installer says where the agent gets an installer and how it runs it.
type Installer struct {
	Kind string `json:"kind"`
	// URL is a public https download (catalog packages); File is the SHA-256
	// of an uploaded installer on the server. One of the two is set, except
	// for apt.
	URL  string `json:"url,omitempty"`
	File string `json:"file,omitempty"`
	// FileName keeps the extension the installer needs (setup.exe, x.msi).
	FileName string `json:"fileName,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size,omitempty"`
	// AnyHash accepts a file whose hash differs from SHA256: for download
	// links that always serve the newest version.
	AnyHash bool `json:"anyHash,omitempty"`
	// Args are the unattended arguments (DefaultSilentArgs when empty).
	Args string `json:"args,omitempty"`
	// Package is the apt package name.
	Package string `json:"package,omitempty"`
	// SuccessCodes and RebootCodes are exit codes besides 0 (and 3010/1641
	// for a needed restart) that mean the install worked.
	SuccessCodes []int `json:"successCodes,omitempty"`
	RebootCodes  []int `json:"rebootCodes,omitempty"`
}

// Detect identifies an installed program, to skip an install that is not
// needed and to report the installed version afterwards.
type Detect struct {
	// Name matches the start of the display name in "Apps & features" (or
	// the dpkg package name on Linux), ignoring case.
	Name string `json:"name"`
	// ProductCode is an MSI product code ({GUID}).
	ProductCode string `json:"productCode,omitempty"`
	// Version is the version the job installs; an equal or newer installed
	// version skips the install. Empty: install when the program is missing.
	Version string `json:"version,omitempty"`
	// Exact matches the whole display name instead of its start.
	Exact bool `json:"exact,omitempty"`
	// MachineOnly ignores installs for a single user.
	MachineOnly bool `json:"machineOnly,omitempty"`
}

// Uninstall names a program from the inventory.
type Uninstall struct {
	Name    string `json:"name"`              // exact display name or package name
	Version string `json:"version,omitempty"` // only this version; empty: any
	// Args replaces the agent's choice of unattended uninstall arguments.
	Args string `json:"args,omitempty"`
	// Purge also removes configuration files (apt purge).
	Purge bool `json:"purge,omitempty"`
}

// SoftwareJob asks the agent to install or uninstall.
type SoftwareJob struct {
	JobID     int64      `json:"jobId"`
	Action    string     `json:"action"`
	Name      string     `json:"name"` // for logs and the user
	Installer *Installer `json:"installer,omitempty"`
	Detect    *Detect    `json:"detect,omitempty"`
	Uninstall *Uninstall `json:"uninstall,omitempty"`
	// TimeoutMinutes bounds the installer or uninstaller run.
	TimeoutMinutes int `json:"timeoutMinutes"`
	// Browsers and Prompt are set for ActionBrowsers.
	Browsers []BrowserUpdate `json:"browsers,omitempty"`
	Prompt   *UserPrompt     `json:"prompt,omitempty"`
}

// BrowserUpdate is one browser to bring to Version.
type BrowserUpdate struct {
	Key       string    `json:"key"` // chrome, edge, firefox
	Name      string    `json:"name"`
	Version   string    `json:"version"` // the version the installer brings
	Installer Installer `json:"installer"`
	Detect    Detect    `json:"detect"`
	// Process is the executable that runs while the browser is open
	// (chrome.exe); RestoreArgs reopen the last session when it starts.
	Process     string `json:"process"`
	RestoreArgs string `json:"restoreArgs,omitempty"`
	// Firefox restores its session through a preference instead.
	Firefox bool `json:"firefox,omitempty"`
}

// UserPrompt is how the agent asks the logged-on user, with the postpone
// settings of the device's patch policy.
type UserPrompt struct {
	MaxPostpones     int `json:"maxPostpones"`
	PostponeMinutes  int `json:"postponeMinutes"`
	CountdownSeconds int `json:"countdownSeconds"`
}

// Software job states.
const (
	SoftwarePending      = "pending" // server side: waiting for the device
	SoftwareSent         = "sent"    // server side: sent, no answer yet
	SoftwareDownloading  = "downloading"
	SoftwareInstalling   = "installing"
	SoftwareUninstalling = "uninstalling"
	SoftwareWaiting      = "waiting" // asked the user, or postponed by the user
	SoftwareCompleted    = "completed"
	SoftwareSkipped      = "skipped" // nothing to do: already installed, or not installed
	SoftwareFailed       = "failed"
	SoftwareCancelled    = "cancelled" // server side
	SoftwareExpired      = "expired"   // server side: the device did not come online in time
)

// SoftwareFinished reports whether a job state is final.
func SoftwareFinished(state string) bool {
	switch state {
	case SoftwareCompleted, SoftwareSkipped, SoftwareFailed, SoftwareCancelled, SoftwareExpired:
		return true
	}
	return false
}

// SoftwareStatus reports the progress of a SoftwareJob.
type SoftwareStatus struct {
	JobID   int64  `json:"jobId"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	// ExitCode of the installer or uninstaller, when it ran.
	ExitCode *int `json:"exitCode,omitempty"`
	// Output is the end of the installer's output or log.
	Output         string    `json:"output,omitempty"`
	RebootRequired bool      `json:"rebootRequired,omitempty"`
	Version        string    `json:"version,omitempty"` // installed version afterwards
	At             time.Time `json:"at,omitzero"`
}
