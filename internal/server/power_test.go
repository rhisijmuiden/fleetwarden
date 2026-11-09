package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestPowerSlots(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, ams)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	weekdays := 0b0111110 // Monday - Friday
	// 2026-11-13 is a Friday.
	if s, ok := lastSlot(weekdays, 19*60, "Europe/Amsterdam", at("2026-11-13 19:00")); !ok || !s.Equal(at("2026-11-13 19:00")) {
		t.Errorf("slot at the minute: %v", s)
	}
	if s, ok := lastSlot(weekdays, 19*60, "Europe/Amsterdam", at("2026-11-15 10:00")); !ok || !s.Equal(at("2026-11-13 19:00")) {
		t.Errorf("slot on Sunday: %v", s)
	}
	if n := nextPowerSlot(weekdays, 19*60, "Europe/Amsterdam", at("2026-11-13 19:00")); n == nil || !n.Equal(at("2026-11-16 19:00")) {
		t.Errorf("next slot: %v", n)
	}
	if _, ok := lastSlot(0, 0, "", time.Now()); ok {
		t.Error("slot without days")
	}
	// A window of Tuesday 22:00 - 02:00 is current until the grace ends.
	p := store.PatchPolicy{WindowDays: 1 << 2, WindowStart: 22 * 60, WindowMinutes: 240, TimeZone: "Europe/Amsterdam"}
	if s, e, ok := currentWindow(p, at("2026-11-11 01:00"), time.Hour); !ok || !s.Equal(at("2026-11-10 22:00")) || !e.Equal(at("2026-11-11 02:00")) {
		t.Errorf("open window: %v %v %v", s, e, ok)
	}
	if _, _, ok := currentWindow(p, at("2026-11-11 02:30"), time.Hour); !ok {
		t.Error("window in its grace time")
	}
	if _, _, ok := currentWindow(p, at("2026-11-11 03:30"), time.Hour); ok {
		t.Error("window after its grace time")
	}
}

func netDev(id, host, remote string, group int64, kind string, ifs ...agentproto.NetIface) store.PowerDevice {
	raw, _ := json.Marshal(ifs)
	return store.PowerDevice{ID: id, Hostname: host, RemoteIP: remote, GroupID: group, Kind: kind, Network: raw}
}

func TestWakeRelays(t *testing.T) {
	target := newPowerNet(netDev("t", "PC-OFF", "203.0.113.1", 1, "workstation",
		agentproto.NetIface{Name: "Ethernet", MAC: "00:11:22:33:44:55", IPs: []string{"10.1.0.20"}, CIDRs: []string{"10.1.0.20/22"}},
		agentproto.NetIface{Name: "vEthernet", MAC: "00:00:00:00:00:00"},
		agentproto.NetIface{Name: "Wi-Fi", MAC: "00:11:22:33:44:66", IPs: []string{"192.168.8.4"}}, // old agent: /24
	))
	if !slices.Equal(target.macs, []string{"00:11:22:33:44:55", "00:11:22:33:44:66"}) {
		t.Errorf("macs: %v", target.macs)
	}
	if bc := target.broadcasts(); !slices.Equal(bc, []string{"10.1.3.255", "192.168.8.255", "255.255.255.255"}) {
		t.Errorf("broadcasts: %v", bc)
	}
	peers := []powerNet{
		newPowerNet(netDev("a", "PC-OTHER-SITE", "198.51.100.7", 1, "workstation", agentproto.NetIface{MAC: "00:aa:00:00:00:01", CIDRs: []string{"10.1.2.9/22"}})),
		newPowerNet(netDev("b", "PC-COLLEAGUE", "203.0.113.1", 2, "workstation", agentproto.NetIface{MAC: "00:aa:00:00:00:02", CIDRs: []string{"10.1.1.30/22"}})),
		newPowerNet(netDev("c", "SRV-FILE", "203.0.113.1", 2, "server", agentproto.NetIface{MAC: "00:aa:00:00:00:03", CIDRs: []string{"10.1.0.2/22"}})),
		newPowerNet(netDev("d", "PC-ELSEWHERE", "203.0.113.1", 1, "workstation", agentproto.NetIface{MAC: "00:aa:00:00:00:04", CIDRs: []string{"10.9.0.2/24"}})),
		newPowerNet(netDev("e", "PC-WIFI", "203.0.113.1", 1, "workstation", agentproto.NetIface{MAC: "00:aa:00:00:00:05", IPs: []string{"192.168.8.77"}})),
		newPowerNet(netDev("t", "PC-OFF", "203.0.113.1", 1, "workstation", agentproto.NetIface{MAC: "00:11:22:33:44:55", CIDRs: []string{"10.1.0.20/22"}})),
	}
	var got []string
	for _, r := range relaysFor(target, peers) {
		got = append(got, r.dev.Hostname)
	}
	// Same public address first, servers before workstations, the other
	// site last; at most three.
	if !slices.Equal(got, []string{"SRV-FILE", "PC-WIFI", "PC-COLLEAGUE"}) {
		t.Errorf("relays: %v", got)
	}
	// Devices that only report their addresses (no inventory yet).
	old := newPowerNet(store.PowerDevice{ID: "x", LocalIPs: []string{"172.16.5.5", "fe80::1"}, Network: json.RawMessage("null")})
	if len(old.nets) != 1 || old.nets[0].String() != "172.16.5.0/24" || len(old.macs) != 0 {
		t.Errorf("old device: %+v", old)
	}
	if !old.sharesNetwork(powerNet{ips: []net.IP{net.ParseIP("172.16.5.200").To4()}}) {
		t.Error("same /24")
	}
}

