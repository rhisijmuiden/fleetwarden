package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// ErrRemoved means the server deleted this device; the agent stops.
var ErrRemoved = errors.New("this device was removed from the management server")

// ErrInvalidToken means the enrollment token was refused.
var ErrInvalidToken = errors.New("the enrollment token is invalid, expired or revoked")

// Agent runs the connection to the management server.
type Agent struct {
	ConfigPath string
	Log        *slog.Logger
	HTTP       *http.Client
	// Collect gathers the inventory; defaults to the platform collector.
	Collect func(ctx context.Context) (agentproto.Inventory, error)
	// Once stops after the first inventory was sent (install tests).
	Once bool
	// StateDir holds update files; defaults to the platform's state folder.
	StateDir string
	// StartHelper starts the update helper; defaults to a detached process.
	StartHelper func(exe, plan string) error
	// Patches installs updates; defaults to the platform backend.
	Patches PatchBackend
	// Drivers scans for and installs driver and firmware updates; defaults
	// to the platform backend.
	Drivers DriverBackend
	// Software installs and removes programs; defaults to the platform backend.
	Software SoftwareBackend
	// Notify asks the logged-on user before patching (the tray pop-up on
	// Windows); nil installs without asking.
	Notify Notifier
	// Processes finds and stops running programs (browser updates);
	// defaults to the platform implementation.
	Processes ProcessControl
	// Remote opens remote desktop sessions; defaults to the platform
	// backend (nil where there is none).
	Remote RemoteBackend
	// Terminal starts shells for remote terminal sessions; defaults to
	// terminal.Start.
	Terminal TerminalStarter
	// Config applies configurations; defaults to the platform backend.
	Config ConfigBackend
	// Manage lists and controls processes, services and event logs;
	// defaults to the platform backend.
	Manage ManageBackend
	// Minute is the length of a postpone or inventory-interval minute;
	// tests shorten it.
	Minute time.Duration
	// MachineID overrides the platform machine ID (tests).
	MachineID string
	// BootTime returns when the system started; tests replace it.
	BootTime func() time.Time

	mu       sync.Mutex
	cfg      Config
	updating atomic.Bool
	patch    patchState
	software softwareState // guarded by mu
	remote   remoteState
	config   configRunner
	ctx      context.Context // of Run: jobs outlive a single session
	// installMu keeps patch installs and software jobs apart: Windows
	// Installer and dpkg run one installation at a time.
	installMu sync.Mutex
	// invNow asks the running session for a full inventory.
	invNow chan struct{}
}

func (a *Agent) runCtx() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

func (a *Agent) init() {
	if a.Log == nil {
		a.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if a.HTTP == nil {
		a.HTTP = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second,
		}}
	}
	if a.Collect == nil {
		a.Collect = CollectInventory
	}
	a.mu.Lock()
	if a.invNow == nil {
		a.invNow = make(chan struct{}, 1)
	}
	a.mu.Unlock()
}

