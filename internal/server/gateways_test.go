package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/gateway"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

type testGateway struct {
	cfg    gateway.Config
	gw     *gateway.Gateway
	tunnel *httptest.Server
	agents *httptest.Server
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	cfg := gateway.Config{TLSMode: gateway.TLSProxy, DataDir: t.TempDir(), TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	g, err := gateway.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "9.9.9-gw")
	if err != nil {
		t.Fatal(err)
	}
	tun := httptest.NewUnstartedServer(g.TunnelHandler())
	tun.TLS = &tls.Config{Certificates: []tls.Certificate{g.TunnelCertificate()}}
	tun.StartTLS()
	t.Cleanup(tun.Close)
	ag := httptest.NewServer(g.AgentHandler())
	t.Cleanup(ag.Close)
	return &testGateway{cfg: cfg, gw: g, tunnel: tun, agents: ag}
}

func (tg *testGateway) code(t *testing.T) string {
	t.Helper()
	info, err := gateway.ReadPairing(tg.cfg)
	if err != nil || info.Paired || info.Code == "" {
		t.Fatalf("pairing info: %+v %v", info, err)
	}
	return info.Code
}

func waitUntil(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) gatewayState(c *client, id int64) gatewayView {
	var v gatewaysView
	if code := c.do("GET", "/api/v1/gateways", nil, &v); code != 200 {
		e.t.Fatalf("list gateways: %d", code)
	}
	for _, g := range v.Gateways {
		if g.ID == id {
			return g
		}
	}
	return gatewayView{}
}

func TestSecureGateway(t *testing.T) {
	e := newEnv(t)
	e.srv.reconcileGateways(context.Background())
	t.Cleanup(e.srv.bgCancel)
	admin, _ := e.signedInAdmin()
	tg := newTestGateway(t)
	addr := tg.tunnel.Listener.Addr().String()

	// Not connected yet: the gateway answers agents with 503 and relays nothing else.
	if res, err := http.Get(tg.agents.URL + "/agent/v1/hello"); err != nil || res.StatusCode != 503 {
		t.Fatalf("hello before pairing: %v %v", res, err)
	}

	var apiErr apiErr
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ", Address: addr, PublicURL: tg.agents.URL, PairingCode: "AAAA-BBBB-CCCC-DDDD"}, &apiErr); code != 400 || apiErr.Error != "gateway_bad_pairing_code" {
		t.Fatalf("wrong code: %d %+v", code, apiErr)
	}
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ", Address: "127.0.0.1:1", PublicURL: tg.agents.URL, PairingCode: tg.code(t)}, &apiErr); code != 502 || apiErr.Error != "gateway_unreachable" {
		t.Fatalf("unreachable: %d %+v", code, apiErr)
	}
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ", Address: addr, PublicURL: "ftp://x"}, &apiErr); code != 400 || apiErr.Error != "invalid_url" {
		t.Fatalf("bad url: %d %+v", code, apiErr)
	}
	var created gatewayView
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ", Address: "wss://" + addr, PublicURL: tg.agents.URL + "/", PairingCode: strings.ToLower(tg.code(t))}, &created); code != 201 {
		t.Fatalf("pair: %d", code)
	}
	if created.Address != addr || created.PublicURL != tg.agents.URL || created.Version != "9.9.9-gw" || created.Fingerprint == "" {
		t.Errorf("created: %+v", created)
	}
	if info, _ := gateway.ReadPairing(tg.cfg); !info.Paired {
		t.Error("the gateway did not pin the server")
	}
	// A paired gateway refuses another pairing.
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ 2", Address: addr, PublicURL: tg.agents.URL, PairingCode: "AAAA-BBBB-CCCC-DDDD"}, &apiErr); code != 409 || apiErr.Error != "gateway_already_paired" {
		t.Fatalf("second pairing: %d %+v", code, apiErr)
	}
	waitUntil(t, "tunnel", 10*time.Second, func() bool { return e.gatewayState(admin, created.ID).State == "connected" })
	waitUntil(t, "gateway status", 10*time.Second, func() bool {
		st := e.gatewayState(admin, created.ID).Status
		return st != nil && st.Version == "9.9.9-gw"
	})

	// Only agent paths pass the gateway.
	if res, err := http.Get(tg.agents.URL + "/api/v1/auth/config"); err != nil || res.StatusCode != 404 {
		t.Errorf("API through the gateway: %v %v", res, err)
	}
	if res, err := http.Get(tg.agents.URL + "/healthz"); err != nil || res.StatusCode != 200 {
		t.Errorf("gateway health: %v %v", res, err)
	}

	// The server's external URL comes first in the address list for agents.
	if code := admin.do("PUT", "/api/v1/settings/general", generalSettings{PublicURL: e.ts.URL}, nil); code != 200 {
		t.Fatalf("public url: %d", code)
	}

	var test struct{ Checks []gatewayCheck }
	if code := admin.do("POST", fmt.Sprintf("/api/v1/gateways/%d/test", created.ID), nil, &test); code != 200 || len(test.Checks) != 2 || !test.Checks[0].OK || !test.Checks[1].OK {
		t.Fatalf("test: %d %+v", code, test)
	}

	// Install commands through the gateway, and enrollment through it.
	var groups []store.DeviceGroup
	admin.do("GET", "/api/v1/groups", nil, &groups)
	tok, _ := e.createToken(admin, groups[0].ID)
	var info installInfo
	if code := admin.do("GET", fmt.Sprintf("/api/v1/enrollment-tokens/%d/install?via=%d", tok.ID, created.ID), nil, &info); code != 200 || info.ServerURL != tg.agents.URL ||
		!strings.Contains(info.LinuxCommand, tg.agents.URL+"/agent/install.sh") {
		t.Fatalf("install via gateway: %d %+v", code, info)
	}
	res, err := http.Get(tg.agents.URL + "/agent/install.sh")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("install script through the gateway: %v %v", res, err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), tg.agents.URL) {
		t.Error("the install script does not point at the gateway")
	}

	path := filepath.Join(t.TempDir(), "agent.json")
	if err := agent.SaveConfig(path, agent.Config{Server: tg.agents.URL, Token: info.Token}); err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{ConfigPath: path, StateDir: t.TempDir(), Collect: fakeInventory("LAPTOP-THUIS")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	var dev store.Device
	waitUntil(t, "device via gateway", 15*time.Second, func() bool {
		var list deviceList
		admin.do("GET", "/api/v1/devices", nil, &list)
		if len(list.Items) == 1 && list.Items[0].Online {
			dev = list.Items[0]
			return true
		}
		return false
	})
	if dev.ConnectedVia != "DMZ" || dev.RemoteIP != "127.0.0.1" {
		t.Errorf("device: via %q ip %q", dev.ConnectedVia, dev.RemoteIP)
	}
	waitUntil(t, "endpoints", 10*time.Second, func() bool {
		c, _ := agent.LoadConfig(path)
		return slices.Equal(c.Endpoints, []string{e.ts.URL, tg.agents.URL})
	})
	if v := e.gatewayState(admin, created.ID); v.Agents != 1 {
		t.Errorf("agents via gateway: %d", v.Agents)
	}
	cancel()
	<-done
	waitUntil(t, "offline", 10*time.Second, func() bool { on, _ := e.srv.hub.via(dev.ID); return !on })

	// Failover: the configured address and the first endpoint are dead; the
	// agent reaches the server through the gateway.
	c, _ := agent.LoadConfig(path)
	c.Server, c.Endpoints = "http://127.0.0.1:1", []string{"http://127.0.0.1:2", tg.agents.URL}
	if err := agent.SaveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- a.Run(ctx) }()
	waitUntil(t, "failover", 20*time.Second, func() bool { on, gw := e.srv.hub.via(dev.ID); return on && gw == created.ID })

	// Disabling the gateway closes the tunnel; the agent comes back on the
	// server's own address, which the server sent it.
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/gateways/%d", created.ID), map[string]any{"name": "DMZ", "address": addr, "publicUrl": tg.agents.URL, "enabled": false}, nil); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	waitUntil(t, "tunnel closed", 10*time.Second, func() bool { return !tg.gw.Connected() })
	waitUntil(t, "direct connection", 30*time.Second, func() bool { on, gw := e.srv.hub.via(dev.ID); return on && gw == 0 })
	if v := e.gatewayState(admin, created.ID); v.State != "disabled" {
		t.Errorf("state after disable: %q", v.State)
	}
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/gateways/%d", created.ID), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var list gatewaysView
	admin.do("GET", "/api/v1/gateways", nil, &list)
	if len(list.Gateways) != 0 || len(list.Endpoints) != 0 {
		t.Errorf("after delete: %+v", list)
	}
	cancel()
	<-done
}

