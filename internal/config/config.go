// Package config loads the server configuration from environment variables.
//
// On a packaged install these come from /etc/fleetwarden/server.env, which the
// systemd unit loads with EnvironmentFile=.
package config

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// Server is the management server configuration.
type Server struct {
	// Listen is the address the HTTP server binds to. The server is designed to
	// run behind a reverse proxy, so the default only listens on localhost.
	Listen string
	// WebDir is the directory with the built web interface.
	WebDir string
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// TrustedProxies are the reverse proxy addresses whose X-Forwarded-* headers
	// are trusted.
	TrustedProxies []netip.Prefix
	// DataDir holds server state such as the secret key (FW_DATA_DIR).
	DataDir string
	// UpdateStatusDir is where the privileged updater and restore helper report
	// progress (FW_UPDATE_STATUS_DIR). The server only reads it.
	UpdateStatusDir string
	// EnvFile is the configuration file, included in backups for reference (FW_ENV_FILE).
	EnvFile string
	// AgentDir holds the agent installers shipped with the server package (FW_AGENT_DIR).
	AgentDir string
}

// AgentKeyPath is the server's Ed25519 key that agents pin at enrollment.
func (c Server) AgentKeyPath() string { return filepath.Join(c.DataDir, "agent-signing.key") }

// AgentReleasesDir holds agent releases downloaded from GitHub or uploaded.
func (c Server) AgentReleasesDir() string { return filepath.Join(c.DataDir, "agent-releases") }

// BackupDir is the parent folder of "local folder" backup locations.
func (c Server) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

// RestoreDir holds a downloaded backup waiting to be restored, and the
// rollback copy of the previous state.
func (c Server) RestoreDir() string { return filepath.Join(c.DataDir, "restore") }

// TmpDir holds temporary files such as the database dump during a backup.
func (c Server) TmpDir() string { return filepath.Join(c.DataDir, "tmp") }

// BackupExclude are the top-level entries of DataDir that are not backed up
// and not replaced by a restore. Agent releases are downloaded again.
var BackupExclude = []string{"backups", "restore", "tmp", "update", "agent-releases", "catalog", "repository", "initial-admin-password", "server.env.from-backup"}

// SecretKeyPath is the AES key that encrypts stored secrets.
func (c Server) SecretKeyPath() string { return filepath.Join(c.DataDir, "secret.key") }

// InitialPasswordPath holds the installer-generated admin password until the
// first password change.
func (c Server) InitialPasswordPath() string {
	return filepath.Join(c.DataDir, "initial-admin-password")
}

// UpdateRequestDir is where the server asks the updater to install a version.
func (c Server) UpdateRequestDir() string { return filepath.Join(c.DataDir, "update") }

// FromEnv reads the configuration from the environment.
func FromEnv() (Server, error) {
	c := Server{
		Listen:      getenv("FW_LISTEN", "127.0.0.1:8080"),
		WebDir:      getenv("FW_WEB_DIR", "/usr/share/fleetwarden/web"),
		DatabaseURL: os.Getenv("FW_DATABASE_URL"),
		DataDir:     getenv("FW_DATA_DIR", "/var/lib/fleetwarden"),

		UpdateStatusDir: getenv("FW_UPDATE_STATUS_DIR", "/var/lib/fleetwarden-update"),
		EnvFile:         getenv("FW_ENV_FILE", "/etc/fleetwarden/server.env"),
		AgentDir:        getenv("FW_AGENT_DIR", "/usr/share/fleetwarden/agent"),
	}
	proxies, err := ParsePrefixes(getenv("FW_TRUSTED_PROXIES", "127.0.0.1/32,::1/128"))
	if err != nil {
		return c, fmt.Errorf("FW_TRUSTED_PROXIES: %w", err)
	}
	c.TrustedProxies = proxies
	return c, nil
}

// ParsePrefixes parses a comma-separated list of IP addresses or CIDR prefixes.
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			addr, err := netip.ParseAddr(part)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// LoadEnvFile sets variables from a KEY=VALUE file (the systemd
// EnvironmentFile format) that are not already set. Missing files are ignored.
// Command-line tools use it so that `sudo fleetwarden-server reset-admin`
// works without loading the file by hand.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}
