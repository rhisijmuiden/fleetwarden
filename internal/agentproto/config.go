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
)

// ConfigTypes lists the types per platform.
var ConfigTypes = map[string][]string{
	"windows": {ConfigBrowser, ConfigRegistry, ConfigScript},
	"linux":   {ConfigBrowser, ConfigScript},
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
