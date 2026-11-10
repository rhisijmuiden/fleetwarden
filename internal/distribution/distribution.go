package distribution

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
)

// Timing.
const (
	reportEvery   = time.Minute
	resyncEvery   = 15 * time.Minute // also without a manifest change, to retry failures
	requestTimout = 30 * time.Second
	maxRecent     = 20
)

// Server is a running distribution server.
type Server struct {
	cfg     Config
	log     *slog.Logger
	version string
	key     ed25519.PrivateKey
	id      Identity
	cert    tls.Certificate
	certSHA string
	client  *http.Client
	started time.Time
	now     func() time.Time

	kick      chan struct{}
	bandwidth atomic.Int64 // kbit/s, 0 = unlimited

	mu          sync.Mutex
	manifest    distproto.Manifest
	wanted      string          // manifest version the management server has
	have        map[string]bool // sha256 -> stored and checked
	priority    []string        // asked for by agents, fetch first
	downloading string
	lastSync    *time.Time
	syncErr     string
	enabled     bool
	recent      []distproto.Download
	served      atomic.Int64
	servedBytes atomic.Int64
	misses      atomic.Int64
	refused     atomic.Int64
}

// New loads the key, identity and certificate. The server must be enrolled.
func New(cfg Config, log *slog.Logger, version string) (*Server, error) {
	id, ok, err := LoadIdentity(cfg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not enrolled yet: run `fleetwarden-distribution enroll --server URL --token TOKEN`")
	}
	if cfg.Server == "" {
		cfg.Server = id.Server
	}
	key, err := loadOrCreateKey(cfg.keyPath())
	if err != nil {
		return nil, err
	}
	cert, err := loadOrCreateCert(cfg)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	client, err := upstreamClient(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.ContentDir, 0o700); err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: log, version: version, key: key, id: id, cert: cert, certSHA: distproto.CertSHA256(cert.Certificate[0]),
		client: client, started: time.Now().UTC(), now: time.Now, kick: make(chan struct{}, 1), have: map[string]bool{}, enabled: true}
	s.scanContent()
	return s, nil
}

// upstreamClient talks to the management server; it honours the proxy
// environment and trusts the system CAs plus FW_DIST_CA_FILE.
func upstreamClient(cfg Config) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = time.Minute
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("FW_DIST_CA_FILE: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("FW_DIST_CA_FILE: no certificates found")
		}
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	return &http.Client{Transport: tr}, nil
}

// Enroll registers the distribution server with a one-time token from the
// web interface and stores the identity it gets.
func Enroll(ctx context.Context, cfg Config, server, token, version string) (Identity, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if err := checkServerURL(server); err != nil {
		return Identity{}, err
	}
	key, err := loadOrCreateKey(cfg.keyPath())
	if err != nil {
		return Identity{}, err
	}
	cert, err := loadOrCreateCert(cfg)
	if err != nil {
		return Identity{}, err
	}
	client, err := upstreamClient(cfg)
	if err != nil {
		return Identity{}, err
	}
	host, _ := os.Hostname()
	body, _ := json.Marshal(distproto.EnrollRequest{Token: strings.TrimSpace(token), PublicKey: key.Public().(ed25519.PublicKey), Hostname: host,
		Version: version, Port: cfg.Port(), Addresses: localAddresses(), CertSHA256: distproto.CertSHA256(cert.Certificate[0])})
	ctx, cancel := context.WithTimeout(ctx, requestTimout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server+distproto.PathEnroll, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fleetwarden-distribution/"+version)
	res, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("cannot reach the management server: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var e struct{ Error string }
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&e)
		if e.Error == "invalid_token" {
			return Identity{}, errors.New("the token is unknown, already used or expired; create a new one in the web interface")
		}
		return Identity{}, fmt.Errorf("the management server answered %s %s", res.Status, e.Error)
	}
	var er distproto.EnrollResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&er); err != nil || !strings.HasPrefix(er.ID, distproto.IDPrefix) {
		return Identity{}, errors.New("unexpected answer from the management server")
	}
	id := Identity{ID: er.ID, Name: er.Name, Server: server, EnrolledAt: time.Now().UTC()}
	return id, saveIdentity(cfg, id)
}

