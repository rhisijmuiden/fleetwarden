// Package agentproto is the wire protocol between the FleetWarden agent and
// the management server.
//
// Transport: the agent enrolls once over HTTPS (POST /agent/v1/enroll) and then
// keeps a WebSocket open to /agent/v1/ws. The reverse proxy may terminate TLS,
// so the protocol does not rely on it for authenticity:
//
//   - At enrollment the agent creates an Ed25519 key pair and registers the
//     public key; it pins the server's Ed25519 public key from the answer.
//   - Every WebSocket session starts with a fresh server nonce. Every message
//     in either direction is signed over (nonce, direction, sequence, type, id,
//     data); sequence numbers strictly increase, so messages cannot be
//     replayed, reordered or moved to another session.
//   - File downloads (agent updates) carry a signed request header.
//
// Messages address one device, so a later DMZ gateway can relay them without
// understanding them.
package agentproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Version is the protocol version. The server accepts agents with the same
// major version.
const Version = 1

// Paths on the management server.
const (
	PathEnroll   = "/agent/v1/enroll"
	PathWS       = "/agent/v1/ws"
	PathHello    = "/agent/v1/hello"
	PathDownload = "/agent/v1/files/" // + release file name
)

// Direction is part of what is signed, so a message cannot be reflected back.
type Direction byte

// Directions.
const (
	ToServer Direction = 'S'
	ToAgent  Direction = 'A'
)

// Message types.
const (
	// server -> agent
	TypeChallenge        = "challenge"         // first message, unsigned nonce
	TypeWelcome          = "welcome"           // auth accepted, carries settings
	TypeInventoryRequest = "inventory.request" // send a full inventory now
	TypeUpdate           = "agent.update"      // install another agent version
	TypeRemove           = "agent.remove"      // device was deleted: forget enrollment
	TypeAgentConfig      = "agent.config"      // changed settings for this session
	TypeEndpoints        = "agent.endpoints"   // addresses the agent may connect to
	// agent -> server
	TypeAuth         = "auth"
	TypeHeartbeat    = "heartbeat"
	TypeInventory    = "inventory"
	TypeUpdateStatus = "agent.update.status"
)