// Enroll registers the agent with its token and stores the device identity.
func (a *Agent) Enroll(ctx context.Context) error {
	a.init()
	cfg, err := LoadConfig(a.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Enrolled() {
		return nil
	}
	if cfg.Server == "" || cfg.Token == "" {
		return errors.New("not configured: run 'fleetwarden-agent configure --server URL --token TOKEN'")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	mid := a.MachineID
	if mid == "" {
		mid = machineID()
	}
	req := agentproto.EnrollRequest{
		Token: cfg.Token, PublicKey: pub, MachineID: mid, Hostname: host, Platform: runtime.GOOS,
		Arch: runtime.GOARCH, AgentVersion: version.Version, Group: cfg.Group,
		Proof: ed25519.Sign(key, agentproto.EnrollProof(cfg.Token, pub)),
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Server+agentproto.PathEnroll, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("User-Agent", "fleetwarden-agent/"+version.Version)
	res, err := a.HTTP.Do(hreq)
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return ErrInvalidToken
	}
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("enroll: server answered %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	var out agentproto.EnrollResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	if out.DeviceID == "" || len(out.ServerPublicKey) != ed25519.PublicKeySize {
		return errors.New("enroll: incomplete answer from the server")
	}
	cfg.DeviceID, cfg.PrivateKey, cfg.ServerPublicKey = out.DeviceID, key.Seed(), out.ServerPublicKey
	cfg.Token, cfg.Group = "", ""
	if err := SaveConfig(a.ConfigPath, cfg); err != nil {
		return err
	}
	a.Log.Info("enrolled", "device", out.DeviceID, "group", out.GroupName, "server", cfg.Server)
	return nil
}

// Run enrolls when needed and keeps the session open until ctx ends,
// reconnecting with backoff. It returns ErrRemoved when the server deleted
// the device.
func (a *Agent) Run(ctx context.Context) error {
	a.init()
	backoff := newBackoff()
	for {
		err := a.Enroll(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.Log.Warn("enrollment failed, retrying", "err", err)
		if a.Once {
			return err
		}
		if !sleep(ctx, backoff.next(errors.Is(err, ErrInvalidToken))) {
			return ctx.Err()
		}
	}
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	if !a.Once {
		if a.Notify == nil {
			a.Notify = platformNotifier(ctx, a.Log)
		}
		if a.Remote == nil {
			a.Remote = platformRemote(a.Log)
		}
		a.resumePatchJob(ctx)
		a.resumeSoftwareJob()
		go a.runConfigLoop(ctx)
	}
	backoff = newBackoff()
	next := 0 // index into the candidate addresses
	for {
		cfg, err := LoadConfig(a.ConfigPath)
		if err != nil {
			return err
		}
		cands := cfg.Candidates()
		if next >= len(cands) {
			next = 0
		}
		// The session, downloads and remote control all use the address
		// that answered; the configured one stays on disk.
		active := cfg
		active.Server = cands[next]
		a.mu.Lock()
		a.cfg = active
		a.mu.Unlock()
		start := time.Now()
		err = a.session(ctx, active, len(cands) > 1)
		if errors.Is(err, errDial) && next+1 < len(cands) && ctx.Err() == nil {
			a.Log.Info("server address not reachable, trying the next one", "server", active.Server, "err", err)
			next++
			continue
		}
		next = 0 // after a lost session or a full round, start with the preferred address again
		switch {
		case errors.Is(err, ErrRemoved):
			a.Log.Warn("the server removed this device; clearing the enrollment")
			cfg.DeviceID, cfg.PrivateKey, cfg.ServerPublicKey = "", nil, nil
			_ = SaveConfig(a.ConfigPath, cfg)
			return err
		case errors.Is(err, errSessionDone):
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		}
		if a.Once {
			return err
		}
		if time.Since(start) > 5*time.Minute {
			backoff = newBackoff() // the connection was healthy for a while
		}
		wait := backoff.next(errors.Is(err, errUnknownDevice))
		a.Log.Warn("connection lost, reconnecting", "err", err, "in", wait.Round(time.Second))
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

var (
	errDial          = errors.New("connect")
	errSessionDone   = errors.New("session finished")
	errUnknownDevice = errors.New("the server does not know this device")
)

func (a *Agent) session(ctx context.Context, cfg Config, alternatives bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dialTimeout := 30 * time.Second
	if alternatives {
		dialTimeout = 10 * time.Second // other addresses wait
	}
	dctx, dcancel := context.WithTimeout(ctx, dialTimeout)
	ws, res, err := websocket.Dial(dctx, wsURL(cfg.Server)+agentproto.PathWS, &websocket.DialOptions{
		HTTPClient:      a.HTTP,
		HTTPHeader:      http.Header{"User-Agent": {"fleetwarden-agent/" + version.Version}},
		CompressionMode: websocket.CompressionContextTakeover,
	})
	dcancel()
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errDial, err)
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(4 << 20)

	ch, err := readEnvelope(ctx, ws, 30*time.Second)
	if err != nil {
		return closeReason(err)
	}
	var c agentproto.Challenge
	if ch.Type != agentproto.TypeChallenge || ch.Decode(&c) != nil || len(c.Nonce) < 16 {
		return errors.New("unexpected first message from the server")
	}
	sess := agentproto.NewSession(c.Nonce, cfg.Key(), ed25519.PublicKey(cfg.ServerPublicKey), agentproto.ToServer)
	auth, _ := sess.Seal(agentproto.TypeAuth, "", agentproto.Auth{DeviceID: cfg.DeviceID, AgentVersion: version.Version, ProtocolVersion: agentproto.Version})
	if err := writeEnvelope(ctx, ws, auth); err != nil {
		return err
	}
	wel, err := readEnvelope(ctx, ws, 30*time.Second)
	if err != nil {
		return closeReason(err)
	}
	var w agentproto.Welcome
	if sess.Open(wel) != nil || wel.Type != agentproto.TypeWelcome || wel.Decode(&w) != nil {
		// The server could not prove it holds the key we pinned.
		return errors.New("the server's signature does not match the pinned server key")
	}
	a.Log.Info("connected", "server", cfg.Server, "device", cfg.DeviceID)
	a.markConnected()

	out := make(chan *agentproto.Envelope, 16)
	trySend := func(typ string, v any) bool {
		e, err := sess.Seal(typ, "", v)
		if err != nil {
			return false
		}
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	send := func(typ string, v any) { trySend(typ, v) }
	// reply answers a request by its id.
	reply := func(id string, v any) {
		e, err := sess.Seal(agentproto.TypeManageResult, id, v)
		if err != nil {
			return
		}
		select {
		case out <- e:
		case <-ctx.Done():
		}
	}
	errc := make(chan error, 2)
	go func() { // writer
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-out:
				if err := writeEnvelope(ctx, ws, e); err != nil {
					errc <- err
					return
				}
			}
		}
	}()
	inventoryNow := a.invNow
	if w.InventoryRequired || a.Once {
		a.requestInventory()
	}
	sent := make(chan struct{}, 1)
	// invEvery carries a changed inventory interval to the timers.
	invEvery := make(chan time.Duration, 1)
	go func() { // timers: heartbeat and inventory
		hb := time.NewTicker(seconds(w.HeartbeatSeconds, 60))
		invInterval := a.inventoryInterval(w.InventoryMinutes)
		inv := time.NewTicker(invInterval)
		defer hb.Stop()
		defer inv.Stop()
		a.reportUpdateResult(send)
		gen := a.attachPatchSession(trySend)
		defer a.detachPatchSession(gen)
		a.mu.Lock()
		swRunning, swLast := a.software.running, a.software.last
		a.mu.Unlock()
		if swRunning && swLast != nil {
			trySend(agentproto.TypeSoftwareStatus, *swLast)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				send(agentproto.TypeHeartbeat, collectHeartbeat())
				a.reportUpdateResult(send) // a rollback may finish after the reconnect
			case <-inv.C:
				a.sendInventory(ctx, send)
			case d := <-invEvery:
				if d != invInterval {
					a.Log.Info("inventory interval changed", "from", invInterval, "to", d)
					invInterval = d
					inv.Reset(d)
				}
			case <-inventoryNow:
				a.sendInventory(ctx, send)
				select {
				case sent <- struct{}{}:
				default:
				}
			}
		}
	}()
	go func() { // reader
		for {
			e, err := readEnvelope(ctx, ws, 0)
			if err != nil {
				errc <- closeReason(err)
				return
			}
			if err := sess.Open(e); err != nil {
				errc <- err
				return
			}
			switch e.Type {
			case agentproto.TypeInventoryRequest:
				a.requestInventory()
			case agentproto.TypeRemove:
				errc <- ErrRemoved
				return
			case agentproto.TypeUpdate:
				a.handleUpdate(ctx, e, send)
			case agentproto.TypeAgentConfig:
				var c agentproto.AgentConfig
				if e.Decode(&c) == nil && c.InventoryMinutes > 0 {
					select { // keep only the newest value
					case <-invEvery:
					default:
					}
					invEvery <- a.inventoryInterval(c.InventoryMinutes)
				}
			case agentproto.TypeEndpoints:
				var ep agentproto.Endpoints
				if e.Decode(&ep) == nil {
					a.saveEndpoints(ep.URLs)
				}
			case agentproto.TypePatchConfig:
				a.handlePatchConfig(e)
			case agentproto.TypePatchScan:
				a.handlePatchScan(a.runCtx())
			case agentproto.TypePatchInstall:
				a.handlePatchInstall(e)
			case agentproto.TypeSoftwareJob:
				a.handleSoftwareJob(e)
			case agentproto.TypeConfigPolicy:
				a.handleConfigPolicy(e)
			case agentproto.TypeConfigApply:
				a.handleConfigApply()
			case agentproto.TypeRemoteStart:
				a.handleRemoteStart(e, send)
			case agentproto.TypeRemoteStop:
				a.handleRemoteStop(e)
			case agentproto.TypeManageRequest:
				a.handleManage(e, reply)
			default:
				a.Log.Debug("ignoring message", "type", e.Type)
			}
		}
	}()
	if a.Once {
		select {
		case <-sent:
			// Give the writer a moment to flush, then close cleanly.
			time.Sleep(500 * time.Millisecond)
			ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
			return errSessionDone
		case err := <-errc:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		ws.Close(websocket.StatusGoingAway, "agent stopping") //nolint:errcheck
		return ctx.Err()
	}
}

// saveEndpoints stores the server's address list when it changed.
func (a *Agent) saveEndpoints(urls []string) {
	eps := cleanEndpoints(urls)
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, err := LoadConfig(a.ConfigPath)
	if err != nil || !cfg.Enrolled() || slices.Equal(cfg.Endpoints, eps) || (len(cfg.Endpoints) == 0 && len(eps) == 0) {
		return
	}
	cfg.Endpoints = eps
	if len(eps) == 0 {
		cfg.Endpoints = nil
	}
	if err := SaveConfig(a.ConfigPath, cfg); err != nil {
		a.Log.Warn("saving server addresses", "err", err)
		return
	}
	a.cfg.Endpoints = cfg.Endpoints
	a.Log.Info("server addresses updated", "endpoints", eps)
}

// inventoryInterval turns the server's interval into a duration, never less
// than 15 minutes.
func (a *Agent) inventoryInterval(minutes int) time.Duration {
	minute := a.Minute
	if minute <= 0 {
		minute = time.Minute
	}
	return time.Duration(max(minutes, 15)) * minute
}

func (a *Agent) sendInventory(ctx context.Context, send func(string, any)) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	inv, err := a.Collect(cctx)
	if err != nil {
		a.Log.Warn("inventory incomplete", "err", err)
	}
	inv.CollectedAt = time.Now().UTC()
	inv.Heartbeat = collectHeartbeat()
	if inv.Hostname == "" {
		inv.Hostname, _ = os.Hostname()
	}
	send(agentproto.TypeInventory, inv)
	a.Log.Info("inventory sent", "programs", len(inv.Software))
}

func collectHeartbeat() agentproto.Heartbeat {
	return agentproto.Heartbeat{LoggedInUser: loggedInUser(), BootTime: bootTime(), LocalIPs: localIPs(), AgentVersion: version.Version,
		Volumes: fixedVolumes()}
}

func seconds(v, def int) time.Duration {
	if v < 10 {
		v = def
	}
	return time.Duration(v) * time.Second
}

func closeReason(err error) error {
	switch websocket.CloseStatus(err) {
	case 4404:
		return errUnknownDevice
	case 4401:
		return errors.New("the server rejected this agent's signature")
	}
	return err
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, e *agentproto.Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, raw)
}

func readEnvelope(ctx context.Context, ws *websocket.Conn, timeout time.Duration) (*agentproto.Envelope, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	typ, raw, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("unexpected binary message")
	}
	var e agentproto.Envelope
	return &e, json.Unmarshal(raw, &e)
}

