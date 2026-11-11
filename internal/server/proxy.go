package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Reverse proxy settings: which proxies may set X-Forwarded-* (on top of
// FW_TRUSTED_PROXIES), HSTS, and the inputs of the configuration examples the
// web interface generates. A test checks the whole path end to end.

const keyProxy = "proxy"

const (
	probeTTL       = 3 * time.Minute
	probeUploadMax = 64 << 20
	probeTimeout   = 10 * time.Second
)

type proxySettings struct {
	TrustedProxies []string `json:"trustedProxies"`
	HSTS           bool     `json:"hsts"`
	// Inputs of the generated examples.
	Kind     string `json:"kind"`     // nginx, caddy, haproxy, zoraxy
	Upstream string `json:"upstream"` // host:port the proxy forwards to
	TLSMode  string `json:"tlsMode"`  // acme (Let's Encrypt) or own (own certificate files)
}

var proxyKinds = []string{"nginx", "caddy", "haproxy", "zoraxy"}

// proxyRequestView describes how the server sees one request.
type proxyRequestView struct {
	Peer           string `json:"peer"`
	PeerTrusted    bool   `json:"peerTrusted"`
	ClientIP       string `json:"clientIp"`
	Scheme         string `json:"scheme"`
	Host           string `json:"host"`
	ForwardedFor   string `json:"forwardedFor"`
	ForwardedProto string `json:"forwardedProto"`
	ForwardedHost  string `json:"forwardedHost"`
	Nonce          string `json:"nonce,omitempty"`
}

type proxyView struct {
	proxySettings
	PublicURL         string           `json:"publicUrl"`
	DetectedURL       string           `json:"detectedUrl"`
	EnvTrustedProxies []string         `json:"envTrustedProxies"`
	Listen            string           `json:"listen"`
	ListenLocalOnly   bool             `json:"listenLocalOnly"`
	ServerAddresses   []string         `json:"serverAddresses"`
	EnvFile           string           `json:"envFile"`
	Request           proxyRequestView `json:"request"`
	MaxUploadMiB      int              `json:"maxUploadMiB"`
}

func (s *Server) proxyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/settings/proxy", s.authed(auth.PermSettingsManage, 0, s.handleGetProxy))
	mux.Handle("PUT /api/v1/settings/proxy", s.authed(auth.PermSettingsManage, 0, s.handlePutProxy))
	mux.Handle("POST /api/v1/settings/proxy/test", s.authed(auth.PermSettingsManage, 0, s.handleTestProxy))
	// Probes answer only for a nonce a running test created, so they can be
	// reached through the proxy without a session (as an agent would).
	mux.HandleFunc("GET /api/v1/proxy-probe/{nonce}", s.handleProbe)
	mux.HandleFunc("GET /api/v1/proxy-probe/{nonce}/ws", s.handleProbeWS)
	mux.HandleFunc("POST /api/v1/proxy-probe/{nonce}/upload", s.handleProbeUpload)
}

// trustedProxies are the addresses from FW_TRUSTED_PROXIES plus the ones set
// in the web interface.
func (s *Server) trustedProxies() []netip.Prefix {
	if p := s.trusted.Load(); p != nil {
		return *p
	}
	return s.cfg.TrustedProxies
}

func (s *Server) applyProxySettings(p proxySettings) {
	all := slices.Clone(s.cfg.TrustedProxies)
	for _, v := range p.TrustedProxies {
		if pf, err := parsePrefix(v); err == nil && !slices.Contains(all, pf) {
			all = append(all, pf)
		}
	}
	s.trusted.Store(&all)
	s.hsts.Store(p.HSTS)
}

func (s *Server) loadProxySettings(ctx context.Context) proxySettings {
	var p proxySettings
	if err := s.st.GetSetting(ctx, keyProxy, &p); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("proxy settings", "err", err)
	}
	if p.TrustedProxies == nil {
		p.TrustedProxies = []string{}
	}
	if !slices.Contains(proxyKinds, p.Kind) {
		p.Kind = "zoraxy"
	}
	if p.TLSMode != "own" {
		p.TLSMode = "acme"
	}
	return p
}

