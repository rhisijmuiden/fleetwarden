package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/secrets"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
)

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesManage, 0, h) }
	groups := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermGroupsManage, 0, h) }

	mux.Handle("GET /api/v1/devices", view(s.handleListDevices))
	mux.Handle("GET /api/v1/devices/stats", view(s.handleDeviceStats))
	mux.Handle("GET /api/v1/devices/{id}", view(s.handleGetDevice))
	mux.Handle("GET /api/v1/devices/{id}/software", view(s.handleDeviceSoftware))
	mux.Handle("PATCH /api/v1/devices/{id}", manage(s.handleUpdateDevice))
	mux.Handle("POST /api/v1/devices/{id}/inventory", manage(s.handleRequestInventory))
	mux.Handle("DELETE /api/v1/devices/{id}", manage(s.handleDeleteDevice))
	mux.Handle("POST /api/v1/devices/move", manage(s.handleMoveDevices))

	mux.Handle("GET /api/v1/groups", view(s.handleListGroups))
	mux.Handle("POST /api/v1/groups", groups(s.handleCreateGroup))
	mux.Handle("PUT /api/v1/groups/{id}", groups(s.handleUpdateGroup))
	mux.Handle("DELETE /api/v1/groups/{id}", groups(s.handleDeleteGroup))

	mux.Handle("GET /api/v1/enrollment-tokens", manage(s.handleListTokens))
	mux.Handle("POST /api/v1/enrollment-tokens", manage(s.handleCreateToken))
	mux.Handle("GET /api/v1/enrollment-tokens/{id}/install", manage(s.handleTokenInstall))
	mux.Handle("GET /api/v1/enrollment-tokens/{id}/devices", manage(s.handleTokenDevices))
	mux.Handle("DELETE /api/v1/enrollment-tokens/{id}", manage(s.handleRevokeToken))
	mux.Handle("GET /api/v1/agent/packages", manage(s.handleAgentPackages))
}

// scope returns the device-group scope of the signed-in user.
func (s *Server) scope(r *http.Request) store.Scope {
	u, _ := currentUser(r)
	return store.UserScope(u)
}

// deviceInScope loads a device and answers 404 when it does not exist or lies
// outside the user's scope.
func (s *Server) deviceInScope(w http.ResponseWriter, r *http.Request) (store.Device, bool) {
	d, err := s.st.DeviceByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !s.scope(r).Allows(d.GroupID)) {
		apiError(w, http.StatusNotFound, "not_found")
		return d, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return d, false
	}
	s.markOnline(&d)
	return d, true
}

type deviceList struct {
	Items []store.Device `json:"items"`
	Total int            `json:"total"`
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dq := store.DeviceQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")), Platform: q.Get("platform"),
		Software: strings.TrimSpace(q.Get("software"))}
	dq.GroupID, _ = strconv.ParseInt(q.Get("group"), 10, 64)
	dq.Limit, _ = strconv.Atoi(q.Get("limit"))
	dq.Offset, _ = strconv.Atoi(q.Get("offset"))
	switch q.Get("status") {
	case "online":
		dq.IDs = s.hub.onlineIDs()
	case "offline":
		dq.ExcludeIDs = s.hub.onlineIDs()
	}
	items, total, err := s.st.ListDevices(r.Context(), dq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range items {
		s.markOnline(&items[i])
	}
	writeJSON(w, http.StatusOK, deviceList{Items: items, Total: total})
}

type deviceStats struct {
	store.DeviceStats
	Online int `json:"online"`
}