// Envelope is one signed WebSocket message.
type Envelope struct {
	Type string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"d,omitempty"`
	Sig  []byte          `json:"sig,omitempty"`
}

func signingBytes(nonce []byte, dir Direction, e *Envelope) []byte {
	b := make([]byte, 0, 64+len(e.Type)+len(e.ID)+len(e.Data))
	b = append(b, "fleetwarden-agent-msg-v1\x00"...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(nonce)))
	b = append(b, nonce...)
	b = append(b, byte(dir))
	b = binary.BigEndian.AppendUint64(b, e.Seq)
	b = binary.BigEndian.AppendUint16(b, uint16(len(e.Type)))
	b = append(b, e.Type...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(e.ID)))
	b = append(b, e.ID...)
	return append(b, e.Data...)
}

// Sign signs the envelope for a session.
func (e *Envelope) Sign(key ed25519.PrivateKey, nonce []byte, dir Direction) {
	e.Sig = ed25519.Sign(key, signingBytes(nonce, dir, e))
}

// Verify checks the envelope signature.
func (e *Envelope) Verify(pub ed25519.PublicKey, nonce []byte, dir Direction) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, signingBytes(nonce, dir, e), e.Sig)
}

// Decode unmarshals the payload.
func (e *Envelope) Decode(v any) error {
	if len(e.Data) == 0 {
		return errors.New("empty payload")
	}
	return json.Unmarshal(e.Data, v)
}

// NewEnvelope builds an unsigned envelope with a JSON payload.
func NewEnvelope(typ, id string, seq uint64, v any) (*Envelope, error) {
	e := &Envelope{Type: typ, ID: id, Seq: seq}
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		e.Data = raw
	}
	return e, nil
}

// Challenge opens a session.
type Challenge struct {
	Nonce           []byte    `json:"nonce"`
	ServerTime      time.Time `json:"serverTime"`
	ProtocolVersion int       `json:"protocolVersion"`
}

// Auth is the agent's answer to the challenge (sequence 1, signed).
type Auth struct {
	DeviceID        string `json:"deviceId"`
	AgentVersion    string `json:"agentVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// Welcome confirms the session (signed by the server).
type Welcome struct {
	DeviceID          string `json:"deviceId"`
	HeartbeatSeconds  int    `json:"heartbeatSeconds"`
	InventoryMinutes  int    `json:"inventoryMinutes"`
	InventoryRequired bool   `json:"inventoryRequired"`
}

// AgentConfig changes session settings after the welcome. Older agents
// ignore it and keep the interval from the welcome until they reconnect.
type AgentConfig struct {
	InventoryMinutes int `json:"inventoryMinutes"`
}

// Endpoints lists the addresses of the management server, in order of
// preference: its own external URL and the secure gateways in the DMZ. The
// agent tries them in turn when the connection fails; the server key it
// pinned at enrollment makes any of them equally trustworthy. An empty list
// means: only use the configured server address.
type Endpoints struct {
	URLs []string `json:"urls"`
}

// Update asks the agent to install another agent version. The agent fetches
// the package with a signed request from PathDownload+Package and checks the
// hash. Previous is the package of the running version, kept for a rollback
// when the new version does not come back online.
type Update struct {
	Version        string `json:"version"`
	Package        string `json:"package"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	Previous       string `json:"previous,omitempty"`
	PreviousSHA256 string `json:"previousSha256,omitempty"`
}

// Update states reported in UpdateStatus.
const (
	UpdateDownloading = "downloading"
	UpdateInstalling  = "installing"
	UpdateCompleted   = "completed"
	UpdateFailed      = "failed"
	UpdateRolledBack  = "rolled-back"
)

// UpdateStatus reports the progress of an Update.
type UpdateStatus struct {
	Version string `json:"version"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

// EnrollRequest registers a new agent.
type EnrollRequest struct {
	Token        string `json:"token"`
	PublicKey    []byte `json:"publicKey"`
	MachineID    string `json:"machineId"`
	Hostname     string `json:"hostname"`
	Platform     string `json:"platform"` // windows, linux
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
	// Group is an optional group name (msiexec GROUP=). It only applies when
	// the token allows choosing the group; otherwise the token's group wins.
	Group string `json:"group,omitempty"`
	// Proof is an Ed25519 signature over EnrollProof(token, publicKey): it
	// proves the agent holds the private key it registers.
	Proof []byte `json:"proof"`
}

// EnrollProof is what the agent signs in an enrollment request.
func EnrollProof(token string, pub []byte) []byte {
	return append([]byte("fleetwarden-enroll-v1\x00"+token+"\x00"), pub...)
}

// EnrollResponse is the server's answer to a successful enrollment.
type EnrollResponse struct {
	DeviceID        string `json:"deviceId"`
	ServerPublicKey []byte `json:"serverPublicKey"`
	GroupName       string `json:"groupName"`
}

// Hello is the public server identity (used by the installer wizard to test
// the connection).
type Hello struct {
	Product         string `json:"product"`
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
	ServerPublicKey []byte `json:"serverPublicKey"`
}

// Heartbeat is sent periodically and carries values that change often.
type Heartbeat struct {
	LoggedInUser string    `json:"loggedInUser"`
	BootTime     time.Time `json:"bootTime"`
	LocalIPs     []string  `json:"localIps"`
	AgentVersion string    `json:"agentVersion"`
	// Volumes are the local fixed file systems with their free space, sent
	// with every heartbeat for disk space alerts. Older agents leave it out.
	Volumes []Volume `json:"volumes,omitempty"`
}

// Inventory is the full hardware and software inventory.
type Inventory struct {
	CollectedAt time.Time  `json:"collectedAt"`
	Hostname    string     `json:"hostname"`
	OS          OSInfo     `json:"os"`
	Hardware    Hardware   `json:"hardware"`
	Software    []Software `json:"software"`
	Heartbeat
}

// OSInfo describes the operating system.
type OSInfo struct {
	Name         string `json:"name"`    // "Windows 11 Pro", "Debian GNU/Linux 13 (trixie)"
	Version      string `json:"version"` // "24H2", "13"
	Build        string `json:"build"`   // "26100.2314", kernel release
	Kind         string `json:"kind"`    // workstation, server, hypervisor
	Domain       string `json:"domain"`
	DomainJoined bool   `json:"domainJoined"`
	OU           string `json:"ou"`
}

// Hardware describes the machine.
type Hardware struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Serial       string `json:"serial"`
	BIOS         string `json:"bios"`
	// The firmware platform, used to match vendor BIOS catalogs: the
	// system SKU (Dell), the baseboard product (HP) and the bare BIOS
	// version as SMBIOS reports it ("1.21.0", "S70 Ver. 01.23.00",
	// "N3BET70W (1.50 )").
	SystemSKU   string     `json:"systemSku,omitempty"`
	Baseboard   string     `json:"baseboard,omitempty"`
	BIOSVersion string     `json:"biosVersion,omitempty"`
	CPU         string     `json:"cpu"`
	CPUCores    int        `json:"cpuCores"`
	CPUThreads  int        `json:"cpuThreads"`
	MemoryBytes int64      `json:"memoryBytes"`
	Disks       []Disk     `json:"disks"`
	Volumes     []Volume   `json:"volumes"`
	Network     []NetIface `json:"network"`
	GPUs        []string   `json:"gpus,omitempty"`
	Virtual     bool       `json:"virtual"`
}

// Disk is a physical or virtual disk.
type Disk struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	SizeBytes int64  `json:"sizeBytes"`
	Media     string `json:"media"` // ssd, hdd, nvme, virtual
}