func TestValidPowerSchedule(t *testing.T) {
	p := defaultPowerSchedule()
	p.Name, p.GroupIDs = " Evening ", []int64{3, 3, 0, 4}
	if v := validPowerSchedule(&p); v != "" || p.Name != "Evening" || !slices.Equal(p.GroupIDs, []int64{3, 4}) {
		t.Errorf("valid: %q %+v", v, p)
	}
	p.Action = "hibernate"
	if v := validPowerSchedule(&p); v != "invalid_power_action" {
		t.Errorf("action: %q", v)
	}
	p.Action, p.AtMinute = agentproto.PowerWake, 1440
	if v := validPowerSchedule(&p); v != "invalid_schedule_time" {
		t.Errorf("time: %q", v)
	}
	p.AtMinute, p.OnlyIfRebootPending, p.SkipIfUserLoggedOn = 360, true, true
	if v := validPowerSchedule(&p); v != "" || p.OnlyIfRebootPending || p.SkipIfUserLoggedOn || p.NotifyUsers {
		t.Errorf("wake options: %q %+v", v, p)
	}
}

// fakePower is a PowerBackend for tests.
type fakePower struct {
	mu        sync.Mutex
	restarts  int
	shutdowns int
	prepare   []bool
}

func (f *fakePower) Restart(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return nil
}

func (f *fakePower) Shutdown(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
	return nil
}

func (f *fakePower) PrepareWake(on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepare = append(f.prepare, on)
	return nil
}

func (f *fakePower) counts() (int, int, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarts, f.shutdowns, slices.Clone(f.prepare)
}

func netInventory(host, mac, cidr string) func(context.Context) (agentproto.Inventory, error) {
	base := fakeInventory(host)
	return func(ctx context.Context) (agentproto.Inventory, error) {
		inv, err := base(ctx)
		ip, _, _ := net.ParseCIDR(cidr)
		inv.Hardware.Network = []agentproto.NetIface{{Name: "Ethernet", MAC: mac, IPs: []string{ip.String()}, CIDRs: []string{cidr}}}
		inv.LocalIPs = []string{ip.String()}
		return inv, err
	}
}

func (e *env) powerActions(c *client, device string) []store.PowerAction {
	var list []store.PowerAction
	if code := c.do("GET", "/api/v1/power/actions?device="+device, nil, &list); code != 200 {
		e.t.Fatalf("power actions: %d", code)
	}
	return list
}