// ---- content on disk ----

func (s *Server) contentPath(sum string) string { return filepath.Join(s.cfg.ContentDir, sum) }

// scanContent notes the files already stored.
func (s *Server) scanContent() {
	entries, err := os.ReadDir(s.cfg.ContentDir)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && distproto.ValidSHA(name) {
			s.have[name] = true
		} else if strings.HasSuffix(name, ".part") {
			_ = os.Remove(filepath.Join(s.cfg.ContentDir, name))
		}
	}
}

// stored reports whether a file is here with the expected size.
func (s *Server) stored(sum string, size int64) bool {
	s.mu.Lock()
	ok := s.have[sum]
	s.mu.Unlock()
	if !ok {
		return false
	}
	st, err := os.Stat(s.contentPath(sum))
	return err == nil && (size <= 0 || st.Size() == size)
}

// ---- requests to the management server ----

func (s *Server) signed(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.Server+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentproto.HeaderAuth, agentproto.SignRequest(s.key, s.id.ID, method, path, time.Now()))
	req.Header.Set("User-Agent", "fleetwarden-distribution/"+s.version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (s *Server) getJSON(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimout)
	defer cancel()
	req, err := s.signed(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the management server answered %s", res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(out)
}

// Status is the current report.
func (s *Server) Status() distproto.Status {
	host, _ := os.Hostname()
	st := distproto.Status{Version: s.version, Hostname: host, Port: s.cfg.Port(), Addresses: localAddresses(), CertSHA256: s.certSHA,
		StartedAt: s.started, ServedFiles: s.served.Load(), ServedBytes: s.servedBytes.Load(), Misses: s.misses.Load(),
		Refused: s.refused.Load(), Updatable: s.updatable(), Update: s.updateStatus(), BandwidthKbps: int(s.bandwidth.Load()),
		ReportedAt: s.now().UTC()}
	if free, total, ok := diskSpace(s.cfg.ContentDir); ok {
		st.DiskFree, st.DiskTotal = int64(free), int64(total) //nolint:gosec // disk sizes fit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Manifest, st.Downloading, st.LastSyncAt, st.SyncError = s.manifest.Version, s.downloading, s.lastSync, s.syncErr
	for _, f := range s.manifest.Files {
		if s.have[f.SHA256] {
			st.Files++
			st.Bytes += f.Size
		} else {
			st.PendingFiles++
			st.PendingBytes += f.Size
		}
	}
	st.Recent = slices.Clone(s.recent)
	return st
}

// Report sends the status and acts on the answer.
func (s *Server) Report(ctx context.Context) error {
	body, _ := json.Marshal(s.Status())
	ctx, cancel := context.WithTimeout(ctx, requestTimout)
	defer cancel()
	req, err := s.signed(ctx, http.MethodPost, distproto.PathStatus, body)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("the management server does not know this distribution server any more; enroll it again")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the management server answered %s", res.Status)
	}
	var reply distproto.StatusReply
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&reply); err != nil {
		return err
	}
	s.bandwidth.Store(int64(max(reply.BandwidthKbps, 0)))
	s.mu.Lock()
	s.enabled = reply.Enabled
	changed := reply.Manifest != "" && reply.Manifest != s.manifest.Version
	s.wanted = reply.Manifest
	s.mu.Unlock()
	if changed {
		s.wake()
	}
	if reply.Update != "" {
		s.requestUpdate(reply.Update)
	}
	return nil
}

func (s *Server) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ---- syncing ----

