package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
)

func TestTrustedProxiesFromSettings(t *testing.T) {
	env, _ := config.ParsePrefixes("127.0.0.1/32")
	s := New(config.Server{TrustedProxies: env}, Deps{})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:4444"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "em.example.com")

	ctx := s.withProxyContext(req)
	if ip := ctx.Value(clientIPKey); ip != "10.0.0.5" {
		t.Fatalf("untrusted proxy: client ip %v", ip)
	}
	s.applyProxySettings(proxySettings{TrustedProxies: []string{"10.0.0.0/24"}, HSTS: true})
	ctx = s.withProxyContext(req)
	if ip, scheme, host := ctx.Value(clientIPKey), ctx.Value(schemeKey), ctx.Value(hostKey); ip != "203.0.113.9" || scheme != "https" || host != "em.example.com" {
		t.Fatalf("trusted proxy: %v %v %v", ip, scheme, host)
	}
	// The environment list always stays trusted.
	if got := s.trustedProxies(); len(got) != 2 || got[0] != netip.MustParsePrefix("127.0.0.1/32") {
		t.Fatalf("trusted list: %v", got)
	}
	rec := httptest.NewRecorder()
	s.withClientIP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header missing")
	}
}

func TestProxySettingsAndEndToEndTest(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	var v proxyView
	if code := admin.do("GET", "/api/v1/settings/proxy", nil, &v); code != 200 || v.Kind != "zoraxy" || v.TLSMode != "acme" || len(v.EnvTrustedProxies) != 1 {
		t.Fatalf("defaults: %d %+v", code, v)
	}
	for _, bad := range []map[string]any{
		{"trustedProxies": []string{"not-an-ip"}},
		{"trustedProxies": []string{"0.0.0.0/0"}},
		{"upstream": "10.0.0.1"},
		{"upstream": "10.0.0.1:99999"},
		{"publicUrl": "ftp://x"},
	} {
		if code := admin.do("PUT", "/api/v1/settings/proxy", bad, nil); code != 400 {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}

	// A reverse proxy in front of the server, like nginx would be.
	backend, _ := url.Parse(e.ts.URL)
	rp := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(backend)
		pr.Out.Host = pr.In.Host
		pr.SetXForwarded()
	}}
	proxy := httptest.NewServer(rp)
	t.Cleanup(proxy.Close)

	if code := admin.do("PUT", "/api/v1/settings/proxy", map[string]any{
		"trustedProxies": []string{"192.0.2.10", "10.1.0.0/16"}, "kind": "nginx", "upstream": "127.0.0.1:8080", "tlsMode": "own",
		"hsts": false, "publicUrl": proxy.URL + "/",
	}, &v); code != 200 || len(v.TrustedProxies) != 2 || v.TrustedProxies[0] != "192.0.2.10/32" || v.PublicURL != proxy.URL || v.Kind != "nginx" {
		t.Fatalf("save: %d %+v", code, v)
	}

	var res proxyTestResult
	if code := admin.do("POST", "/api/v1/settings/proxy/test", map[string]string{}, &res); code != 200 || res.PublicURL != proxy.URL {
		t.Fatalf("test: %d %+v", code, res)
	}
	want := map[string]string{
		"listen": "pass", "public_url": "warn", "dns": "pass", "tls": "skip", "reach": "pass", "peer_trusted": "pass",
		"forwarded_proto": "pass", "forwarded_host": "pass", "forwarded_for": "pass", "agent_hello": "pass", "websocket": "pass", "http_redirect": "skip",
	}
	for _, c := range res.Checks {
		if want[c.ID] != c.Status {
			t.Errorf("check %s: %s (%s), want %s", c.ID, c.Status, c.Detail, want[c.ID])
		}
		delete(want, c.ID)
	}
	if len(want) > 0 {
		t.Errorf("missing checks: %v", want)
	}

	// The browser-side probes work with the nonce, through the proxy.
	var pv proxyRequestView
	if code := admin.do("GET", "/api/v1/proxy-probe/"+res.Nonce, nil, &pv); code != 200 || pv.Nonce != res.Nonce {
		t.Fatalf("probe: %d %+v", code, pv)
	}
	if code := admin.do("GET", "/api/v1/proxy-probe/0123456789abcdef", nil, nil); code != 404 {
		t.Errorf("unknown nonce: %d", code)
	}
	req, _ := http.NewRequest("POST", proxy.URL+"/api/v1/proxy-probe/"+res.Nonce+"/upload", bytes.NewReader(make([]byte, 5<<20)))
	req.Header.Set("X-Requested-With", "fleetwarden")
	up, err := http.DefaultClient.Do(req)
	if err != nil || up.StatusCode != 200 {
		t.Fatalf("upload through proxy: %v %v", err, up)
	}
	up.Body.Close()

	// No public URL and nothing configured: the test says so.
	admin.do("PUT", "/api/v1/settings/proxy", map[string]any{"publicUrl": ""}, nil)
	admin.do("POST", "/api/v1/settings/proxy/test", map[string]string{}, &res)
	if res.Checks[1].ID != "public_url" || res.Checks[1].Status != "fail" {
		t.Errorf("without url: %+v", res.Checks[1])
	}
}
