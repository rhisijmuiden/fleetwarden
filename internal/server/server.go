// Package server implements the FleetWarden management server HTTP API.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// Pinger checks that a dependency such as the database is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server is the HTTP server.
type Server struct {
	cfg config.Server
	db  Pinger
	log *slog.Logger
}

// New creates a server. db may be nil when no database is configured.
func New(cfg config.Server, db Pinger, log *slog.Logger) *Server {
	return &Server{cfg: cfg, db: db, log: log}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.Handle("/", s.webHandler())
	return s.withClientIP(securityHeaders(mux))
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
		if _, err := os.Stat(filepath.Join(s.cfg.WebDir, clean)); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

type ctxKey int

const clientIPKey ctxKey = 0

// ClientIP returns the client address, taking trusted reverse proxies into account.
func ClientIP(r *http.Request) string {
	if v, ok := r.Context().Value(clientIPKey).(string); ok {
		return v
	}
	return ""
}

// withClientIP resolves the real client IP. X-Forwarded-For is only honoured
// when the direct peer is a configured trusted proxy.
func (s *Server) withClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := resolveClientIP(r, s.cfg.TrustedProxies)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, ip)))
	})
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
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
