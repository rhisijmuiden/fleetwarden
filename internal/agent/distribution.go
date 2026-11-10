package agent

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
)

// distState holds the distribution servers for this agent. The list comes
// from the server on every connection and is not stored.
type distState struct {
	mu      sync.Mutex
	servers []agentproto.DistributionServer
}

const maxDistServers = 3

func (a *Agent) setDistribution(list []agentproto.DistributionServer) {
	var ok []agentproto.DistributionServer
	for _, d := range list {
		u, err := url.Parse(d.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || !distproto.ValidSHA(strings.ToLower(d.CertSHA256)) {
			continue
		}
		d.CertSHA256 = strings.ToLower(d.CertSHA256)
		ok = append(ok, d)
		if len(ok) == maxDistServers {
			break
		}
	}
	a.dist.mu.Lock()
	changed := !slices.Equal(a.dist.servers, ok)
	a.dist.servers = ok
	a.dist.mu.Unlock()
	if changed && a.Log != nil {
		names := make([]string, 0, len(ok))
		for _, d := range ok {
			names = append(names, d.Name)
		}
		a.Log.Info("distribution servers", "servers", names)
	}
}

func (a *Agent) distServers() []agentproto.DistributionServer {
	a.dist.mu.Lock()
	defer a.dist.mu.Unlock()
	return slices.Clone(a.dist.servers)
}

// distClient talks to one distribution server. Its certificate is usually
// self-signed: the agent pins the hash the management server sent instead
// of trusting a CA. No proxy is used: the server is on the local network.
func distClient(d agentproto.DistributionServer) *http.Client {
	pin := d.CertSHA256
	return &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: time.Minute, // it asks the management server first
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, //nolint:gosec // pinned below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 || distproto.CertSHA256(cs.PeerCertificates[0].Raw) != pin {
					return errors.New("the distribution server's certificate does not match")
				}
				return nil
			}},
	}}
}
