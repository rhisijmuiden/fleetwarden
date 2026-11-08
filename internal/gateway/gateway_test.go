package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("FW_GW_TLS", "acme")
	t.Setenv("FW_GW_ACME_DOMAINS", "")
	if _, err := FromEnv(); err == nil {
		t.Error("acme without domains")
	}
	t.Setenv("FW_GW_ACME_DOMAINS", "Agents.Example.com, b.example.com")
	c, err := FromEnv()
	if err != nil || c.Listen != ":443" || len(c.ACMEDomains) != 2 || c.ACMEDomains[0] != "agents.example.com" || c.TunnelListen != ":8444" {
		t.Errorf("acme: %+v %v", c, err)
	}
	t.Setenv("FW_GW_TLS", "proxy")
	t.Setenv("FW_GW_TUNNEL_ALLOW", "10.0.0.5")
	c, err = FromEnv()
	if err != nil || c.Listen != "127.0.0.1:8080" || len(c.TunnelAllow) != 1 {
		t.Errorf("proxy: %+v %v", c, err)
	}
	t.Setenv("FW_GW_TLS", "bogus")
	if _, err := FromEnv(); err == nil {
		t.Error("bogus mode")
	}
}

func TestAgentHandlerFilters(t *testing.T) {
	g, err := New(Config{TLSMode: TLSProxy, DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(g.AgentHandler())
	defer ts.Close()
	for path, want := range map[string]int{
		"/healthz":                 503, // no tunnel
		"/agent/v1/hello":          503,
		"/api/v1/auth/config":      404,
		"/":                        404,
		"/agent/../api/v1/users":   404,
		"/agent/v1/files/%2e%2e/x": 404,
		"/gateway/v1/tunnel":       404,
	} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/agent/v1/hello", nil)
	if res, _ := http.DefaultClient.Do(req); res == nil || res.StatusCode != 404 {
		t.Error("DELETE passed")
	}
}

func TestClientInfo(t *testing.T) {
	g := &Gateway{cfg: Config{TLSMode: TLSProxy, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	r := httptest.NewRequest("GET", "/agent/v1/ws", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 127.0.0.1")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "agents.example.com")
	ip, scheme, host := g.clientInfo(r)
	if ip != "203.0.113.9" || scheme != "https" || host != "agents.example.com" {
		t.Errorf("trusted: %s %s %s", ip, scheme, host)
	}
	r.RemoteAddr = "198.51.100.1:5000" // not a trusted proxy: headers ignored
	ip, scheme, _ = g.clientInfo(r)
	if ip != "198.51.100.1" || scheme != "http" {
		t.Errorf("untrusted: %s %s", ip, scheme)
	}
}
