// Package distproto is the protocol between the management server, its
// distribution servers and the agents that download from them.
//
// A distribution server is a cache in a branch office. It connects out to
// the management server (through the same address and reverse proxy the
// agents use), downloads every approved file once and serves it to the
// agents near it. Files are content addressed: the agent checks the size
// and SHA-256 the management server gave it, so a distribution server can
// not hand out anything else. Whether an agent may have a file is still
// decided by the management server: the distribution server forwards the
// agent's signed request to it before serving.
package distproto

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// Paths on the management server, under /agent/ so they also pass a secure
// gateway. Every request except enroll is signed with the distribution
// server's key (agentproto.SignRequest with the server ID as identity).
const (
	PathEnroll    = "/agent/v1/dist/enroll"
	PathStatus    = "/agent/v1/dist/status"
	PathManifest  = "/agent/v1/dist/manifest"
	PathContent   = "/agent/v1/dist/content/" // + sha256
	PathAuthorize = "/agent/v1/dist/authorize"
)

// Headers of an authorize request: the agent's own signature and the path
// it asked the distribution server for.
const (
	HeaderAgentAuth = "X-FleetWarden-Agent-Auth"
	HeaderAgentPath = "X-FleetWarden-Agent-Path"
)

// DefaultPort is where a distribution server serves agents (HTTPS).
const DefaultPort = 8445

// IDPrefix makes a distribution server identity distinct from a device ID.
const IDPrefix = "dist-"

// EnrollRequest is sent once, with the one-time token from the web interface.
type EnrollRequest struct {
	Token      string   `json:"token"`
	PublicKey  []byte   `json:"publicKey"` // Ed25519
	Hostname   string   `json:"hostname"`
	Version    string   `json:"version"`
	Port       int      `json:"port"`
	Addresses  []string `json:"addresses"`
	CertSHA256 string   `json:"certSha256"`
}

// EnrollResponse names the distribution server.
type EnrollResponse struct {
	ID   string `json:"id"` // identity for signed requests ("dist-<n>")
	Name string `json:"name"`
}

// File is one file a distribution server keeps.
type File struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`           // software, driver, agent
	Name   string `json:"name,omitempty"` // agent packages: the file name agents ask for
}

// Manifest is everything the distribution server should hold.
type Manifest struct {
	Version string `json:"version"` // changes whenever Files changes
	Files   []File `json:"files"`
}

// CertSHA256 is the pin agents check: the SHA-256 of the leaf certificate.
func CertSHA256(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// ManifestVersion hashes a file list (sorted by the caller).
func ManifestVersion(files []File) string {
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.SHA256 + "\x00" + f.Name + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Status is what a distribution server reports every minute.
type Status struct {
	Version    string    `json:"version"`
	Hostname   string    `json:"hostname"`
	Port       int       `json:"port"`
	Addresses  []string  `json:"addresses"`
	CertSHA256 string    `json:"certSha256"`
	StartedAt  time.Time `json:"startedAt"`

	Manifest      string     `json:"manifest"` // manifest version it has worked through
	Files         int        `json:"files"`    // files stored that are in the manifest
	Bytes         int64      `json:"bytes"`
	PendingFiles  int        `json:"pendingFiles"`
	PendingBytes  int64      `json:"pendingBytes"`
	Downloading   string     `json:"downloading,omitempty"` // sha256 being fetched
	LastSyncAt    *time.Time `json:"lastSyncAt,omitempty"`
	SyncError     string     `json:"syncError,omitempty"`
	DiskFree      int64      `json:"diskFree"`
	DiskTotal     int64      `json:"diskTotal"`
	ServedFiles   int64      `json:"servedFiles"` // since start
	ServedBytes   int64      `json:"servedBytes"`
	Misses        int64      `json:"misses"`  // asked for a file it did not have yet
	Refused       int64      `json:"refused"` // the management server said no
	Updatable     bool       `json:"updatable"`
	Update        *Update    `json:"update,omitempty"`
	BandwidthKbps int        `json:"bandwidthKbps"`
	ReportedAt    time.Time  `json:"reportedAt"`
	Errors        []string   `json:"errors,omitempty"`
	Recent        []Download `json:"recent,omitempty"` // last files served
}

// Download is one file served to an agent.
type Download struct {
	SHA256 string    `json:"sha256"`
	Device string    `json:"device"`
	Bytes  int64     `json:"bytes"`
	At     time.Time `json:"at"`
}

// Update is the result of the root updater (as for the gateway).
type Update struct {
	State       string `json:"state"`
	Message     string `json:"message"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	UpdatedAt   string `json:"updatedAt"`
}

// StatusReply is the management server's answer to a status report.
type StatusReply struct {
	Manifest      string `json:"manifest"`         // current manifest version
	BandwidthKbps int    `json:"bandwidthKbps"`    // 0: unlimited
	Update        string `json:"update,omitempty"` // install this release
	Enabled       bool   `json:"enabled"`
}

// AuthorizeReply says which file an agent may have.
type AuthorizeReply struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA reports whether v is a lower-case hex SHA-256.
func ValidSHA(v string) bool { return sha256Hex.MatchString(v) }

// ServedPrefixes are the agent download paths a distribution server answers.
var ServedPrefixes = []string{"/agent/v1/software/", "/agent/v1/repository/", "/agent/v1/files/"}

// Served reports whether an agent path may be fetched from a distribution server.
func Served(path string) bool {
	for _, p := range ServedPrefixes {
		if strings.HasPrefix(path, p) && len(path) > len(p) && !strings.Contains(path[len(p):], "/") {
			return true
		}
	}
	return false
}
