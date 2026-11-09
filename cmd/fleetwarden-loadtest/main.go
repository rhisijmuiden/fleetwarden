// Command fleetwarden-loadtest simulates many agents against a FleetWarden
// server and measures how the server copes: enrollment, WebSocket sessions,
// heartbeats and inventories, plus the response times of the web interface's
// API while the agents are connected.
//
//	fleetwarden-loadtest -server https://em.example.com -token fwe_... -agents 2000
//
// The simulated agents enroll once; their keys are kept in a state file, so a
// second run reconnects the same devices instead of creating new ones. Use a
// test server: the devices appear in the device list like real ones.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type options struct {
	server, token, state, report string
	agents, software, enrollers  int
	ramp, duration, heartbeat    time.Duration
	apiUser, apiPass             string
	apiInterval                  time.Duration
	reconnectStorm, cleanup      bool
}

// identity is one simulated agent, kept in the state file.
type identity struct {
	Host     string `json:"host"`
	DeviceID string `json:"deviceId"`
	Seed     []byte `json:"seed"`
	ServerPK []byte `json:"serverPublicKey"`
}

func main() {
	var o options
	flag.StringVar(&o.server, "server", "http://127.0.0.1:8080", "server URL")
	flag.StringVar(&o.token, "token", "", "enrollment token (needed for agents not yet in the state file)")
	flag.StringVar(&o.state, "state", "loadtest-agents.json", "file with the simulated agents' keys")
	flag.StringVar(&o.report, "report", "", "write the results as JSON to this file")
	flag.IntVar(&o.agents, "agents", 2000, "number of simulated agents")
	flag.IntVar(&o.software, "software", 150, "programs in each simulated inventory")
	flag.IntVar(&o.enrollers, "enrollers", 16, "parallel enrollments")
	flag.DurationVar(&o.ramp, "ramp", 2*time.Minute, "time over which the agents connect")
	flag.DurationVar(&o.duration, "duration", 10*time.Minute, "how long to keep the agents connected after the ramp")
	flag.DurationVar(&o.heartbeat, "heartbeat", 0, "heartbeat interval (0: what the server asks)")
	flag.StringVar(&o.apiUser, "api-user", "", "web interface user for API response times (optional, no TOTP)")
	flag.StringVar(&o.apiPass, "api-password", "", "password of -api-user")
	flag.DurationVar(&o.apiInterval, "api-interval", 2*time.Second, "time between API measurements")
	flag.BoolVar(&o.reconnectStorm, "reconnect-storm", true, "after the run, drop all agents at once and measure how fast they are back")
	flag.BoolVar(&o.cleanup, "cleanup", false, "delete the simulated devices (needs -api-user) and the state file, then stop")
	flag.Parse()
	o.server = strings.TrimRight(o.server, "/")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.cleanup {
		if err := cleanup(ctx, o); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(ctx, o); err != nil {
		log.Fatal(err)
	}
}

// ---- statistics ----

type latencies struct {
	mu sync.Mutex
	v  []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.v = append(l.v, d)
	l.mu.Unlock()
}

type summary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50Ms"`
	P95   float64 `json:"p95Ms"`
	P99   float64 `json:"p99Ms"`
	Max   float64 `json:"maxMs"`
}

