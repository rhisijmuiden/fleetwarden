package server

// Power management (phase 14).
//
// Wake-on-LAN: a magic packet is a broadcast that stays inside its network,
// so the server asks up to three online agents in the network of a
// switched-off device to send it (and sends it itself too, which helps when
// the server is in that network). The wake has worked when the device comes
// online within wakeTimeout.
//
// Restart and shut down: by hand, on a weekly schedule per group, or after
// the patch window for devices that were woken for it. The agent asks the
// logged-on user first with a countdown and postpones (the tray pop-up).

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/wol"
)

const (
	keyPower = "power"
	// A wake failed when the device is not online after this time.
	wakeTimeout = 15 * time.Minute
	// A restart or shutdown without news for this long failed (postpones
	// can add up to a day).
	powerActionStale = 26 * time.Hour
	// A schedule slot that was missed by more than this (server down) is
	// skipped.
	powerScheduleCatchUp = 30 * time.Minute
	// Devices woken for a patch window are shut down when their patch job
	// is done, or after this time online when no job started.
	afterPatchIdle = 60 * time.Minute
	// How long after the end of a patch window woken devices may still be
	// shut down.
	afterPatchGrace = 6 * time.Hour
	maxWakeRelays   = 3
	maxWakeMACs     = 8
	maxPowerBulk    = 500
	powerHistoryDay = 400
)

// powerSettings are the server-wide power settings.
type powerSettings struct {
	// ServerWake sends the magic packets from the server as well.
	ServerWake bool `json:"serverWake"`
	// Port is the UDP port of the magic packets (9 or 7).
	Port int `json:"port"`
	// PrepareWake has the agents turn on wake on magic packet (and turn off
	// Fast Startup on Windows).
	PrepareWake bool `json:"prepareWake"`
}

func (s *Server) loadPowerSettings(ctx context.Context) powerSettings {
	c := powerSettings{ServerWake: true, Port: agentproto.WakePort}
	_ = s.st.GetSetting(ctx, keyPower, &c)
	if !wol.ValidPort(c.Port) || c.Port == 0 {
		c.Port = agentproto.WakePort
	}
	return c
}

// ---- schedules (pure functions) ----

// lastSlot returns the most recent time at or before now on which a weekly
// schedule (days bit 0 = Sunday, minute of the day) is due.
func lastSlot(days, at int, tz string, now time.Time) (time.Time, bool) {
	loc := scheduleLocation(tz)
	t := now.In(loc)
	for back := 0; back <= 7; back++ {
		slot := time.Date(t.Year(), t.Month(), t.Day()-back, at/60, at%60, 0, 0, loc)
		if days&(1<<int(slot.Weekday())) != 0 && !slot.After(now) {
			return slot, true
		}
	}
	return time.Time{}, false
}

// nextPowerSlot returns the first time after now on which a schedule is due.
func nextPowerSlot(days, at int, tz string, now time.Time) *time.Time {
	loc := scheduleLocation(tz)
	t := now.In(loc)
	for d := 0; d <= 7; d++ {
		slot := time.Date(t.Year(), t.Month(), t.Day()+d, at/60, at%60, 0, 0, loc)
		if days&(1<<int(slot.Weekday())) != 0 && slot.After(now) {
			return &slot
		}
	}
	return nil
}

// currentWindow returns the start and end of the patch window that is open,
// or that ended at most grace ago.
func currentWindow(p store.PatchPolicy, now time.Time, grace time.Duration) (time.Time, time.Time, bool) {
	length := time.Duration(p.WindowMinutes) * time.Minute
	start, ok := lastSlot(p.WindowDays, p.WindowStart, p.TimeZone, now)
	if !ok || now.After(start.Add(length+grace)) {
		return time.Time{}, time.Time{}, false
	}
	return start, start.Add(length), true
}

func defaultPowerSchedule() store.PowerSchedule {
	return store.PowerSchedule{Enabled: true, Action: agentproto.PowerShutdown, Days: 0b0111110, AtMinute: 19 * 60,
		NotifyUsers: true, MaxPostpones: 2, PostponeMinutes: 30, CountdownSeconds: 300, SkipIfUserLoggedOn: true}
}

