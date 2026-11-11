package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// Agent connection timing.
const (
	agentAuthTimeout   = 15 * time.Second
	agentHeartbeat     = 60 * time.Second
	agentPingInterval  = 30 * time.Second
	agentWriteTimeout  = 20 * time.Second
	agentReadLimit     = 16 << 20 // a full inventory with thousands of programs
	maxSoftwareEntries = 10000
)

// LoadOrCreateAgentKey loads the Ed25519 key the server signs agent messages
// with, creating it on first start. Agents pin its public key at enrollment,
// so the file is part of backups.
func LoadOrCreateAgentKey(path string) (ed25519.PrivateKey, error) {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, key.Seed(), 0o600); err != nil {
		return nil, err
	}
	return key, os.Rename(tmp, path)
}

func (s *Server) agentPublicKey() ed25519.PublicKey { return s.agentKey.Public().(ed25519.PublicKey) }

// ---- hub of connected agents ----

type agentConn struct {
	deviceID string
	groupID  int64
	sess     *agentproto.Session
	ws       *websocket.Conn
	out      chan *agentproto.Envelope
	cancel   context.CancelFunc
	since    time.Time
	gateway  int64             // the gateway the agent came through; 0 for direct
	sent     map[string]string // last value of settings messages, guarded by the hub
	// sendMu makes signing and queueing one step: the agent rejects a
	// message whose sequence number is lower than the previous one, so two
	// goroutines must not queue their messages in a different order than
	// they were numbered.
	sendMu sync.Mutex
}

// enqueue signs a message and queues it; false when the queue is full.
func (c *agentConn) enqueue(typ, msgID string, v any) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	e, err := c.sess.Seal(typ, msgID, v)
	if err != nil {
		return false
	}
	select {
	case c.out <- e:
		return true
	default:
		return false
	}
}

type agentHub struct {
	mu    sync.RWMutex
	conns map[string]*agentConn
}

func newAgentHub() *agentHub { return &agentHub{conns: map[string]*agentConn{}} }

// add registers a connection and closes an older one of the same device.
func (h *agentHub) add(c *agentConn) {
	h.mu.Lock()
	old := h.conns[c.deviceID]
	h.conns[c.deviceID] = c
	h.mu.Unlock()
	if old != nil {
		old.cancel()
	}
}

func (h *agentHub) remove(c *agentConn) {
	h.mu.Lock()
	if h.conns[c.deviceID] == c {
		delete(h.conns, c.deviceID)
	}
	h.mu.Unlock()
}

func (h *agentHub) online(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[id]
	return ok
}

// via returns whether a device is online and the gateway it uses.
func (h *agentHub) via(id string) (bool, int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.conns[id]
	if !ok {
		return false, 0
	}
	return true, c.gateway
}

// viaCounts counts connected agents per gateway (0: direct).
func (h *agentHub) viaCounts() map[int64]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[int64]int{}
	for _, c := range h.conns {
		out[c.gateway]++
	}
	return out
}

func (h *agentHub) onlineIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

// send queues a signed message for a connected device. It reports false
// when the device is offline or its queue is full.
func (h *agentHub) send(id, typ string, v any) bool {
	h.mu.RLock()
	c := h.conns[id]
	h.mu.RUnlock()
	if c == nil {
		return false
	}
	return c.enqueue(typ, "", v)
}

// sendID queues a signed message with an id (a request the agent answers).
func (h *agentHub) sendID(id, typ, msgID string, v any) bool {
	h.mu.RLock()
	c := h.conns[id]
	h.mu.RUnlock()
	if c == nil {
		return false
	}
	return c.enqueue(typ, msgID, v)
}

// sendChanged sends a settings message unless the session already got the
// same value.
func (h *agentHub) sendChanged(id, key, typ string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.RLock()
	c := h.conns[id]
	same := c != nil && c.sent[key] == string(raw)
	h.mu.RUnlock()
	if c == nil || same || !h.send(id, typ, v) {
		return
	}
	h.mu.Lock()
	if c.sent == nil {
		c.sent = map[string]string{}
	}
	c.sent[key] = string(raw)
	h.mu.Unlock()
}

func (h *agentHub) closeAll() {
	h.mu.Lock()
	conns := h.conns
	h.conns = map[string]*agentConn{}
	h.mu.Unlock()
	for _, c := range conns {
		c.cancel()
	}
}

