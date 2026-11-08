package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/gwproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// Gateway link timing.
const (
	gatewayDialTimeout  = 15 * time.Second
	gatewayStatusEvery  = 30 * time.Second
	gatewayMaxBackoff   = time.Minute
	gatewayTestTimeout  = 15 * time.Second
	headerGatewayID     = "X-FleetWarden-Gateway"
	gatewayEndpointsKey = "agent.endpoints"
)

// gatewayRef identifies the gateway a request came through.
type gatewayRef struct {
	ID        int64
	Name      string
	PublicURL string
}

const gatewayCtxKey ctxKey = 100

func gatewayFrom(r *http.Request) (gatewayRef, bool) {
	g, ok := r.Context().Value(gatewayCtxKey).(gatewayRef)
	return g, ok
}

// ---- links: one tunnel per enabled gateway ----

type gwLink struct {
	gw     store.Gateway
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	state     string // connecting, connected, error
	since     time.Time
	lastError string
	sess      *yamux.Session
	status    *gwproto.Status
}

func (l *gwLink) set(state, errMsg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != state {
		l.since = time.Now()
	}
	l.state = state
	if errMsg != "" || state == "connected" {
		l.lastError = errMsg
	}
}

type gatewayManager struct {
	mu    sync.Mutex
	links map[int64]*gwLink
}

func (m *gatewayManager) link(id int64) *gwLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[id]
}

// linkKey is what forces a reconnect when it changes.
func linkKey(g store.Gateway) string {
	return g.Address + "\x00" + g.PublicURL + "\x00" + g.Name + "\x00" + string(g.PublicKey)
}

// reconcileGateways starts and stops tunnels to match the stored gateways.
func (s *Server) reconcileGateways(ctx context.Context) {
	if s.st == nil {
		return
	}
	gws, err := s.st.ListGateways(ctx)
	if err != nil {
		s.log.Warn("gateways", "err", err)
		return
	}
	want := map[int64]store.Gateway{}
	for _, g := range gws {
		if g.Enabled {
			want[g.ID] = g
		}
	}
	m := &s.gateways
	m.mu.Lock()
	if m.links == nil {
		m.links = map[int64]*gwLink{}
	}
	var stop []*gwLink
	for id, l := range m.links {
		if g, ok := want[id]; !ok || linkKey(g) != linkKey(l.gw) {
			stop = append(stop, l)
			delete(m.links, id)
		}
	}
	for id, g := range want {
		if _, ok := m.links[id]; ok {
			continue
		}
		lctx, cancel := context.WithCancel(s.bgCtx)
		l := &gwLink{gw: g, cancel: cancel, done: make(chan struct{}), state: "connecting", since: time.Now()}
		m.links[id] = l
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			defer close(l.done)
			s.runGatewayLink(lctx, l)
		}()
	}
	m.mu.Unlock()
	for _, l := range stop {
		l.cancel()
		l.mu.Lock()
		if l.sess != nil {
			_ = l.sess.Close()
		}
		l.mu.Unlock()
	}
}

func (s *Server) runGatewayLink(ctx context.Context, l *gwLink) {
	wait := 2 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := s.serveGateway(ctx, l)
		if ctx.Err() != nil {
			return
		}
		msg := gatewayErrorText(err)
		l.set("error", msg)
		if err != nil {
			s.log.Warn("gateway tunnel", "gateway", l.gw.Name, "err", err)
			_ = s.st.GatewayFailed(context.WithoutCancel(ctx), l.gw.ID, msg)
		}
		if time.Since(start) > 2*time.Minute {
			wait = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, gatewayMaxBackoff)
	}
}

// dialGateway opens the tunnel WebSocket and runs the handshake. pinned is
// nil to pair with code.
func dialGateway(ctx context.Context, addr string, pinned []byte, code string, key gwproto.ServerSide) (*websocket.Conn, gwproto.ServerResult, error) {
	var certHash []byte
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: gatewayDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: gatewayDialTimeout,
		// The gateway certificate is usually self-signed: the handshake
		// binds its hash to both keys instead of trusting a CA.
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, //nolint:gosec // see above
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("the gateway sent no certificate")
				}
				certHash = gwproto.CertHash(cs.PeerCertificates[0].Raw)
				return nil
			}},
	}
	dctx, cancel := context.WithTimeout(ctx, gatewayDialTimeout)
	defer cancel()
	ws, res, err := websocket.Dial(dctx, "wss://"+addr+gwproto.PathTunnel, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: tr}, CompressionMode: websocket.CompressionDisabled,
		HTTPHeader: http.Header{"User-Agent": {"fleetwarden-server/" + version.Version}},
	})
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	if err != nil {
		return nil, gwproto.ServerResult{}, &gwproto.LocalError{Code: "unreachable", Detail: err.Error()}
	}
	key.Pinned, key.PairingCode, key.CertHash = pinned, code, certHash
	out, err := gwproto.ServerHandshake(ctx, ws, key)
	if err != nil {
		ws.CloseNow() //nolint:errcheck
		return nil, out, err
	}
	return ws, out, nil
}