func TestGatewayRepair(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.srv.bgCancel)
	admin, _ := e.signedInAdmin()
	tg := newTestGateway(t)
	addr := tg.tunnel.Listener.Addr().String()
	var created gatewayView
	if code := admin.do("POST", "/api/v1/gateways", gatewayReq{Name: "DMZ", Address: addr, PublicURL: tg.agents.URL, PairingCode: tg.code(t)}, &created); code != 201 {
		t.Fatalf("pair: %d", code)
	}
	waitUntil(t, "tunnel", 10*time.Second, func() bool { return e.gatewayState(admin, created.ID).State == "connected" })

	// The gateway is reset: the old pin is gone and it waits for a new code.
	info, err := gateway.ResetPairing(tg.cfg)
	if err != nil || info.Paired {
		t.Fatalf("reset: %+v %v", info, err)
	}
	var apiErr apiErr
	if code := admin.do("POST", fmt.Sprintf("/api/v1/gateways/%d/pair", created.ID), gatewayReq{PairingCode: "wrong"}, &apiErr); code != 400 {
		t.Fatalf("repair with a wrong code: %d %+v", code, apiErr)
	}
	if code := admin.do("POST", fmt.Sprintf("/api/v1/gateways/%d/pair", created.ID), gatewayReq{PairingCode: info.Code}, nil); code != 200 {
		t.Fatalf("repair: %d", code)
	}
	waitUntil(t, "tunnel after repair", 15*time.Second, func() bool { return e.gatewayState(admin, created.ID).State == "connected" })
}

func TestGatewayAddressParsing(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.5":              "10.0.0.5:8444",
		"gw.example.com:9000":   "gw.example.com:9000",
		"wss://gw.example.com/": "gw.example.com:8444",
		"[2001:db8::1]:8444":    "[2001:db8::1]:8444",
		"2001:db8::1":           "[2001:db8::1]:8444",
		"":                      "",
		"user@host":             "",
		"host:99999":            "",
	} {
		got, ok := normalizeGatewayAddress(in)
		if (want == "") == ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
}
