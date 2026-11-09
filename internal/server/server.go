// Package server implements the FleetWarden management server HTTP API.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/backup"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/secrets"
	"github.com/rhisijmuiden/fleetwarden/internal/sso"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// Pinger checks that a dependency such as the database is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the server's dependencies. Store and Secrets may be nil when no
// database is configured; the API then answers 503.
type Deps struct {
	DB      Pinger
	Store   *store.Store
	Secrets *secrets.Box
	Log     *slog.Logger
	// Mailer sends e-mail; defaults to SMTP. Tests replace it.
	Mailer  Mailer
	Updates *updates.Checker
	SSO     *sso.Client
	// Backup overrides how backup locations are reached (tests).
	Backup backup.Options
	// AgentKey signs messages to agents; a fresh key is generated when nil (tests).
	AgentKey ed25519.PrivateKey
	// Winget is the software catalog; defaults to the winget community source.
	Winget *catalog.Winget
}

// Mailer delivers one message with a mail configuration.
type Mailer interface {
	Send(ctx context.Context, cfg mail.Config, msg mail.Message) error
}

// Server is the HTTP server.
type Server struct {
	cfg      config.Server
	db       Pinger
	st       *store.Store
	box      *secrets.Box
	log      *slog.Logger
	mailer   Mailer
	sso      *sso.Client
	updates  *updates.Checker
	ipFails  *auth.Limiter
	usrFails *auth.Limiter
	now      func() time.Time

	agentKey   ed25519.PrivateKey
	hub        *agentHub
	remote     remoteHub
	manage     manageHub
	pkgs       pkgCatalog
	agentFails *auth.Limiter
	fetcher    releaseFetcher
	winget     *catalog.Winget

	backupOpts backup.Options
	backupMu   sync.Mutex // held while a backup runs or a restore is prepared
	progress   progressState
	restore    restorePrep
	gateways   gatewayManager
	drivers    driverRepo
	vendors    vendorSync
	appOnce    sync.Once
	app        http.Handler
	trusted    atomic.Pointer[[]netip.Prefix] // FW_TRUSTED_PROXIES plus the web interface list
	hsts       atomic.Bool
	probes     probeSet
	bgCtx      context.Context // ends when RunWorkers' context ends
	bgCancel   context.CancelFunc
	bgWG       sync.WaitGroup
	// workersStarted is when RunWorkers started; offline alerts wait for
	// agents to reconnect after a restart.
	workersStarted time.Time
}

// Session lifetimes.
const (
	sessionAbsolute = 12 * time.Hour
	sessionIdle     = 2 * time.Hour
	mfaWindow       = 5 * time.Minute
)

// New creates a server.
func New(cfg config.Server, d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Mailer == nil {
		d.Mailer = &mail.Sender{}
	}
	if d.Updates == nil {
		d.Updates = updates.NewChecker("rhisijmuiden/fleetwarden")
	}
	if d.SSO == nil {
		d.SSO = sso.NewClient()
	}
	if d.AgentKey == nil {
		_, d.AgentKey, _ = ed25519.GenerateKey(rand.Reader)
	}
	if d.Winget == nil {
		d.Winget = &catalog.Winget{Dir: filepath.Join(cfg.DataDir, "catalog")}
	}
	if err := d.Winget.Open(); err != nil {
		d.Log.Warn("winget catalog", "err", err)
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	s := &Server{winget: d.Winget,
		agentKey: d.AgentKey, hub: newAgentHub(), agentFails: auth.NewLimiter(30, 15*time.Minute),
		bgCtx: bgCtx, bgCancel: bgCancel,
		cfg: cfg, db: d.DB, st: d.Store, box: d.Secrets, log: d.Log, mailer: d.Mailer, sso: d.SSO, updates: d.Updates, backupOpts: d.Backup,
		ipFails:  auth.NewLimiter(20, 15*time.Minute),
		usrFails: auth.NewLimiter(8, 15*time.Minute),
		now:      time.Now,
	}
	if s.st != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.applyProxySettings(s.loadProxySettings(ctx))
		cancel()
	}
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.withClientIP(s.appHandler())
}

// appHandler is the API and web interface without the client address
// resolution; gateway tunnels set the address themselves.
func (s *Server) appHandler() http.Handler {
	s.appOnce.Do(func() { s.app = s.buildApp() })
	return s.app
}