func (s *Server) serverSide() gwproto.ServerSide {
	return gwproto.ServerSide{Key: s.agentKey, Version: version.Version}
}

// serveGateway keeps one tunnel open until it breaks.
func (s *Server) serveGateway(ctx context.Context, l *gwLink) error {
	l.set("connecting", "")
	ws, res, err := dialGateway(ctx, l.gw.Address, l.gw.PublicKey, "", s.serverSide())
	if err != nil {
		return err
	}
	ws.SetReadLimit(64 << 20)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := websocket.NetConn(sctx, ws, websocket.MessageBinary)
	sess, err := yamux.Client(conn, gwproto.MuxConfig())
	if err != nil {
		ws.CloseNow() //nolint:errcheck
		return err
	}
	defer sess.Close()
	l.mu.Lock()
	l.sess = sess
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.sess, l.status = nil, nil
		l.mu.Unlock()
	}()
	l.set("connected", "")
	_ = s.st.GatewayConnected(context.WithoutCancel(ctx), l.gw.ID, cleanText(res.Version, 50))
	s.log.Info("gateway connected", "gateway", l.gw.Name, "address", l.gw.Address, "version", res.Version)
	defer s.log.Info("gateway disconnected", "gateway", l.gw.Name)

	go s.pollGatewayStatus(sctx, l, sess)
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()
	srv := &http.Server{Handler: s.gatewayAgentHandler(l.gw), ReadHeaderTimeout: 30 * time.Second}
	_ = srv.Serve(sess) // returns when the session closes
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("the tunnel was closed")
}

func (s *Server) pollGatewayStatus(ctx context.Context, l *gwLink, sess *yamux.Session) {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return sess.OpenStream() },
	}}
	tick := time.NewTicker(gatewayStatusEvery)
	defer tick.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://gateway"+gwproto.PathStatus, nil)
		if res, err := client.Do(req); err == nil {
			var st gwproto.Status
			if res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&st) == nil {
				l.mu.Lock()
				l.status = &st
				l.mu.Unlock()
			}
			res.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// gatewayAgentHandler serves the agent requests the gateway relays. Only
// agent paths are answered; the client address comes from the gateway.
func (s *Server) gatewayAgentHandler(g store.Gateway) http.Handler {
	app := s.appHandler()
	ref := gatewayRef{ID: g.ID, Name: g.Name, PublicURL: strings.TrimRight(g.PublicURL, "/")}
	scheme, host := "https", ""
	if u, err := url.Parse(ref.PublicURL); err == nil {
		scheme, host = u.Scheme, u.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, gwproto.AgentPrefix) {
			http.NotFound(w, r)
			return
		}
		ip := ""
		if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Forwarded-For"))); err == nil {
			ip = a.Unmap().String()
		}
		h := host
		if h == "" {
			h = r.Header.Get("X-Forwarded-Host")
		}
		ctx := context.WithValue(r.Context(), clientIPKey, ip)
		ctx = context.WithValue(ctx, schemeKey, scheme)
		ctx = context.WithValue(ctx, hostKey, h)
		ctx = context.WithValue(ctx, gatewayCtxKey, ref)
		w.Header().Set(headerGatewayID, strconv.FormatInt(g.ID, 10))
		app.ServeHTTP(w, r.WithContext(ctx))
	})
}

// gatewayName returns the name of a gateway with a live link.
func (s *Server) gatewayName(id int64) string {
	if l := s.gateways.link(id); l != nil {
		return l.gw.Name
	}
	return ""
}

// ---- endpoints for agents ----

// agentEndpoints are the addresses agents may use, in order of preference:
// the server's own external URL, then every enabled gateway.
func (s *Server) agentEndpoints(ctx context.Context) []string {
	gws, err := s.st.ListGateways(ctx)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, g := range gws {
		if g.Enabled {
			out = append(out, strings.TrimRight(g.PublicURL, "/"))
		}
	}
	if len(out) == 0 {
		return out // no gateways: agents keep their configured address
	}
	var gen generalSettings
	if err := s.st.GetSetting(ctx, keyGeneral, &gen); err == nil && gen.PublicURL != "" {
		out = append([]string{strings.TrimRight(gen.PublicURL, "/")}, out...)
	}
	return out
}