// ---- backoff ----

type backoff struct{ cur time.Duration }

func newBackoff() *backoff { return &backoff{} }

// next returns the next wait: 5 s doubling to 5 minutes, or one hour for
// errors that will not fix themselves soon. Jitter spreads reconnects after a
// server restart.
func (b *backoff) next(slow bool) time.Duration {
	if slow {
		return time.Hour
	}
	if b.cur == 0 {
		b.cur = 5 * time.Second
	} else if b.cur *= 2; b.cur > 5*time.Minute {
		b.cur = 5 * time.Minute
	}
	j, _ := rand.Int(rand.Reader, big.NewInt(int64(b.cur/2)+1))
	return b.cur/2 + time.Duration(j.Int64())
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ---- connection test for the installer wizard ----

// TestResult is printed by "fleetwarden-agent test-connection" as JSON.
type TestResult struct {
	OK          bool      `json:"ok"`
	Server      string    `json:"server"`
	Product     string    `json:"product,omitempty"`
	Version     string    `json:"version,omitempty"`
	Error       string    `json:"error,omitempty"`
	TLS         bool      `json:"tls"`
	CertSubject string    `json:"certSubject,omitempty"`
	CertIssuer  string    `json:"certIssuer,omitempty"`
	CertExpires time.Time `json:"certExpires,omitzero"`
	CertSHA256  string    `json:"certSha256,omitempty"`
}

// TestConnection checks that a FleetWarden server answers at the address and
// reports its certificate.
func TestConnection(ctx context.Context, server string) TestResult {
	res := TestResult{Server: server}
	norm, err := NormalizeServer(server)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Server = norm
	var peer *x509.Certificate
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				peer = cs.PeerCertificates[0]
			}
			return nil
		}}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, norm+agentproto.PathHello, nil)
	r, err := client.Do(req)
	if peer != nil {
		res.TLS = true
		res.CertSubject = peer.Subject.String()
		res.CertIssuer = peer.Issuer.String()
		res.CertExpires = peer.NotAfter
		res.CertSHA256 = fingerprint(peer.Raw)
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer r.Body.Close()
	var h agentproto.Hello
	if r.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&h) != nil || h.ProtocolVersion == 0 {
		res.Error = "this address does not answer as a FleetWarden server (" + r.Status + ")"
		return res
	}
	res.OK, res.Product, res.Version = true, h.Product, h.Version
	return res
}
