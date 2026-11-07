package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/debpatch"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func fakeInventory(host string) func(context.Context) (agentproto.Inventory, error) {
	return func(context.Context) (agentproto.Inventory, error) {
		return agentproto.Inventory{
			Hostname: host,
			OS:       agentproto.OSInfo{Name: "Windows 11 Pro", Version: "24H2", Build: "26100.1", Kind: "workstation", Domain: "corp.example", DomainJoined: true},
			Hardware: agentproto.Hardware{Manufacturer: "Dell Inc.", Model: "Latitude 7450", Serial: "ABC123", CPU: "Intel Core Ultra 7", CPUCores: 12, MemoryBytes: 32 << 30,
				Disks: []agentproto.Disk{{Name: "disk0", SizeBytes: 512 << 30, Media: "nvme"}}},
			Software: []agentproto.Software{
				{Name: "Mozilla Firefox", Version: "133.0", Publisher: "Mozilla", Source: "registry", InstalledOn: "2026-10-01"},
				{Name: "7-Zip 24.08", Version: "24.08", Publisher: "Igor Pavlov", Source: "msi"},
			},
		}, nil
	}
}

func newTestAgent(t *testing.T, e *env, token, host string, once bool) *agent.Agent {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := agent.SaveConfig(path, agent.Config{Server: e.ts.URL, Token: token}); err != nil {
		t.Fatal(err)
	}
	return &agent.Agent{ConfigPath: path, Once: once, Collect: fakeInventory(host)}
}

func (e *env) createToken(c *client, groupID int64) (store.EnrollmentToken, installInfo) {
	e.t.Helper()
	var tok store.EnrollmentToken
	if code := c.do("POST", "/api/v1/enrollment-tokens", createTokenReq{GroupID: groupID, Description: "test"}, &tok); code != 201 {
		e.t.Fatalf("create token: %d", code)
	}
	var info installInfo
	if code := c.do("GET", fmt.Sprintf("/api/v1/enrollment-tokens/%d/install", tok.ID), nil, &info); code != 200 || !strings.HasPrefix(info.Token, "fwe_") {
		e.t.Fatalf("install info: %d %+v", code, info)
	}
	return tok, info
}

func TestEnrollmentAndInventory(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	var groups []store.DeviceGroup
	if code := admin.do("GET", "/api/v1/groups", nil, &groups); code != 200 || len(groups) != 1 || groups[0].Name != "Default" {
		t.Fatalf("groups: %d %+v", code, groups)
	}
	var wp store.DeviceGroup
	if code := admin.do("POST", "/api/v1/groups", groupReq{Name: "Werkplekken", UpdateRing: 1}, &wp); code != 201 {
		t.Fatalf("create group: %d", code)
	}
	if code := admin.do("POST", "/api/v1/groups", groupReq{Name: "werkplekken"}, nil); code != 409 {
		t.Errorf("duplicate group name: %d", code)
	}
	tok, info := e.createToken(admin, wp.ID)
	if !strings.Contains(info.WindowsCommand, "/agent/install.ps1?t="+info.Token) || !strings.Contains(info.LinuxCommand, "sudo bash -s -- "+info.Token) {
		t.Errorf("commands: %+v", info)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := newTestAgent(t, e, info.Token, "PC-0001", true)
	if err := a.Run(ctx); err != nil {
		t.Fatalf("agent: %v", err)
	}
	cfg, _ := agent.LoadConfig(a.ConfigPath)
	if !cfg.Enrolled() || cfg.Token != "" {
		t.Fatalf("agent config after enrollment: %+v", cfg)
	}

	var list deviceList
	if code := admin.do("GET", "/api/v1/devices", nil, &list); code != 200 || list.Total != 1 {
		t.Fatalf("devices: %d %+v", code, list)
	}
	d := list.Items[0]
	if d.ID != cfg.DeviceID || d.Hostname != "PC-0001" || d.GroupID != wp.ID || d.OSName != "Windows 11 Pro" || !d.DomainJoined || d.InventoryAt == nil {
		t.Errorf("device: %+v", d)
	}
	var sw []store.SoftwareRow
	if code := admin.do("GET", "/api/v1/devices/"+d.ID+"/software", nil, &sw); code != 200 || len(sw) != 2 || sw[0].Name != "7-Zip 24.08" {
		t.Errorf("software: %d %+v", code, sw)
	}
	if code := admin.do("GET", "/api/v1/devices?software=firefox", nil, &list); code != 200 || list.Total != 1 {
		t.Errorf("software filter: %d %d", code, list.Total)
	}
	var waiting []store.Device
	admin.do("GET", fmt.Sprintf("/api/v1/enrollment-tokens/%d/devices?since=%s", tok.ID, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)), nil, &waiting)
	if len(waiting) != 1 {
		t.Errorf("wizard device list: %+v", waiting)
	}

	// Reinstalling on the same machine keeps the device (same machine id).
	a2 := newTestAgent(t, e, info.Token, "PC-0001", true)
	if err := a2.Run(ctx); err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
	admin.do("GET", "/api/v1/devices", nil, &list)
	if list.Total != 1 {
		t.Errorf("re-enrollment created a second device: %d", list.Total)
	}
	// The first installation's key no longer works.
	if err := a.Run(ctx); err == nil {
		t.Error("old agent key still accepted after re-enrollment")
	}

	// A revoked token cannot enroll.
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/enrollment-tokens/%d", tok.ID), nil, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	a3 := newTestAgent(t, e, info.Token, "PC-0002", true)
	if err := a3.Run(ctx); !errors.Is(err, agent.ErrInvalidToken) {
		t.Errorf("revoked token: %v", err)
	}
}