func validPowerSchedule(p *store.PowerSchedule) string {
	p.Name = cleanText(p.Name, 100)
	p.Description = cleanText(p.Description, 500)
	p.TimeZone = cleanText(p.TimeZone, 64)
	switch {
	case p.Name == "":
		return "invalid_name"
	case !agentproto.ValidPowerAction(p.Action):
		return "invalid_power_action"
	case p.Days < 0 || p.Days > 127 || p.AtMinute < 0 || p.AtMinute > 1439:
		return "invalid_schedule_time"
	case p.MaxPostpones < 0 || p.MaxPostpones > 20 || p.PostponeMinutes < 5 || p.PostponeMinutes > 1440 ||
		p.CountdownSeconds < 30 || p.CountdownSeconds > 3600:
		return "invalid_notification"
	}
	if p.TimeZone != "" {
		if _, err := time.LoadLocation(p.TimeZone); err != nil {
			return "invalid_time_zone"
		}
	}
	if p.Action != agentproto.PowerRestart {
		p.OnlyIfRebootPending = false
	}
	if p.Action == agentproto.PowerWake {
		p.SkipIfUserLoggedOn, p.NotifyUsers = false, false
	}
	seen := map[int64]bool{}
	groups := []int64{}
	for _, g := range p.GroupIDs {
		if g > 0 && !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	p.GroupIDs = groups
	return ""
}

// ---- networks ----

// powerNet is what Wake-on-LAN needs to know about a device.
type powerNet struct {
	dev  store.PowerDevice
	macs []string
	nets []*net.IPNet // its IPv4 networks
	ips  []net.IP     // its IPv4 addresses
}

func newPowerNet(d store.PowerDevice) powerNet {
	pn := powerNet{dev: d}
	var ifs []agentproto.NetIface
	_ = json.Unmarshal(d.Network, &ifs)
	addIP := func(ip net.IP, n *net.IPNet) {
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return
		}
		for _, have := range pn.ips {
			if have.Equal(ip) {
				return
			}
		}
		pn.ips = append(pn.ips, ip.To4())
		pn.nets = append(pn.nets, n)
	}
	for _, i := range ifs {
		if agentproto.WakeableMAC(i.MAC) && len(pn.macs) < maxWakeMACs {
			mac := strings.ToLower(i.MAC)
			if !slices.Contains(pn.macs, mac) {
				pn.macs = append(pn.macs, mac)
			}
		}
		for _, c := range i.CIDRs {
			if ip, n, err := net.ParseCIDR(c); err == nil {
				if ones, _ := n.Mask.Size(); ones >= 8 && ones <= 30 {
					addIP(ip, n)
				}
			}
		}
		if len(i.CIDRs) == 0 { // older agents: assume a /24
			for _, a := range i.IPs {
				addIP(net.ParseIP(a), nil)
			}
		}
	}
	if len(ifs) == 0 {
		for _, a := range d.LocalIPs {
			addIP(net.ParseIP(a), nil)
		}
	}
	for i, ip := range pn.ips {
		if pn.nets[i] == nil {
			pn.nets[i] = &net.IPNet{IP: ip.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}
		}
	}
	return pn
}

// broadcasts are the directed broadcast addresses of the device's networks
// and the limited broadcast.
func (pn powerNet) broadcasts() []string {
	var out []string
	for _, n := range pn.nets {
		ones, _ := n.Mask.Size()
		if bc, ok := wol.Broadcast(n.IP.String() + "/" + strconv.Itoa(ones)); ok && !slices.Contains(out, bc) && len(out) < wol.MaxBroadcasts-1 {
			out = append(out, bc)
		}
	}
	return append(out, "255.255.255.255")
}