// CloseAgents drops every agent connection (on shutdown; agents reconnect).
func (s *Server) CloseAgents() { s.hub.closeAll() }

// ---- public agent endpoints ----

func (s *Server) agentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+agentproto.PathHello, s.handleAgentHello)
	mux.HandleFunc("POST "+agentproto.PathEnroll, s.handleEnroll)
	mux.HandleFunc("GET "+agentproto.PathWS, s.handleAgentWS)
	mux.HandleFunc("GET /agent/install.ps1", s.handleInstallPS1)
	mux.HandleFunc("GET /agent/install.sh", s.handleInstallSh)
	mux.HandleFunc("GET /agent/download/{file}", s.handleAgentDownload)
}

func (s *Server) handleAgentHello(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, agentproto.Hello{
		Product: s.loadBranding(r.Context()).ProductName, Version: version.Version,
		ProtocolVersion: agentproto.Version, ServerPublicKey: s.agentPublicKey(),
	})
}

func cleanText(v string, max int) string {
	v = strings.TrimSpace(strings.ToValidUTF8(v, ""))
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if len(v) > max {
		v = v[:max]
		for !utf8.ValidString(v) {
			v = v[:len(v)-1]
		}
	}
	return v
}

func validPlatform(p string) bool { return p == "windows" || p == "linux" }

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		apiError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var req agentproto.EnrollRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	req.Hostname = cleanText(req.Hostname, 255)
	req.MachineID = cleanText(req.MachineID, 128)
	if req.Hostname == "" || !validPlatform(req.Platform) || len(req.PublicKey) != ed25519.PublicKeySize {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	tok, err := s.st.TokenByValue(r.Context(), strings.TrimSpace(req.Token))
	if err != nil || !tok.Usable(s.now()) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.internalError(w, r, err)
			return
		}
		s.agentFails.Fail(ip)
		s.auditAs(r, nil, "agent", "device.enroll", req.Hostname, false, map[string]any{"reason": "invalid_token", "platform": req.Platform})
		apiError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	if !ed25519.Verify(req.PublicKey, agentproto.EnrollProof(req.Token, req.PublicKey), req.Proof) {
		apiError(w, http.StatusBadRequest, "bad_proof")
		return
	}
	groupID, groupName := tok.GroupID, tok.GroupName
	if name := cleanText(req.Group, 100); name != "" {
		if g, err := s.st.GroupByName(r.Context(), name); err == nil && g.ID != groupID {
			if s.tokenMayPlaceIn(r.Context(), tok, g.ID) {
				groupID, groupName = g.ID, g.Name
			} else {
				s.auditAs(r, nil, "agent", "device.enroll.group_denied", req.Hostname, false, map[string]any{
					"group": g.Name, "token": tok.Prefix})
			}
		}
	}
	d := store.Device{
		GroupID: groupID, TokenID: &tok.ID, PublicKey: req.PublicKey, MachineID: req.MachineID, Hostname: req.Hostname,
		Platform: req.Platform, Arch: cleanText(req.Arch, 20), AgentVersion: cleanText(req.AgentVersion, 50), RemoteIP: ip,
	}
	action := "device.enroll"
	prev, err := store.Device{}, store.ErrNotFound
	if d.MachineID != "" {
		prev, err = s.st.DeviceByMachine(r.Context(), d.MachineID)
	}
	if err == nil && prev.Platform == d.Platform {
		// The same machine enrolls again (agent reinstalled): keep its history.
		d.ID, action = prev.ID, "device.reenroll"
		s.hub.send(prev.ID, agentproto.TypeRemove, nil) // an old agent with the previous key must stop
		err = s.st.ReEnrollDevice(r.Context(), d)
	} else {
		d.ID = newUUID()
		err = s.st.CreateDevice(r.Context(), d)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.auditAs(r, nil, "agent", action, d.Hostname, true, map[string]any{
		"deviceId": d.ID, "group": groupName, "platform": d.Platform, "token": tok.Prefix,
	})
	if action == "device.enroll" {
		s.integrationEnrolled(r.Context(), d, groupName)
	}
	writeJSON(w, http.StatusOK, agentproto.EnrollResponse{DeviceID: d.ID, ServerPublicKey: s.agentPublicKey(), GroupName: groupName})
}