func TestPowerManagement(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	// The server does not send packets itself here: only the relay does.
	var ps powerSettings
	if code := admin.do("PUT", "/api/v1/power/settings", map[string]any{"serverWake": false, "port": 80}, nil); code != 400 {
		t.Errorf("port 80: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/power/settings", map[string]any{"serverWake": false, "port": 9, "prepareWake": true}, &ps); code != 200 || ps.ServerWake {
		t.Fatalf("settings: %d %+v", code, ps)
	}

	_, info := e.createToken(admin, 1)
	relay := newTestAgent(t, e, info.Token, "PC-RELAY", false)
	relay.Collect = netInventory("PC-RELAY", "00:aa:bb:00:00:01", "192.0.2.10/24")
	relayPower := &fakePower{}
	relay.Power, relay.MachineID = relayPower, "machine-relay"
	go relay.Run(ctx) //nolint:errcheck
	e.onlineDevice(admin, "PC-RELAY")
	waitFor(t, "wake-on-LAN settings on the agent", func() bool {
		_, _, prep := relayPower.counts()
		return slices.Equal(prep, []bool{true})
	})

	// The sleeper enrolls, sends its inventory and goes away.
	sleeper := newTestAgent(t, e, info.Token, "PC-SLEEPER", false)
	sleeper.Collect = netInventory("PC-SLEEPER", "00:11:22:33:44:55", "192.0.2.20/24")
	sleeperPower := &fakePower{}
	sleeper.Power, sleeper.MachineID = sleeperPower, "machine-sleeper"
	sctx, scancel := context.WithCancel(ctx)
	sdone := make(chan struct{})
	go func() { _ = sleeper.Run(sctx); close(sdone) }()
	sd := e.onlineDevice(admin, "PC-SLEEPER")
	waitFor(t, "sleeper inventory", func() bool {
		var list []powerDeviceView
		admin.do("GET", "/api/v1/power/devices", nil, &list)
		for _, d := range list {
			if d.Hostname == "PC-SLEEPER" && len(d.MACs) == 1 {
				return true
			}
		}
		return false
	})
	if code := admin.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "wake"}, nil); code != 409 {
		t.Errorf("wake an online device: %d", code)
	}
	scancel()
	<-sdone
	waitFor(t, "sleeper offline", func() bool { return !e.srv.hub.online(sd.ID) })

	var list []powerDeviceView
	admin.do("GET", "/api/v1/power/devices", nil, &list)
	for _, d := range list {
		if d.Hostname == "PC-SLEEPER" && (d.Online || !slices.Equal(d.Relays, []string{"PC-RELAY"}) || !slices.Equal(d.Networks, []string{"192.0.2.0/24"})) {
			t.Errorf("sleeper: %+v", d)
		}
	}

	// Wake by hand: the relay sends the packet, the device comes online.
	ro := e.newUserClient(admin, "viewer", "readonly")
	if code := ro.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "wake"}, nil); code != 403 {
		t.Errorf("read-only wake: %d", code)
	}
	var act store.PowerAction
	if code := admin.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "wake"}, &act); code != 202 ||
		act.State != "sent" || !slices.Equal(act.Relays, []string{"PC-RELAY"}) {
		t.Fatalf("wake: %d %+v", code, act)
	}
	if code := admin.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "wake"}, nil); code != 409 {
		t.Errorf("second wake while one is open: %d", code)
	}
	sctx, scancel = context.WithCancel(ctx)
	sdone = make(chan struct{})
	sleeper.Notify = &fakeNotifier{postpone: 1}
	sleeper.Minute = 10 * time.Millisecond
	go func() { _ = sleeper.Run(sctx); close(sdone) }()
	waitFor(t, "wake completed", func() bool {
		a := e.powerActions(admin, sd.ID)
		return len(a) == 1 && a[0].State == "completed"
	})

	// Restart with a pop-up: the user postpones once.
	if code := admin.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "restart", "notify": true, "maxPostpones": 1,
		"postponeMinutes": 5, "countdownSeconds": 60}, &act); code != 202 {
		t.Fatalf("restart: %d", code)
	}
	waitFor(t, "restart", func() bool {
		r, _, _ := sleeperPower.counts()
		return r == 1
	})
	waitFor(t, "restart reported", func() bool {
		a := e.powerActions(admin, sd.ID)
		return a[0].State == "completed" && a[0].Postpones == 1 && a[0].Action == "restart"
	})
	fn := sleeper.Notify.(*fakeNotifier)
	fn.mu.Lock()
	if len(fn.prompts) != 2 || fn.prompts[0].Kind != "restart" || !fn.prompts[0].CanPostpone || fn.prompts[1].CanPostpone {
		t.Errorf("prompts: %+v", fn.prompts)
	}
	fn.mu.Unlock()

	// Shut down only when nobody works on it.
	sleeper.LoggedOnUser = func() string { return `CORP\alice` }
	if code := admin.do("POST", "/api/v1/devices/"+sd.ID+"/power", map[string]any{"action": "shutdown", "skipIfUserLoggedOn": true}, nil); code != 202 {
		t.Fatalf("shutdown: %d", code)
	}
	waitFor(t, "shutdown skipped", func() bool {
		a := e.powerActions(admin, sd.ID)
		return a[0].State == "skipped"
	})

	// A schedule shuts down the online devices of its groups.
	var sched powerScheduleView
	body := map[string]any{"name": "Evening", "enabled": true, "action": "shutdown", "days": 127, "atMinute": 0, "notifyUsers": false,
		"maxPostpones": 0, "postponeMinutes": 30, "countdownSeconds": 60, "groupIds": []int64{1}}
	if code := admin.do("POST", "/api/v1/power/schedules", body, &sched); code != 201 || sched.NextRun == nil || len(sched.Groups) != 1 {
		t.Fatalf("schedule: %d %+v", code, sched)
	}
	if code := admin.do("POST", "/api/v1/power/schedules", body, nil); code != 409 {
		t.Errorf("duplicate schedule: %d", code)
	}
	sleeper.LoggedOnUser = func() string { return "" }
	e.srv.powerTick(ctx)
	if _, s, _ := sleeperPower.counts(); s != 0 {
		t.Errorf("a new schedule ran for a slot before it existed")
	}
	if _, err := conn.Exec(ctx, "UPDATE power_schedules SET last_run_at = now() - interval '1 day', at_minute = $1",
		int16(time.Now().In(time.Local).Hour()*60+time.Now().In(time.Local).Minute())); err != nil {
		t.Fatal(err)
	}
	e.srv.powerTick(ctx)
	waitFor(t, "scheduled shutdowns", func() bool {
		_, a, _ := sleeperPower.counts()
		_, b, _ := relayPower.counts()
		return a == 1 && b == 1
	})
	e.srv.powerTick(ctx) // the slot is done
	time.Sleep(200 * time.Millisecond)
	if _, a, _ := sleeperPower.counts(); a != 1 {
		t.Errorf("schedule ran twice: %d", a)
	}

	// Patch window: the sleeper is off when the window opens, is woken, and
	// is shut down after its window job finished.
	pol := allDayPolicy("Nightly")
	pol["wakeForWindow"], pol["shutdownAfter"] = true, true
	var pv policyView
	if code := admin.do("POST", "/api/v1/patches/policies", pol, &pv); code != 201 || !pv.WakeForWindow || !pv.ShutdownAfter {
		t.Fatalf("policy: %d %+v", code, pv)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": pv.ID}, nil); code != 200 {
		t.Fatalf("assign policy: %d", code)
	}
	scancel()
	<-sdone
	waitFor(t, "sleeper offline", func() bool { return !e.srv.hub.online(sd.ID) })
	e.srv.powerTick(ctx)
	acts := e.powerActions(admin, sd.ID)
	if acts[0].Action != "wake" || acts[0].Trigger != "patch-window" || acts[0].State != "sent" {
		t.Fatalf("wake for the window: %+v", acts[0])
	}
	sctx, scancel = context.WithCancel(ctx)
	defer scancel()
	go func() { _ = sleeper.Run(sctx) }()
	waitFor(t, "woken for the window", func() bool { return e.powerActions(admin, sd.ID)[0].State == "completed" })
	e.srv.powerTick(ctx)
	if _, a, _ := sleeperPower.counts(); a != 1 {
		t.Errorf("shut down before patching")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO patch_runs (device_id, trigger, state, finished_at) VALUES ($1::uuid, 'window', 'completed', now())`, sd.ID); err != nil {
		t.Fatal(err)
	}
	e.srv.powerTick(ctx)
	waitFor(t, "shutdown after patching", func() bool {
		_, a, _ := sleeperPower.counts()
		return a == 2
	})
	acts = e.powerActions(admin, sd.ID)
	if acts[0].Trigger != "after-patch" || acts[0].Action != "shutdown" {
		t.Errorf("after patch: %+v", acts[0])
	}
	e.srv.powerTick(ctx)
	time.Sleep(200 * time.Millisecond)
	if _, a, _ := sleeperPower.counts(); a != 2 {
		t.Errorf("shut down twice after patching")
	}
	// The relay was not woken for the window (it was on), so it stays on.
	if _, b, _ := relayPower.counts(); b != 1 {
		t.Errorf("relay shut down after patching")
	}
}