func (l *latencies) summary() summary {
	l.mu.Lock()
	v := append([]time.Duration(nil), l.v...)
	l.mu.Unlock()
	if len(v) == 0 {
		return summary{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	q := func(p float64) float64 {
		i := int(math.Ceil(p*float64(len(v)))) - 1
		return ms(v[max(i, 0)])
	}
	return summary{Count: len(v), P50: q(0.50), P95: q(0.95), P99: q(0.99), Max: ms(v[len(v)-1])}
}

func ms(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/10) / 100 }

type stats struct {
	enroll, connect, inventory, heartbeat latencies
	connected, everConnected              atomic.Int64
	sessions, failures, drops             atomic.Int64
	heartbeats, inventories, requests     atomic.Int64
	errMu                                 sync.Mutex
	errs                                  map[string]int
}

func (s *stats) fail(err error) {
	s.failures.Add(1)
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	s.errMu.Lock()
	s.errs[msg]++
	s.errMu.Unlock()
}

// ---- run ----

type report struct {
	Server        string             `json:"server"`
	ServerVersion string             `json:"serverVersion"`
	Agents        int                `json:"agents"`
	Software      int                `json:"softwarePerInventory"`
	Started       time.Time          `json:"started"`
	Ramp          string             `json:"ramp"`
	Duration      string             `json:"duration"`
	PeakConnected int64              `json:"peakConnected"`
	Sessions      int64              `json:"sessions"`
	Failures      int64              `json:"failures"`
	Drops         int64              `json:"unexpectedDisconnects"`
	Heartbeats    int64              `json:"heartbeats"`
	Inventories   int64              `json:"inventories"`
	ServerReqs    int64              `json:"serverRequestsAnswered"`
	Enroll        summary            `json:"enroll"`
	Connect       summary            `json:"connect"`
	Inventory     summary            `json:"inventoryAck"`
	Heartbeat     summary            `json:"heartbeatWrite"`
	API           map[string]summary `json:"api,omitempty"`
	Reconnect     *reconnectResult   `json:"reconnectStorm,omitempty"`
	Errors        map[string]int     `json:"errors,omitempty"`
}

type reconnectResult struct {
	AllBackSeconds float64 `json:"allBackSeconds"`
	Back           int64   `json:"back"`
	Connect        summary `json:"connect"`
}

func run(ctx context.Context, o options) error {
	st := &stats{errs: map[string]int{}}
	ids, err := loadState(o.state)
	if err != nil {
		return err
	}
	var hello agentproto.Hello
	if err := getJSON(ctx, http.DefaultClient, o.server+agentproto.PathHello, &hello); err != nil {
		return fmt.Errorf("server hello: %w", err)
	}
	log.Printf("server %s, version %s; %d agents in %s", o.server, hello.Version, len(ids), o.state)

	if missing := o.agents - len(ids); missing > 0 {
		if o.token == "" {
			return fmt.Errorf("%d agents must still enroll: pass -token", missing)
		}
		log.Printf("enrolling %d agents ...", missing)
		ids, err = enrollAll(ctx, o, ids, missing, st)
		if err != nil {
			return err
		}
		if err := saveState(o.state, ids); err != nil {
			return err
		}
	}
	ids = ids[:o.agents]

	rep := report{Server: o.server, ServerVersion: hello.Version, Agents: o.agents, Software: o.software,
		Started: time.Now().UTC(), Ramp: o.ramp.String(), Duration: o.duration.String()}

	runCtx, cancelAgents := context.WithCancel(ctx)
	var wg sync.WaitGroup
	tr := &http.Transport{MaxIdleConnsPerHost: 64}
	reconnect := make(chan struct{})
	var peak atomic.Int64
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			delay := time.Duration(int64(o.ramp) * int64(i) / int64(len(ids)))
			select {
			case <-time.After(delay):
			case <-runCtx.Done():
				return
			}
			simulate(runCtx, o, &ids[i], tr, st, reconnect)
		}(i)
	}

	// API measurements while the agents run.
	api := map[string]*latencies{}
	apiDone := make(chan struct{})
	if o.apiUser != "" {
		go func() {
			defer close(apiDone)
			measureAPI(runCtx, o, api, st)
		}()
	} else {
		close(apiDone)
	}

	tick := time.NewTicker(10 * time.Second)
	end := time.After(o.ramp + o.duration)
	start := time.Now()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-end:
			break loop
		case <-tick.C:
			c := st.connected.Load()
			if c > peak.Load() {
				peak.Store(c)
			}
			hb := st.heartbeat.summary()
			log.Printf("%4.0fs connected %d/%d, failures %d, drops %d, heartbeats %d (p95 %.1f ms), inventories %d",
				time.Since(start).Seconds(), c, len(ids), st.failures.Load(), st.drops.Load(), st.heartbeats.Load(), hb.P95, st.inventories.Load())
		}
	}
	tick.Stop()

	if o.reconnectStorm && ctx.Err() == nil {
		rep.Reconnect = storm(ctx, o, st, reconnect, len(ids))
	}
	cancelAgents()
	wg.Wait()
	<-apiDone

	rep.PeakConnected = max(peak.Load(), st.connected.Load())
	rep.Sessions, rep.Failures, rep.Drops = st.sessions.Load(), st.failures.Load(), st.drops.Load()
	rep.Heartbeats, rep.Inventories, rep.ServerReqs = st.heartbeats.Load(), st.inventories.Load(), st.requests.Load()
	rep.Enroll, rep.Connect, rep.Inventory, rep.Heartbeat = st.enroll.summary(), st.connect.summary(), st.inventory.summary(), st.heartbeat.summary()
	if len(api) > 0 {
		rep.API = map[string]summary{}
		for k, v := range api {
			rep.API[k] = v.summary()
		}
	}
	rep.Errors = st.errs
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	if o.report != "" {
		return os.WriteFile(o.report, out, 0o644)
	}
	return nil
}