func (s *Server) sendEndpoints(id string, eps []string) {
	s.hub.sendChanged(id, gatewayEndpointsKey, agentproto.TypeEndpoints, agentproto.Endpoints{URLs: eps})
}

// broadcastEndpoints tells every connected agent about changed addresses.
func (s *Server) broadcastEndpoints(ctx context.Context) {
	eps := s.agentEndpoints(ctx)
	for _, id := range s.hub.onlineIDs() {
		s.sendEndpoints(id, eps)
	}
}

// ---- API ----

func (s *Server) gatewayRoutes(mux *http.ServeMux) {
	adm := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSettingsManage, 0, h) }
	mux.Handle("GET /api/v1/gateways", adm(s.handleListGateways))
	mux.Handle("POST /api/v1/gateways", adm(s.handleCreateGateway))
	mux.Handle("PUT /api/v1/gateways/{id}", adm(s.handleUpdateGateway))
	mux.Handle("POST /api/v1/gateways/{id}/pair", adm(s.handleRepairGateway))
	mux.Handle("POST /api/v1/gateways/{id}/test", adm(s.handleTestGateway))
	mux.Handle("DELETE /api/v1/gateways/{id}", adm(s.handleDeleteGateway))
}

type gatewayView struct {
	store.Gateway
	Fingerprint string          `json:"fingerprint"`
	State       string          `json:"state"` // disabled, connecting, connected, error
	StateSince  *time.Time      `json:"stateSince"`
	Error       string          `json:"error"`
	Agents      int             `json:"agents"`
	Status      *gwproto.Status `json:"status"`
}

type gatewaysView struct {
	Gateways    []gatewayView `json:"gateways"`
	DirectAgent int           `json:"directAgents"`
	ServerURL   string        `json:"serverUrl"`
	TunnelPort  int           `json:"tunnelPort"`
	Endpoints   []string      `json:"endpoints"`
	Version     string        `json:"version"`
	Channel     string        `json:"channel"`
}

func (s *Server) gatewayViewOf(g store.Gateway, via map[int64]int) gatewayView {
	v := gatewayView{Gateway: g, Fingerprint: gwproto.Fingerprint(g.PublicKey), State: "disabled", Agents: via[g.ID]}
	if l := s.gateways.link(g.ID); l != nil && g.Enabled {
		l.mu.Lock()
		since := l.since
		v.State, v.StateSince, v.Error, v.Status = l.state, &since, l.lastError, l.status
		l.mu.Unlock()
	}
	return v
}

func (s *Server) handleListGateways(w http.ResponseWriter, r *http.Request) {
	gws, err := s.st.ListGateways(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	via := s.hub.viaCounts()
	out := gatewaysView{Gateways: []gatewayView{}, DirectAgent: via[0], TunnelPort: gwproto.DefaultTunnelPort,
		Endpoints: s.agentEndpoints(r.Context()), Version: version.Version, Channel: version.Get().Channel}
	var gen generalSettings
	if err := s.st.GetSetting(r.Context(), keyGeneral, &gen); err == nil {
		out.ServerURL = gen.PublicURL
	}
	for _, g := range gws {
		out.Gateways = append(out.Gateways, s.gatewayViewOf(g, via))
	}
	writeJSON(w, http.StatusOK, out)
}

type gatewayReq struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	PublicURL   string `json:"publicUrl"`
	PairingCode string `json:"pairingCode"`
	Enabled     *bool  `json:"enabled"`
}

