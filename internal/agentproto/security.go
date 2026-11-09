package agentproto

import "time"

// Security status: disk encryption (BitLocker, LUKS) with the BitLocker
// recovery keys, antivirus (Microsoft Defender and other products Windows
// Security Center knows), the firewall, Secure Boot and TPM, and the members
// of the local administrators group.
//
// The agent sends a SecurityReport after every inventory and when the
// server asks for one. Recovery keys travel inside the signed session; the
// server stores them encrypted and never sends them back to an agent.

// Message types.
const (
	// server -> agent
	TypeSecurityRequest = "security.request" // collect and send a SecurityReport now
	TypeSecurityAction  = "security.action"  // run a SecurityAction
	// agent -> server
	TypeSecurityReport       = "security.report"
	TypeSecurityActionStatus = "security.action.status"
)

// Encryption methods.
const (
	EncryptionBitLocker = "bitlocker"
	EncryptionLUKS      = "luks"
	EncryptionNone      = ""
)

// SecurityReport is the security status of a device.
type SecurityReport struct {
	CollectedAt time.Time       `json:"collectedAt"`
	Volumes     []SecureVolume  `json:"volumes"`
	Antivirus   []Antivirus     `json:"antivirus"`
	Defender    *DefenderStatus `json:"defender,omitempty"`
	Firewall    []FirewallState `json:"firewall"`
	Admins      []LocalAdmin    `json:"admins"`
	// SecureBoot and TPM are nil when the agent could not tell.
	SecureBoot *bool      `json:"secureBoot,omitempty"`
	TPM        *TPMStatus `json:"tpm,omitempty"`
	// Errors are the parts the agent could not read (English).
	Errors []string `json:"errors,omitempty"`
}

// SecureVolume is a fixed volume with its encryption.
type SecureVolume struct {
	Mount string `json:"mount"` // C:, /
	// System marks the volume Windows or Linux runs from.
	System bool `json:"system"`
	// Method is bitlocker, luks or empty when not encrypted.
	Method string `json:"method"`
	// Protection is on, off or suspended (BitLocker); on for an unlocked
	// LUKS volume.
	Protection string `json:"protection"`
	// Status is the conversion state: encrypted, encrypting, decrypting,
	// decrypted, paused.
	Status  string `json:"status"`
	Percent int    `json:"percent"`
	// Cipher is the encryption method (XtsAes128, aes-xts-plain64).
	Cipher string `json:"cipher,omitempty"`
	// Protectors are the BitLocker key protector types (Tpm, TpmPin,
	// RecoveryPassword, ExternalKey, Password).
	Protectors []string `json:"protectors,omitempty"`
	// Keys are the BitLocker recovery passwords of the volume.
	Keys []RecoveryKey `json:"keys,omitempty"`
}

// RecoveryKey is one BitLocker recovery password protector.
type RecoveryKey struct {
	// ID is the protector ID ("{A1B2...}"); BitLocker's recovery screen
	// shows its first eight characters.
	ID       string `json:"id"`
	Password string `json:"password"` // 48 digits in 8 groups
}

// Antivirus is a product registered with Windows Security Center, or a
// scanner found on Linux.
type Antivirus struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	UpToDate bool   `json:"upToDate"`
	// Defender marks Microsoft Defender Antivirus.
	Defender bool `json:"defender,omitempty"`
}

// DefenderStatus is the state of Microsoft Defender Antivirus.
type DefenderStatus struct {
	// Mode is Normal, Passive (another antivirus is active), EDR Block
	// Mode or Not running.
	Mode               string     `json:"mode"`
	ServiceEnabled     bool       `json:"serviceEnabled"`
	AntivirusEnabled   bool       `json:"antivirusEnabled"`
	RealTime           bool       `json:"realTime"`
	TamperProtected    bool       `json:"tamperProtected"`
	SignatureVersion   string     `json:"signatureVersion"`
	SignatureUpdatedAt *time.Time `json:"signatureUpdatedAt,omitempty"`
	EngineVersion      string     `json:"engineVersion"`
	QuickScanAt        *time.Time `json:"quickScanAt,omitempty"`
	FullScanAt         *time.Time `json:"fullScanAt,omitempty"`
	// Threats are the detections of the last 30 days.
	Threats []Threat `json:"threats,omitempty"`
}

// Threat is one Defender detection.
type Threat struct {
	Name       string    `json:"name"`
	Severity   string    `json:"severity"` // Low, Moderate, High, Severe
	DetectedAt time.Time `json:"detectedAt"`
	// Active means Defender has not removed or quarantined it (yet).
	Active    bool     `json:"active"`
	Action    string   `json:"action"` // Quarantine, Remove, Allow, ...
	Resources []string `json:"resources,omitempty"`
}

// FirewallState is one firewall profile (Windows: Domain, Private, Public)
// or the firewall in use on Linux (ufw, firewalld, nftables, iptables).
type FirewallState struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Inbound is the default inbound action: block or allow.
	Inbound string `json:"inbound,omitempty"`
	// Active marks the Windows profile of the current network.
	Active bool `json:"active,omitempty"`
}

// LocalAdmin is a member of the local administrators group (Windows) or of
// sudo, wheel or admin (Linux).
type LocalAdmin struct {
	Name string `json:"name"` // DOMAIN\name, user
	SID  string `json:"sid,omitempty"`
	// Kind is user, group or unknown (an account that no longer exists).
	Kind string `json:"kind"`
	// Builtin marks the built-in Administrator and Domain Admins, root.
	Builtin bool `json:"builtin,omitempty"`
}

// TPMStatus describes the Trusted Platform Module.
type TPMStatus struct {
	Present bool   `json:"present"`
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"` // 2.0
}

// Security actions.
const (
	SecurityQuickScan  = "defender.quickscan"
	SecurityFullScan   = "defender.fullscan"
	SecuritySignatures = "defender.signatures"
	// SecurityRotateKey replaces the recovery passwords of a BitLocker
	// volume with a new one, after a key was used or shown.
	SecurityRotateKey = "bitlocker.rotate"
)

// SecurityAction asks the agent to run one action. The answer is a
// SecurityActionStatus with the same ID.
type SecurityAction struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
	Mount  string `json:"mount,omitempty"` // bitlocker.rotate
}

// Security action states.
const (
	SecurityActionRunning   = "running"
	SecurityActionCompleted = "completed"
	SecurityActionFailed    = "failed"
)

// SecurityActionStatus reports on a SecurityAction.
type SecurityActionStatus struct {
	ID      int64  `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
