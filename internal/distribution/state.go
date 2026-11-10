package distribution

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Identity is what enrollment gave the distribution server.
type Identity struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Server     string    `json:"server"`
	EnrolledAt time.Time `json:"enrolledAt"`
}

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

// LoadIdentity returns the enrollment, or ok false before enrolling.
func LoadIdentity(c Config) (Identity, bool, error) {
	raw, err := os.ReadFile(c.identityPath())
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil || id.ID == "" {
		return Identity{}, false, fmt.Errorf("%s: unreadable", c.identityPath())
	}
	return id, true, nil
}

func saveIdentity(c Config, id Identity) error {
	raw, _ := json.MarshalIndent(id, "", "  ")
	return writeFile(c.identityPath(), raw)
}

// loadOrCreateCert returns the agent-facing certificate: the configured
// files, or a self-signed certificate made on first start. Agents pin its
// hash, which the management server passes on, so no CA is involved.
func loadOrCreateCert(c Config) (tls.Certificate, error) {
	if c.CertFile != "" {
		return loadCert(c.CertFile, c.KeyFile)
	}
	if cert, err := loadCert(c.certPath(), c.certKeyPath()); err == nil {
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
		Subject:      pkix.Name{CommonName: "FleetWarden distribution server " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"fleetwarden-distribution"},
	}
	if host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	for _, a := range localAddresses() {
		if ip := net.ParseIP(a); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFile(c.certPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeFile(c.certKeyPath(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return tls.Certificate{}, err
	}
	return loadCert(c.certPath(), c.certKeyPath())
}

func loadCert(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return cert, err
	}
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	return cert, nil
}

// localAddresses are the machine's unicast addresses, for the management
// server to suggest the address agents use.
func localAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := []string{}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() || n.IP.IsMulticast() {
			continue
		}
		out = append(out, n.IP.String())
	}
	return out
}