// normalizeGatewayAddress accepts "host", "host:port" or a wss:// URL and
// returns host:port.
func normalizeGatewayAddress(v string) (string, bool) {
	v = strings.TrimSpace(v)
	for _, p := range []string{"wss://", "https://"} {
		v = strings.TrimPrefix(v, p)
	}
	v, _, _ = strings.Cut(v, "/")
	if v == "" || strings.ContainsAny(v, " @?#") {
		return "", false
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		host, port = strings.Trim(v, "[]"), strconv.Itoa(gwproto.DefaultTunnelPort)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || host == "" {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

// normalizeGatewayURL validates the URL agents use to reach a gateway.
func normalizeGatewayURL(v string) (string, bool) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// validGatewayReq checks the fields shared by create and update.
func (s *Server) validGatewayReq(w http.ResponseWriter, r *http.Request, req *gatewayReq, id int64) bool {
	req.Name = cleanText(req.Name, 100)
	addr, okA := normalizeGatewayAddress(req.Address)
	pub, okU := normalizeGatewayURL(req.PublicURL)
	switch {
	case req.Name == "":
		apiError(w, http.StatusBadRequest, "name_required")
		return false
	case !okA:
		apiError(w, http.StatusBadRequest, "invalid_address")
		return false
	case !okU:
		apiError(w, http.StatusBadRequest, "invalid_url")
		return false
	}
	req.Address, req.PublicURL = addr, pub
	taken, err := s.st.GatewayNameTaken(r.Context(), req.Name, id)
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	if taken {
		apiError(w, http.StatusConflict, "name_taken")
		return false
	}
	return true
}

// gatewayErrorCode maps a tunnel error to a code the web interface translates.
func gatewayErrorCode(err error) (string, string) {
	var re *gwproto.RemoteError
	var le *gwproto.LocalError
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &re):
		return re.Code, ""
	case errors.As(err, &le):
		return le.Code, le.Detail
	case errors.Is(err, gwproto.ErrHandshake):
		return "handshake_failed", err.Error()
	}
	return "tunnel_failed", err.Error()
}

func gatewayErrorText(err error) string {
	code, detail := gatewayErrorCode(err)
	if detail != "" {
		return cleanText(code+": "+detail, 500)
	}
	return code
}

func (s *Server) pairError(w http.ResponseWriter, err error) {
	code, detail := gatewayErrorCode(err)
	status := http.StatusBadGateway
	switch code {
	case gwproto.ErrCodeBadPairing, gwproto.ErrCodeNeedsPairing:
		status = http.StatusBadRequest
	case gwproto.ErrCodeRateLimited:
		status = http.StatusTooManyRequests
	case gwproto.ErrCodeAlreadyPaired, gwproto.ErrCodeOtherServer:
		status = http.StatusConflict
	}
	apiErrorDetail(w, status, "gateway_"+strings.TrimPrefix(code, "gateway_"), cleanText(detail, 300))
}

// pairGateway pairs with the code and closes the test connection again; the
// link connects with the pinned key afterwards.
func (s *Server) pairGateway(ctx context.Context, addr, code string) (gwproto.ServerResult, error) {
	ws, res, err := dialGateway(ctx, addr, nil, code, s.serverSide())
	if err != nil {
		return res, err
	}
	ws.Close(websocket.StatusNormalClosure, "paired") //nolint:errcheck
	return res, nil
}

func (s *Server) handleCreateGateway(w http.ResponseWriter, r *http.Request) {
	var req gatewayReq
	if !decode(w, r, &req) || !s.validGatewayReq(w, r, &req, 0) {
		return
	}
	if gwproto.NormalizeCode(req.PairingCode) == "" {
		apiError(w, http.StatusBadRequest, "gateway_pairing_code_required")
		return
	}
	res, err := s.pairGateway(r.Context(), req.Address, req.PairingCode)
	if err != nil {
		s.audit(r, "gateway.pair", req.Name, false, map[string]any{"address": req.Address, "error": gatewayErrorText(err)})
		s.pairError(w, err)
		return
	}
	u, _ := currentUser(r)
	g, err := s.st.CreateGateway(r.Context(), store.Gateway{Name: req.Name, Address: req.Address, PublicURL: req.PublicURL,
		PublicKey: res.PublicKey, Enabled: true, Version: cleanText(res.Version, 50), CreatedBy: u.Username})
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "gateway_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "gateway.create", g.Name, true, map[string]any{"address": g.Address, "publicUrl": g.PublicURL, "fingerprint": gwproto.Fingerprint(g.PublicKey)})
	s.reconcileGateways(r.Context())
	s.broadcastEndpoints(r.Context())
	writeJSON(w, http.StatusCreated, s.gatewayViewOf(g, s.hub.viaCounts()))
}

func (s *Server) gatewayByPath(w http.ResponseWriter, r *http.Request) (store.Gateway, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.Gateway{}, false
	}
	g, err := s.st.GatewayByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return g, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return g, false
	}
	return g, true
}