func (s *Server) handleDeviceStats(w http.ResponseWriter, r *http.Request) {
	sc := s.scope(r)
	st, err := s.st.CountDevices(r.Context(), sc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	_, online, err := s.st.ListDevices(r.Context(), store.DeviceQuery{Scope: sc, IDs: s.hub.onlineIDs(), Limit: 1})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deviceStats{DeviceStats: st, Online: online})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.deviceInScope(w, r); ok {
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Server) handleDeviceSoftware(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	sw, err := s.st.DeviceSoftware(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sw)
}

type updateDeviceReq struct {
	Notes   *string `json:"notes"`
	GroupID *int64  `json:"groupId"`
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req updateDeviceReq
	if !decode(w, r, &req) {
		return
	}
	if req.GroupID != nil && *req.GroupID != d.GroupID {
		if !s.moveAllowed(w, r, *req.GroupID) {
			return
		}
		if _, err := s.st.MoveDevices(r.Context(), []string{d.ID}, *req.GroupID); err != nil {
			s.internalError(w, r, err)
			return
		}
		s.audit(r, "device.move", d.Hostname, true, map[string]any{"from": d.GroupName, "toGroupId": *req.GroupID})
	}
	if req.Notes != nil {
		if err := s.st.SetDeviceNotes(r.Context(), d.ID, cleanNotes(*req.Notes)); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	d, ok = s.deviceInScope(w, r)
	if ok {
		writeJSON(w, http.StatusOK, d)
	}
}

func cleanNotes(v string) string {
	v = strings.TrimSpace(strings.ToValidUTF8(v, ""))
	if len(v) > 4000 {
		v = v[:4000]
	}
	return v
}

// moveAllowed checks that the target group exists and is in the user's scope.
func (s *Server) moveAllowed(w http.ResponseWriter, r *http.Request, groupID int64) bool {
	if _, err := s.st.GroupByID(r.Context(), groupID); err != nil || !s.scope(r).Allows(groupID) {
		apiError(w, http.StatusBadRequest, "invalid_group")
		return false
	}
	return true
}

type moveReq struct {
	IDs     []string `json:"ids"`
	GroupID int64    `json:"groupId"`
}

func (s *Server) handleMoveDevices(w http.ResponseWriter, r *http.Request) {
	var req moveReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > 5000 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !s.moveAllowed(w, r, req.GroupID) {
		return
	}
	// Only devices the user can see are moved.
	visible, _, err := s.st.ListDevices(r.Context(), store.DeviceQuery{Scope: s.scope(r), IDs: req.IDs, Limit: 5000})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ids := make([]string, 0, len(visible))
	for _, d := range visible {
		ids = append(ids, d.ID)
	}
	n, err := s.st.MoveDevices(r.Context(), ids, req.GroupID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "device.move", strconv.FormatInt(n, 10)+" devices", true, map[string]any{"toGroupId": req.GroupID})
	writeJSON(w, http.StatusOK, map[string]int64{"moved": n})
}

func (s *Server) handleRequestInventory(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	if !s.hub.send(d.ID, agentproto.TypeInventoryRequest, nil) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	// A connected agent forgets its enrollment and stops; an offline agent is
	// refused at its next connection and must be enrolled again.
	s.hub.send(d.ID, agentproto.TypeRemove, nil)
	if err := s.st.DeleteDevice(r.Context(), d.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "device.delete", d.Hostname, true, map[string]any{"deviceId": d.ID, "group": d.GroupName})
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.st.ListGroups(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	online, err := s.st.OnlineCounts(r.Context(), s.hub.onlineIDs())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range groups {
		groups[i].OnlineCount = online[groups[i].ID]
	}
	writeJSON(w, http.StatusOK, groups)
}

type groupReq struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	UpdateRing   int     `json:"updateRing"`
	AgentChannel *string `json:"agentChannel"`
	WindowStart  *int    `json:"windowStart"`
	WindowEnd    *int    `json:"windowEnd"`
	PatchPolicy  *int64  `json:"patchPolicyId"`
	// RemoteConsent: auto, direct, notify or ask (empty keeps auto).
	RemoteConsent string `json:"remoteConsent"`
	// Scan interval overrides in minutes; null uses the default from Settings.
	InventoryMinutes *int `json:"inventoryMinutes"`
	PatchScanMinutes *int `json:"patchScanMinutes"`
}

func (req groupReq) toGroup() (store.DeviceGroup, string) {
	g := store.DeviceGroup{Name: cleanText(req.Name, 100), Description: cleanText(req.Description, 500), UpdateRing: req.UpdateRing,
		WindowStart: req.WindowStart, WindowEnd: req.WindowEnd, RemoteConsent: req.RemoteConsent,
		InventoryMinutes: req.InventoryMinutes, PatchScanMinutes: req.PatchScanMinutes}
	if req.PatchPolicy != nil && *req.PatchPolicy > 0 {
		g.PatchPolicy = req.PatchPolicy
	}
	if g.Name == "" {
		return g, "invalid_name"
	}
	if g.UpdateRing < 0 || g.UpdateRing > 3 {
		return g, "invalid_ring"
	}
	if !validConsentSetting(g.RemoteConsent) {
		return g, "invalid_consent"
	}
	if (g.InventoryMinutes != nil && !validInventoryMinutes(*g.InventoryMinutes)) || (g.PatchScanMinutes != nil && !validPatchScanMinutes(*g.PatchScanMinutes)) {
		return g, "invalid_scan_interval"
	}
	if req.AgentChannel != nil && *req.AgentChannel != "" {
		if !updates.ValidChannel(*req.AgentChannel) {
			return g, "invalid_channel"
		}
		g.AgentChannel = req.AgentChannel
	}
	validHour := func(h *int) bool { return h == nil || (*h >= 0 && *h <= 23) }
	if !validHour(g.WindowStart) || !validHour(g.WindowEnd) || (g.WindowStart == nil) != (g.WindowEnd == nil) {
		return g, "invalid_window"
	}
	return g, ""
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupReq
	if !decode(w, r, &req) {
		return
	}
	g, bad := req.toGroup()
	if bad == "" && g.PatchPolicy != nil {
		if _, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err != nil {
			bad = "invalid_policy"
		}
	}
	if bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	g, err := s.st.CreateGroup(r.Context(), g)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "group_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "group.create", g.Name, true, nil)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req groupReq
	if !decode(w, r, &req) {
		return
	}
	g, bad := req.toGroup()
	if bad == "" && g.PatchPolicy != nil {
		if _, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err != nil {
			bad = "invalid_policy"
		}
	}
	if bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	g.ID = id
	err := s.st.UpdateGroup(r.Context(), g)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "group_exists")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "group.update", g.Name, true, map[string]any{"ring": g.UpdateRing, "channel": g.AgentChannel, "patchPolicy": g.PatchPolicy, "remoteConsent": g.RemoteConsent,
		"inventoryMinutes": g.InventoryMinutes, "patchScanMinutes": g.PatchScanMinutes})
	g, _ = s.st.GroupByID(r.Context(), id)
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	g, err := s.st.GroupByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if g.DeviceCount > 0 {
		apiError(w, http.StatusConflict, "group_not_empty")
		return
	}
	if err := s.st.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apiError(w, http.StatusConflict, "group_not_empty")
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "group.delete", g.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- enrollment tokens ----

const tokenPrefix = "fwe_"

// CreateEnrollmentToken creates a token for t.GroupID and returns it with its
// plain value. expiresInDays and maxUses of 0 mean no limit.
func CreateEnrollmentToken(ctx context.Context, st *store.Store, box *secrets.Box, t store.EnrollmentToken, expiresInDays, maxUses int) (store.EnrollmentToken, string, error) {
	plain := tokenPrefix + newToken()
	enc, err := box.Seal(plain)
	if err != nil {
		return t, "", err
	}
	t.TokenEnc, t.Prefix = enc, plain[:len(tokenPrefix)+6]
	if expiresInDays > 0 {
		exp := time.Now().Add(time.Duration(expiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	if maxUses > 0 {
		t.MaxUses = &maxUses
	}
	t, err = st.CreateToken(ctx, t, plain)
	return t, plain, err
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.st.ListTokens(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toks)
}

type createTokenReq struct {
	GroupID       int64  `json:"groupId"`
	Description   string `json:"description"`
	ExpiresInDays int    `json:"expiresInDays"` // 0: never
	MaxUses       int    `json:"maxUses"`       // 0: unlimited
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenReq
	if !decode(w, r, &req) {
		return
	}
	if _, err := s.st.GroupByID(r.Context(), req.GroupID); err != nil || !s.scope(r).Allows(req.GroupID) {
		apiError(w, http.StatusBadRequest, "invalid_group")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 || req.MaxUses < 0 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if s.box == nil {
		s.internalError(w, r, errNoSecrets)
		return
	}
	me, _ := currentUser(r)
	t, _, err := CreateEnrollmentToken(r.Context(), s.st, s.box, store.EnrollmentToken{Description: cleanText(req.Description, 200),
		GroupID: req.GroupID, CreatedBy: me.Username}, req.ExpiresInDays, req.MaxUses)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "token.create", t.Prefix, true, map[string]any{"group": t.GroupName, "expiresAt": t.ExpiresAt, "maxUses": t.MaxUses})
	writeJSON(w, http.StatusCreated, t)
}

// tokenInScope loads a token the user may manage.
func (s *Server) tokenInScope(w http.ResponseWriter, r *http.Request) (store.EnrollmentToken, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.EnrollmentToken{}, false
	}
	t, err := s.st.TokenByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !s.scope(r).Allows(t.GroupID)) {
		apiError(w, http.StatusNotFound, "not_found")
		return t, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return t, false
	}
	return t, true
}

type installInfo struct {
	Token          string           `json:"token"`
	ServerURL      string           `json:"serverUrl"`
	Usable         bool             `json:"usable"`
	WindowsCommand string           `json:"windowsCommand"`
	LinuxCommand   string           `json:"linuxCommand"`
	MsiexecCommand string           `json:"msiexecCommand"`
	Downloads      []installPackage `json:"downloads"`
}

type installPackage struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	Name     string `json:"name"`
}

// handleTokenInstall returns the token with ready-made install commands and
// download links for the Add client wizard.
func (s *Server) handleTokenInstall(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tokenInScope(w, r)
	if !ok {
		return
	}
	plain, err := s.box.Open(t.TokenEnc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	base := s.baseURL(r)
	if via := r.URL.Query().Get("via"); via != "" {
		// Install commands for devices outside the network: through a gateway.
		id, err := strconv.ParseInt(via, 10, 64)
		g, gerr := s.st.GatewayByID(r.Context(), id)
		if err != nil || gerr != nil || !g.Enabled {
			apiError(w, http.StatusBadRequest, "invalid_gateway")
			return
		}
		base = strings.TrimRight(g.PublicURL, "/")
	}
	info := installInfo{
		Token: plain, ServerURL: base, Usable: t.Usable(s.now()),
		WindowsCommand: "iwr '" + base + "/agent/install.ps1?t=" + plain + "' -UseBasicParsing | iex",
		LinuxCommand:   "curl -fsSL " + base + "/agent/install.sh | sudo bash -s -- " + plain,
		MsiexecCommand: "msiexec /i fleetwarden-agent.msi /qn SERVER=" + base + " TOKEN=" + plain,
		Downloads:      []installPackage{},
	}
	channel := s.groupChannel(r, t.GroupID)
	for _, p := range s.agentPackages().latestPerTarget(channel) {
		info.Downloads = append(info.Downloads, installPackage{Platform: p.Platform, Arch: p.Arch, Kind: p.Kind, Version: p.Version,
			Name: p.downloadName(), URL: base + "/agent/download/" + p.target() + "?t=" + plain})
	}
	s.audit(r, "token.reveal", t.Prefix, true, nil)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleTokenDevices(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tokenInScope(w, r)
	if !ok {
		return
	}
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		since = s.now().Add(-time.Hour)
	}
	devs, err := s.st.DevicesEnrolledSince(r.Context(), t.ID, since)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range devs {
		s.markOnline(&devs[i])
	}
	writeJSON(w, http.StatusOK, devs)
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tokenInScope(w, r)
	if !ok {
		return
	}
	if err := s.st.RevokeToken(r.Context(), t.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "token.revoke", t.Prefix, true, map[string]any{"group": t.GroupName})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentPackages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agentPackages().all())
}

// markOnline fills in the connection state of a device.
func (s *Server) markOnline(d *store.Device) {
	online, gw := s.hub.via(d.ID)
	d.Online, d.ConnectedVia = online, ""
	if online && gw != 0 {
		d.ConnectedVia = s.gatewayName(gw)
	}
}
