package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Behind a reverse proxy that rewrites Host to the upstream address (Zoraxy
// does), the browser's Origin names the public host. The viewer socket must
// still open, and a foreign origin must still be refused.
func TestBrowserWebSocketOriginBehindProxy(t *testing.T) {
	e := newEnv(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "10.0.0.5:8080" // what the proxy sent upstream
		r = r.WithContext(e.srv.withProxyContext(r))
		ws, err := e.srv.acceptBrowserWS(w, r)
		if err != nil {
			return
		}
		ws.Close(websocket.StatusNormalClosure, "")
	}))
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	dial := func(origin, fwdHost string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := http.Header{"Origin": {origin}}
		if fwdHost != "" {
			h.Set("X-Forwarded-Host", fwdHost)
		}
		ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			ws.CloseNow()
		}
		return err
	}
	pub := "https://fleet.example.com"
	if dial(pub, "") == nil {
		t.Fatal("unknown origin accepted without a forwarded host or public URL")
	}
	if err := dial(pub, "fleet.example.com"); err != nil {
		t.Fatalf("forwarded host: %v", err)
	}
	if dial("https://evil.example", "fleet.example.com") == nil {
		t.Fatal("foreign origin accepted")
	}
	if err := e.st.PutSetting(context.Background(), keyGeneral, generalSettings{PublicURL: pub}); err != nil {
		t.Fatal(err)
	}
	if err := dial(pub, ""); err != nil {
		t.Fatalf("public URL: %v", err)
	}
	if err := dial("https://FLEET.example.com", ""); err != nil {
		t.Fatalf("public URL, other case: %v", err)
	}
	if dial("https://evil.example", "") == nil {
		t.Fatal("foreign origin accepted with a public URL")
	}
}