func parsePrefix(v string) (netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, "/") {
		a, err := netip.ParseAddr(v)
		if err != nil {
			return netip.Prefix{}, err
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(v)
	return p.Masked(), err
}

func (s *Server) requestView(r *http.Request) proxyRequestView {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	scheme, _ := r.Context().Value(schemeKey).(string)
	host, _ := r.Context().Value(hostKey).(string)
	return proxyRequestView{
		Peer: peer, PeerTrusted: peerTrusted(r, s.trustedProxies()), ClientIP: ClientIP(r), Scheme: scheme, Host: host,
		ForwardedFor: r.Header.Get("X-Forwarded-For"), ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
		ForwardedHost: r.Header.Get("X-Forwarded-Host"),
	}
}

// serverAddresses are the non-loopback addresses of this host, to fill in the
// upstream of the examples.
func serverAddresses() []string {
	out := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		pf, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := pf.Addr()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}

func listenLocalOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func (s *Server) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	p := s.loadProxySettings(r.Context())
	var g generalSettings
	_ = s.st.GetSetting(r.Context(), keyGeneral, &g)
	env := []string{}
	for _, pf := range s.cfg.TrustedProxies {
		env = append(env, pf.String())
	}
	listen := s.cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	writeJSON(w, http.StatusOK, proxyView{
		proxySettings: p, PublicURL: g.PublicURL, DetectedURL: s.detectedURL(r), EnvTrustedProxies: env,
		Listen: listen, ListenLocalOnly: listenLocalOnly(listen), ServerAddresses: serverAddresses(),
		EnvFile: s.cfg.EnvFile, Request: s.requestView(r), MaxUploadMiB: maxUploadSize >> 20,
	})
}

type proxyPut struct {
	proxySettings
	PublicURL *string `json:"publicUrl"`
}

func (s *Server) handlePutProxy(w http.ResponseWriter, r *http.Request) {
	var req proxyPut
	if !decode(w, r, &req) {
		return
	}
	p := req.proxySettings
	nets := []string{}
	for _, n := range p.TrustedProxies {
		if strings.TrimSpace(n) == "" {
			continue
		}
		pf, err := parsePrefix(n)
		if err != nil {
			apiErrorDetail(w, http.StatusBadRequest, "invalid_network", n)
			return
		}
		// Trusting everyone would let any client spoof its address.
		if pf.Bits() == 0 {
			apiErrorDetail(w, http.StatusBadRequest, "network_too_wide", n)
			return
		}
		if !slices.Contains(nets, pf.String()) {
			nets = append(nets, pf.String())
		}
	}
	p.TrustedProxies = nets
	if !slices.Contains(proxyKinds, p.Kind) {
		p.Kind = "zoraxy"
	}
	if p.TLSMode != "own" {
		p.TLSMode = "acme"
	}
	p.Upstream = strings.TrimSpace(p.Upstream)
	if p.Upstream != "" {
		host, port, err := net.SplitHostPort(p.Upstream)
		n, perr := strconv.Atoi(port)
		if err != nil || host == "" || perr != nil || n < 1 || n > 65535 || strings.ContainsAny(host, " /;{}\"'") {
			apiError(w, http.StatusBadRequest, "invalid_upstream")
			return
		}
	}
	if req.PublicURL != nil {
		u, ok := normalizePublicURL(*req.PublicURL)
		if !ok {
			apiError(w, http.StatusBadRequest, "invalid_url")
			return
		}
		if err := s.st.PutSetting(r.Context(), keyGeneral, generalSettings{PublicURL: u}); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if err := s.st.PutSetting(r.Context(), keyProxy, p); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.applyProxySettings(p)
	details := map[string]any{"trustedProxies": nets, "hsts": p.HSTS, "kind": p.Kind, "upstream": p.Upstream}
	if req.PublicURL != nil {
		details["publicUrl"] = *req.PublicURL
		s.broadcastEndpoints(r.Context())
	}
	s.audit(r, "settings.proxy", "proxy", true, details)
	// The response reflects the new trust list for this very request.
	r = r.WithContext(s.withProxyContext(r))
	s.handleGetProxy(w, r)
}

// normalizePublicURL validates an external base URL; "" clears it.
func normalizePublicURL(v string) (string, bool) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", true
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.User != nil {
		return "", false
	}
	return v, true
}

// ---- end-to-end test ----

type proxyCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pass, warn, fail, skip
	Detail string `json:"detail,omitempty"`
}

type proxyTestResult struct {
	PublicURL string       `json:"publicUrl"`
	Nonce     string       `json:"nonce"`
	Checks    []proxyCheck `json:"checks"`
}

type probeSet struct {
	mu     sync.Mutex
	nonces map[string]time.Time
}

func (p *probeSet) create(now time.Time) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	n := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nonces == nil {
		p.nonces = map[string]time.Time{}
	}
	for k, exp := range p.nonces {
		if now.After(exp) {
			delete(p.nonces, k)
		}
	}
	p.nonces[n] = now.Add(probeTTL)
	return n
}