func (s *Server) buildApp() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	if s.st != nil {
		s.routes(mux)
	} else {
		mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				http.NotFound(w, r)
				return
			}
			apiError(w, http.StatusServiceUnavailable, "no_database")
		})
	}
	mux.Handle("/", s.webHandler())
	return securityHeaders(csrfGuard(mux))
}

func (s *Server) routes(mux *http.ServeMux) {
	// Public: login page needs these before anyone is signed in.
	mux.HandleFunc("GET /api/v1/auth/config", s.handleAuthConfig)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/totp", s.handleLoginTOTP)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/sso/start", s.handleSSOStart)
	mux.HandleFunc("GET /api/v1/auth/sso/callback", s.handleSSOCallback)
	mux.HandleFunc("GET /api/v1/branding", s.handleBranding)
	mux.HandleFunc("GET /api/v1/branding/{asset}", s.handleBrandingAsset)
	s.agentRoutes(mux)
	s.deviceRoutes(mux)
	s.patchRoutes(mux)
	s.powerRoutes(mux)
	s.driverRoutes(mux)
	s.agentUpdateRoutes(mux)
	s.softwareRoutes(mux)
	s.configRoutes(mux)
	s.remoteRoutes(mux)
	s.manageRoutes(mux)
	s.reportRoutes(mux)
	s.alertRoutes(mux)
	s.gatewayRoutes(mux)
	s.proxyRoutes(mux)

	// Signed in. allowPending lets a user with a forced password change reach these.
	mux.Handle("GET /api/v1/auth/me", s.authed("", allowPending, s.handleMe))
	mux.Handle("POST /api/v1/me/password", s.authed("", allowPending, s.handleChangePassword))
	mux.Handle("PUT /api/v1/me/language", s.authed("", allowPending, s.handleSetLanguage))
	mux.Handle("POST /api/v1/me/totp/setup", s.authed("", 0, s.handleTOTPSetup))
	mux.Handle("POST /api/v1/me/totp/enable", s.authed("", 0, s.handleTOTPEnable))
	mux.Handle("POST /api/v1/me/totp/disable", s.authed("", 0, s.handleTOTPDisable))

	mux.Handle("GET /api/v1/roles", s.authed(auth.PermUsersManage, 0, s.handleRoles))
	mux.Handle("GET /api/v1/users", s.authed(auth.PermUsersManage, 0, s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.authed(auth.PermUsersManage, 0, s.handleCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.authed(auth.PermUsersManage, 0, s.handleUpdateUser))
	mux.Handle("DELETE /api/v1/users/{id}", s.authed(auth.PermUsersManage, 0, s.handleDeleteUser))
	mux.Handle("POST /api/v1/users/{id}/reset-password", s.authed(auth.PermUsersManage, 0, s.handleResetPassword))

	mux.Handle("GET /api/v1/settings/authentication", s.authed(auth.PermSettingsManage, 0, s.handleGetAuthSettings))
	mux.Handle("PUT /api/v1/settings/authentication", s.authed(auth.PermSettingsManage, 0, s.handlePutAuthSettings))
	mux.Handle("POST /api/v1/settings/authentication/test", s.authed(auth.PermSettingsManage, 0, s.handleTestSSO))
	mux.Handle("GET /api/v1/settings/role-mappings", s.authed(auth.PermSettingsManage, 0, s.handleListMappings))
	mux.Handle("POST /api/v1/settings/role-mappings", s.authed(auth.PermSettingsManage, 0, s.handleCreateMapping))
	mux.Handle("DELETE /api/v1/settings/role-mappings/{id}", s.authed(auth.PermSettingsManage, 0, s.handleDeleteMapping))

	mux.Handle("GET /api/v1/settings/mail", s.authed(auth.PermSettingsManage, 0, s.handleGetMail))
	mux.Handle("PUT /api/v1/settings/mail", s.authed(auth.PermSettingsManage, 0, s.handlePutMail))
	mux.Handle("POST /api/v1/settings/mail/test", s.authed(auth.PermSettingsManage, 0, s.handleTestMail))
	mux.Handle("GET /api/v1/mail/log", s.authed(auth.PermSettingsManage, 0, s.handleMailLog))
	mux.Handle("POST /api/v1/mail/log/{id}/retry", s.authed(auth.PermSettingsManage, 0, s.handleMailRetry))

	mux.Handle("PUT /api/v1/settings/branding", s.authed(auth.PermSettingsManage, 0, s.handlePutBranding))
	mux.Handle("PUT /api/v1/settings/branding/{asset}", s.authed(auth.PermSettingsManage, 0, s.handleUploadAsset))
	mux.Handle("DELETE /api/v1/settings/branding/{asset}", s.authed(auth.PermSettingsManage, 0, s.handleDeleteAsset))

	mux.Handle("GET /api/v1/settings/general", s.authed(auth.PermSettingsManage, 0, s.handleGetGeneral))
	mux.Handle("PUT /api/v1/settings/general", s.authed(auth.PermSettingsManage, 0, s.handlePutGeneral))
	mux.Handle("GET /api/v1/settings/scans", s.authed(auth.PermSettingsManage, 0, s.handleGetScans))
	mux.Handle("PUT /api/v1/settings/scans", s.authed(auth.PermSettingsManage, 0, s.handlePutScans))

	mux.Handle("GET /api/v1/audit", s.authed(auth.PermAuditView, 0, s.handleAudit))

	mux.Handle("GET /api/v1/system/update", s.authed(auth.PermSystemUpdate, 0, s.handleUpdateStatus))
	mux.Handle("POST /api/v1/system/update/check", s.authed(auth.PermSystemUpdate, 0, s.handleUpdateCheck))
	mux.Handle("PUT /api/v1/system/channel", s.authed(auth.PermSystemUpdate, 0, s.handleSetChannel))
	mux.Handle("POST /api/v1/system/update/apply", s.authed(auth.PermSystemUpdate, 0, s.handleApplyUpdate))

	bk := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermBackupManage, 0, h) }
	mux.Handle("GET /api/v1/backup/config", bk(s.handleGetBackupConfig))
	mux.Handle("PUT /api/v1/backup/schedule", bk(s.handlePutSchedule))
	mux.Handle("PUT /api/v1/backup/passphrase", bk(s.handlePutPassphrase))
	mux.Handle("GET /api/v1/backup/locations", bk(s.handleListLocations))
	mux.Handle("POST /api/v1/backup/locations", bk(s.handleCreateLocation))
	mux.Handle("PUT /api/v1/backup/locations/{id}", bk(s.handleUpdateLocation))
	mux.Handle("DELETE /api/v1/backup/locations/{id}", bk(s.handleDeleteLocation))
	mux.Handle("POST /api/v1/backup/locations/test", bk(s.handleTestLocation))
	mux.Handle("POST /api/v1/backup/ssh-key", bk(s.handleSSHKey))
	mux.Handle("POST /api/v1/backup/run", bk(s.handleRunBackup))
	mux.Handle("GET /api/v1/backup/progress", bk(s.handleBackupProgress))
	mux.Handle("GET /api/v1/backup/runs", bk(s.handleBackupRuns))
	mux.Handle("POST /api/v1/backup/snapshots", bk(s.handleListSnapshots))
	mux.Handle("POST /api/v1/backup/snapshots/details", bk(s.handleSnapshotDetails))
	mux.Handle("GET /api/v1/backup/restore", bk(s.handleRestoreStatus))
	mux.Handle("POST /api/v1/backup/restore", bk(s.handleStartRestore))
	mux.Handle("DELETE /api/v1/backup/restore", bk(s.handleCancelRestore))
}