// storm drops every session at once (like a server restart from the agents'
// point of view) and measures how long it takes until all are back.
func storm(ctx context.Context, o options, st *stats, reconnect chan struct{}, n int) *reconnectResult {
	log.Printf("reconnect storm: dropping %d sessions", st.connected.Load())
	st.connect.mu.Lock()
	st.connect.v = nil
	st.connect.mu.Unlock()
	before := st.sessions.Load()
	start := time.Now()
	close(reconnect)
	deadline := time.After(5 * time.Minute)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			back := st.sessions.Load() - before
			log.Printf("reconnect storm: only %d of %d back after 5 minutes", back, n)
			return &reconnectResult{AllBackSeconds: -1, Back: back, Connect: st.connect.summary()}
		case <-t.C:
			if back := st.sessions.Load() - before; back >= int64(n) {
				d := time.Since(start).Seconds()
				log.Printf("reconnect storm: all %d back in %.1f s", n, d)
				return &reconnectResult{AllBackSeconds: math.Round(d*10) / 10, Back: back, Connect: st.connect.summary()}
			}
		}
	}
}

// ---- enrollment ----

func enrollAll(ctx context.Context, o options, ids []identity, n int, st *stats) ([]identity, error) {
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	base := len(ids)
	for w := 0; w < o.enrollers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				id, err := enroll(ctx, o, fmt.Sprintf("LOADTEST-%05d", base+i+1), st)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					ids = append(ids, id)
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < n && ctx.Err() == nil; i++ {
		mu.Lock()
		failed := firstErr != nil
		mu.Unlock()
		if failed {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return ids, fmt.Errorf("enroll: %w (%d enrolled; they are saved)", firstErr, len(ids)-base)
	}
	return ids, nil
}

func enroll(ctx context.Context, o options, host string, st *stats) (identity, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return identity{}, err
	}
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: o.token, PublicKey: pub, MachineID: "loadtest-" + host, Hostname: host,
		Platform: "windows", Arch: "amd64", AgentVersion: "loadtest", Proof: ed25519.Sign(key, agentproto.EnrollProof(o.token, pub))})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.server+agentproto.PathEnroll, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	t0 := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return identity{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return identity{}, fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	var er agentproto.EnrollResponse
	if err := json.NewDecoder(res.Body).Decode(&er); err != nil {
		return identity{}, err
	}
	st.enroll.add(time.Since(t0))
	return identity{Host: host, DeviceID: er.DeviceID, Seed: key.Seed(), ServerPK: er.ServerPublicKey}, nil
}

// ---- one simulated agent ----

