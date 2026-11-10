// Package distribution is the FleetWarden distribution server: a cache in a
// branch office that keeps approved software, driver files and agent
// updates and serves them to the agents near it, so each file crosses the
// slow link to the management server once.
package distribution

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
)

// Config is the distribution server configuration (environment variables,
// normally from /etc/fleetwarden-distribution/distribution.env).
type Config struct {
	// Server is the management server's address, the same one agents use
	// (FW_DIST_SERVER).
	Server string
	// Listen is where agents download (HTTPS, FW_DIST_LISTEN, default :8445).
	Listen string
	// CertFile and KeyFile replace the generated self-signed certificate
	// (FW_DIST_TLS_CERT, FW_DIST_TLS_KEY). Agents pin it either way.
	CertFile, KeyFile string
	// CAFile adds a CA for the management server's certificate (FW_DIST_CA_FILE).
	CAFile string
	// DataDir holds the key, the identity and the certificate (FW_DIST_DATA_DIR).
	DataDir string
	// ContentDir holds the files (FW_DIST_CONTENT_DIR, default DataDir/content).
	ContentDir string
	// MinFreeBytes is the disk space kept free (FW_DIST_MIN_FREE_GB, default 2).
	MinFreeBytes int64
	// UpdateDir is where the server asks the root updater for a release;
	// UpdateStatusDir is where the updater reports (FW_DIST_UPDATE_DIR,
	// FW_DIST_UPDATE_STATUS_DIR). Updates are offered when UpdateDir exists.
	UpdateDir, UpdateStatusDir string
}

// EnvFile is the packaged configuration file.
const EnvFile = "/etc/fleetwarden-distribution/distribution.env"

func (c Config) keyPath() string      { return filepath.Join(c.DataDir, "distribution.key") }
func (c Config) identityPath() string { return filepath.Join(c.DataDir, "identity.json") }
func (c Config) certPath() string     { return filepath.Join(c.DataDir, "tls.crt") }
func (c Config) certKeyPath() string  { return filepath.Join(c.DataDir, "tls.key") }

// Port is the port of the listen address.
func (c Config) Port() int {
	_, p, err := net.SplitHostPort(c.Listen)
	if n, perr := strconv.Atoi(p); err == nil && perr == nil {
		return n
	}
	return distproto.DefaultPort
}

// FromEnv reads the configuration.
func FromEnv() (Config, error) {
	c := Config{
		Server:   strings.TrimRight(strings.TrimSpace(os.Getenv("FW_DIST_SERVER")), "/"),
		Listen:   getenv("FW_DIST_LISTEN", fmt.Sprintf(":%d", distproto.DefaultPort)),
		CertFile: os.Getenv("FW_DIST_TLS_CERT"),
		KeyFile:  os.Getenv("FW_DIST_TLS_KEY"),
		CAFile:   os.Getenv("FW_DIST_CA_FILE"),
		DataDir:  getenv("FW_DIST_DATA_DIR", "/var/lib/fleetwarden-distribution"),
	}
	c.ContentDir = getenv("FW_DIST_CONTENT_DIR", filepath.Join(c.DataDir, "content"))
	c.UpdateDir = getenv("FW_DIST_UPDATE_DIR", filepath.Join(c.DataDir, "update"))
	c.UpdateStatusDir = getenv("FW_DIST_UPDATE_STATUS_DIR", "/var/lib/fleetwarden-distribution-update")
	gb, err := strconv.ParseFloat(getenv("FW_DIST_MIN_FREE_GB", "2"), 64)
	if err != nil || gb < 0 {
		return c, fmt.Errorf("FW_DIST_MIN_FREE_GB must be a number of gigabytes")
	}
	c.MinFreeBytes = int64(gb * (1 << 30))
	if (c.CertFile == "") != (c.KeyFile == "") {
		return c, fmt.Errorf("set both FW_DIST_TLS_CERT and FW_DIST_TLS_KEY, or neither")
	}
	if c.Server != "" {
		if err := checkServerURL(c.Server); err != nil {
			return c, fmt.Errorf("FW_DIST_SERVER: %w", err)
		}
	}
	return c, nil
}

func checkServerURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) address", v)
	}
	return nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}