// Volume is a mounted file system.
type Volume struct {
	Mount      string `json:"mount"`
	FileSystem string `json:"fileSystem"`
	SizeBytes  int64  `json:"sizeBytes"`
	FreeBytes  int64  `json:"freeBytes"`
}

// NetIface is a network adapter.
type NetIface struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips"`
}

// Software is one installed program or package.
type Software struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	InstalledOn string `json:"installedOn,omitempty"` // YYYY-MM-DD
	Source      string `json:"source"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
}

// ---- signed HTTP requests (file downloads) ----

// HeaderAuth carries "deviceID:unixTime:base64(signature)".
const HeaderAuth = "X-FleetWarden-Agent"

func requestSigningBytes(method, path string, ts int64, deviceID string) []byte {
	return []byte("fleetwarden-agent-req-v1\x00" + method + "\x00" + path + "\x00" + strconv.FormatInt(ts, 10) + "\x00" + deviceID)
}

// SignRequest returns the HeaderAuth value for a request.
func SignRequest(key ed25519.PrivateKey, deviceID, method, path string, now time.Time) string {
	ts := now.Unix()
	sig := ed25519.Sign(key, requestSigningBytes(method, path, ts, deviceID))
	return fmt.Sprintf("%s:%d:%s", deviceID, ts, base64.RawStdEncoding.EncodeToString(sig))
}

// ParseRequestAuth splits a HeaderAuth value.
func ParseRequestAuth(v string) (deviceID string, ts int64, sig []byte, err error) {
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return "", 0, nil, errors.New("malformed agent signature")
	}
	ts, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, nil, errors.New("malformed agent signature")
	}
	sig, err = base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", 0, nil, errors.New("malformed agent signature")
	}
	return parts[0], ts, sig, nil
}

// VerifyRequest checks a request signature made within maxSkew of now.
func VerifyRequest(pub ed25519.PublicKey, deviceID, method, path string, ts int64, sig []byte, now time.Time, maxSkew time.Duration) bool {
	d := now.Sub(time.Unix(ts, 0))
	if d < -maxSkew || d > maxSkew {
		return false
	}
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, requestSigningBytes(method, path, ts, deviceID), sig)
}
