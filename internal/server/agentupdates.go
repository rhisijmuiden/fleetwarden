package server

// Agent updates: the server downloads agent releases from GitHub (or an admin
// uploads them) and rolls a version out ring by ring. Each device group has an
// update ring (0 = pilot ... 3 = last), an agent channel and an update window.
// A ring advances after the soak time; too many failed devices pause the
// rollout. Agents install the package themselves and roll back on failure.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
)

const (
	keyAgentUpdates     = "agent_updates"
	maxAgentPackageSize = 300 << 20
	updatesPerTick      = 25               // update messages sent per minute
	updateRetryAfter    = 30 * time.Minute // before a failed device is tried again
	updateMaxAttempts   = 2
	updateTimeout       = 45 * time.Minute // no progress: the attempt failed
	agentReleaseCheck   = time.Hour
)

var agentVersionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-z]+\.[0-9]+)?$`)

func validAgentVersion(v string) bool { return agentVersionRE.MatchString(v) }

// comparableVersion reports whether a device's agent version takes part in
// updates (hand-made builds without a version are left alone).
func comparableVersion(v string) bool { return v != "0.0.0-dev" && semver.IsValid("v"+v) }

type agentUpdateSettings struct {
	AutoDownload bool      `json:"autoDownload"` // fetch new agent releases from GitHub
	AutoRollout  bool      `json:"autoRollout"`  // start a rollout for each new release
	SoakHours    int       `json:"soakHours"`    // per ring
	MaxFailures  int       `json:"maxFailures"`  // per ring, before the rollout pauses
	KeepReleases int       `json:"keepReleases"` // downloaded releases kept on disk
	LastCheck    time.Time `json:"lastCheck"`
	LastError    string    `json:"lastError"`
}

func defaultAgentUpdateSettings() agentUpdateSettings {
	return agentUpdateSettings{AutoDownload: true, AutoRollout: true, SoakHours: 24, MaxFailures: 2, KeepReleases: 5}
}

func (s *Server) loadAgentUpdateSettings(ctx context.Context) agentUpdateSettings {
	v := defaultAgentUpdateSettings()
	_ = s.st.GetSetting(ctx, keyAgentUpdates, &v)
	return v
}

func (v *agentUpdateSettings) valid() bool {
	return v.SoakHours >= 0 && v.SoakHours <= 720 && v.MaxFailures >= 0 && v.MaxFailures <= 1000 && v.KeepReleases >= 1 && v.KeepReleases <= 50
}

func channelOfVersion(v string) string {
	if strings.Contains(v, "-") {
		return updates.ChannelTest
	}
	return updates.ChannelProduction
}

// ---- rollout planning (pure functions) ----

type groupInfo struct {
	store.DeviceGroup
	channel string
}

func effectiveChannel(g store.DeviceGroup, serverChannel string) string {
	if g.AgentChannel != nil && updates.ValidChannel(*g.AgentChannel) {
		return *g.AgentChannel
	}
	return serverChannel
}

// targetRollout picks the rollout a group follows now: the highest version
// among rollouts that reached the group's ring and fit its channel.
func targetRollout(rollouts []store.Rollout, g groupInfo) *store.Rollout {
	var best *store.Rollout
	for i, r := range rollouts {
		switch r.Status {
		case store.RolloutActive, store.RolloutPaused:
			if g.UpdateRing > r.Ring {
				continue
			}
		case store.RolloutCompleted:
		default:
			continue
		}
		if !channelAllows(g.channel, r.Version) {
			continue
		}
		if best == nil || updates.Compare(r.Version, best.Version) > 0 {
			best = &rollouts[i]
		}
	}
	return best
}

// needsVersion reports whether a device runs an older agent than v.
func needsVersion(d store.DeviceUpdate, v string) bool {
	return comparableVersion(d.AgentVersion) && updates.Compare(d.AgentVersion, v) < 0
}

func updateFailed(state string) bool {
	return state == agentproto.UpdateFailed || state == agentproto.UpdateRolledBack
}

func updateBusy(state string) bool {
	return state == "sent" || state == agentproto.UpdateDownloading || state == agentproto.UpdateInstalling
}

// inUpdateWindow reports whether the group allows updates at this hour.
func inUpdateWindow(g store.DeviceGroup, now time.Time) bool {
	if g.WindowStart == nil || g.WindowEnd == nil || *g.WindowStart == *g.WindowEnd {
		return true
	}
	h, a, b := now.Hour(), *g.WindowStart, *g.WindowEnd
	if a < b {
		return h >= a && h < b
	}
	return h >= a || h < b
}

type ringStats struct {
	Ring       int      `json:"ring"`
	Groups     []string `json:"groups"`
	Devices    int      `json:"devices"`    // devices of the channel in this ring
	Updated    int      `json:"updated"`    // already on the version (or newer)
	InProgress int      `json:"inProgress"` // update sent, not finished
	Failed     int      `json:"failed"`
	Pending    int      `json:"pending"` // still to update
}

// rolloutRings counts the devices per ring for a rollout.
func rolloutRings(r store.Rollout, groups map[int64]groupInfo, devs []store.DeviceUpdate) []ringStats {
	rings := make([]ringStats, 4)
	for i := range rings {
		rings[i].Ring = i
		rings[i].Groups = []string{}
	}
	for _, g := range sortedGroups(groups) {
		if channelAllows(g.channel, r.Version) {
			rings[g.UpdateRing].Groups = append(rings[g.UpdateRing].Groups, g.Name)
		}
	}
	for _, d := range devs {
		g, ok := groups[d.GroupID]
		if !ok || !channelAllows(g.channel, r.Version) || !comparableVersion(d.AgentVersion) {
			continue
		}
		rs := &rings[g.UpdateRing]
		rs.Devices++
		mine := d.Rollout != nil && *d.Rollout == r.ID && d.Version == r.Version
		switch {
		case !needsVersion(d, r.Version):
			rs.Updated++
		case mine && updateFailed(d.State):
			rs.Failed++
		case mine && updateBusy(d.State):
			rs.InProgress++
		default:
			rs.Pending++
		}
	}
	return rings
}

func sortedGroups(groups map[int64]groupInfo) []groupInfo {
	out := make([]groupInfo, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// advance moves an active rollout on: pause when a ring has too many
// failures, otherwise the next ring once the soak time has passed. Rings
// without devices are skipped at once. It reports whether r changed.
func advance(r *store.Rollout, rings []ringStats, now time.Time) bool {
	if r.Status != store.RolloutActive {
		return false
	}
	changed := false
	for {
		cur := rings[r.Ring]
		if cur.Failed > r.MaxFailures {
			r.Status = store.RolloutPaused
			r.Message = fmt.Sprintf("paused: %d devices in ring %d failed to update", cur.Failed, r.Ring)
			return true
		}
		soaked := now.Sub(r.RingStartedAt) >= time.Duration(r.SoakHours)*time.Hour
		if cur.Devices > 0 && !soaked {
			return changed
		}
		changed = true
		if r.Ring == 3 {
			r.Status, r.Message = store.RolloutCompleted, ""
			return true
		}
		r.Ring++
		r.RingStartedAt, r.Message = now, ""
	}
}

// ---- worker ----

func (s *Server) groupInfos(ctx context.Context) (map[int64]groupInfo, error) {
	gs, err := s.st.ListGroups(ctx, store.Scope{All: true})
	if err != nil {
		return nil, err
	}
	ch := s.loadUpdateSettings(ctx).Channel
	out := make(map[int64]groupInfo, len(gs))
	for _, g := range gs {
		out[g.ID] = groupInfo{DeviceGroup: g, channel: effectiveChannel(g, ch)}
	}
	return out, nil
}

// agentUpdateTick advances rollouts and sends update requests to online
// agents. It runs every minute.
func (s *Server) agentUpdateTick(ctx context.Context) {
	now := s.now()
	if _, err := s.st.TimeOutUpdates(ctx, now.Add(-updateTimeout)); err != nil {
		s.log.Warn("agent updates", "err", err)
		return
	}
	rollouts, err := s.st.ListRollouts(ctx, 50)
	if err != nil {
		s.log.Warn("agent updates", "err", err)
		return
	}
	groups, err := s.groupInfos(ctx)
	if err != nil {
		s.log.Warn("agent updates", "err", err)
		return
	}
	devs, err := s.st.DeviceUpdates(ctx)
	if err != nil {
		s.log.Warn("agent updates", "err", err)
		return
	}
	for i := range rollouts {
		r := &rollouts[i]
		if advance(r, rolloutRings(*r, groups, devs), now) {
			if err := s.st.SaveRolloutState(ctx, *r); err != nil {
				s.log.Warn("agent rollout", "id", r.ID, "err", err)
				continue
			}
			s.log.Info("agent rollout", "id", r.ID, "version", r.Version, "status", r.Status, "ring", r.Ring, "message", r.Message)
			if r.Status == store.RolloutPaused {
				_ = s.st.Audit(ctx, auditSystem("agent.rollout_paused", r.Version, false, map[string]any{"rollout": r.ID, "ring": r.Ring, "message": r.Message}))
			}
		}
	}
	pkgs := s.agentPackages()
	sent := 0
	for _, d := range devs {
		if sent >= updatesPerTick {
			break
		}
		if !s.hub.online(d.DeviceID) {
			continue
		}
		g, ok := groups[d.GroupID]
		if !ok {
			continue
		}
		t := targetRollout(rollouts, g)
		if t == nil || t.Status == store.RolloutPaused || !needsVersion(d, t.Version) || !inUpdateWindow(g.DeviceGroup, now.Local()) {
			continue
		}
		if t.Status != store.RolloutActive && t.Status != store.RolloutCompleted {
			continue
		}
		if d.Version == t.Version && d.Rollout != nil && *d.Rollout == t.ID {
			if updateBusy(d.State) {
				continue
			}
			if d.At != nil && now.Sub(*d.At) < updateRetryAfter {
				continue
			}
			if updateFailed(d.State) && d.Attempts >= updateMaxAttempts {
				continue
			}
		}
		msg, ok := updateMessage(pkgs, d, t.Version)
		if !ok {
			continue
		}
		if s.hub.send(d.DeviceID, agentproto.TypeUpdate, msg) {
			sent++
			if err := s.st.StartDeviceUpdate(ctx, d.DeviceID, t.ID, t.Version); err != nil {
				s.log.Warn("agent update", "device", d.DeviceID, "err", err)
			}
			s.log.Info("agent update sent", "device", d.DeviceID, "host", d.Hostname, "from", d.AgentVersion, "to", t.Version)
		}
	}
}

func deviceTarget(platform, arch string) string {
	if platform == "windows" {
		return "windows-x64.msi"
	}
	return "linux-" + arch + ".deb"
}

// updateMessage builds the update request for a device, with the package of
// its current version for a rollback when the server has it.
func updateMessage(pkgs agentPackages, d store.DeviceUpdate, v string) (agentproto.Update, bool) {
	target := deviceTarget(d.Platform, d.Arch)
	p, ok := pkgs.version(target, v)
	if !ok || p.SHA256 == "" {
		return agentproto.Update{}, false
	}
	u := agentproto.Update{Version: v, Package: p.Name, SHA256: p.SHA256, Size: p.Size}
	if prev, ok := pkgs.version(target, d.AgentVersion); ok && prev.SHA256 != "" {
		u.Previous, u.PreviousSHA256 = prev.Name, prev.SHA256
	}
	return u, true
}

// handleUpdateStatus stores progress an agent reports.
func (s *Server) handleUpdateStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.UpdateStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	switch st.State {
	case agentproto.UpdateDownloading, agentproto.UpdateInstalling, agentproto.UpdateCompleted, agentproto.UpdateFailed, agentproto.UpdateRolledBack:
	default:
		return fmt.Errorf("unknown update state %q", st.State)
	}
	msg := cleanText(st.Message, 500)
	if updateFailed(st.State) {
		s.log.Warn("agent update failed", "device", c.deviceID, "version", st.Version, "state", st.State, "message", msg)
	}
	return s.st.SetDeviceUpdateState(ctx, c.deviceID, cleanText(st.Version, 50), st.State, msg)
}

// ---- signed package download for agents ----

func (s *Server) handleAgentFile(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	id, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(agentproto.HeaderAuth))
	if err != nil {
		s.agentFails.Fail(ip)
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	dev, err := s.st.DeviceByID(r.Context(), id)
	if err != nil || !agentproto.VerifyRequest(dev.PublicKey, dev.ID, r.Method, r.URL.Path, ts, sig, s.now(), 5*time.Minute) {
		s.agentFails.Fail(ip)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	var pkg *agentPackage
	for _, p := range s.agentPackages() {
		if p.Name == name {
			pkg = &p
			break
		}
	}
	if pkg == nil {
		http.Error(w, "no such package", http.StatusNotFound)
		return
	}
	f, err := os.Open(pkg.path)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, pkg.Name, pkg.Added, f)
}

// ---- releases from GitHub ----

type releaseFetcher struct {
	mu sync.Mutex // one fetch at a time
}

// fetchAgentReleases downloads the newest agent release of every channel in
// use, starts automatic rollouts and prunes old downloads.
func (s *Server) fetchAgentReleases(ctx context.Context, force bool) error {
	if !s.fetcher.mu.TryLock() {
		return errors.New("a release check is already running")
	}
	defer s.fetcher.mu.Unlock()
	set := s.loadAgentUpdateSettings(ctx)
	err := s.fetchAgentReleasesLocked(ctx, force, set)
	set = s.loadAgentUpdateSettings(ctx) // settings may have changed meanwhile
	set.LastCheck, set.LastError = s.now(), ""
	if err != nil {
		set.LastError = err.Error()
	}
	if perr := s.st.PutSetting(ctx, keyAgentUpdates, set); perr != nil && err == nil {
		err = perr
	}
	return err
}

func (s *Server) fetchAgentReleasesLocked(ctx context.Context, force bool, set agentUpdateSettings) error {
	rels, err := s.updates.AgentReleases(ctx, force)
	if err != nil {
		return err
	}
	groups, err := s.groupInfos(ctx)
	if err != nil {
		return err
	}
	channels := map[string]bool{s.loadUpdateSettings(ctx).Channel: true}
	for _, g := range groups {
		channels[g.channel] = true
	}
	var errs []error
	for _, ch := range []string{updates.ChannelProduction, updates.ChannelTest} {
		if !channels[ch] {
			continue
		}
		var rel *updates.AgentRelease
		for i, r := range rels { // newest first
			if channelAllows(ch, r.Version) {
				rel = &rels[i]
				break
			}
		}
		if rel == nil {
			continue
		}
		if err := s.downloadAgentRelease(ctx, *rel); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel.Version, err))
			continue
		}
		if set.AutoRollout {
			if err := s.autoRollout(ctx, rel.Version, set); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := s.pruneAgentReleases(ctx, set.KeepReleases); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Server) releaseDir(v string) string { return filepath.Join(s.cfg.AgentReleasesDir(), v) }

func (s *Server) downloadAgentRelease(ctx context.Context, rel updates.AgentRelease) error {
	final := s.releaseDir(rel.Version)
	if _, err := os.Stat(filepath.Join(final, "release.json")); err == nil {
		return nil // already here
	}
	var sumsURL string
	for _, a := range rel.Assets {
		if a.Name == "SHA256SUMS" {
			sumsURL = a.URL
		}
	}
	if sumsURL == "" {
		return errors.New("the release has no SHA256SUMS")
	}
	if err := os.MkdirAll(s.cfg.AgentReleasesDir(), 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.cfg.AgentReleasesDir(), ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	sumsPath := filepath.Join(tmp, "SHA256SUMS.github")
	if _, err := s.fetchFile(ctx, sumsURL, sumsPath, 1<<20); err != nil {
		return fmt.Errorf("SHA256SUMS: %w", err)
	}
	sums := readSumsFile(sumsPath)
	var lines []string
	for _, a := range rel.Assets {
		if _, ok := parseAgentFile(a.Name); !ok {
			continue
		}
		want, ok := sums[a.Name]
		if !ok {
			return fmt.Errorf("%s is not in SHA256SUMS", a.Name)
		}
		got, err := s.fetchFile(ctx, a.URL, filepath.Join(tmp, a.Name), maxAgentPackageSize)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		if got != want {
			return fmt.Errorf("%s: SHA-256 does not match", a.Name)
		}
		lines = append(lines, want+"  "+a.Name)
	}
	if len(lines) == 0 {
		return errors.New("the release has no agent packages")
	}
	_ = os.Remove(sumsPath)
	if err := os.WriteFile(filepath.Join(tmp, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		return err
	}
	if err := writeReleaseMeta(tmp, releaseMeta{Source: "github", Added: s.now().UTC()}); err != nil {
		return err
	}
	_ = os.RemoveAll(final) // an incomplete earlier attempt or upload
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	s.log.Info("downloaded agent release", "version", rel.Version, "files", len(lines))
	_ = s.st.Audit(ctx, auditSystem("agent.release_download", rel.Version, true, map[string]any{"files": len(lines)}))
	return nil
}

func writeReleaseMeta(dir string, m releaseMeta) error {
	raw, _ := json.Marshal(m)
	return os.WriteFile(filepath.Join(dir, "release.json"), raw, 0o640)
}

// fetchFile downloads url to dst and returns its SHA-256.
func (s *Server) fetchFile(ctx context.Context, url, dst string, limit int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "fleetwarden-server")
	client := &http.Client{}
	if s.updates.HTTP != nil {
		client.Transport = s.updates.HTTP.Transport
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", res.Status)
	}
	return writeHashed(dst, res.Body, limit)
}

func writeHashed(dst string, r io.Reader, limit int64) (string, error) {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errors.New("file is too large")
	}
	if err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// autoRollout starts a rollout for v unless one exists for it or a newer version.
func (s *Server) autoRollout(ctx context.Context, v string, set agentUpdateSettings) error {
	rs, err := s.st.ListRollouts(ctx, 50)
	if err != nil {
		return err
	}
	ch := channelOfVersion(v)
	for _, r := range rs {
		if r.Version == v || (r.Channel == ch && r.Status != store.RolloutCancelled && updates.Compare(r.Version, v) > 0) {
			return nil
		}
	}
	r, err := s.st.CreateRollout(ctx, store.Rollout{Version: v, Channel: ch, SoakHours: set.SoakHours, MaxFailures: set.MaxFailures,
		Automatic: true, CreatedBy: "system"})
	if err != nil {
		return err
	}
	s.log.Info("started agent rollout", "id", r.ID, "version", v)
	_ = s.st.Audit(ctx, auditSystem("agent.rollout_start", v, true, map[string]any{"rollout": r.ID, "automatic": true}))
	return nil
}

// pruneAgentReleases removes downloaded releases beyond keep, except versions
// devices still run (rollback packages) and versions of open rollouts.
func (s *Server) pruneAgentReleases(ctx context.Context, keep int) error {
	dirs, err := os.ReadDir(s.cfg.AgentReleasesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	inUse := map[string]bool{}
	if devs, err := s.st.DeviceUpdates(ctx); err == nil {
		for _, d := range devs {
			inUse[d.AgentVersion] = true
		}
	} else {
		return err
	}
	if rs, err := s.st.ListRollouts(ctx, 50); err == nil {
		for _, r := range rs {
			if r.Open() {
				inUse[r.Version] = true
			}
		}
	} else {
		return err
	}
	var versions []string
	for _, d := range dirs {
		if d.IsDir() && validAgentVersion(d.Name()) {
			var m releaseMeta
			if raw, err := os.ReadFile(filepath.Join(s.cfg.AgentReleasesDir(), d.Name(), "release.json")); err == nil && json.Unmarshal(raw, &m) == nil && m.Source == "github" {
				versions = append(versions, d.Name())
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool { return updates.Compare(versions[i], versions[j]) > 0 })
	for i, v := range versions {
		if i < keep || inUse[v] {
			continue
		}
		if err := os.RemoveAll(s.releaseDir(v)); err != nil {
			return err
		}
		s.log.Info("removed old agent release", "version", v)
	}
	return nil
}

func (s *Server) agentReleaseLoop(ctx context.Context) {
	defer s.bgWG.Done()
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.loadAgentUpdateSettings(ctx).AutoDownload {
			if err := s.fetchAgentReleases(ctx, false); err != nil {
				s.log.Warn("agent releases", "err", err)
			}
		}
		t.Reset(agentReleaseCheck)
	}
}

// ---- admin API ----

func (s *Server) agentUpdateRoutes(mux *http.ServeMux) {
	adm := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermAgentsManage, 0, h) }
	mux.HandleFunc("GET "+agentproto.PathDownload+"{name}", s.handleAgentFile)
	mux.Handle("GET /api/v1/agent/updates", adm(s.handleGetAgentUpdates))
	mux.Handle("PUT /api/v1/agent/updates/settings", adm(s.handlePutAgentUpdateSettings))
	mux.Handle("POST /api/v1/agent/updates/check", adm(s.handleCheckAgentReleases))
	mux.Handle("POST /api/v1/agent/releases", adm(s.handleUploadAgentPackage))
	mux.Handle("DELETE /api/v1/agent/releases/{version}", adm(s.handleDeleteAgentRelease))
	mux.Handle("POST /api/v1/agent/rollouts", adm(s.handleCreateRollout))
	mux.Handle("POST /api/v1/agent/rollouts/{id}/{action}", adm(s.handleRolloutAction))
}

type releaseView struct {
	Version    string         `json:"version"`
	Prerelease bool           `json:"prerelease"`
	Source     string         `json:"source"`
	Added      time.Time      `json:"added"`
	Packages   []agentPackage `json:"packages"`
	Deletable  bool           `json:"deletable"`
	Devices    int            `json:"devices"` // devices running it
}

type rolloutView struct {
	store.Rollout
	Rings      []ringStats `json:"rings"`
	SoakEndsAt *time.Time  `json:"soakEndsAt"`
}

type deviceProblem struct {
	DeviceID string     `json:"deviceId"`
	Hostname string     `json:"hostname"`
	From     string     `json:"from"`
	Version  string     `json:"version"`
	State    string     `json:"state"`
	Message  string     `json:"message"`
	At       *time.Time `json:"at"`
}

type agentUpdatesView struct {
	Settings agentUpdateSettings `json:"settings"`
	Channel  string              `json:"channel"` // the server's channel (groups without their own)
	Releases []releaseView       `json:"releases"`
	Rollouts []rolloutView       `json:"rollouts"`
	Problems []deviceProblem     `json:"problems"`
	Versions map[string]int      `json:"versions"` // devices per agent version
}

func (s *Server) handleGetAgentUpdates(w http.ResponseWriter, r *http.Request) {
	v, err := s.agentUpdatesView(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) agentUpdatesView(ctx context.Context) (agentUpdatesView, error) {
	v := agentUpdatesView{Settings: s.loadAgentUpdateSettings(ctx), Channel: s.loadUpdateSettings(ctx).Channel,
		Releases: []releaseView{}, Rollouts: []rolloutView{}, Problems: []deviceProblem{}, Versions: map[string]int{}}
	groups, err := s.groupInfos(ctx)
	if err != nil {
		return v, err
	}
	devs, err := s.st.DeviceUpdates(ctx)
	if err != nil {
		return v, err
	}
	rollouts, err := s.st.ListRollouts(ctx, 20)
	if err != nil {
		return v, err
	}
	for _, d := range devs {
		v.Versions[d.AgentVersion]++
	}
	open := map[string]bool{}
	for _, ro := range rollouts {
		rv := rolloutView{Rollout: ro, Rings: rolloutRings(ro, groups, devs)}
		if ro.Status == store.RolloutActive {
			end := ro.RingStartedAt.Add(time.Duration(ro.SoakHours) * time.Hour)
			rv.SoakEndsAt = &end
		}
		if ro.Open() {
			open[ro.Version] = true
		}
		v.Rollouts = append(v.Rollouts, rv)
	}
	byVersion := map[string]*releaseView{}
	var order []string
	for _, p := range s.agentPackages() {
		rv := byVersion[p.Version]
		if rv == nil {
			rv = &releaseView{Version: p.Version, Prerelease: p.prerelease(), Source: p.Source, Added: p.Added, Devices: v.Versions[p.Version]}
			byVersion[p.Version] = rv
			order = append(order, p.Version)
		}
		if p.Source != "bundled" && rv.Source == "bundled" {
			rv.Source = p.Source
		}
		rv.Packages = append(rv.Packages, p)
	}
	for _, ver := range order {
		rv := byVersion[ver]
		_, err := os.Stat(s.releaseDir(ver))
		rv.Deletable = err == nil && !open[ver]
		v.Releases = append(v.Releases, *rv)
	}
	for _, d := range devs {
		if d.Rollout != nil && updateFailed(d.State) && needsVersion(d, d.Version) {
			v.Problems = append(v.Problems, deviceProblem{DeviceID: d.DeviceID, Hostname: d.Hostname, From: d.AgentVersion,
				Version: d.Version, State: d.State, Message: d.Message, At: d.At})
		}
	}
	sort.Slice(v.Problems, func(i, j int) bool {
		return strings.ToLower(v.Problems[i].Hostname) < strings.ToLower(v.Problems[j].Hostname)
	})
	return v, nil
}

func (s *Server) handlePutAgentUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req agentUpdateSettings
	if !decode(w, r, &req) {
		return
	}
	if !req.valid() {
		apiError(w, http.StatusBadRequest, "invalid_settings")
		return
	}
	cur := s.loadAgentUpdateSettings(r.Context())
	req.LastCheck, req.LastError = cur.LastCheck, cur.LastError
	if err := s.st.PutSetting(r.Context(), keyAgentUpdates, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "agent.update_settings", "", true, map[string]any{"autoDownload": req.AutoDownload, "autoRollout": req.AutoRollout,
		"soakHours": req.SoakHours, "maxFailures": req.MaxFailures})
	s.handleGetAgentUpdates(w, r)
}

func (s *Server) handleCheckAgentReleases(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	err := s.fetchAgentReleases(ctx, true)
	s.audit(r, "agent.release_check", "", err == nil, nil)
	s.handleGetAgentUpdates(w, r)
}

// handleUploadAgentPackage stores an uploaded .msi or .deb (multipart field "file").
func (s *Server) handleUploadAgentPackage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentPackageSize+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			apiError(w, http.StatusBadRequest, "no_file")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		name := filepath.Base(part.FileName())
		pkg, ok := parseAgentFile(name)
		if !ok {
			apiError(w, http.StatusBadRequest, "invalid_package_name")
			return
		}
		dir := s.releaseDir(pkg.Version)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			s.internalError(w, r, err)
			return
		}
		tmp := filepath.Join(dir, "."+name+".upload")
		br := bufio.NewReader(part)
		head, _ := br.Peek(8)
		if !validPackageMagic(pkg.Kind, head) {
			apiError(w, http.StatusBadRequest, "invalid_package")
			return
		}
		sum, err := writeHashed(tmp, br, maxAgentPackageSize)
		if err != nil {
			apiError(w, http.StatusRequestEntityTooLarge, "too_large")
			return
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			s.internalError(w, r, err)
			return
		}
		if err := updateSums(dir, name, sum); err != nil {
			s.internalError(w, r, err)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, "release.json")); err != nil {
			_ = writeReleaseMeta(dir, releaseMeta{Source: "upload", Added: s.now().UTC()})
		}
		s.audit(r, "agent.package_upload", name, true, map[string]any{"sha256": sum})
		s.handleGetAgentUpdates(w, r)
		return
	}
}

func validPackageMagic(kind string, head []byte) bool {
	switch kind {
	case "deb":
		return string(head) == "!<arch>\n"
	case "msi":
		return len(head) == 8 && string(head) == "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
	}
	return false
}

// updateSums sets the line for name in the folder's SHA256SUMS.
func updateSums(dir, name, sum string) error {
	sums := readSums(dir)
	sums[name] = sum
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o640)
}

func (s *Server) handleDeleteAgentRelease(w http.ResponseWriter, r *http.Request) {
	v := r.PathValue("version")
	if !validAgentVersion(v) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if _, err := os.Stat(s.releaseDir(v)); err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	rs, err := s.st.ListRollouts(r.Context(), 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, ro := range rs {
		if ro.Open() && ro.Version == v {
			apiError(w, http.StatusConflict, "release_in_use")
			return
		}
	}
	if err := os.RemoveAll(s.releaseDir(v)); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "agent.release_delete", v, true, nil)
	s.handleGetAgentUpdates(w, r)
}

func (s *Server) handleCreateRollout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version     string `json:"version"`
		SoakHours   *int   `json:"soakHours"`
		MaxFailures *int   `json:"maxFailures"`
	}
	if !decode(w, r, &req) {
		return
	}
	found := false
	for _, p := range s.agentPackages() {
		if p.Version == req.Version {
			found = true
			break
		}
	}
	if !validAgentVersion(req.Version) || !found {
		apiError(w, http.StatusBadRequest, "unknown_version")
		return
	}
	set := s.loadAgentUpdateSettings(r.Context())
	ro := store.Rollout{Version: req.Version, Channel: channelOfVersion(req.Version), SoakHours: set.SoakHours, MaxFailures: set.MaxFailures}
	if req.SoakHours != nil {
		ro.SoakHours = *req.SoakHours
	}
	if req.MaxFailures != nil {
		ro.MaxFailures = *req.MaxFailures
	}
	if ro.SoakHours < 0 || ro.SoakHours > 720 || ro.MaxFailures < 0 {
		apiError(w, http.StatusBadRequest, "invalid_settings")
		return
	}
	if u, ok := currentUser(r); ok {
		ro.CreatedBy = u.Username
	}
	created, err := s.st.CreateRollout(r.Context(), ro)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "agent.rollout_start", req.Version, true, map[string]any{"rollout": created.ID})
	s.handleGetAgentUpdates(w, r)
}

func (s *Server) handleRolloutAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	ro, err := s.st.RolloutByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	action := r.PathValue("action")
	now := s.now()
	switch {
	case action == "pause" && ro.Status == store.RolloutActive:
		ro.Status, ro.Message = store.RolloutPaused, "paused by an administrator"
	case action == "resume" && ro.Status == store.RolloutPaused:
		// Failures that paused the rollout are retried once more.
		if _, err := s.st.ResetFailedUpdates(r.Context(), ro.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
		ro.Status, ro.Message = store.RolloutActive, ""
	case action == "advance" && ro.Status == store.RolloutActive:
		if ro.Ring == 3 {
			ro.Status = store.RolloutCompleted
		} else {
			ro.Ring++
			ro.RingStartedAt = now
		}
		ro.Message = ""
	case action == "cancel" && ro.Open():
		ro.Status, ro.Message = store.RolloutCancelled, "cancelled by an administrator"
	case action == "retry" && ro.Open():
		if _, err := s.st.ResetFailedUpdates(r.Context(), ro.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	default:
		apiError(w, http.StatusConflict, "invalid_action")
		return
	}
	if err := s.st.SaveRolloutState(r.Context(), ro); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "agent.rollout_"+action, ro.Version, true, map[string]any{"rollout": ro.ID, "ring": ro.Ring})
	s.handleGetAgentUpdates(w, r)
}