func (p *probeSet) valid(n string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.nonces[n]
	return ok && now.Before(exp)
}

func (s *Server) probeOK(w http.ResponseWriter, r *http.Request) bool {
	if !s.probes.valid(r.PathValue("nonce"), s.now()) {
		apiError(w, http.StatusNotFound, "not_found")
		return false
	}
	return true
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if !s.probeOK(w, r) {
		return
	}
	v := s.requestView(r)
	v.Nonce = r.PathValue("nonce")
	writeJSON(w, http.StatusOK, v)
}

// handleProbeWS echoes text messages, with the same origin rules as the
// remote control viewer.
func (s *Server) handleProbeWS(w http.ResponseWriter, r *http.Request) {
	if !s.probeOK(w, r) {
		return
	}
	ws, err := s.acceptBrowserWS(w, r)
	if err != nil {
		return
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(4 << 10)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if err := ws.Write(ctx, typ, data); err != nil {
			return
		}
	}
	ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
}

func (s *Server) handleProbeUpload(w http.ResponseWriter, r *http.Request) {
	if !s.probeOK(w, r) {
		return
	}
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, probeUploadMax))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"bytes": n})
}

type proxyTestReq struct {
	PublicURL string `json:"publicUrl"`
}

func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	var req proxyTestReq
	if !decode(w, r, &req) {
		return
	}
	pub, ok := normalizePublicURL(req.PublicURL)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_url")
		return
	}
	if pub == "" {
		var g generalSettings
		_ = s.st.GetSetting(r.Context(), keyGeneral, &g)
		pub = g.PublicURL
	}
	res := proxyTestResult{PublicURL: pub, Nonce: s.probes.create(s.now())}
	res.Checks = s.runProxyChecks(r.Context(), pub, res.Nonce)
	failed := 0
	for _, c := range res.Checks {
		if c.Status == "fail" {
			failed++
		}
	}
	s.audit(r, "settings.proxy.test", pub, failed == 0, map[string]any{"failed": failed})
	writeJSON(w, http.StatusOK, res)
}

// runProxyChecks reaches the server through its public URL, the way agents do.
func (s *Server) runProxyChecks(ctx context.Context, pub, nonce string) []proxyCheck {
	var out []proxyCheck
	add := func(id, status, detail string) { out = append(out, proxyCheck{ID: id, Status: status, Detail: detail}) }
	skipRest := func(ids ...string) {
		for _, id := range ids {
			add(id, "skip", "")
		}
	}
	all := []string{"dns", "tls", "reach", "peer_trusted", "forwarded_proto", "forwarded_host", "forwarded_for", "client_ip", "agent_hello", "websocket", "http_redirect"}

	add("listen", "pass", s.cfg.Listen)
	if pub == "" {
		add("public_url", "fail", "")
		skipRest(all...)
		return out
	}
	u, _ := url.Parse(pub)
	if u.Scheme != "https" {
		add("public_url", "warn", pub)
	} else {
		add("public_url", "pass", pub)
	}

	// DNS
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		add("dns", "pass", ip.String())
	} else {
		rctx, cancel := context.WithTimeout(ctx, probeTimeout)
		addrs, err := net.DefaultResolver.LookupHost(rctx, host)
		cancel()
		if err != nil || len(addrs) == 0 {
			add("dns", "fail", errText(err))
			skipRest(all[1:]...)
			return out
		}
		add("dns", "pass", strings.Join(addrs, ", "))
	}

	client := s.probeClient()

	// TLS certificate
	if u.Scheme == "https" {
		st, d := s.checkTLS(ctx, u)
		add("tls", st, d)
	} else {
		add("tls", "skip", "")
	}
	if last := out[len(out)-1]; last.Status == "fail" {
		// Without a valid certificate agents refuse to connect; still show what the proxy forwards.
		client = s.probeClient()
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // diagnostics only
	}

	// Reach the probe and see what the proxy forwards.
	var v proxyRequestView
	err := getJSON(ctx, client, pub+"/api/v1/proxy-probe/"+nonce, &v)
	switch {
	case err != nil:
		add("reach", "fail", errText(err))
		skipRest(all[3:]...)
		return out
	case v.Nonce != nonce:
		add("reach", "fail", "wrong_backend")
		skipRest(all[3:]...)
		return out
	default:
		add("reach", "pass", "")
	}
	if v.PeerTrusted {
		add("peer_trusted", "pass", v.Peer)
	} else {
		add("peer_trusted", "fail", v.Peer)
	}
	switch fp := strings.ToLower(firstHeaderValue(v.ForwardedProto)); {
	case fp == u.Scheme:
		add("forwarded_proto", "pass", fp)
	case fp == "":
		add("forwarded_proto", "fail", "–")
	default:
		add("forwarded_proto", "fail", fp)
	}
	switch {
	case strings.EqualFold(v.Host, u.Host):
		add("forwarded_host", "pass", v.Host)
	default:
		add("forwarded_host", "warn", v.Host)
	}
	if v.ForwardedFor != "" {
		add("forwarded_for", "pass", v.ForwardedFor)
	} else {
		add("forwarded_for", "fail", "")
	}
	// With another proxy in front (Cloudflare, a load balancer) that is not
	// trusted, every client seems to come from that proxy: rate limits then
	// hit all agents and users behind it at once.
	if first := firstHeaderValue(v.ForwardedFor); first != "" && v.ClientIP != first {
		add("client_ip", "warn", v.ClientIP)
	} else {
		add("client_ip", "pass", v.ClientIP)
	}

	// The agent endpoint answers with this server's key.
	var hello agentproto.Hello
	if err := getJSON(ctx, client, pub+agentproto.PathHello, &hello); err != nil {
		add("agent_hello", "fail", errText(err))
	} else if !bytes.Equal(hello.ServerPublicKey, s.agentPublicKey()) {
		add("agent_hello", "fail", "wrong_backend")
	} else {
		add("agent_hello", "pass", hello.Version)
	}

	// WebSocket upgrade (agents and remote control).
	wst, wd := s.checkWS(ctx, client, pub, nonce)
	add("websocket", wst, wd)

	// Plain HTTP should redirect to HTTPS.
	if u.Scheme == "https" {
		rst, rd := s.checkRedirect(ctx, u)
		add("http_redirect", rst, rd)
	} else {
		add("http_redirect", "skip", "")
	}
	return out
}

