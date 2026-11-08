package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/acme/autocert"

	"github.com/rhisijmuiden/fleetwarden/internal/gwproto"
)

// Limits on the agent side.
const (
	maxBodyBytes    = 64 << 20 // agents post small JSON; inventories go over the WebSocket
	tunnelReadLimit = 64 << 20
	pairAttempts    = 5
	pairWindow      = 15 * time.Minute
)

var errNoTunnel = errors.New("the management server is not connected to this gateway")

// Gateway relays agent requests to the management server over the tunnel.
type Gateway struct {
	cfg     Config
	log     *slog.Logger
	version string
	key     ed25519.PrivateKey
	cert    tls.Certificate
	hash    []byte // SHA-256 of the tunnel certificate

	mu          sync.Mutex
	sess        *yamux.Session
	connectedAt time.Time
	serverAddr  string
	pairFails   []time.Time

	transport *http.Transport
	proxy     *httputil.ReverseProxy
	started   time.Time

	requests atomic.Int64
	sockets  atomic.Int64
	rejected atomic.Int64
}

// New loads (or creates) the gateway identity and tunnel certificate.
func New(cfg Config, log *slog.Logger, version string) (*Gateway, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(cfg.keyPath())
	if err != nil {
		return nil, err
	}
	cert, err := loadOrCreateTunnelCert(cfg)
	if err != nil {
		return nil, fmt.Errorf("tunnel certificate: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("tunnel certificate: empty chain")
	}
	g := &Gateway{cfg: cfg, log: log, version: version, key: key, cert: cert, hash: gwproto.CertHash(cert.Certificate[0]), started: time.Now()}
	g.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			s := g.session()
			if s == nil {
				return nil, errNoTunnel
			}
			return s.OpenStream()
		},
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
	}
	g.proxy = &httputil.ReverseProxy{
		Rewrite:       g.rewrite,
		Transport:     g.transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			g.log.Warn("relay failed", "path", r.URL.Path, "err", err)
			jsonError(w, http.StatusBadGateway, "server_unavailable")
		},
	}
	return g, nil
}

// PublicKey is the gateway's identity.
func (g *Gateway) PublicKey() ed25519.PublicKey { return g.key.Public().(ed25519.PublicKey) }

// TunnelCertificate is the certificate the tunnel listener presents.
func (g *Gateway) TunnelCertificate() tls.Certificate { return g.cert }

func (g *Gateway) session() *yamux.Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sess != nil && g.sess.IsClosed() {
		g.sess = nil
	}
	return g.sess
}

// Connected reports whether the management server's tunnel is up.
func (g *Gateway) Connected() bool { return g.session() != nil }

func (g *Gateway) setSession(s *yamux.Session, addr string) {
	g.mu.Lock()
	old := g.sess
	g.sess, g.connectedAt, g.serverAddr = s, time.Now(), addr
	g.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	g.transport.CloseIdleConnections()
}

func (g *Gateway) clearSession(s *yamux.Session) {
	g.mu.Lock()
	if g.sess == s {
		g.sess = nil
	}
	g.mu.Unlock()
	g.transport.CloseIdleConnections()
}

func (g *Gateway) allowPairing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cut := time.Now().Add(-pairWindow)
	kept := g.pairFails[:0]
	for _, t := range g.pairFails {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	g.pairFails = kept
	return len(kept) < pairAttempts
}

func (g *Gateway) pairFailed() {
	g.mu.Lock()
	g.pairFails = append(g.pairFails, time.Now())
	g.mu.Unlock()
}

// ---- tunnel side ----

func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

