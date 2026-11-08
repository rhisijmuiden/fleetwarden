package agentproto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Configuration management.
//
// An administrator makes configurations (browser policies, registry values,
// scripts, ...) and assigns them to device groups. The server sends every
// device the full list of configurations that apply to it (ConfigPolicy)
// whenever that list changes. The agent keeps the list on disk, applies it,
// and applies it again every ReapplyMinutes: a setting that someone changed
// is put back. Settings the agent made for a configuration that no longer
// applies are removed. After every run the agent reports the state of each
// configuration (ConfigStatus).
const (
	// server -> agent
	TypeConfigPolicy = "config.policy" // the configurations that apply now
	TypeConfigApply  = "config.apply"  // apply everything now (the "Apply now" button)
	// agent -> server
	TypeConfigStatus = "config.status"
)

// Configuration types.
const (
	ConfigBrowser  = "browser"  // browser policies for Chrome, Edge and Firefox
	ConfigRegistry = "registry" // Windows registry values (HKLM, or HKCU of every user)
	ConfigScript   = "script"   // a PowerShell, cmd, bash or sh script
	ConfigDrives   = "drives"   // network drives of the users (Windows)
	ConfigPrinters = "printers" // printers, with their drivers (Windows)
	ConfigDesktop  = "desktop"  // wallpaper and screen lock (Windows)
	ConfigPower    = "power"    // screen and sleep timeouts (Windows)
	ConfigAdmins   = "admins"   // members of the local Administrators group (Windows)
	ConfigFirewall = "firewall" // Windows Firewall rules
)

// ConfigTypes lists the types per platform.
var ConfigTypes = map[string][]string{
	"windows": {ConfigBrowser, ConfigRegistry, ConfigScript, ConfigDrives, ConfigPrinters,
		ConfigDesktop, ConfigPower, ConfigAdmins, ConfigFirewall},
	"linux": {ConfigBrowser, ConfigScript},
}

// ValidConfigType reports whether a configuration type exists on a platform.
func ValidConfigType(platform, typ string) bool {
	for _, t := range ConfigTypes[platform] {
		if t == typ {
			return true
		}
	}
	return false
}

// DefaultReapplyMinutes is how often the agent checks and repairs the
// settings when the server does not say otherwise.
const DefaultReapplyMinutes = 30

// ConfigPolicy is the complete set of configurations for one device, in the
// order they apply: when two configurations set the same thing, the later
// one wins.
type ConfigPolicy struct {
	Items          []ConfigItem `json:"items"`
	ReapplyMinutes int          `json:"reapplyMinutes"`
}

// Hash identifies the content of a policy, so the agent and server can tell
// which version a report is about.
func (p ConfigPolicy) Hash() string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// ConfigItem is one configuration. Exactly one of the settings fields is
// set, matching Type.
type ConfigItem struct {
	ID       int64  `json:"id"`
	Revision int    `json:"revision"` // goes up with every change
	Name     string `json:"name"`
	Type     string `json:"type"`

	Browser  *BrowserPolicy    `json:"browser,omitempty"`
	Registry *RegistrySettings `json:"registry,omitempty"`
	Script   *ScriptConfig     `json:"script,omitempty"`
	Drives   *DriveSettings    `json:"drives,omitempty"`
	Printers *PrinterSettings  `json:"printers,omitempty"`
	Desktop  *DesktopSettings  `json:"desktop,omitempty"`
	Power    *PowerSettings    `json:"power,omitempty"`
	Admins   *AdminSettings    `json:"admins,omitempty"`
	Firewall *FirewallSettings `json:"firewall,omitempty"`
}

// Browser keys.
const (
	BrowserChrome  = "chrome"
	BrowserEdge    = "edge"
	BrowserFirefox = "firefox"
)

// Startup modes of a browser policy.
const (
	StartupDefault = ""        // leave it to the user
	StartupNewTab  = "newtab"  // open the new tab page
	StartupRestore = "restore" // continue where the user left off
	StartupURLs    = "urls"    // open StartupURLs
)

// BrowserPolicy sets browser policies. Empty fields are not managed.
type BrowserPolicy struct {
	Browsers []string `json:"browsers"` // chrome, edge, firefox
	// Homepage is the page of the home button.
	Homepage       string   `json:"homepage,omitempty"`
	ShowHomeButton *bool    `json:"showHomeButton,omitempty"`
	StartupMode    string   `json:"startupMode,omitempty"`
	StartupURLs    []string `json:"startupUrls,omitempty"`
	// PasswordManager turns the built-in password manager on or off.
	PasswordManager *bool `json:"passwordManager,omitempty"`
	// Extensions are installed and cannot be removed. Chrome and Edge take
	// extension IDs from their web stores (optionally "id;update URL");
	// Firefox takes install URLs of .xpi files.
	ChromeExtensions  []string `json:"chromeExtensions,omitempty"`
	EdgeExtensions    []string `json:"edgeExtensions,omitempty"`
	FirefoxExtensions []string `json:"firefoxExtensions,omitempty"`
	// Extra holds further policies per browser by their documented names,
	// for example {"chrome": {"BookmarkBarEnabled": true}}.
	Extra map[string]map[string]any `json:"extra,omitempty"`
}