// tokenMayPlaceIn reports whether a device enrolling with tok may pick group
// groupID instead of the token's own group (msiexec GROUP=). That is only
// allowed within the device-group scope of the user who created the token, so
// a token from a technician limited to some groups cannot place a device in
// another group and receive that group's configurations and software.
func (s *Server) tokenMayPlaceIn(ctx context.Context, tok store.EnrollmentToken, groupID int64) bool {
	if tok.CreatedBy == "" {
		return false
	}
	u, err := s.st.UserByUsername(ctx, tok.CreatedBy)
	if errors.Is(err, store.ErrNotFound) && tok.CreatedBy == "cli" {
		return true // made by root on the server console (fleetwarden-server enrollment-token)
	}
	if err != nil || u.Disabled {
		return false
	}
	return store.UserScope(u).Allows(groupID)
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// handleAgentWS runs one agent session: challenge, signed auth, then signed
// messages both ways.
func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		apiError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionNoContextTakeover})
	if err != nil {
		return
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(agentReadLimit)
	ctx, cancel := context.WithCancel(s.bgCtx)
	defer cancel()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return
	}
	ch, _ := agentproto.NewEnvelope(agentproto.TypeChallenge, "", 0, agentproto.Challenge{
		Nonce: nonce, ServerTime: s.now().UTC(), ProtocolVersion: agentproto.Version,
	})
	if err := writeEnvelope(ctx, ws, ch); err != nil {
		return
	}

	actx, acancel := context.WithTimeout(ctx, agentAuthTimeout)
	first, err := readEnvelope(actx, ws)
	acancel()
	if err != nil || first.Type != agentproto.TypeAuth || first.Seq != 1 {
		ws.Close(websocket.StatusPolicyViolation, "authentication required") //nolint:errcheck
		return
	}
	var a agentproto.Auth
	if err := first.Decode(&a); err != nil {
		ws.Close(websocket.StatusPolicyViolation, "bad auth") //nolint:errcheck
		return
	}
	dev, err := s.st.DeviceByID(ctx, a.DeviceID)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Info("unknown device tried to connect", "device", a.DeviceID, "version", a.AgentVersion, "ip", ip)
		s.agentFails.Fail(ip)
		ws.Close(4404, "unknown device") //nolint:errcheck
		return
	}
	if err != nil {
		ws.Close(websocket.StatusInternalError, "") //nolint:errcheck
		return
	}
	sess := agentproto.NewSession(nonce, s.agentKey, ed25519.PublicKey(dev.PublicKey), agentproto.ToAgent)
	if err := sess.Open(first); err != nil {
		s.agentFails.Fail(ip)
		ws.Close(4401, "bad signature") //nolint:errcheck
		return
	}
	if a.ProtocolVersion != agentproto.Version {
		ws.Close(4426, "unsupported protocol version") //nolint:errcheck
		return
	}

	c := &agentConn{deviceID: dev.ID, groupID: dev.GroupID, sess: sess, ws: ws, out: make(chan *agentproto.Envelope, 32), cancel: cancel, since: s.now()}
	if g, ok := gatewayFrom(r); ok {
		c.gateway = g.ID
	}
	intervals := s.loadScanSettings(ctx)
	if g, err := s.st.GroupByID(ctx, dev.GroupID); err == nil {
		intervals = intervals.effective(g.InventoryMinutes, g.PatchScanMinutes)
	}
	welcome, _ := sess.Seal(agentproto.TypeWelcome, "", agentproto.Welcome{
		DeviceID: dev.ID, HeartbeatSeconds: int(agentHeartbeat / time.Second), InventoryMinutes: intervals.InventoryMinutes,
		InventoryRequired: inventoryDue(dev.InventoryAt, s.now(), intervals.InventoryMinutes), SelfService: true,
	})
	// The welcome carries the interval, so the scheduler need not resend it.
	if raw, err := json.Marshal(agentproto.AgentConfig{InventoryMinutes: intervals.InventoryMinutes}); err == nil {
		c.sent = map[string]string{agentConfigKey: string(raw)}
	}
	if err := writeEnvelope(ctx, ws, welcome); err != nil {
		return
	}
	s.hub.add(c)
	defer s.hub.remove(c)
	s.sendEndpoints(dev.ID, s.agentEndpoints(ctx))
	go s.sendDistribution(ctx, dev.ID)
	agentVersion := cleanText(a.AgentVersion, 50)
	_ = s.st.TouchDevice(context.WithoutCancel(ctx), dev.ID, ip, agentVersion, dev.LoggedInUser, dev.LocalIPs, nil)
	if dev.UpdateVersion != "" && dev.UpdateVersion == agentVersion && dev.UpdateState != "completed" {
		_ = s.st.CompleteDeviceUpdate(context.WithoutCancel(ctx), dev.ID, agentVersion)
		s.log.Info("agent updated", "device", dev.ID, "host", dev.Hostname, "version", agentVersion)
	}
	s.log.Info("agent connected", "device", dev.ID, "host", dev.Hostname, "ip", ip, "gateway", c.gateway)
	defer s.log.Info("agent disconnected", "device", dev.ID, "host", dev.Hostname)

	go s.agentWriter(ctx, c)
	go s.patchOnConnect(ctx, dev)
	go s.softwareOnConnect(ctx, dev.ID)
	go s.configOnConnect(ctx, dev.ID)
	go s.securityOnConnect(ctx, dev.ID)
	go s.powerOnConnect(ctx, dev.ID)
	for {
		e, err := readEnvelope(ctx, ws)
		if err != nil {
			return
		}
		if err := sess.Open(e); err != nil {
			ws.Close(4401, "bad signature") //nolint:errcheck
			return
		}
		if err := s.handleAgentMessage(ctx, c, ip, e); err != nil && !canceled(err) {
			s.log.Warn("agent message", "device", dev.ID, "type", e.Type, "err", err)
		}
	}
}

