package gateway

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/gwproto"
)

// writeFile writes atomically with private permissions.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadOrCreateKey returns the gateway's Ed25519 identity.
func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s: unexpected key size", path)
		}
		return ed25519.NewKeyFromSeed(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return key, writeFile(path, key.Seed())
}

// loadServerKey returns the pinned server key, or nil while unpaired.
func loadServerKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s: unexpected key size", path)
	}
	return raw, nil
}

// loadOrCreatePairingCode returns the one-time pairing code.
func loadOrCreatePairingCode(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err == nil && gwproto.NormalizeCode(string(raw)) != "" {
		return strings.TrimSpace(string(raw)), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	code := gwproto.NewPairingCode()
	return code, writeFile(path, []byte(code+"\n"))
}

// loadOrCreateTunnelCert returns the tunnel TLS certificate: the configured
// files, or a self-signed certificate generated on first start. The server
// does not check it against a CA; the handshake binds it instead.
func loadOrCreateTunnelCert(c Config) (tls.Certificate, error) {
	if c.TunnelCertFile != "" {
		return tls.LoadX509KeyPair(c.TunnelCertFile, c.TunnelKeyFile)
	}
	if cert, err := tls.LoadX509KeyPair(c.tunnelCertPath(), c.tunnelKeyPath()); err == nil {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	host, _ := os.Hostname()
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "FleetWarden gateway " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"fleetwarden-gateway"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFile(c.tunnelCertPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFile(c.tunnelKeyPath(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(c.tunnelCertPath(), c.tunnelKeyPath())
}

// PairingInfo is what `fleetwarden-gateway pairing-code` prints.
type PairingInfo struct {
	Paired      bool
	Code        string
	Fingerprint string // of the gateway key
	Server      string // fingerprint of the paired server key
}

// ReadPairing reports the pairing state from the data directory, creating
// the key and code when they do not exist yet.
func ReadPairing(c Config) (PairingInfo, error) {
	key, err := loadOrCreateKey(c.keyPath())
	if err != nil {
		return PairingInfo{}, err
	}
	info := PairingInfo{Fingerprint: gwproto.Fingerprint(key.Public().(ed25519.PublicKey))}
	srv, err := loadServerKey(c.serverKeyPath())
	if err != nil {
		return info, err
	}
	if srv != nil {
		info.Paired, info.Server = true, gwproto.Fingerprint(srv)
		return info, nil
	}
	info.Code, err = loadOrCreatePairingCode(c.pairingPath())
	return info, err
}

// ResetPairing forgets the paired server and creates a new code. The
// gateway key stays, so its fingerprint does not change.
func ResetPairing(c Config) (PairingInfo, error) {
	if err := os.Remove(c.serverKeyPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return PairingInfo{}, err
	}
	if err := os.Remove(c.pairingPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return PairingInfo{}, err
	}
	return ReadPairing(c)
}
