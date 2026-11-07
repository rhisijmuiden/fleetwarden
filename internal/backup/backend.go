package backup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Backend stores objects at a backup location. Names are slash-separated and
// relative to the location's folder. Implementations are safe for concurrent use.
type Backend interface {
	// Put stores data under name, creating parent folders, replacing an existing object.
	Put(ctx context.Context, name string, data []byte) error
	// Get returns an object or an error wrapping ErrNotExist.
	Get(ctx context.Context, name string) ([]byte, error)
	// List returns the entries directly inside dir; a missing dir is empty.
	List(ctx context.Context, dir string) ([]Entry, error)
	// Delete removes an object; a missing object is not an error.
	Delete(ctx context.Context, name string) error
	// RemoveAll removes a folder and everything in it.
	RemoveAll(ctx context.Context, dir string) error
	Close() error
}

// Entry is one item of a listing.
type Entry struct {
	Name string
	Dir  bool
	Size int64
}

// ErrNotExist is returned for missing objects.
var ErrNotExist = errors.New("object does not exist")

// Location types.
const (
	TypeLocal    = "local"
	TypeFTP      = "ftp"
	TypeFTPS     = "ftps"
	TypeSFTP     = "sftp"
	TypeOneDrive = "onedrive"
)

// Location is the configuration of one backup destination. Secrets
// (Password, PrivateKey, ClientSecret) are plain here; the server seals them at rest.
type Location struct {
	Type string `json:"type"`

	// FTP, FTPS and SFTP.
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// FTPS: implicit TLS (usually port 990) instead of explicit AUTH TLS.
	ImplicitTLS bool `json:"implicitTls,omitempty"`
	// FTPS: SHA-256 fingerprint of a trusted certificate (self-signed servers).
	TLSFingerprint string `json:"tlsFingerprint,omitempty"`
	// SFTP: private key (OpenSSH or PEM); empty means password authentication.
	PrivateKey string `json:"privateKey,omitempty"`
	// SFTP: the trusted host key in authorized_keys format.
	HostKey string `json:"hostKey,omitempty"`

	// OneDrive (Microsoft Graph, app-only).
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	DriveUser    string `json:"driveUser,omitempty"` // UPN or object ID of the OneDrive owner

	// Folder at the location (all types).
	Path string `json:"path,omitempty"`
}

var guidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DefaultPort returns the default port of a location type.
func DefaultPort(typ string, implicitTLS bool) int {
	switch typ {
	case TypeFTP:
		return 21
	case TypeFTPS:
		if implicitTLS {
			return 990
		}
		return 21
	case TypeSFTP:
		return 22
	}
	return 0
}

// Normalize trims fields and fills defaults.
func (l *Location) Normalize() {
	l.Type = strings.ToLower(strings.TrimSpace(l.Type))
	l.Host = strings.TrimSpace(l.Host)
	l.Username = strings.TrimSpace(l.Username)
	l.TenantID = strings.ToLower(strings.TrimSpace(l.TenantID))
	l.ClientID = strings.ToLower(strings.TrimSpace(l.ClientID))
	l.DriveUser = strings.TrimSpace(l.DriveUser)
	l.HostKey = strings.TrimSpace(l.HostKey)
	l.TLSFingerprint = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(l.TLSFingerprint), ":", ""))
	l.Path = strings.Trim(strings.TrimSpace(strings.ReplaceAll(l.Path, "\\", "/")), "/")
	if l.Port == 0 {
		l.Port = DefaultPort(l.Type, l.ImplicitTLS)
	}
	if l.Type != TypeFTPS {
		l.ImplicitTLS, l.TLSFingerprint = false, ""
	}
	if l.Type != TypeSFTP {
		l.PrivateKey, l.HostKey = "", ""
	}
}

// Validate checks a normalised location.
func (l Location) Validate() error {
	for _, seg := range strings.Split(l.Path, "/") {
		if seg == ".." || seg == "." {
			return errors.New("the folder may not contain . or ..")
		}
	}
	switch l.Type {
	case TypeLocal:
		if l.Path == "" {
			return errors.New("folder is required")
		}
	case TypeFTP, TypeFTPS, TypeSFTP:
		if l.Host == "" || strings.ContainsAny(l.Host, "/ ") {
			return errors.New("host is required")
		}
		if l.Port < 1 || l.Port > 65535 {
			return errors.New("invalid port")
		}
		if l.Username == "" {
			return errors.New("username is required")
		}
	case TypeOneDrive:
		if !guidRe.MatchString(l.TenantID) || !guidRe.MatchString(l.ClientID) {
			return errors.New("tenant ID and client ID must be GUIDs")
		}
		if l.ClientSecret == "" {
			return errors.New("client secret is required")
		}
		if l.DriveUser == "" {
			return errors.New("OneDrive user is required")
		}
	default:
		return fmt.Errorf("unknown location type %q", l.Type)
	}
	return nil
}

func (l Location) addr() string { return net.JoinHostPort(l.Host, strconv.Itoa(l.Port)) }

// Options are server-side settings for opening backends.
type Options struct {
	// LocalBase is the parent folder of local locations.
	LocalBase string
	// GraphURL and TokenURL override the Microsoft endpoints (tests).
	GraphURL string
	TokenURL string
}

// Open connects to a location.
func Open(ctx context.Context, l Location, o Options) (Backend, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	switch l.Type {
	case TypeLocal:
		return openLocal(path.Join(o.LocalBase, l.Path))
	case TypeFTP, TypeFTPS:
		return openFTP(ctx, l)
	case TypeSFTP:
		return openSFTP(ctx, l)
	case TypeOneDrive:
		return openOneDrive(ctx, l, o)
	}
	return nil, fmt.Errorf("unknown location type %q", l.Type)
}

// TrustError is returned when the server presents a host key or certificate
// that is not trusted yet. The administrator can accept it in the interface.
type TrustError struct {
	Kind        string // "ssh" or "tls"
	Fingerprint string // shown to the administrator
	Value       string // stored in HostKey or TLSFingerprint when accepted
	Mismatch    bool   // a different key or certificate was trusted before
	Reason      string
}

func (e *TrustError) Error() string {
	if e.Mismatch {
		return fmt.Sprintf("the %s key of the server changed (now %s); if this is expected, trust the new key", e.Kind, e.Fingerprint)
	}
	return fmt.Sprintf("the server's %s key %s is not trusted yet: %s", e.Kind, e.Fingerprint, e.Reason)
}

// join builds an object path inside a location folder.
func join(base, name string) string {
	if base == "" {
		return name
	}
	if name == "" {
		return base
	}
	return base + "/" + name
}