func simulate(ctx context.Context, o options, id *identity, tr *http.Transport, st *stats, reconnect chan struct{}) {
	backoff := time.Second
	for ctx.Err() == nil {
		dropped, err := session(ctx, o, id, tr, st, reconnect)
		if ctx.Err() != nil {
			return
		}
		if dropped {
			reconnect = nil // the storm happens once
			backoff = time.Second
		} else if err != nil {
			st.fail(err)
			backoff = min(backoff*2, 60*time.Second)
		}
		// Same as the real agent: wait with jitter before reconnecting.
		wait := backoff/2 + time.Duration(mrand.Int64N(int64(backoff)))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// session runs one WebSocket session. dropped reports a deliberate disconnect
// of the reconnect storm.
func session(ctx context.Context, o options, id *identity, tr *http.Transport, st *stats, reconnect chan struct{}) (dropped bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	key := ed25519.NewKeyFromSeed(id.Seed)
	t0 := time.Now()
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	ws, res, err := websocket.Dial(dctx, wsURL(o.server)+agentproto.PathWS, &websocket.DialOptions{
		HTTPClient:      &http.Client{Transport: tr},
		CompressionMode: websocket.CompressionContextTakeover,
	})
	dcancel()
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(16 << 20)

	ch, err := read(ctx, ws, 30*time.Second)
	if err != nil {
		return false, fmt.Errorf("challenge: %w", err)
	}
	var c agentproto.Challenge
	if ch.Type != agentproto.TypeChallenge || ch.Decode(&c) != nil {
		return false, errors.New("unexpected first message")
	}
	sess := agentproto.NewSession(c.Nonce, key, ed25519.PublicKey(id.ServerPK), agentproto.ToServer)
	auth, _ := sess.Seal(agentproto.TypeAuth, "", agentproto.Auth{DeviceID: id.DeviceID, AgentVersion: "loadtest", ProtocolVersion: agentproto.Version})
	if err := write(ctx, ws, auth); err != nil {
		return false, err
	}
	wel, err := read(ctx, ws, 30*time.Second)
	if err != nil {
		return false, fmt.Errorf("welcome: %w", err)
	}
	var w agentproto.Welcome
	if sess.Open(wel) != nil || wel.Type != agentproto.TypeWelcome || wel.Decode(&w) != nil {
		return false, errors.New("bad welcome")
	}
	st.connect.add(time.Since(t0))
	st.sessions.Add(1)
	st.connected.Add(1)
	defer st.connected.Add(-1)

	var wmu sync.Mutex
	send := func(typ string, v any) error {
		e, err := sess.Seal(typ, "", v)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		return write(ctx, ws, e)
	}
	sendInventory := func() {
		t := time.Now()
		if err := send(agentproto.TypeInventory, fakeInventory(id.Host, o.software)); err == nil {
			st.inventory.add(time.Since(t))
			st.inventories.Add(1)
		}
	}
	if w.InventoryRequired {
		go sendInventory()
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			e, err := read(ctx, ws, 0)
			if err != nil {
				readErr <- err
				return
			}
			if sess.Open(e) != nil {
				readErr <- errors.New("bad signature from server")
				return
			}
			switch e.Type {
			case agentproto.TypeInventoryRequest:
				st.requests.Add(1)
				go sendInventory()
			case agentproto.TypeRemove:
				readErr <- errors.New("server removed the device")
				return
			default:
				// Jobs (patch scans, configurations, ...) are not simulated.
			}
		}
	}()

	every := o.heartbeat
	if every <= 0 {
		every = time.Duration(max(w.HeartbeatSeconds, 10)) * time.Second
	}
	// Spread the first heartbeat over the interval, like agents that started at different times.
	timer := time.NewTimer(time.Duration(mrand.Int64N(int64(every))))
	defer timer.Stop()
	boot := time.Now().Add(-time.Duration(mrand.IntN(72)) * time.Hour)
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case <-reconnect:
			ws.Close(websocket.StatusGoingAway, "reconnect storm") //nolint:errcheck
			return true, nil
		case err := <-readErr:
			st.drops.Add(1)
			return false, fmt.Errorf("session: %w", err)
		case <-timer.C:
			t := time.Now()
			err := send(agentproto.TypeHeartbeat, agentproto.Heartbeat{LoggedInUser: "CORP\\user", BootTime: boot,
				LocalIPs: []string{"10.20.0.1"}, AgentVersion: "loadtest",
				Volumes: []agentproto.Volume{{Mount: "C:", SizeBytes: 512 << 30, FreeBytes: int64(100+mrand.IntN(300)) << 30}}})
			if err != nil {
				st.drops.Add(1)
				return false, fmt.Errorf("heartbeat: %w", err)
			}
			st.heartbeat.add(time.Since(t))
			st.heartbeats.Add(1)
			timer.Reset(every)
		}
	}
}