// Registry hives.
const (
	HiveMachine = "HKLM"
	HiveUser    = "HKCU" // every user who logs on
)

// Registry value types. RegDelete removes the value.
const (
	RegString       = "string"
	RegExpandString = "expandstring"
	RegMultiString  = "multistring" // Value holds one string per line
	RegDWord        = "dword"
	RegQWord        = "qword"
	RegDelete       = "delete"
)

// RegistryValue is one value to set or remove.
type RegistryValue struct {
	Hive  string `json:"hive"`
	Key   string `json:"key"`  // without the hive, e.g. SOFTWARE\Contoso\App
	Name  string `json:"name"` // empty: the default value
	Type  string `json:"type"`
	Value string `json:"value,omitempty"` // numbers in decimal
}

// RegistrySettings sets registry values. Values set by a configuration that
// no longer applies are removed again, unless Keep is set.
type RegistrySettings struct {
	Values []RegistryValue `json:"values"`
	Keep   bool            `json:"keep,omitempty"`
}

// Script shells.
const (
	ShellPowerShell = "powershell"
	ShellCmd        = "cmd"
	ShellBash       = "bash"
	ShellSh         = "sh"
)

// ScriptShells lists the shells per platform.
var ScriptShells = map[string][]string{
	"windows": {ShellPowerShell, ShellCmd},
	"linux":   {ShellBash, ShellSh},
}

// When a script runs.
const (
	TriggerOnce     = "once"     // once per device, again after a change
	TriggerStartup  = "startup"  // after every start of the device
	TriggerLogon    = "logon"    // when a user logs on (Windows)
	TriggerInterval = "interval" // every IntervalMinutes
)

// Who a script runs as.
const (
	RunAsSystem = "system" // SYSTEM or root
	RunAsUser   = "user"   // the logged-on user, in their session (Windows)
)

// ScriptConfig runs a script.
type ScriptConfig struct {
	Shell           string `json:"shell"`
	Content         string `json:"content"`
	RunAs           string `json:"runAs"`
	Trigger         string `json:"trigger"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
	TimeoutMinutes  int    `json:"timeoutMinutes"`
}

// DriveMapping maps a network drive for every user who logs on. Path may
// hold %USERNAME% and %USERDOMAIN%, which the agent fills in per user.
type DriveMapping struct {
	Letter string `json:"letter"` // H
	Path   string `json:"path"`   // \\server\home\%USERNAME%
	Label  string `json:"label,omitempty"`
}

// DriveSettings maps network drives. The user's own Windows sign-in is
// used to connect; on a PC without a domain Windows asks for a password the
// first time, unless the user has the same account on the server.
type DriveSettings struct {
	Drives []DriveMapping `json:"drives"`
}

// Printer kinds.
const (
	PrinterShared = "shared" // a printer shared by a print server: \\server\name
	PrinterIP     = "ip"     // a network printer by its address (TCP/IP)
)

// Who gets a shared printer.
const (
	PrinterDevice = "device" // every user of the device
	PrinterUser   = "user"   // added in the profile of each user who logs on
)

// TCP/IP printer protocols.
const (
	PrinterRaw = "raw" // RAW on PortNumber (9100)
	PrinterLPR = "lpr" // LPR with Queue
)

// Printer adds one printer.
type Printer struct {
	Kind string `json:"kind"`
	// Path of a shared printer, and who gets it.
	Path  string `json:"path,omitempty"`
	Scope string `json:"scope,omitempty"`
	// A TCP/IP printer: its name on the device, address and driver.
	Name     string `json:"name,omitempty"`
	Address  string `json:"address,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
	Queue    string `json:"queue,omitempty"`
	Driver   string `json:"driver,omitempty"`
	// DriverINF is the .inf file of the driver, on a share or the device,
	// for a driver that is not in Windows yet.
	DriverINF string `json:"driverInf,omitempty"`
	// Default makes it the default printer of every user.
	Default bool `json:"default,omitempty"`
}

// PrinterSettings adds printers. Printers the agent added are removed again
// when they are no longer configured.
type PrinterSettings struct {
	Printers []Printer `json:"printers"`
}

// Wallpaper styles.
var WallpaperStyles = []string{"fill", "fit", "stretch", "center", "tile", "span"}

