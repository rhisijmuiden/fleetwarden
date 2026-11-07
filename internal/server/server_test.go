package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestServer(t *testing.T, db Pinger) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	trusted, _ := config.ParsePrefixes("10.0.0.0/8")
	cfg := config.Server{WebDir: dir, TrustedProxies: trusted}
	return New(cfg, Deps{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func TestHealth(t *testing.T) {
	cases := []struct {
		name string
		db   Pinger
		code int
		want string
	}{
		{"no database", nil, 200, "not configured"},
		{"database ok", fakeDB{}, 200, "ok"},
		{"database down", fakeDB{err: errors.New("down")}, 503, "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestServer(t, tc.db).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d", rec.Code, tc.code)
			}
			var h health
			if err := json.NewDecoder(rec.Body).Decode(&h); err != nil {
				t.Fatal(err)
			}
			if h.Database != tc.want {
				t.Errorf("database = %q, want %q", h.Database, tc.want)
			}
		})
	}
}

func TestVersionEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t, nil).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var v map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v["version"] == "" || v["channel"] == "" {
		t.Errorf("missing fields in %v", v)
	}
}

func TestSPAFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t, nil).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/endpoints/123", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>app</html>" {
		t.Errorf("got %d %q, want index.html", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	newTestServer(t, nil).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/unknown", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("unknown API route code = %d, want an error", rec.Code)
	}
}

func TestResolveClientIP(t *testing.T) {
	trusted, _ := config.ParsePrefixes("10.0.0.0/8")
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "198.51.100.1", "203.0.113.5"},         // untrusted peer: header ignored
		{"10.0.0.2:1234", "198.51.100.1", "198.51.100.1"},           // trusted proxy
		{"10.0.0.2:1234", "198.51.100.1, 10.0.0.3", "198.51.100.1"}, // chain of trusted proxies
		{"10.0.0.2:1234", "", "10.0.0.2"},                           // no header
		{"10.0.0.2:1234", "1.2.3.4, 198.51.100.1", "198.51.100.1"},  // spoofed left-most entry ignored
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := resolveClientIP(r, trusted); got != tc.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}