// fakeInventory builds a Windows inventory; most programs are shared between
// devices, like in a real fleet.
func fakeInventory(host string, n int) agentproto.Inventory {
	sw := make([]agentproto.Software, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Common Application %03d", i)
		if i%10 == 9 {
			name = fmt.Sprintf("Department Tool %03d", mrand.IntN(500))
		}
		sw = append(sw, agentproto.Software{Name: name, Version: fmt.Sprintf("%d.%d.%d", 1+i%9, i%7, mrand.IntN(3)),
			Publisher: fmt.Sprintf("Vendor %02d", i%40), Source: "registry", InstalledOn: "2026-10-01", SizeBytes: int64(i+1) << 20})
	}
	return agentproto.Inventory{
		CollectedAt: time.Now().UTC(), Hostname: host,
		OS: agentproto.OSInfo{Name: "Windows 11 Pro", Version: "24H2", Build: "26100.2314", Kind: "workstation", Domain: "corp.example", DomainJoined: true},
		Hardware: agentproto.Hardware{Manufacturer: "Dell Inc.", Model: "Latitude 7450", Serial: strings.TrimPrefix(host, "LOADTEST-"), CPU: "Intel Core Ultra 7 165U",
			CPUCores: 12, MemoryBytes: 32 << 30, Disks: []agentproto.Disk{{Name: "disk0", SizeBytes: 512 << 30, Media: "nvme"}}},
		Software:  sw,
		Heartbeat: agentproto.Heartbeat{LoggedInUser: "CORP\\user", LocalIPs: []string{"10.20.0.1"}, AgentVersion: "loadtest"},
	}
}

// ---- API measurements ----

// apiPaths are the pages an administrator opens most.
var apiPaths = []string{
	"/api/v1/devices?limit=50",
	"/api/v1/devices/stats",
	"/api/v1/patches/summary",
	"/api/v1/alerts/count",
	"/api/v1/security/summary",
	"/api/v1/vulnerabilities/summary",
	"/api/v1/vulnerabilities?limit=100",
	"/api/v1/software/installed",
	"/api/v1/assets",
}

// login signs in to the web interface API.
func login(ctx context.Context, o options) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 60 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": o.apiUser, "password": o.apiPass})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, o.server+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "fleetwarden")
	res, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api login: %w", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api login: %s", res.Status)
	}
	return cl, nil
}

// cleanup deletes the simulated devices from the server.
func cleanup(ctx context.Context, o options) error {
	ids, err := loadState(o.state)
	if err != nil {
		return err
	}
	cl, err := login(ctx, o)
	if err != nil {
		return err
	}
	deleted := 0
	for _, id := range ids {
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, o.server+"/api/v1/devices/"+id.DeviceID, nil)
		req.Header.Set("X-Requested-With", "fleetwarden")
		res, err := cl.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode/100 == 2 || res.StatusCode == http.StatusNotFound {
			deleted++
		} else {
			return fmt.Errorf("delete %s: %s", id.Host, res.Status)
		}
	}
	log.Printf("deleted %d simulated devices", deleted)
	return os.Remove(o.state)
}

func measureAPI(ctx context.Context, o options, out map[string]*latencies, st *stats) {
	cl, err := login(ctx, o)
	if err != nil {
		st.fail(err)
		return
	}
	for _, p := range apiPaths {
		out[p] = &latencies{}
	}
	t := time.NewTicker(o.apiInterval)
	defer t.Stop()
	for {
		for _, p := range apiPaths {
			start := time.Now()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, o.server+p, nil)
			res, err := cl.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				st.fail(fmt.Errorf("api %s: %w", p, err))
				continue
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				st.fail(fmt.Errorf("api %s: %s", p, res.Status))
				continue
			}
			out[p].add(time.Since(start))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- helpers ----

func wsURL(server string) string {
	if strings.HasPrefix(server, "https://") {
		return "wss://" + strings.TrimPrefix(server, "https://")
	}
	return "ws://" + strings.TrimPrefix(server, "http://")
}

func read(ctx context.Context, ws *websocket.Conn, timeout time.Duration) (*agentproto.Envelope, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_, raw, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	var e agentproto.Envelope
	return &e, json.Unmarshal(raw, &e)
}

func write(ctx context.Context, ws *websocket.Conn, e *agentproto.Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, raw)
}

func getJSON(ctx context.Context, cl *http.Client, url string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New(res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

func loadState(path string) ([]identity, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []identity
	return ids, json.Unmarshal(raw, &ids)
}

func saveState(path string, ids []identity) error {
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