func (s *Server) handleUpdateGateway(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gatewayByPath(w, r)
	if !ok {
		return
	}
	var req gatewayReq
	if !decode(w, r, &req) || !s.validGatewayReq(w, r, &req, g.ID) {
		return
	}
	g.Name, g.Address, g.PublicURL = req.Name, req.Address, req.PublicURL
	if req.Enabled != nil {
		g.Enabled = *req.Enabled
	}
	if err := s.st.UpdateGateway(r.Context(), g); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "gateway.update", g.Name, true, map[string]any{"address": g.Address, "publicUrl": g.PublicURL, "enabled": g.Enabled})
	s.reconcileGateways(r.Context())
	s.broadcastEndpoints(r.Context())
	g, _ = s.st.GatewayByID(r.Context(), g.ID)
	writeJSON(w, http.StatusOK, s.gatewayViewOf(g, s.hub.viaCounts()))
}

// handleRepairGateway pairs an existing gateway again, for example after
// it was reinstalled or reset with `fleetwarden-gateway reset-pairing`.
func (s *Server) handleRepairGateway(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gatewayByPath(w, r)
	if !ok {
		return
	}
	var req gatewayReq
	if !decode(w, r, &req) {
		return
	}
	addr := g.Address
	if strings.TrimSpace(req.Address) != "" {
		if addr, ok = normalizeGatewayAddress(req.Address); !ok {
			apiError(w, http.StatusBadRequest, "invalid_address")
			return
		}
	}
	if gwproto.NormalizeCode(req.PairingCode) == "" {
		apiError(w, http.StatusBadRequest, "gateway_pairing_code_required")
		return
	}
	res, err := s.pairGateway(r.Context(), addr, req.PairingCode)
	if err != nil {
		s.audit(r, "gateway.pair", g.Name, false, map[string]any{"address": addr, "error": gatewayErrorText(err)})
		s.pairError(w, err)
		return
	}
	if err := s.st.RepairGateway(r.Context(), g.ID, addr, res.PublicKey, cleanText(res.Version, 50)); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apiError(w, http.StatusConflict, "gateway_exists")
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "gateway.pair", g.Name, true, map[string]any{"address": addr, "fingerprint": gwproto.Fingerprint(res.PublicKey)})
	s.reconcileGateways(r.Context())
	g, _ = s.st.GatewayByID(r.Context(), g.ID)
	writeJSON(w, http.StatusOK, s.gatewayViewOf(g, s.hub.viaCounts()))
}

func (s *Server) handleDeleteGateway(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gatewayByPath(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteGateway(r.Context(), g.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "gateway.delete", g.Name, true, map[string]any{"address": g.Address})
	s.reconcileGateways(r.Context())
	s.broadcastEndpoints(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

type gatewayCheck struct {
	Name   string `json:"name"` // tunnel, publicUrl
	OK     bool   `json:"ok"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// handleTestGateway checks the tunnel and that the public URL leads agents
// through this gateway to this server.
func (s *Server) handleTestGateway(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gatewayByPath(w, r)
	if !ok {
		return
	}
	checks := []gatewayCheck{}
	tunnel := gatewayCheck{Name: "tunnel"}
	if l := s.gateways.link(g.ID); l != nil {
		l.mu.Lock()
		tunnel.OK, tunnel.Code, tunnel.Detail = l.state == "connected", l.state, l.lastError
		l.mu.Unlock()
	} else {
		tunnel.Code = "disabled"
	}
	checks = append(checks, tunnel)
	checks = append(checks, s.checkGatewayURL(r.Context(), g))
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

func (s *Server) checkGatewayURL(ctx context.Context, g store.Gateway) gatewayCheck {
	c := gatewayCheck{Name: "publicUrl"}
	ctx, cancel := context.WithTimeout(ctx, gatewayTestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.PublicURL, "/")+agentproto.PathHello, nil)
	if err != nil {
		c.Code, c.Detail = "invalid_url", err.Error()
		return c
	}
	req.Header.Set("User-Agent", "fleetwarden-server/"+version.Version)
	res, err := (&http.Client{Timeout: gatewayTestTimeout}).Do(req)
	if err != nil {
		c.Code, c.Detail = "unreachable", cleanText(err.Error(), 300)
		return c
	}
	defer res.Body.Close()
	var hello agentproto.Hello
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&hello) != nil {
		c.Code, c.Detail = "bad_answer", res.Status
		return c
	}
	if string(hello.ServerPublicKey) != string(s.agentPublicKey()) {
		c.Code = "other_server"
		return c
	}
	if res.Header.Get(headerGatewayID) != strconv.FormatInt(g.ID, 10) {
		// The URL reaches this server, but not through this gateway.
		c.Code = "not_via_gateway"
		return c
	}
	c.OK = true
	return c
}