// sharesNetwork reports whether peer has an address in one of the
// device's networks.
func (pn powerNet) sharesNetwork(peer powerNet) bool {
	for _, n := range pn.nets {
		for _, ip := range peer.ips {
			if n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (pn powerNet) targets() []agentproto.WakeTarget {
	bcs := pn.broadcasts()
	out := make([]agentproto.WakeTarget, 0, len(pn.macs))
	for _, m := range pn.macs {
		out = append(out, agentproto.WakeTarget{MAC: m, Broadcasts: bcs})
	}
	return out
}

// relaysFor picks the online devices that send the magic packet for a
// device: those in its network, the ones behind the same public address
// first (two sites may use the same private range), then servers (always
// on), then the device's own group.
func relaysFor(target powerNet, peers []powerNet) []powerNet {
	var out []powerNet
	for _, p := range peers {
		if p.dev.ID != target.dev.ID && target.sharesNetwork(p) {
			out = append(out, p)
		}
	}
	rank := func(p powerNet) int {
		r := 0
		if target.dev.RemoteIP == "" || p.dev.RemoteIP != target.dev.RemoteIP {
			r += 4
		}
		if p.dev.Kind == "workstation" {
			r += 2
		}
		if p.dev.GroupID != target.dev.GroupID {
			r++
		}
		return r
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank(out[i]), rank(out[j]); a != b {
			return a < b
		}
		return strings.ToLower(out[i].dev.Hostname) < strings.ToLower(out[j].dev.Hostname)
	})
	if len(out) > maxWakeRelays {
		out = out[:maxWakeRelays]
	}
	return out
}

// onlinePeers returns the network view of the connected devices.
func (s *Server) onlinePeers(ctx context.Context) ([]powerNet, error) {
	ids := s.hub.onlineIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	devs, err := s.st.PowerDevices(ctx, store.PowerDeviceQuery{IDs: ids})
	if err != nil {
		return nil, err
	}
	out := make([]powerNet, 0, len(devs))
	for _, d := range devs {
		if s.hub.online(d.ID) {
			out = append(out, newPowerNet(d))
		}
	}
	return out, nil
}

// ---- carrying out actions ----

type powerRequest struct {
	Trigger    string
	ScheduleID *int64
	By         string
}

var (
	errPowerOpen   = errors.New("an action is still open")
	errAlreadyOn   = errors.New("device online")
	errNoWakeRoute = errors.New("no way to wake")
)

// wakeDevices wakes switched-off devices and records an action for each.
// peers may be nil; it is loaded when needed.
func (s *Server) wakeDevices(ctx context.Context, devs []store.PowerDevice, peers []powerNet, req powerRequest) ([]store.PowerAction, error) {
	if len(devs) == 0 {
		return nil, nil
	}
	if peers == nil {
		var err error
		if peers, err = s.onlinePeers(ctx); err != nil {
			return nil, err
		}
	}
	cfg := s.loadPowerSettings(ctx)
	var out []store.PowerAction
	var serverTargets []agentproto.WakeTarget
	for _, d := range devs {
		if s.hub.online(d.ID) {
			continue
		}
		if open, err := s.st.OpenPowerAction(ctx, d.ID, agentproto.PowerWake); err != nil || open {
			continue
		}
		pn := newPowerNet(d)
		a := store.PowerAction{DeviceID: d.ID, Action: agentproto.PowerWake, Trigger: req.Trigger, ScheduleID: req.ScheduleID, RequestedBy: req.By}
		if len(pn.macs) == 0 {
			a.State, a.Message = agentproto.PowerFailed, "no MAC address known (no inventory yet)"
			if act, err := s.st.CreatePowerAction(ctx, a, true); err == nil {
				out = append(out, act)
			}
			continue
		}
		relays := relaysFor(pn, peers)
		for _, r := range relays {
			a.Relays = append(a.Relays, r.dev.Hostname)
		}
		if cfg.ServerWake {
			a.Relays = append(a.Relays, "server")
		}
		if len(relays) == 0 && !cfg.ServerWake {
			a.State, a.Message = agentproto.PowerFailed, "no online device in the same network to send the wake-up packet"
			if act, err := s.st.CreatePowerAction(ctx, a, true); err == nil {
				out = append(out, act)
			}
			continue
		}
		act, err := s.st.CreatePowerAction(ctx, a, false)
		if err != nil {
			return out, err
		}
		sent := 0
		for _, r := range relays {
			if s.hub.send(r.dev.ID, agentproto.TypePowerWake, agentproto.PowerWakeJob{ID: act.ID, Targets: pn.targets(), Port: cfg.Port}) {
				sent++
			}
		}
		if cfg.ServerWake {
			serverTargets = append(serverTargets, pn.targets()...)
			sent++
		}
		if sent == 0 {
			_ = s.st.FailPowerAction(ctx, act.ID, "the devices that should send the wake-up packet went offline")
			act.State = agentproto.PowerFailed
		}
		s.log.Info("wake sent", "device", d.Hostname, "relays", a.Relays, "trigger", req.Trigger)
		out = append(out, act)
	}
	if len(serverTargets) > 0 {
		go func() {
			for len(serverTargets) > 0 {
				n := min(len(serverTargets), wol.MaxTargets)
				if _, err := wol.Send(serverTargets[:n], cfg.Port); err != nil {
					s.log.Debug("wake from the server", "err", err)
				}
				serverTargets = serverTargets[n:]
			}
		}()
	}
	return out, nil
}

// powerOptions are the user notification settings of a restart or shutdown.
type powerOptions struct {
	Notify              bool `json:"notify"`
	MaxPostpones        int  `json:"maxPostpones"`
	PostponeMinutes     int  `json:"postponeMinutes"`
	CountdownSeconds    int  `json:"countdownSeconds"`
	SkipIfUserLoggedOn  bool `json:"skipIfUserLoggedOn"`
	OnlyIfRebootPending bool `json:"onlyIfRebootPending"`
}

func (o *powerOptions) clean() {
	o.MaxPostpones = min(max(o.MaxPostpones, 0), 20)
	if o.PostponeMinutes < 5 || o.PostponeMinutes > 1440 {
		o.PostponeMinutes = 30
	}
	if o.CountdownSeconds < 30 || o.CountdownSeconds > 3600 {
		o.CountdownSeconds = 300
	}
}

// sendPowerAction asks an online device to restart or shut down.
func (s *Server) sendPowerAction(ctx context.Context, d store.PowerDevice, action string, o powerOptions, req powerRequest) (store.PowerAction, error) {
	if !s.hub.online(d.ID) {
		return store.PowerAction{}, errDeviceOffline
	}
	for _, kind := range []string{agentproto.PowerRestart, agentproto.PowerShutdown} {
		if open, err := s.st.OpenPowerAction(ctx, d.ID, kind); err != nil {
			return store.PowerAction{}, err
		} else if open {
			return store.PowerAction{}, errPowerOpen
		}
	}
	o.clean()
	act, err := s.st.CreatePowerAction(ctx, store.PowerAction{DeviceID: d.ID, Action: action, Trigger: req.Trigger, ScheduleID: req.ScheduleID,
		RequestedBy: req.By}, false)
	if err != nil {
		return act, err
	}
	job := agentproto.PowerActionJob{ID: act.ID, Action: action, Notify: o.Notify, MaxPostpones: o.MaxPostpones,
		PostponeMinutes: o.PostponeMinutes, CountdownSeconds: o.CountdownSeconds, SkipIfUserLoggedOn: o.SkipIfUserLoggedOn,
		OnlyIfRebootPending: o.OnlyIfRebootPending && action == agentproto.PowerRestart}
	if !s.hub.send(d.ID, agentproto.TypePowerAction, job) {
		_ = s.st.FailPowerAction(ctx, act.ID, "the device went offline")
		return act, errDeviceOffline
	}
	s.log.Info("power action sent", "device", d.Hostname, "action", action, "trigger", req.Trigger)
	return act, nil
}

// runPowerSchedule carries out a schedule for the devices of its groups.
func (s *Server) runPowerSchedule(ctx context.Context, p store.PowerSchedule, by string) (int, error) {
	if len(p.GroupIDs) == 0 {
		return 0, nil
	}
	devs, err := s.st.PowerDevices(ctx, store.PowerDeviceQuery{Groups: p.GroupIDs})
	if err != nil {
		return 0, err
	}
	trigger := "schedule"
	if by != "" {
		trigger = "manual"
	}
	req := powerRequest{Trigger: trigger, ScheduleID: &p.ID, By: by}
	if p.Action == agentproto.PowerWake {
		acts, err := s.wakeDevices(ctx, devs, nil, req)
		return len(acts), err
	}
	o := powerOptions{Notify: p.NotifyUsers, MaxPostpones: p.MaxPostpones, PostponeMinutes: p.PostponeMinutes,
		CountdownSeconds: p.CountdownSeconds, SkipIfUserLoggedOn: p.SkipIfUserLoggedOn, OnlyIfRebootPending: p.OnlyIfRebootPending}
	n := 0
	for _, d := range devs {
		if !s.hub.online(d.ID) {
			continue
		}
		if _, err := s.sendPowerAction(ctx, d, p.Action, o, req); err == nil {
			n++
		}
	}
	return n, nil
}

// powerTick runs every minute: it closes stale actions, carries out due
// schedules and wakes and shuts down devices around patch windows.
func (s *Server) powerTick(ctx context.Context) {
	if err := s.st.FailStalePowerActions(ctx, wakeTimeout, powerActionStale); err != nil {
		s.log.Warn("power actions", "err", err)
	}
	cfg := s.loadPowerSettings(ctx)
	for _, id := range s.hub.onlineIDs() {
		s.pushPowerConfig(id, cfg)
	}
	now := s.now()
	schedules, err := s.st.ListPowerSchedules(ctx)
	if err != nil {
		s.log.Warn("power schedules", "err", err)
		return
	}
	for _, p := range schedules {
		if !p.Enabled {
			continue
		}
		slot, ok := lastSlot(p.Days, p.AtMinute, p.TimeZone, now)
		if !ok || now.Sub(slot) > powerScheduleCatchUp || (p.LastRunAt != nil && !p.LastRunAt.Before(slot)) {
			continue
		}
		if mine, err := s.st.MarkPowerScheduleRun(ctx, p.ID, slot); err != nil || !mine {
			continue
		}
		n, err := s.runPowerSchedule(ctx, p, "")
		s.log.Info("power schedule", "name", p.Name, "action", p.Action, "devices", n, "err", err)
	}
	s.patchWindowPower(ctx, now)
}

// patchWindowPower wakes the devices that are off when a patch window
// opens (when the policy says so) and shuts the woken ones down again once
// they are done.
func (s *Server) patchWindowPower(ctx context.Context, now time.Time) {
	policies, err := s.st.ListPatchPolicies(ctx)
	if err != nil {
		return
	}
	var peers []powerNet
	for _, p := range policies {
		if !p.Enabled || (!p.WakeForWindow && !p.ShutdownAfter) {
			continue
		}
		start, end, ok := currentWindow(p, now, afterPatchGrace)
		if !ok {
			continue
		}
		devs, err := s.st.PatchWindowDevices(ctx, p.ID, start)
		if err != nil {
			s.log.Warn("patch window devices", "err", err)
			continue
		}
		var wake []string
		for _, d := range devs {
			online := s.hub.online(d.ID)
			// Wake: while the window is open with at least half an hour left,
			// twice at most.
			if p.WakeForWindow && !online && !d.WakeOpen && d.WokeAt == nil && d.Wakes < 2 && now.Before(end.Add(-30*time.Minute)) {
				wake = append(wake, d.ID)
				continue
			}
			if !p.ShutdownAfter || d.WokeAt == nil || d.ShutdownSent || !online || d.RunOpen {
				continue
			}
			if !d.RunFinished && now.Sub(*d.WokeAt) < afterPatchIdle && now.Before(end) {
				continue // its patch job has not started or finished yet
			}
			pd := store.PowerDevice{ID: d.ID, Hostname: d.Hostname}
			o := powerOptions{Notify: true, MaxPostpones: 0, CountdownSeconds: 120, SkipIfUserLoggedOn: true}
			if _, err := s.sendPowerAction(ctx, pd, agentproto.PowerShutdown, o, powerRequest{Trigger: "after-patch"}); err != nil &&
				!errors.Is(err, errPowerOpen) {
				s.log.Debug("shutdown after patching", "device", d.Hostname, "err", err)
			}
		}
		if len(wake) == 0 {
			continue
		}
		if peers == nil {
			if peers, err = s.onlinePeers(ctx); err != nil {
				continue
			}
		}
		targets, err := s.st.PowerDevices(ctx, store.PowerDeviceQuery{IDs: wake})
		if err != nil {
			continue
		}
		if _, err := s.wakeDevices(ctx, targets, peers, powerRequest{Trigger: "patch-window"}); err != nil {
			s.log.Warn("wake for the patch window", "err", err)
		}
	}
}

func (s *Server) pushPowerConfig(id string, cfg powerSettings) {
	s.hub.sendChanged(id, "power.config", agentproto.TypePowerConfig, agentproto.PowerConfig{PrepareWake: cfg.PrepareWake})
}

// powerOnConnect closes the wakes of a device that came online and sends
// the power settings.
func (s *Server) powerOnConnect(ctx context.Context, id string) {
	if n, err := s.st.CompleteWakes(ctx, id); err == nil && n > 0 {
		s.log.Info("device woke up", "device", id)
	}
	s.pushPowerConfig(id, s.loadPowerSettings(ctx))
}

func (s *Server) handlePowerStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.PowerStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	if st.Wake {
		if st.State == agentproto.PowerFailed {
			host := c.deviceID
			if d, err := s.st.DeviceByID(ctx, c.deviceID); err == nil {
				host = d.Hostname
			}
			return s.st.NoteWakeRelay(ctx, st.ID, cleanText(host+": "+st.Message, 1000))
		}
		return nil
	}
	switch st.State {
	case agentproto.PowerWaitingUser, agentproto.PowerPostponed, agentproto.PowerExecuting, agentproto.PowerSkipped, agentproto.PowerFailed:
	default:
		return errors.New("unknown power state " + strconv.Quote(st.State))
	}
	u := store.PowerActionUpdate{State: st.State, Message: cleanText(st.Message, 1000), Postpones: max(st.Postpones, 0),
		Finished: agentproto.PowerFinished(st.State)}
	if st.State == agentproto.PowerExecuting {
		u.State = agentproto.PowerCompleted
	}
	if pu := st.PostponedUntil; !pu.IsZero() && pu.After(s.now().Add(-time.Hour)) && pu.Before(s.now().Add(48*time.Hour)) {
		u.PostponedUntil = &pu
	}
	err := s.st.UpdatePowerAction(ctx, st.ID, c.deviceID, u)
	if errors.Is(err, store.ErrNotFound) {
		return nil // a late report for an action that was closed already
	}
	return err
}

// ---- API ----

func (s *Server) powerRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	control := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermPowerControl, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermPowerManage, 0, h) }

	mux.Handle("GET /api/v1/power/devices", view(s.handlePowerDevices))
	mux.Handle("GET /api/v1/power/actions", view(s.handleListPowerActions))
	mux.Handle("POST /api/v1/power/actions", control(s.handleBulkPower))
	mux.Handle("POST /api/v1/devices/{id}/power", control(s.handleDevicePower))
	mux.Handle("GET /api/v1/power/schedules", view(s.handleListPowerSchedules))
	mux.Handle("POST /api/v1/power/schedules", manage(s.handleCreatePowerSchedule))
	mux.Handle("PUT /api/v1/power/schedules/{id}", manage(s.handleUpdatePowerSchedule))
	mux.Handle("DELETE /api/v1/power/schedules/{id}", manage(s.handleDeletePowerSchedule))
	mux.Handle("POST /api/v1/power/schedules/{id}/run", manage(s.handleRunPowerSchedule))
	mux.Handle("GET /api/v1/power/settings", view(s.handleGetPowerSettings))
	mux.Handle("PUT /api/v1/power/settings", manage(s.handlePutPowerSettings))
}