// Sync brings the content in line with the manifest once.
func (s *Server) Sync(ctx context.Context) error {
	s.mu.Lock()
	enabled := s.enabled
	s.mu.Unlock()
	if !enabled {
		return nil
	}
	var m distproto.Manifest
	if err := s.getJSON(ctx, distproto.PathManifest, &m); err != nil {
		return s.syncDone(fmt.Errorf("manifest: %w", err))
	}
	want := map[string]distproto.File{}
	for _, f := range m.Files {
		if distproto.ValidSHA(f.SHA256) {
			want[f.SHA256] = f
		}
	}
	s.mu.Lock()
	s.manifest = m
	s.mu.Unlock()
	// Files no longer approved go first: they free the space.
	if entries, err := os.ReadDir(s.cfg.ContentDir); err == nil {
		for _, e := range entries {
			if _, ok := want[e.Name()]; !ok && distproto.ValidSHA(e.Name()) {
				_ = os.Remove(s.contentPath(e.Name()))
				s.mu.Lock()
				delete(s.have, e.Name())
				s.mu.Unlock()
				s.log.Info("removed a file that is no longer distributed", "sha256", e.Name())
			}
		}
	}
	var firstErr error
	for {
		f, ok := s.next(want)
		if !ok {
			break
		}
		if err := s.fetch(ctx, f); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("download from the management server", "sha256", f.SHA256, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			delete(want, f.SHA256) // try again on the next round
		}
	}
	return s.syncDone(firstErr)
}

func (s *Server) syncDone(err error) error {
	now := s.now().UTC()
	s.mu.Lock()
	s.lastSync, s.downloading, s.syncErr = &now, "", ""
	if err != nil {
		s.syncErr = err.Error()
	}
	s.mu.Unlock()
	return err
}

// next picks the next missing file: those agents asked for first, then the
// smallest, so many files arrive quickly.
func (s *Server) next(want map[string]distproto.File) (distproto.File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.priority) > 0 {
		sum := s.priority[0]
		s.priority = s.priority[1:]
		if f, ok := want[sum]; ok && !s.have[sum] {
			return f, true
		}
	}
	var best distproto.File
	found := false
	for sum, f := range want {
		if s.have[sum] {
			continue
		}
		if !found || f.Size < best.Size || (f.Size == best.Size && f.SHA256 < best.SHA256) {
			best, found = f, true
		}
	}
	return best, found
}

var errDiskFull = errors.New("not enough free disk space")

// fetch downloads one file, checks its hash and stores it.
func (s *Server) fetch(ctx context.Context, f distproto.File) error {
	if free, _, ok := diskSpace(s.cfg.ContentDir); ok && int64(free)-f.Size < s.cfg.MinFreeBytes { //nolint:gosec // disk sizes fit
		return fmt.Errorf("%w for %s (%d bytes)", errDiskFull, f.SHA256, f.Size)
	}
	s.mu.Lock()
	s.downloading = f.SHA256
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.downloading = ""
		s.mu.Unlock()
	}()
	req, err := s.signed(ctx, http.MethodGet, distproto.PathContent+f.SHA256, nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the management server answered %s", res.Status)
	}
	tmp := s.contentPath(f.SHA256) + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), &limitedReader{ctx: ctx, r: io.LimitReader(res.Body, f.Size+1), rate: &s.bandwidth})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != f.Size {
		err = fmt.Errorf("got %d bytes, expected %d", n, f.Size)
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		err = errors.New("SHA-256 does not match")
	}
	if err == nil {
		err = os.Rename(tmp, s.contentPath(f.SHA256))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.have[f.SHA256] = true
	s.mu.Unlock()
	s.log.Info("stored", "sha256", f.SHA256, "kind", f.Kind, "bytes", f.Size)
	return nil
}

// limitedReader keeps the download under the bandwidth limit.
type limitedReader struct {
	ctx   context.Context
	r     io.Reader
	rate  *atomic.Int64
	start time.Time
	n     int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	kbps := l.rate.Load()
	if kbps > 0 && len(p) > 64<<10 {
		p = p[:64<<10]
	}
	n, err := l.r.Read(p)
	if kbps <= 0 || n == 0 {
		return n, err
	}
	if l.start.IsZero() {
		l.start = time.Now()
	}
	l.n += int64(n)
	due := time.Duration(float64(l.n*8) / float64(kbps*1000) * float64(time.Second))
	if wait := due - time.Since(l.start); wait > 0 {
		select {
		case <-l.ctx.Done():
			return n, l.ctx.Err()
		case <-time.After(wait):
		}
	}
	return n, err
}