// DesktopSettings sets the wallpaper of every user and the screen lock.
// Empty fields are not managed. They are Windows policies: the user cannot
// change them, and the wallpaper changes at the next sign-in.
type DesktopSettings struct {
	// Wallpaper is an image on the device or a share (C:\..., \\server\...).
	Wallpaper      string `json:"wallpaper,omitempty"`
	WallpaperStyle string `json:"wallpaperStyle,omitempty"`
	// LockMinutes locks the PC after this many minutes without input.
	LockMinutes int `json:"lockMinutes,omitempty"`
}

// PowerSettings sets timeouts in minutes, on mains power (AC) and on battery
// (DC); 0 is never, nil is not managed. They are Windows power policies:
// the user cannot change them.
type PowerSettings struct {
	MonitorAC   *int `json:"monitorAc,omitempty"`
	MonitorDC   *int `json:"monitorDc,omitempty"`
	SleepAC     *int `json:"sleepAc,omitempty"`
	SleepDC     *int `json:"sleepDc,omitempty"`
	HibernateAC *int `json:"hibernateAc,omitempty"`
	HibernateDC *int `json:"hibernateDc,omitempty"`
}

// AdminSettings adds accounts to the local Administrators group
// (S-1-5-32-544, whatever its name in the language of Windows). Members the
// agent added are removed again when they are no longer configured; other
// members stay.
type AdminSettings struct {
	// Members are accounts or groups: DOMAIN\name, a local name,
	// AzureAD\user@example.com, or a SID.
	Members []string `json:"members"`
}

// Firewall rule fields.
const (
	FirewallIn    = "in"
	FirewallOut   = "out"
	FirewallAllow = "allow"
	FirewallBlock = "block"
)

// FirewallProtocols are the protocols a rule can match; "any" matches all.
var FirewallProtocols = []string{"any", "tcp", "udp", "icmpv4", "icmpv6"}

// FirewallProfiles are the Windows network profiles.
var FirewallProfiles = []string{"domain", "private", "public"}

// FirewallRule is one rule. Empty fields match everything.
type FirewallRule struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
	Action    string `json:"action"`
	Protocol  string `json:"protocol"`
	// LocalPorts and RemotePorts are comma separated ports and ranges
	// (80,443,8000-8080), for TCP and UDP only.
	LocalPorts  string `json:"localPorts,omitempty"`
	RemotePorts string `json:"remotePorts,omitempty"`
	// RemoteAddresses are comma separated addresses, ranges and subnets
	// (192.168.1.0/24, 10.0.0.1-10.0.0.9), or LocalSubnet.
	RemoteAddresses string `json:"remoteAddresses,omitempty"`
	// Program is the full path of the program the rule is for.
	Program  string   `json:"program,omitempty"`
	Profiles []string `json:"profiles,omitempty"` // empty: all
}

// FirewallGroup is the Windows Firewall group of the rules FleetWarden makes.
const FirewallGroup = "FleetWarden"

// FirewallSettings adds firewall rules, in the group FirewallGroup. Rules
// the agent added are removed again when they are no longer configured.
type FirewallSettings struct {
	Rules []FirewallRule `json:"rules"`
}

// Configuration states in a report.
const (
	ConfigApplied = "applied" // everything is as configured
	ConfigFailed  = "failed"
	ConfigWaiting = "waiting" // waits for a user to log on, or for the next run
	// ConfigUnsupported: the device cannot apply it (no such browser, ...).
	ConfigUnsupported = "unsupported"
)

// ConfigStatus reports the state of every configuration in the policy.
type ConfigStatus struct {
	Policy string             `json:"policy"` // ConfigPolicy.Hash of the applied policy
	Items  []ConfigItemStatus `json:"items"`
	At     time.Time          `json:"at"`
}

// ConfigItemStatus is the state of one configuration on the device.
type ConfigItemStatus struct {
	ID       int64  `json:"id"`
	Revision int    `json:"revision"`
	State    string `json:"state"`
	Message  string `json:"message,omitempty"`
	// Repaired counts settings that had been changed outside FleetWarden
	// and were put back in this run (another tool such as Group Policy, or
	// the user, may be changing them).
	Repaired  int       `json:"repaired,omitempty"`
	AppliedAt time.Time `json:"appliedAt,omitzero"`
	// Runs are the script runs since the last report, oldest first.
	Runs []ScriptRun `json:"runs,omitempty"`
}

// ScriptRun is the outcome of one script run.
type ScriptRun struct {
	At         time.Time `json:"at"`
	User       string    `json:"user,omitempty"` // for scripts that run as the user
	ExitCode   int       `json:"exitCode"`
	Output     string    `json:"output,omitempty"`
	DurationMs int64     `json:"durationMs"`
	Error      string    `json:"error,omitempty"` // the script could not run or timed out
}