func TestLiveAgentCommandsAndRemoval(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	inventories := make(chan struct{}, 4)
	a := newTestAgent(t, e, info.Token, "SRV-01", false)
	collect := a.Collect
	a.Collect = func(ctx context.Context) (agentproto.Inventory, error) {
		inventories <- struct{}{}
		return collect(ctx)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	<-inventories // first inventory after enrollment

	var list deviceList
	for i := 0; i < 50; i++ {
		admin.do("GET", "/api/v1/devices?status=online", nil, &list)
		if list.Total == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if list.Total != 1 || !list.Items[0].Online {
		t.Fatalf("online devices: %+v", list)
	}
	id := list.Items[0].ID
	if code := admin.do("POST", "/api/v1/devices/"+id+"/inventory", nil, nil); code != 202 {
		t.Fatalf("request inventory: %d", code)
	}
	select {
	case <-inventories:
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not send the requested inventory")
	}
	var stats deviceStats
	if admin.do("GET", "/api/v1/devices/stats", nil, &stats); stats.Total != 1 || stats.Online != 1 {
		t.Errorf("stats: %+v", stats)
	}
	if code := admin.do("DELETE", "/api/v1/groups/1", nil, nil); code != 409 {
		t.Errorf("delete non-empty group: %d", code)
	}
	if code := admin.do("DELETE", "/api/v1/devices/"+id, nil, nil); code != 204 {
		t.Fatalf("delete device: %d", code)
	}
	select {
	case err := <-done:
		if !errors.Is(err, agent.ErrRemoved) {
			t.Errorf("agent after removal: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not stop after the device was removed")
	}
	cfg, _ := agent.LoadConfig(a.ConfigPath)
	if cfg.Enrolled() {
		t.Error("agent kept its enrollment after removal")
	}
}

func TestDeviceScopes(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	var other store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Servers"}, &other)
	_, info := e.createToken(admin, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := newTestAgent(t, e, info.Token, "PC-1", true).Run(ctx); err != nil {
		t.Fatal(err)
	}

	var created userWithPassword
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: "tech", Role: "technician", Password: "blue-kettle-orbit-91",
		Scoped: true, ScopeGroups: []int64{other.ID}}, &created); code != 201 || !created.User.Scoped {
		t.Fatalf("create scoped user: %d %+v", code, created.User)
	}
	tech := e.client()
	tech.login("tech", "blue-kettle-orbit-91")
	tech.do("POST", "/api/v1/me/password", map[string]string{"current": "blue-kettle-orbit-91", "new": "green-lantern-pivot-37"}, nil)

	var list deviceList
	if code := tech.do("GET", "/api/v1/devices", nil, &list); code != 200 || list.Total != 0 {
		t.Fatalf("scoped device list: %d %+v", code, list)
	}
	admin.do("GET", "/api/v1/devices", nil, &list)
	id := list.Items[0].ID
	if code := tech.do("GET", "/api/v1/devices/"+id, nil, nil); code != 404 {
		t.Errorf("device outside scope: %d", code)
	}
	var groups []store.DeviceGroup
	if tech.do("GET", "/api/v1/groups", nil, &groups); len(groups) != 1 || groups[0].ID != other.ID {
		t.Errorf("scoped groups: %+v", groups)
	}
	if code := tech.do("POST", "/api/v1/enrollment-tokens", createTokenReq{GroupID: 1}, nil); code != 400 {
		t.Errorf("token for a group outside scope: %d", code)
	}
	if code := tech.do("POST", "/api/v1/devices/move", moveReq{IDs: []string{id}, GroupID: other.ID}, nil); code != 200 {
		t.Errorf("move: %d", code)
	}
	if tech.do("GET", "/api/v1/devices", nil, &list); list.Total != 0 {
		t.Error("technician moved a device outside their scope")
	}
	if code := tech.do("POST", "/api/v1/groups", groupReq{Name: "Mine"}, nil); code != 403 {
		t.Errorf("technician created a group: %d", code)
	}
	// Widening the scope makes the device visible.
	if code := admin.do("PATCH", fmt.Sprintf("/api/v1/users/%d", created.User.ID), map[string]any{"scopeGroups": []int64{1, other.ID}}, nil); code != 200 {
		t.Fatalf("update scope: %d", code)
	}
	tech2 := e.client()
	tech2.login("tech", "green-lantern-pivot-37")
	if tech2.do("GET", "/api/v1/devices", nil, &list); list.Total != 1 {
		t.Errorf("after widening the scope: %+v", list)
	}
}

func TestInstallDownloadsAndScripts(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.AgentDir = t.TempDir()
	deb := buildTestDeb(t)
	if err := os.WriteFile(filepath.Join(e.srv.cfg.AgentDir, "fleetwarden-agent_0.3.0_amd64.deb"), deb, 0o644); err != nil {
		t.Fatal(err)
	}
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	if len(info.Downloads) != 1 || info.Downloads[0].Kind != "deb" || info.Downloads[0].Version != "0.3.0" {
		t.Fatalf("downloads: %+v", info.Downloads)
	}
	get := func(path string) (int, []byte) {
		res, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	code, body := get("/agent/download/linux-amd64.deb?t=" + info.Token)
	if code != 200 {
		t.Fatalf("download: %d %s", code, body)
	}
	baked, err := debpatch.ReadFile(body, enrollFile)
	if err != nil || !strings.Contains(string(baked), info.Token) || !strings.Contains(string(baked), e.ts.URL) {
		t.Errorf("baked enroll file: %v %s", err, baked)
	}
	if code, _ := get("/agent/download/linux-amd64.deb?t=fwe_wrong"); code != 401 {
		t.Errorf("download with a wrong token: %d", code)
	}
	if code, _ := get("/agent/download/windows-x64.msi?t=" + info.Token); code != 404 {
		t.Errorf("missing MSI: %d", code)
	}
	code, body = get("/agent/install.ps1?t=" + info.Token)
	if code != 200 || !strings.Contains(string(body), "$token = '"+info.Token+"'") {
		t.Errorf("install.ps1: %d %s", code, body)
	}
	code, body = get("/agent/install.sh")
	if code != 200 || !strings.Contains(string(body), "SERVER='"+e.ts.URL+"'") {
		t.Errorf("install.sh: %d %s", code, body)
	}
	var hello agentproto.Hello
	if code := e.client().do("GET", agentproto.PathHello, nil, &hello); code != 200 || len(hello.ServerPublicKey) != 32 {
		t.Errorf("hello: %d %+v", code, hello)
	}
	res := agent.TestConnection(context.Background(), e.ts.URL)
	if !res.OK || res.TLS {
		t.Errorf("test connection: %+v", res)
	}
}

func buildTestDeb(t *testing.T) []byte {
	t.Helper()
	deb, err := debpatch.BuildMinimal("fleetwarden-agent", "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	return deb
}