func (s *Server) agentWriter(ctx context.Context, c *agentConn) {
	ping := time.NewTicker(agentPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			c.ws.Close(websocket.StatusGoingAway, "") //nolint:errcheck
			return
		case e := <-c.out:
			if err := writeEnvelope(ctx, c.ws, e); err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, agentWriteTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, e *agentproto.Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, agentWriteTimeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, raw)
}

func readEnvelope(ctx context.Context, ws *websocket.Conn) (*agentproto.Envelope, error) {
	typ, raw, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("binary message")
	}
	var e agentproto.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Server) handleAgentMessage(ctx context.Context, c *agentConn, ip string, e *agentproto.Envelope) error {
	switch e.Type {
	case agentproto.TypeHeartbeat:
		var hb agentproto.Heartbeat
		if err := e.Decode(&hb); err != nil {
			return err
		}
		if err := s.saveVolumes(ctx, c.deviceID, hb.Volumes); err != nil {
			return err
		}
		return s.st.TouchDevice(ctx, c.deviceID, ip, cleanText(hb.AgentVersion, 50), cleanText(hb.LoggedInUser, 255),
			cleanList(hb.LocalIPs, 32, 64), validTime(hb.BootTime))
	case agentproto.TypeInventory:
		var inv agentproto.Inventory
		if err := e.Decode(&inv); err != nil {
			return err
		}
		if err := s.saveInventory(ctx, c.deviceID, inv); err != nil {
			return err
		}
		if err := s.saveVolumes(ctx, c.deviceID, inv.Volumes); err != nil {
			return err
		}
		return s.st.TouchDevice(ctx, c.deviceID, ip, cleanText(inv.AgentVersion, 50), cleanText(inv.LoggedInUser, 255),
			cleanList(inv.LocalIPs, 32, 64), validTime(inv.BootTime))
	case agentproto.TypeUpdateStatus:
		return s.handleUpdateStatusMsg(ctx, c, e)
	case agentproto.TypePatchScanResult:
		return s.handlePatchScanMsg(ctx, c, e)
	case agentproto.TypePatchStatus:
		return s.handlePatchStatusMsg(ctx, c, e)
	case agentproto.TypeSoftwareStatus:
		return s.handleSoftwareStatusMsg(ctx, c, e)
	case agentproto.TypeConfigStatus:
		return s.handleConfigStatusMsg(ctx, c, e)
	case agentproto.TypePowerStatus:
		return s.handlePowerStatusMsg(ctx, c, e)
	case agentproto.TypeRemoteStatus:
		return s.handleRemoteStatusMsg(c, e)
	case agentproto.TypeSecurityReport:
		return s.handleSecurityReportMsg(ctx, c, e)
	case agentproto.TypeSecurityActionStatus:
		return s.handleSecurityActionStatusMsg(ctx, c, e)
	case agentproto.TypeManageResult:
		return s.handleManageResultMsg(c, e)
	case agentproto.TypeSelfServiceRequest:
		return s.handleSelfServiceMsg(ctx, c, e)
	case agentproto.TypeChatStatus:
		return s.handleChatStatusMsg(ctx, c, e)
	default:
		return fmt.Errorf("unknown message type %q", e.Type)
	}
}