// ---- serving agents ----

// Handler serves agents and the health check.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		st := s.Status()
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": s.id.Name, "version": s.version, "files": st.Files,
			"pendingFiles": st.PendingFiles, "manifest": st.Manifest})
	})
	mux.HandleFunc("GET /agent/v1/", s.handleAgent)
	return mux
}

// handleAgent serves a file to an agent after the management server said
// the agent may have it.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	auth := r.Header.Get(agentproto.HeaderAuth)
	if !distproto.Served(path) {
		http.NotFound(w, r)
		return
	}
	if auth == "" {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	dev, _, _, err := agentproto.ParseRequestAuth(auth)
	if err != nil {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	ok, ans, err := s.authorize(r.Context(), path, auth)
	if err != nil {
		s.log.Warn("authorize at the management server", "path", path, "err", err)
		http.Error(w, "management server unreachable", http.StatusServiceUnavailable)
		return
	}
	if !ok {
		s.refused.Add(1)
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	if !s.stored(ans.SHA256, ans.Size) {
		s.misses.Add(1)
		s.mu.Lock()
		if !slices.Contains(s.priority, ans.SHA256) {
			s.priority = append(s.priority, ans.SHA256)
		}
		s.mu.Unlock()
		s.wake()
		w.Header().Set("Retry-After", "60")
		http.Error(w, "not stored here yet", http.StatusServiceUnavailable)
		return
	}
	f, err := os.Open(s.contentPath(ans.SHA256))
	if err != nil {
		http.Error(w, "not stored here yet", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	cw := &countingWriter{ResponseWriter: w}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(cw, r, ans.SHA256, st.ModTime(), f)
	s.served.Add(1)
	s.servedBytes.Add(cw.n)
	s.mu.Lock()
	s.recent = append([]distproto.Download{{SHA256: ans.SHA256, Device: dev, Bytes: cw.n, At: s.now().UTC()}}, s.recent...)
	if len(s.recent) > maxRecent {
		s.recent = s.recent[:maxRecent]
	}
	s.mu.Unlock()
}

// authorize forwards the agent's signed request to the management server.
func (s *Server) authorize(ctx context.Context, path, agentAuth string) (bool, distproto.AuthorizeReply, error) {
	var ans distproto.AuthorizeReply
	ctx, cancel := context.WithTimeout(ctx, requestTimout)
	defer cancel()
	req, err := s.signed(ctx, http.MethodGet, distproto.PathAuthorize, nil)
	if err != nil {
		return false, ans, err
	}
	req.Header.Set(distproto.HeaderAgentAuth, agentAuth)
	req.Header.Set(distproto.HeaderAgentPath, path)
	res, err := s.client.Do(req)
	if err != nil {
		return false, ans, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&ans); err != nil || !distproto.ValidSHA(ans.SHA256) {
			return false, ans, errors.New("unexpected answer")
		}
		return true, ans, nil
	case http.StatusNotFound, http.StatusForbidden, http.StatusBadRequest:
		return false, ans, nil
	}
	return false, ans, fmt.Errorf("the management server answered %s", res.Status)
}

type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- running ----

// Certificate is the agent-facing certificate (tests serve it themselves).
func (s *Server) Certificate() tls.Certificate { return s.cert }

// Loop reports and syncs until ctx ends.
func (s *Server) Loop(ctx context.Context) {
	go func() {
		t := time.NewTicker(reportEvery)
		defer t.Stop()
		for {
			if err := s.Report(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("report to the management server", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	t := time.NewTicker(resyncEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-t.C:
		}
		if err := s.Sync(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("sync", "err", err)
		}
		_ = s.Report(ctx) // show the result now
	}
}

// Run serves agents and syncs until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{s.cert}},
	}
	errc := make(chan error, 1)
	go func() {
		s.log.Info("serving agents", "addr", ln.Addr().String(), "certificate", s.certSHA)
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	go s.Loop(ctx)
	s.wake()
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	return err
}
