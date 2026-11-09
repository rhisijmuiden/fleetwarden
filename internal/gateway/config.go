// Package gateway is the FleetWarden secure gateway: a small relay in the
// DMZ that accepts agent connections from the internet and passes them to
// the management server through a tunnel the server opens from the LAN.
package gateway

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/gwproto"
)

// TLS modes of the agent listener.
const (
	TLSProxy = "proxy" // plain HTTP behind a reverse proxy that terminates TLS
	TLSFiles = "files" // certificate and key files
	TLSACME  = "acme"  // Let's Encrypt certificates (HTTP-01 on port 80)
)

// Config is the gateway configuration (environment variables, normally from
// /etc/fleetwarden-gateway/gateway.env).
type Config struct {
	// Listen is the agent-facing address (FW_GW_LISTEN).
	Listen string
	// TLSMode is proxy, files or acme (FW_GW_TLS).
	TLSMode string
	// CertFile and KeyFile are used in files mode (FW_GW_TLS_CERT, FW_GW_TLS_KEY).
	CertFile, KeyFile string
	// ACMEDomains and ACMEEmail are used in acme mode (FW_GW_ACME_DOMAINS, FW_GW_ACME_EMAIL).
	ACMEDomains []string
	ACMEEmail   string
	// ACMEHTTPListen serves the HTTP-01 challenge (FW_GW_ACME_HTTP_LISTEN, default :80).
	ACMEHTTPListen string
	// TrustedProxies may set X-Forwarded-For in proxy mode (FW_GW_TRUSTED_PROXIES).
	TrustedProxies []netip.Prefix

	// TunnelListen is where the management server connects (FW_GW_TUNNEL_LISTEN).
	TunnelListen string
	// TunnelAllow limits which addresses may open the tunnel (FW_GW_TUNNEL_ALLOW);
	// empty allows all (the handshake still requires the paired key).
	TunnelAllow []netip.Prefix
	// TunnelCertFile and TunnelKeyFile replace the generated self-signed
	// tunnel certificate (FW_GW_TUNNEL_CERT, FW_GW_TUNNEL_KEY).
	TunnelCertFile, TunnelKeyFile string

	// UpdateDir is where the gateway asks the root updater for a release
	// (FW_GW_UPDATE_DIR); UpdateStatusDir is where the updater reports
	// (FW_GW_UPDATE_STATUS_DIR). Updates are offered when UpdateDir exists.
	UpdateDir, UpdateStatusDir string

	// DataDir holds the gateway key, the pinned server key, the pairing code
	// and certificates (FW_GW_DATA_DIR).
	DataDir string
}

// Paths in DataDir.
func (c Config) keyPath() string        { return filepath.Join(c.DataDir, "gateway.key") }
func (c Config) serverKeyPath() string  { return filepath.Join(c.DataDir, "server.pub") }
func (c Config) pairingPath() string    { return filepath.Join(c.DataDir, "pairing-code") }
func (c Config) tunnelCertPath() string { return filepath.Join(c.DataDir, "tunnel.crt") }
func (c Config) tunnelKeyPath() string  { return filepath.Join(c.DataDir, "tunnel.key") }
func (c Config) acmeDir() string        { return filepath.Join(c.DataDir, "acme") }

// EnvFile is the packaged configuration file.
const EnvFile = "/etc/fleetwarden-gateway/gateway.env"

// FromEnv reads the configuration.
func FromEnv() (Config, error) {
	c := Config{
		TLSMode:        strings.ToLower(getenv("FW_GW_TLS", TLSProxy)),
		CertFile:       os.Getenv("FW_GW_TLS_CERT"),
		KeyFile:        os.Getenv("FW_GW_TLS_KEY"),
		ACMEEmail:      os.Getenv("FW_GW_ACME_EMAIL"),
		ACMEHTTPListen: getenv("FW_GW_ACME_HTTP_LISTEN", ":80"),
		TunnelListen:   getenv("FW_GW_TUNNEL_LISTEN", fmt.Sprintf(":%d", gwproto.DefaultTunnelPort)),
		TunnelCertFile: os.Getenv("FW_GW_TUNNEL_CERT"),
		TunnelKeyFile:  os.Getenv("FW_GW_TUNNEL_KEY"),
		DataDir:        getenv("FW_GW_DATA_DIR", "/var/lib/fleetwarden-gateway"),
	}
	c.UpdateDir = getenv("FW_GW_UPDATE_DIR", c.DataDir+"/update")
	c.UpdateStatusDir = getenv("FW_GW_UPDATE_STATUS_DIR", "/var/lib/fleetwarden-gateway-update")
	for _, d := range strings.Split(os.Getenv("FW_GW_ACME_DOMAINS"), ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			c.ACMEDomains = append(c.ACMEDomains, d)
		}
	}
	switch c.TLSMode {
	case TLSProxy:
		c.Listen = getenv("FW_GW_LISTEN", "127.0.0.1:8080")
	case TLSFiles:
		c.Listen = getenv("FW_GW_LISTEN", ":443")
		if c.CertFile == "" || c.KeyFile == "" {
			return c, fmt.Errorf("FW_GW_TLS=files needs FW_GW_TLS_CERT and FW_GW_TLS_KEY")
		}
	case TLSACME:
		c.Listen = getenv("FW_GW_LISTEN", ":443")
		if len(c.ACMEDomains) == 0 {
			return c, fmt.Errorf("FW_GW_TLS=acme needs FW_GW_ACME_DOMAINS")
		}
	default:
		return c, fmt.Errorf("FW_GW_TLS must be proxy, files or acme, not %q", c.TLSMode)
	}
	var err error
	if c.TrustedProxies, err = config.ParsePrefixes(getenv("FW_GW_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")); err != nil {
		return c, fmt.Errorf("FW_GW_TRUSTED_PROXIES: %w", err)
	}
	if c.TunnelAllow, err = config.ParsePrefixes(os.Getenv("FW_GW_TUNNEL_ALLOW")); err != nil {
		return c, fmt.Errorf("FW_GW_TUNNEL_ALLOW: %w", err)
	}
	if (c.TunnelCertFile == "") != (c.TunnelKeyFile == "") {
		return c, fmt.Errorf("set both FW_GW_TUNNEL_CERT and FW_GW_TUNNEL_KEY, or neither")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}