func validTime(t time.Time) *time.Time {
	if t.IsZero() || t.Year() < 2000 || t.After(time.Now().Add(24*time.Hour)) {
		return nil
	}
	return &t
}

func cleanList(in []string, maxItems, maxLen int) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = cleanText(v, maxLen); v != "" && len(out) < maxItems {
			out = append(out, v)
		}
	}
	return out
}

func validKind(k string) string {
	switch k {
	case "workstation", "server", "hypervisor":
		return k
	}
	return "workstation"
}

func (s *Server) saveInventory(ctx context.Context, id string, inv agentproto.Inventory) error {
	hw := inv.Hardware
	if len(hw.Disks) > 64 {
		hw.Disks = hw.Disks[:64]
	}
	if len(hw.Volumes) > 128 {
		hw.Volumes = hw.Volumes[:128]
	}
	if len(hw.Network) > 64 {
		hw.Network = hw.Network[:64]
	}
	if len(hw.GPUs) > 8 {
		hw.GPUs = hw.GPUs[:8]
	}
	hwRaw, err := json.Marshal(hw)
	if err != nil {
		return err
	}
	sw := inv.Software
	if len(sw) > maxSoftwareEntries {
		sw = sw[:maxSoftwareEntries]
	}
	rows := make([]store.SoftwareRow, 0, len(sw))
	for _, p := range sw {
		name := cleanText(p.Name, 300)
		if name == "" {
			continue
		}
		row := store.SoftwareRow{Name: name, Version: cleanText(p.Version, 100), Publisher: cleanText(p.Publisher, 200),
			Source: cleanText(p.Source, 20), SizeBytes: max(p.SizeBytes, 0),
			SrcPackage: cleanText(p.SourcePackage, 128), SrcVersion: cleanText(p.SourceVersion, 100)}
		if on := cleanText(p.InstalledOn, 10); on != "" {
			row.InstalledOn = &on
		}
		rows = append(rows, row)
	}
	return s.st.SaveInventory(ctx, id, store.InventoryUpdate{
		Hostname: cleanText(inv.Hostname, 255), OSName: cleanText(inv.OS.Name, 200), OSVersion: cleanText(inv.OS.Version, 100),
		OSBuild: cleanText(inv.OS.Build, 100), Kind: validKind(inv.OS.Kind), Domain: cleanText(inv.OS.Domain, 255),
		OU: cleanText(inv.OS.OU, 500), DomainJoined: inv.OS.DomainJoined, Manufacturer: cleanText(hw.Manufacturer, 200),
		Model: cleanText(hw.Model, 200), Serial: cleanText(hw.Serial, 200), CPU: cleanText(hw.CPU, 200),
		LoggedInUser: cleanText(inv.LoggedInUser, 255), CPUCores: max(hw.CPUCores, 0), MemoryBytes: max(hw.MemoryBytes, 0),
		LocalIPs: cleanList(inv.LocalIPs, 32, 64), BootTime: validTime(inv.BootTime), Hardware: hwRaw, Software: rows,
	})
}

// saveVolumes stores the volumes an agent sent with its heartbeat. Agents
// that don't send them keep the volumes from their inventory.
func (s *Server) saveVolumes(ctx context.Context, deviceID string, vols []agentproto.Volume) error {
	if len(vols) == 0 {
		return nil
	}
	if len(vols) > 64 {
		vols = vols[:64]
	}
	out := make([]store.ReportVolume, 0, len(vols))
	for _, v := range vols {
		if v.SizeBytes <= 0 || v.FreeBytes < 0 {
			continue
		}
		out = append(out, store.ReportVolume{Mount: cleanText(v.Mount, 255), SizeBytes: v.SizeBytes, FreeBytes: min(v.FreeBytes, v.SizeBytes)})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return s.st.SaveVolumes(ctx, deviceID, raw)
}
