// Package agent is the FleetWarden endpoint agent: enrollment, the signed
// WebSocket session with the management server, and inventory collection.
package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config is the agent's persistent state. It holds the device's private key,
// so it is only readable by root/SYSTEM.
type Config struct {
	Server string `json:"server"`
	// Token and Group are only kept until enrollment succeeds.
	Token string `json:"token,omitempty"`
	Group string `json:"group,omitempty"`

	DeviceID        string `json:"deviceId,omitempty"`
	PrivateKey      []byte `json:"privateKey,omitempty"` // Ed25519 seed
	ServerPublicKey []byte `json:"serverPublicKey,omitempty"`
}

// Enrolled reports whether the agent has a device identity.
func (c Config) Enrolled() bool {
	return c.DeviceID != "" && len(c.PrivateKey) == ed25519.SeedSize && len(c.ServerPublicKey) == ed25519.PublicKeySize
}

// Key returns the device's private key.
func (c Config) Key() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(c.PrivateKey) }

// ConfigPath is where the configuration lives (overridable with FW_AGENT_CONFIG).
func ConfigPath() string {
	if p := os.Getenv("FW_AGENT_CONFIG"); p != "" {
		return p
	}
	return defaultConfigPath()
}

// LoadConfig reads the configuration; a missing file gives an empty config.
func LoadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// SaveConfig writes the configuration atomically with private permissions.
func SaveConfig(path string, c Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictDir(dir); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NormalizeServer checks and cleans a server address. A bare host name gets https://.
func NormalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("the server address is empty")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("invalid server address %q", s)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func wsURL(server string) string {
	if strings.HasPrefix(server, "https://") {
		return "wss://" + strings.TrimPrefix(server, "https://")
	}
	return "ws://" + strings.TrimPrefix(server, "http://")
}