type health struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Version  string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := health{Status: "ok", Database: "not configured", Version: version.Version}
	code := http.StatusOK
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			s.log.Warn("database health check failed", "err", err)
			h.Status, h.Database, code = "degraded", "unreachable", http.StatusServiceUnavailable
		} else {
			h.Database = "ok"
		}
	}
	writeJSON(w, code, h)
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, version.Get())
}

// webHandler serves the single-page web interface, falling back to index.html
// for client-side routes.
func (s *Server) webHandler() http.Handler {
	files := http.FileServer(http.Dir(s.cfg.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if _, err := os.Stat(filepath.Join(s.cfg.WebDir, clean)); err != nil || clean == "/" || clean == "/index.html" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			// The SPA shell must never be cached, so a server update loads the new UI.
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

type ctxKey int

const (
	clientIPKey ctxKey = iota
	schemeKey
	hostKey
	userKey
	tokenKey
)

// ClientIP returns the client address, taking trusted reverse proxies into account.
func ClientIP(r *http.Request) string {
	if v, ok := r.Context().Value(clientIPKey).(string); ok {
		return v
	}
	return ""
}

// withClientIP resolves the real client IP, scheme and host. X-Forwarded-*
// headers are only honoured when the direct peer is a trusted proxy.
func (s *Server) withClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := s.withProxyContext(r)
		if scheme, _ := ctx.Value(schemeKey).(string); scheme == "https" && s.hsts.Load() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) withProxyContext(r *http.Request) context.Context {
	trusted := s.trustedProxies()
	ip := resolveClientIP(r, trusted)
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if peerTrusted(r, trusted) {
		if p := strings.ToLower(firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))); p == "https" || p == "http" {
			scheme = p
		}
		if h := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); h != "" {
			host = h
		}
	}
	ctx := context.WithValue(r.Context(), clientIPKey, ip)
	ctx = context.WithValue(ctx, schemeKey, scheme)
	return context.WithValue(ctx, hostKey, host)
}