// powerDeviceView says whether a device can be woken.
type powerDeviceView struct {
	ID           string    `json:"id"`
	Hostname     string    `json:"hostname"`
	GroupID      int64     `json:"groupId"`
	Platform     string    `json:"platform"`
	Kind         string    `json:"kind"`
	Online       bool      `json:"online"`
	LoggedInUser string    `json:"loggedInUser"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	MACs         []string  `json:"macs"`
	Networks     []string  `json:"networks"`
	// Relays are the online devices that can wake it now.
	Relays []string `json:"relays"`
}

func (s *Server) handlePowerDevices(w http.ResponseWriter, r *http.Request) {
	sc := s.scope(r)
	devs, err := s.st.PowerDevices(r.Context(), store.PowerDeviceQuery{Scope: &sc})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	peers, err := s.onlinePeers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]powerDeviceView, 0, len(devs))
	for _, d := range devs {
		pn := newPowerNet(d)
		v := powerDeviceView{ID: d.ID, Hostname: d.Hostname, GroupID: d.GroupID, Platform: d.Platform, Kind: d.Kind, Online: s.hub.online(d.ID),
			LoggedInUser: d.LoggedInUser, MACs: pn.macs, Networks: []string{}, Relays: []string{}}
		if d.LastSeenAt != nil {
			v.LastSeenAt = *d.LastSeenAt
		}
		if v.MACs == nil {
			v.MACs = []string{}
		}
		for _, n := range pn.nets {
			if !slices.Contains(v.Networks, n.String()) {
				v.Networks = append(v.Networks, n.String())
			}
		}
		for _, p := range relaysFor(pn, peers) {
			v.Relays = append(v.Relays, p.dev.Hostname)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListPowerActions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListPowerActions(r.Context(), store.PowerActionQuery{Scope: s.scope(r), DeviceID: r.URL.Query().Get("device"), Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type powerReq struct {
	Action string `json:"action"`
	powerOptions
	DeviceIDs []string `json:"deviceIds,omitempty"` // bulk
}

type bulkPowerResult struct {
	Sent    int                 `json:"sent"`
	Skipped int                 `json:"skipped"`
	Actions []store.PowerAction `json:"actions"`
}

// carryOut sends one action to the devices and counts what happened.
func (s *Server) carryOut(ctx context.Context, devs []store.PowerDevice, req powerReq, by string) (bulkPowerResult, error) {
	res := bulkPowerResult{Actions: []store.PowerAction{}}
	pr := powerRequest{Trigger: "manual", By: by}
	if req.Action == agentproto.PowerWake {
		var off []store.PowerDevice
		for _, d := range devs {
			if !s.hub.online(d.ID) {
				off = append(off, d)
			}
		}
		acts, err := s.wakeDevices(ctx, off, nil, pr)
		if err != nil {
			return res, err
		}
		res.Actions = append(res.Actions, acts...)
		for _, a := range acts {
			if a.State == agentproto.PowerFailed {
				res.Skipped++
			} else {
				res.Sent++
			}
		}
		res.Skipped += len(devs) - len(acts)
		return res, nil
	}
	for _, d := range devs {
		a, err := s.sendPowerAction(ctx, d, req.Action, req.powerOptions, pr)
		if err != nil {
			res.Skipped++
			continue
		}
		res.Sent++
		res.Actions = append(res.Actions, a)
	}
	return res, nil
}

func (s *Server) handleDevicePower(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req powerReq
	if !decode(w, r, &req) {
		return
	}
	if !agentproto.ValidPowerAction(req.Action) {
		apiError(w, http.StatusBadRequest, "invalid_power_action")
		return
	}
	online := s.hub.online(d.ID)
	switch {
	case req.Action == agentproto.PowerWake && online:
		apiError(w, http.StatusConflict, "device_online")
		return
	case req.Action != agentproto.PowerWake && !online:
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	devs, err := s.st.PowerDevices(r.Context(), store.PowerDeviceQuery{IDs: []string{d.ID}})
	if err != nil || len(devs) != 1 {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if req.Action == agentproto.PowerWake {
		if open, _ := s.st.OpenPowerAction(r.Context(), d.ID, agentproto.PowerWake); open {
			apiError(w, http.StatusConflict, "power_action_open")
			return
		}
		acts, err := s.wakeDevices(r.Context(), devs, nil, powerRequest{Trigger: "manual", By: u.Username})
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if len(acts) != 1 {
			apiError(w, http.StatusConflict, "power_action_open")
			return
		}
		s.audit(r, "power.wake", d.Hostname, acts[0].State != agentproto.PowerFailed, map[string]any{"relays": acts[0].Relays})
		if acts[0].State == agentproto.PowerFailed {
			apiErrorDetail(w, http.StatusConflict, "wake_impossible", acts[0].Message)
			return
		}
		writeJSON(w, http.StatusAccepted, acts[0])
		return
	}
	a, err := s.sendPowerAction(r.Context(), devs[0], req.Action, req.powerOptions, powerRequest{Trigger: "manual", By: u.Username})
	switch {
	case errors.Is(err, errPowerOpen):
		apiError(w, http.StatusConflict, "power_action_open")
		return
	case errors.Is(err, errDeviceOffline):
		apiError(w, http.StatusConflict, "device_offline")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power."+req.Action, d.Hostname, true, map[string]any{"notify": req.Notify})
	writeJSON(w, http.StatusAccepted, a)
}

func (s *Server) handleBulkPower(w http.ResponseWriter, r *http.Request) {
	var req powerReq
	if !decode(w, r, &req) {
		return
	}
	if !agentproto.ValidPowerAction(req.Action) {
		apiError(w, http.StatusBadRequest, "invalid_power_action")
		return
	}
	if len(req.DeviceIDs) == 0 || len(req.DeviceIDs) > maxPowerBulk {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	devs, err := s.st.PowerDevices(r.Context(), store.PowerDeviceQuery{IDs: req.DeviceIDs})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sc := s.scope(r)
	allowed := devs[:0]
	for _, d := range devs {
		if sc.Allows(d.GroupID) {
			allowed = append(allowed, d)
		}
	}
	u, _ := currentUser(r)
	res, err := s.carryOut(r.Context(), allowed, req, u.Username)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power."+req.Action, strconv.Itoa(len(allowed))+" devices", true, map[string]any{"sent": res.Sent, "skipped": res.Skipped})
	writeJSON(w, http.StatusOK, res)
}

type powerScheduleView struct {
	store.PowerSchedule
	NextRun *time.Time `json:"nextRun"`
}

type powerSchedulesView struct {
	Schedules  []powerScheduleView `json:"schedules"`
	Defaults   store.PowerSchedule `json:"defaults"`
	ServerZone string              `json:"serverTimeZone"`
}

func (s *Server) powerScheduleView(p store.PowerSchedule) powerScheduleView {
	v := powerScheduleView{PowerSchedule: p}
	if p.Enabled {
		v.NextRun = nextPowerSlot(p.Days, p.AtMinute, p.TimeZone, s.now())
	}
	return v
}

func (s *Server) handleListPowerSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListPowerSchedules(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := powerSchedulesView{Schedules: make([]powerScheduleView, 0, len(list)), Defaults: defaultPowerSchedule(), ServerZone: serverZoneName()}
	out.Defaults.Groups, out.Defaults.GroupIDs = []store.GroupRef{}, []int64{}
	for _, p := range list {
		out.Schedules = append(out.Schedules, s.powerScheduleView(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePowerSchedule(w http.ResponseWriter, r *http.Request) {
	var p store.PowerSchedule
	if !decode(w, r, &p) {
		return
	}
	if bad := validPowerSchedule(&p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	p, err := s.st.CreatePowerSchedule(r.Context(), p)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "schedule_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power.schedule.create", p.Name, true, map[string]any{"action": p.Action})
	writeJSON(w, http.StatusCreated, s.powerScheduleView(p))
}

func (s *Server) handleUpdatePowerSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var p store.PowerSchedule
	if !decode(w, r, &p) {
		return
	}
	if bad := validPowerSchedule(&p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	p.ID = id
	err := s.st.UpdatePowerSchedule(r.Context(), p)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "schedule_exists")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power.schedule.update", p.Name, true, map[string]any{"action": p.Action, "enabled": p.Enabled})
	p, _ = s.st.PowerScheduleByID(r.Context(), id)
	writeJSON(w, http.StatusOK, s.powerScheduleView(p))
}

func (s *Server) handleDeletePowerSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := s.st.PowerScheduleByID(r.Context(), id)
	if err == nil {
		err = s.st.DeletePowerSchedule(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power.schedule.delete", p.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunPowerSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := s.st.PowerScheduleByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	n, err := s.runPowerSchedule(r.Context(), p, u.Username)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power.schedule.run", p.Name, true, map[string]any{"devices": n})
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}

func (s *Server) handleGetPowerSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadPowerSettings(r.Context()))
}

func (s *Server) handlePutPowerSettings(w http.ResponseWriter, r *http.Request) {
	var req powerSettings
	if !decode(w, r, &req) {
		return
	}
	if req.Port != 7 && req.Port != 9 {
		apiError(w, http.StatusBadRequest, "invalid_wake_port")
		return
	}
	if err := s.st.PutSetting(r.Context(), keyPower, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "power.settings", "", true, map[string]any{"serverWake": req.ServerWake, "port": req.Port, "prepareWake": req.PrepareWake})
	for _, id := range s.hub.onlineIDs() {
		s.pushPowerConfig(id, req)
	}
	writeJSON(w, http.StatusOK, req)
}
