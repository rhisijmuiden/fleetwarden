package agent

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
)

func TestDownloadPrefersDistributionServer(t *testing.T) {
	body := []byte("the installer bytes")
	h := sha256.Sum256(body)
	sum := hex.EncodeToString(h[:])
	path := agentproto.PathSoftware + sum

	var direct, nearby atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.Write(body)
	}))
	defer server.Close()
	mode := "ok"
	dist := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nearby.Add(1)
		if r.Header.Get(agentproto.HeaderAuth) == "" || r.URL.Path != path {
			http.Error(w, "bad", 400)
			return
		}
		switch mode {
		case "miss":
			http.Error(w, "not stored here yet", http.StatusServiceUnavailable)
		case "corrupt":
			w.Write([]byte("something else entirely"))
		default:
			w.Write(body)
		}
	}))
	defer dist.Close()
	pin := distproto.CertSHA256(dist.Certificate().Raw)

	_, key, _ := ed25519.GenerateKey(nil)
	a := &Agent{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: &http.Client{}}
	a.cfg = Config{Server: server.URL, DeviceID: "dev-1", PrivateKey: key.Seed()}
	dst := filepath.Join(t.TempDir(), "file")
	get := func() {
		t.Helper()
		if err := a.downloadSigned(t.Context(), path, sum, int64(len(body)), dst); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dst); string(got) != string(body) {
			t.Fatalf("got %q", got)
		}
	}

	// No distribution server: straight from the server.
	get()
	if direct.Load() != 1 || nearby.Load() != 0 {
		t.Fatalf("direct %d nearby %d", direct.Load(), nearby.Load())
	}
	// A wrong pin and a bad address are skipped.
	a.setDistribution([]agentproto.DistributionServer{
		{ID: 1, Name: "Bad pin", URL: dist.URL, CertSHA256: strings.Repeat("0", 64)},
		{ID: 2, Name: "Plain", URL: "http://10.0.0.1", CertSHA256: pin},
	})
	if n := len(a.distServers()); n != 1 {
		t.Fatalf("kept %d servers", n)
	}
	get()
	if direct.Load() != 2 || nearby.Load() != 0 {
		t.Fatalf("wrong pin: direct %d nearby %d", direct.Load(), nearby.Load())
	}
	// The right pin: from the distribution server only.
	a.setDistribution([]agentproto.DistributionServer{{ID: 3, Name: "Utrecht", URL: dist.URL, CertSHA256: strings.ToUpper(pin)}})
	get()
	if direct.Load() != 2 || nearby.Load() != 1 {
		t.Fatalf("nearby: direct %d nearby %d", direct.Load(), nearby.Load())
	}
	// Not there yet, or wrong bytes: the agent falls back to the server.
	for _, m := range []string{"miss", "corrupt"} {
		mode = m
		get()
	}
	if direct.Load() != 4 || nearby.Load() != 3 {
		t.Fatalf("fallback: direct %d nearby %d", direct.Load(), nearby.Load())
	}
	// Paths that are not files are never sent to it.
	if distproto.Served(agentproto.PathWS) || distproto.Served(agentproto.PathSoftware+"a/b") || !distproto.Served(agentproto.PathDownload+"fleetwarden-agent_1.0.0_amd64.deb") {
		t.Error("served paths")
	}
}