func (s *Server) probeClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // test the direct path, as agents on the LAN would
	return &http.Client{Transport: tr, Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *Server) checkTLS(ctx context.Context, u *url.URL) (string, string) {
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "443")
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: probeTimeout}, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}
	dctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		var cv *tls.CertificateVerificationError
		var ua x509.UnknownAuthorityError
		var hn x509.HostnameError
		switch {
		case errors.As(err, &ua):
			return "fail", "untrusted_ca"
		case errors.As(err, &hn):
			return "fail", "wrong_hostname"
		case errors.As(err, &cv):
			return "fail", "invalid_certificate: " + err.Error()
		}
		return "fail", errText(err)
	}
	defer c.Close()
	st := c.(*tls.Conn).ConnectionState()
	leaf := st.PeerCertificates[0]
	days := int(time.Until(leaf.NotAfter).Hours() / 24)
	detail := fmt.Sprintf("%s, %s, %d", leaf.Issuer.CommonName, tls.VersionName(st.Version), days)
	if days < 14 {
		return "warn", detail
	}
	return "pass", detail
}

func (s *Server) checkWS(ctx context.Context, client *http.Client, pub, nonce string) (string, string) {
	wsURL := "ws" + strings.TrimPrefix(pub, "http") + "/api/v1/proxy-probe/" + nonce + "/ws"
	dctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// Browsers send an Origin; agents do not. Send the public one, like the viewer.
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: client.Transport},
		HTTPHeader: http.Header{"Origin": {pub}}})
	if err != nil {
		return "fail", errText(err)
	}
	defer ws.CloseNow() //nolint:errcheck
	msg := "fleetwarden-" + nonce
	if err := ws.Write(dctx, websocket.MessageText, []byte(msg)); err != nil {
		return "fail", errText(err)
	}
	_, data, err := ws.Read(dctx)
	if err != nil || string(data) != msg {
		return "fail", errText(err)
	}
	ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	return "pass", ""
}

func (s *Server) checkRedirect(ctx context.Context, u *url.URL) (string, string) {
	plain := "http://" + u.Hostname() + "/"
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, http.MethodGet, plain, nil)
	res, err := s.probeClient().Do(req)
	if err != nil {
		// Port 80 closed is fine; browsers then simply need https://.
		return "pass", "closed"
	}
	defer res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode >= 300 && res.StatusCode < 400 && strings.HasPrefix(loc, "https://") {
		return "pass", loc
	}
	return "warn", strconv.Itoa(res.StatusCode)
}

func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