func contains(list []netip.Prefix, a netip.Addr) bool {
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// TunnelHandler accepts the management server's tunnel.
func (g *Gateway) TunnelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != gwproto.PathTunnel {
			http.NotFound(w, r)
			return
		}
		peer := remoteAddr(r)
		if len(g.cfg.TunnelAllow) > 0 && !contains(g.cfg.TunnelAllow, peer) {
			g.log.Warn("tunnel refused: address not in FW_GW_TUNNEL_ALLOW", "peer", peer.String())
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer ws.CloseNow() //nolint:errcheck
		pinned, err := loadServerKey(g.cfg.serverKeyPath())
		if err != nil {
			g.log.Error("reading the paired server key", "err", err)
			return
		}
		code := ""
		if pinned == nil {
			if code, err = loadOrCreatePairingCode(g.cfg.pairingPath()); err != nil {
				g.log.Error("reading the pairing code", "err", err)
				return
			}
		}
		res, err := gwproto.GatewayHandshake(r.Context(), ws, gwproto.GatewaySide{
			Key: g.key, Version: g.version, Pinned: pinned, PairingCode: code, CertHash: g.hash,
			AllowAttempt: g.allowPairing, Failed: g.pairFailed,
		})
		if err != nil {
			g.log.Warn("tunnel handshake failed", "peer", peer.String(), "err", err)
			ws.Close(websocket.StatusPolicyViolation, "handshake failed") //nolint:errcheck
			return
		}
		if res.Paired {
			if err := writeFile(g.cfg.serverKeyPath(), res.PublicKey); err != nil {
				g.log.Error("saving the paired server key", "err", err)
				ws.Close(websocket.StatusInternalError, "") //nolint:errcheck
				return
			}
			_ = os.Remove(g.cfg.pairingPath())
			g.log.Info("paired with the management server", "server", gwproto.Fingerprint(res.PublicKey), "peer", peer.String())
		}
		ws.SetReadLimit(tunnelReadLimit)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		sess, err := yamux.Server(conn, gwproto.MuxConfig())
		if err != nil {
			return
		}
		defer sess.Close()
		g.setSession(sess, peer.String())
		defer g.clearSession(sess)
		g.log.Info("management server connected", "peer", peer.String(), "serverVersion", res.Version)
		ctl := &http.Server{Handler: g.controlHandler(), ReadHeaderTimeout: 30 * time.Second}
		go ctl.Serve(sess) //nolint:errcheck // ends when the session closes
		<-sess.CloseChan()
		g.log.Info("management server disconnected", "peer", peer.String())
	})
}

func (g *Gateway) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+gwproto.PathStatus, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, g.Status())
	})
	mux.HandleFunc("POST "+gwproto.PathUpdate, g.handleUpdate)
	return mux
}

// Status describes the gateway for the management server.
func (g *Gateway) Status() gwproto.Status {
	g.mu.Lock()
	connected := g.connectedAt
	g.mu.Unlock()
	host, _ := os.Hostname()
	return gwproto.Status{
		Version: g.version, Protocol: gwproto.Version, StartedAt: g.started.UTC(), ConnectedAt: connected.UTC(), Hostname: host,
		Listen: []string{g.cfg.Listen}, TLSMode: g.cfg.TLSMode,
		Requests: g.requests.Load(), ActiveSocket: g.sockets.Load(), Rejected: g.rejected.Load(),
		Updatable: g.updatable(), Update: g.updateStatus(),
	}
}

// ---- agent side ----

// AgentHandler serves agents: only the agent paths are relayed.
func (g *Gateway) AgentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			code, state := http.StatusOK, "connected"
			if !g.Connected() {
				code, state = http.StatusServiceUnavailable, "disconnected"
			}
			writeJSON(w, code, map[string]string{"status": "ok", "server": state})
			return
		}
		if !strings.HasPrefix(r.URL.Path, gwproto.AgentPrefix) || strings.Contains(r.URL.Path, "..") ||
			strings.Contains(r.URL.RawPath, "%") || (r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead) {
			g.rejected.Add(1)
			http.NotFound(w, r)
			return
		}
		if !g.Connected() {
			jsonError(w, http.StatusServiceUnavailable, "server_unavailable")
			return
		}
		g.requests.Add(1)
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			g.sockets.Add(1)
			defer g.sockets.Add(-1)
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		g.proxy.ServeHTTP(w, r)
	})
}