func firstHeaderValue(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

func peerTrusted(r *http.Request, trusted []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	return err == nil && isTrusted(peer, trusted)
}

func resolveClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(peer, trusted) {
		return host
	}
	// Walk X-Forwarded-For from right to left and return the first untrusted hop.
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			break
		}
		if !isTrusted(addr, trusted) {
			return addr.String()
		}
		host = addr.String()
	}
	return host
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// csrfGuard rejects state-changing API requests without the custom header the
// web interface sends. Browsers cannot add it cross-site without a CORS
// preflight, which the server never allows.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead &&
			r.Header.Get("X-Requested-With") != "fleetwarden" {
			apiError(w, http.StatusForbidden, "csrf")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiErr struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// apiError answers with a stable error code; the web interface translates it.
func apiError(w http.ResponseWriter, code int, errCode string) {
	writeJSON(w, code, apiErr{Error: errCode})
}

func apiErrorDetail(w http.ResponseWriter, code int, errCode, detail string) {
	writeJSON(w, code, apiErr{Error: errCode, Detail: detail})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	apiError(w, http.StatusInternalServerError, "internal")
}

// decode reads a JSON body of at most 1 MiB.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

// baseURL is the public URL of the server: the configured one, or the one the
// browser used (through a trusted proxy).
func (s *Server) baseURL(r *http.Request) string {
	if g, ok := gatewayFrom(r); ok {
		return g.PublicURL // agents that came through a gateway stay on it
	}
	var g generalSettings
	if err := s.st.GetSetting(r.Context(), keyGeneral, &g); err == nil && g.PublicURL != "" {
		return strings.TrimRight(g.PublicURL, "/")
	}
	scheme, _ := r.Context().Value(schemeKey).(string)
	host, _ := r.Context().Value(hostKey).(string)
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host
}

func isHTTPS(r *http.Request) bool {
	scheme, _ := r.Context().Value(schemeKey).(string)
	return scheme == "https"
}

// audit records an action by the signed-in user.
func (s *Server) audit(r *http.Request, action, target string, success bool, details map[string]any) {
	var actor *store.User
	if u, ok := currentUser(r); ok {
		actor = &u
	}
	s.auditAs(r, actor, "", action, target, success, details)
}

// auditAs records an action for an explicit actor (or only a name, for failed
// logins). Failures to write the audit log are logged, not fatal.
func (s *Server) auditAs(r *http.Request, actor *store.User, name, action, target string, success bool, details map[string]any) {
	e := store.AuditEntry{IP: ClientIP(r), Action: action, Target: target, Success: success, Details: details, Actor: name}
	if actor != nil {
		e.ActorID, e.Actor = &actor.ID, actor.Username
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := s.st.Audit(ctx, e); err != nil {
		s.log.Error("audit log write failed", "action", action, "err", err)
	}
}

var errNoSecrets = errors.New("secret key not loaded")
