// Package config loads the server configuration from environment variables.
//
// On a packaged install these come from /etc/fleetwarden/server.env, which the
// systemd unit loads with EnvironmentFile=.
package config

import (
	"fmt"
	"net/netip"
	"os"
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
}

// FromEnv reads the configuration from the environment.
func FromEnv() (Server, error) {
	c := Server{
		Listen:      getenv("FW_LISTEN", "127.0.0.1:8080"),
		WebDir:      getenv("FW_WEB_DIR", "/usr/share/fleetwarden/web"),
		DatabaseURL: os.Getenv("FW_DATABASE_URL"),
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