// clientInfo is the agent's address, scheme and host as the gateway saw
// them (through a trusted reverse proxy in proxy mode).
func (g *Gateway) clientInfo(r *http.Request) (ip, scheme, host string) {
	peer := remoteAddr(r)
	ip, scheme, host = peer.String(), "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if g.cfg.TLSMode != TLSProxy || !contains(g.cfg.TrustedProxies, peer) {
		return ip, scheme, host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		ip = a.Unmap().String()
		if !contains(g.cfg.TrustedProxies, a.Unmap()) {
			break
		}
	}
	if p := strings.ToLower(first(r.Header.Get("X-Forwarded-Proto"))); p == "https" || p == "http" {
		scheme = p
	}
	if h := first(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	return ip, scheme, host
}

func first(v string) string {
	f, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(f)
}

func (g *Gateway) rewrite(pr *httputil.ProxyRequest) {
	ip, scheme, host := g.clientInfo(pr.In)
	pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "fleetwarden-server"
	pr.Out.Host = host
	pr.Out.Header.Set("X-Forwarded-For", ip)
	pr.Out.Header.Set("X-Forwarded-Proto", scheme)
	pr.Out.Header.Set("X-Forwarded-Host", host)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, errCode string) {
	writeJSON(w, code, map[string]string{"error": errCode})
}

// ---- listeners ----

// Run serves the agent and tunnel listeners until ctx ends.
func (g *Gateway) Run(ctx context.Context) error {
	tunnelTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{g.cert}}
	servers := []*http.Server{}
	errc := make(chan error, 3)
	start := func(srv *http.Server, ln net.Listener, name string) {
		servers = append(servers, srv)
		g.log.Info("listening", "listener", name, "addr", ln.Addr().String())
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}

	tln, err := net.Listen("tcp", g.cfg.TunnelListen)
	if err != nil {
		return fmt.Errorf("tunnel listener: %w", err)
	}
	start(&http.Server{Handler: g.TunnelHandler(), ReadHeaderTimeout: 10 * time.Second}, tls.NewListener(tln, tunnelTLS), "tunnel")

	agent := &http.Server{Handler: g.AgentHandler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	aln, err := net.Listen("tcp", g.cfg.Listen)
	if err != nil {
		return fmt.Errorf("agent listener: %w", err)
	}
	switch g.cfg.TLSMode {
	case TLSFiles:
		cert, err := tls.LoadX509KeyPair(g.cfg.CertFile, g.cfg.KeyFile)
		if err != nil {
			return fmt.Errorf("agent certificate: %w", err)
		}
		agent.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
		start(agent, tls.NewListener(aln, agent.TLSConfig), "agents (TLS)")
	case TLSACME:
		m := &autocert.Manager{
			Prompt: autocert.AcceptTOS, Cache: autocert.DirCache(g.cfg.acmeDir()),
			HostPolicy: autocert.HostWhitelist(g.cfg.ACMEDomains...), Email: g.cfg.ACMEEmail,
		}
		agent.TLSConfig = m.TLSConfig()
		agent.TLSConfig.MinVersion = tls.VersionTLS12
		hln, err := net.Listen("tcp", g.cfg.ACMEHTTPListen)
		if err != nil {
			return fmt.Errorf("ACME HTTP listener: %w", err)
		}
		start(&http.Server{Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}, hln, "acme-http")
		start(agent, tls.NewListener(aln, agent.TLSConfig), "agents (Let's Encrypt)")
	default:
		start(agent, aln, "agents (behind reverse proxy)")
	}

	info, err := ReadPairing(g.cfg)
	if err == nil && !info.Paired {
		g.log.Info("waiting for pairing: add this gateway in the web interface under Admin > Secure gateway",
			"pairingCode", info.Code, "fingerprint", info.Fingerprint)
	}
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s := g.session(); s != nil {
		_ = s.Close()
	}
	for _, srv := range servers {
		_ = srv.Shutdown(sctx)
	}
	return runErr
}
