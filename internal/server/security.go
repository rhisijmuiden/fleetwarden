package server

// Security status: disk encryption with escrowed BitLocker recovery keys,
// antivirus (Defender), the firewall, Secure Boot, the TPM and the local
// administrators of every device, plus Defender scans, signature updates
// and recovery key rotation from the web interface.
//
// Recovery passwords are sealed with the server's secret key as soon as
// they arrive and are never written to the report JSON. Showing one needs
// the security.keys permission and is written to the audit log.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

var (
	recoveryPasswordRE = regexp.MustCompile(`^\d{6}(-\d{6}){7}$`)
	keyProtectorIDRE   = regexp.MustCompile(`^\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\}$`)
	driveLetterRE      = regexp.MustCompile(`^[A-Z]:$`)
)

const (
	maxSecurityVolumes = 64
	maxSecurityAdmins  = 200
	maxSecurityThreats = 50
	maxSecurityItems   = 16
)

func cleanStrings(in []string, maxItems, maxLen int) []string {
	out := cleanList(in, maxItems, maxLen)
	if len(out) == 0 {
		return nil
	}
	return out
}

// cleanSecurity bounds every field of a report and takes the recovery keys
// out of it.
func cleanSecurity(in agentproto.SecurityReport) (agentproto.SecurityReport, []store.SeenKey) {
	out := agentproto.SecurityReport{CollectedAt: in.CollectedAt, SecureBoot: in.SecureBoot, Errors: cleanStrings(in.Errors, 20, 500),
		Volumes: []agentproto.SecureVolume{}, Antivirus: []agentproto.Antivirus{}, Firewall: []agentproto.FirewallState{},
		Admins: []agentproto.LocalAdmin{}}
	var keys []store.SeenKey
	for _, v := range in.Volumes {
		if len(out.Volumes) >= maxSecurityVolumes {
			break
		}
		sv := agentproto.SecureVolume{Mount: cleanText(v.Mount, 255), System: v.System, Method: v.Method, Protection: v.Protection,
			Status: v.Status, Percent: min(max(v.Percent, 0), 100), Cipher: cleanText(v.Cipher, 60), Protectors: cleanStrings(v.Protectors, 16, 40)}
		if sv.Method != agentproto.EncryptionBitLocker && sv.Method != agentproto.EncryptionLUKS {
			sv.Method = agentproto.EncryptionNone
		}
		switch sv.Protection {
		case "on", "off", "suspended":
		default:
			sv.Protection = "off"
		}
		switch sv.Status {
		case "encrypted", "encrypting", "decrypting", "decrypted", "paused":
		default:
			sv.Status = "decrypted"
		}
		if sv.Mount == "" {
			continue
		}
		mount := strings.ToUpper(sv.Mount)
		for _, k := range v.Keys {
			id := strings.ToUpper(strings.TrimSpace(k.ID))
			if v.Method == agentproto.EncryptionBitLocker && keyProtectorIDRE.MatchString(id) && recoveryPasswordRE.MatchString(k.Password) &&
				driveLetterRE.MatchString(mount) && len(keys) < 64 {
				keys = append(keys, store.SeenKey{KeyID: id, Volume: mount, Password: k.Password})
			}
		}
		out.Volumes = append(out.Volumes, sv)
	}
	for _, a := range in.Antivirus {
		if len(out.Antivirus) >= maxSecurityItems {
			break
		}
		if name := cleanText(a.Name, 200); name != "" {
			out.Antivirus = append(out.Antivirus, agentproto.Antivirus{Name: name, Enabled: a.Enabled, UpToDate: a.UpToDate, Defender: a.Defender})
		}
	}
	if d := in.Defender; d != nil {
		ds := &agentproto.DefenderStatus{Mode: cleanText(d.Mode, 60), ServiceEnabled: d.ServiceEnabled, AntivirusEnabled: d.AntivirusEnabled,
			RealTime: d.RealTime, TamperProtected: d.TamperProtected, SignatureVersion: cleanText(d.SignatureVersion, 60),
			SignatureUpdatedAt: validTime(timeOf(d.SignatureUpdatedAt)), EngineVersion: cleanText(d.EngineVersion, 60),
			QuickScanAt: validTime(timeOf(d.QuickScanAt)), FullScanAt: validTime(timeOf(d.FullScanAt))}
		for _, t := range d.Threats {
			if len(ds.Threats) >= maxSecurityThreats {
				break
			}
			name := cleanText(t.Name, 300)
			if name == "" || validTime(t.DetectedAt) == nil {
				continue
			}
			ds.Threats = append(ds.Threats, agentproto.Threat{Name: name, Severity: cleanText(t.Severity, 20), DetectedAt: t.DetectedAt,
				Active: t.Active, Action: cleanText(t.Action, 40), Resources: cleanStrings(t.Resources, 5, 500)})
		}
		out.Defender = ds
	}
	for _, f := range in.Firewall {
		if len(out.Firewall) >= maxSecurityItems {
			break
		}
		name := cleanText(f.Name, 100)
		if name == "" {
			continue
		}
		in := f.Inbound
		if in != "block" && in != "allow" {
			in = ""
		}
		out.Firewall = append(out.Firewall, agentproto.FirewallState{Name: name, Enabled: f.Enabled, Inbound: in, Active: f.Active})
	}
	for _, a := range in.Admins {
		if len(out.Admins) >= maxSecurityAdmins {
			break
		}
		name := cleanText(a.Name, 300)
		if name == "" {
			continue
		}
		kind := a.Kind
		if kind != "user" && kind != "group" {
			kind = "unknown"
		}
		out.Admins = append(out.Admins, agentproto.LocalAdmin{Name: name, SID: cleanText(a.SID, 200), Kind: kind, Builtin: a.Builtin})
	}
	if t := in.TPM; t != nil {
		out.TPM = &agentproto.TPMStatus{Present: t.Present, Ready: t.Ready, Version: cleanText(t.Version, 20)}
	}
	return out, keys
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// summarizeSecurity turns a report into the overview columns.
func summarizeSecurity(platform string, r agentproto.SecurityReport) store.SecuritySummary {
	sm := store.SecuritySummary{Encryption: "unknown", Antivirus: "unknown", Firewall: "unknown", Admins: len(r.Admins), SecureBoot: r.SecureBoot}
	if r.TPM != nil {
		ok := r.TPM.Present && r.TPM.Ready
		sm.TPM = &ok
	}
	// Encryption: the system volume decides; other fixed volumes make it
	// partial.
	var sys *agentproto.SecureVolume
	others := true
	for i, v := range r.Volumes {
		if v.System && sys == nil {
			sys = &r.Volumes[i]
		} else if v.Method == agentproto.EncryptionNone && (!strings.HasPrefix(v.Mount, "/") || v.Mount == "/home") {
			// On Linux only /home matters next to /: /boot is never
			// encrypted and other mounts are often scratch space.
			others = false
		}
	}
	if sys != nil {
		switch {
		case sys.Method == agentproto.EncryptionNone:
			sm.Encryption = "none"
		case sys.Status == "encrypting" || sys.Status == "paused":
			sm.Encryption = "encrypting"
		case sys.Protection == "suspended":
			sm.Encryption = "suspended"
		case sys.Protection != "on" || sys.Status == "decrypting":
			sm.Encryption = "none"
		case !others:
			sm.Encryption = "partial"
		default:
			sm.Encryption = "encrypted"
		}
	}
	// Antivirus: one product on and up to date is enough.
	if len(r.Antivirus) == 0 {
		if platform == "linux" || r.Defender != nil {
			sm.Antivirus = "none"
		}
	} else {
		sm.Antivirus = "disabled"
		for _, a := range r.Antivirus {
			switch {
			case a.Enabled && a.UpToDate:
				sm.Antivirus = "ok"
			case a.Enabled && sm.Antivirus != "ok":
				sm.Antivirus = "outdated"
			}
		}
	}
	// Firewall: on when every profile is, partial when the active one is
	// (Windows) or some are.
	if len(r.Firewall) > 0 {
		on, off := 0, 0
		for _, f := range r.Firewall {
			if f.Enabled {
				on++
			} else {
				off++
			}
		}
		switch {
		case off == 0:
			sm.Firewall = "on"
		case on == 0:
			sm.Firewall = "off"
		default:
			sm.Firewall = "partial"
		}
	}
	if d := r.Defender; d != nil {
		for _, t := range d.Threats {
			if t.Active {
				sm.Threats++
			}
		}
	}
	return sm
}

// bitLockerRead reports whether the BitLocker part of a report succeeded,
// so missing keys mean removed keys.
func bitLockerRead(r agentproto.SecurityReport) bool {
	for _, e := range r.Errors {
		if strings.HasPrefix(e, "BitLocker:") || strings.HasPrefix(e, "security status:") {
			return false
		}
	}
	return true
}

func (s *Server) handleSecurityReportMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var rep agentproto.SecurityReport
	if err := e.Decode(&rep); err != nil {
		return err
	}
	dev, err := s.st.DeviceByID(ctx, c.deviceID)
	if err != nil {
		return err
	}
	clean, keys := cleanSecurity(rep)
	at := validTime(clean.CollectedAt)
	if at == nil {
		now := s.now().UTC()
		at = &now
	}
	clean.CollectedAt = *at
	if s.box == nil && len(keys) > 0 {
		return errNoSecrets
	}
	for i := range keys {
		if keys[i].Password, err = s.box.Seal(keys[i].Password); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	return s.st.SaveSecurity(ctx, store.SecurityUpdate{DeviceID: c.deviceID, CollectedAt: *at, Summary: summarizeSecurity(dev.Platform, clean),
		Report: raw, Keys: keys, KeysComplete: dev.Platform == "windows" && bitLockerRead(clean)})
}

func (s *Server) handleSecurityActionStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.SecurityActionStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	switch st.State {
	case agentproto.SecurityActionRunning, agentproto.SecurityActionCompleted, agentproto.SecurityActionFailed:
	default:
		return errors.New("unknown security action state")
	}
	a, ok, err := s.st.UpdateSecurityAction(ctx, c.deviceID, st.ID, st.State, cleanText(st.Message, 500))
	if err != nil || !ok {
		return err
	}
	if st.State != agentproto.SecurityActionRunning {
		s.log.Info("security action finished", "device", c.deviceID, "action", a.Action, "state", a.State)
		_ = s.st.Audit(context.WithoutCancel(ctx), auditSystem("security."+a.Action, c.deviceID, st.State == agentproto.SecurityActionCompleted,
			map[string]any{"action": a.ID, "message": a.Message}))
	}
	return nil
}

// securityOnConnect sends the actions that waited for the device.
func (s *Server) securityOnConnect(ctx context.Context, deviceID string) {
	list, err := s.st.OpenSecurityActions(ctx, deviceID)
	if err != nil {
		return
	}
	for _, a := range list {
		s.hub.send(deviceID, agentproto.TypeSecurityAction, agentproto.SecurityAction{ID: a.ID, Action: a.Action, Mount: a.Mount})
	}
}

func (s *Server) securityTick(ctx context.Context) {
	if err := s.st.ExpireSecurityActions(ctx); err != nil {
		s.log.Warn("security actions", "err", err)
	}
}

// ---- API ----

func (s *Server) securityRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesManage, 0, h) }
	keys := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSecurityKeys, 0, h) }

	mux.Handle("GET /api/v1/security/summary", view(s.handleSecuritySummary))
	mux.Handle("GET /api/v1/security/devices", view(s.handleSecurityDevices))
	mux.Handle("GET /api/v1/security/keys", keys(s.handleListRecoveryKeys))
	mux.Handle("POST /api/v1/security/keys/{id}/reveal", keys(s.handleRevealRecoveryKey))
	mux.Handle("GET /api/v1/devices/{id}/security", view(s.handleDeviceSecurity))
	mux.Handle("POST /api/v1/devices/{id}/security/refresh", manage(s.handleSecurityRefresh))
	mux.Handle("POST /api/v1/devices/{id}/security/actions", manage(s.handleSecurityAction))
}

func canSeeKeys(r *http.Request) bool {
	u, _ := currentUser(r)
	return auth.Role(u.Role).Has(auth.PermSecurityKeys)
}

func (s *Server) handleSecuritySummary(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.SecurityStats(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

var securityFilters = []string{"", "unencrypted", "antivirus", "firewall", "threats", "secureboot", "unreported"}

func (s *Server) handleSecurityDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sq := store.SecurityQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")), Filter: q.Get("filter")}
	sq.GroupID, _ = strconv.ParseInt(q.Get("group"), 10, 64)
	ok := false
	for _, f := range securityFilters {
		ok = ok || f == sq.Filter
	}
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_filter")
		return
	}
	list, err := s.st.ListSecurity(r.Context(), sq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !canSeeKeys(r) {
		for i := range list {
			list[i].Keys = 0
		}
	}
	writeJSON(w, http.StatusOK, list)
}

type deviceSecurityView struct {
	Reported    bool                   `json:"reported"`
	CollectedAt *time.Time             `json:"collectedAt"`
	ReceivedAt  *time.Time             `json:"receivedAt"`
	Summary     *store.SecuritySummary `json:"summary"`
	Report      json.RawMessage        `json:"report"`
	// Keys are listed (without passwords) for users who may show them.
	CanKeys bool                   `json:"canKeys"`
	Keys    []store.RecoveryKey    `json:"keys"`
	Actions []store.SecurityAction `json:"actions"`
}

func (s *Server) handleDeviceSecurity(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	out := deviceSecurityView{CanKeys: canSeeKeys(r), Keys: []store.RecoveryKey{}, Report: json.RawMessage("null")}
	ds, err := s.st.DeviceSecurity(r.Context(), d.ID)
	switch {
	case err == nil:
		out.Reported, out.CollectedAt, out.ReceivedAt, out.Summary, out.Report = true, &ds.CollectedAt, &ds.ReceivedAt, &ds.Summary, ds.Report
	case !errors.Is(err, store.ErrNotFound):
		s.internalError(w, r, err)
		return
	}
	if out.CanKeys {
		if out.Keys, err = s.st.ListRecoveryKeys(r.Context(), store.RecoveryKeyQuery{Scope: s.scope(r), DeviceID: d.ID}); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if out.Actions, err = s.st.ListSecurityActions(r.Context(), d.ID, 20); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSecurityRefresh(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	if !s.hub.send(d.ID, agentproto.TypeSecurityRequest, struct{}{}) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

type securityActionReq struct {
	Action string `json:"action"`
	Mount  string `json:"mount"`
}

func (s *Server) handleSecurityAction(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req securityActionReq
	if !decode(w, r, &req) {
		return
	}
	switch req.Action {
	case agentproto.SecurityQuickScan, agentproto.SecurityFullScan, agentproto.SecuritySignatures:
		req.Mount = ""
	case agentproto.SecurityRotateKey:
		if !canSeeKeys(r) {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		req.Mount = strings.ToUpper(strings.TrimSpace(req.Mount))
		if !driveLetterRE.MatchString(req.Mount) {
			apiError(w, http.StatusBadRequest, "invalid_volume")
			return
		}
	default:
		apiError(w, http.StatusBadRequest, "invalid_security_action")
		return
	}
	if d.Platform != "windows" {
		apiError(w, http.StatusBadRequest, "not_supported")
		return
	}
	u, _ := currentUser(r)
	a, err := s.st.CreateSecurityAction(r.Context(), store.SecurityAction{DeviceID: d.ID, Action: req.Action, Mount: req.Mount, RequestedBy: u.Username})
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "action_open")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Offline devices get it when they connect.
	s.hub.send(d.ID, agentproto.TypeSecurityAction, agentproto.SecurityAction{ID: a.ID, Action: a.Action, Mount: a.Mount})
	s.audit(r, "security."+req.Action, d.Hostname, true, map[string]any{"action": a.ID, "mount": req.Mount})
	writeJSON(w, http.StatusAccepted, a)
}

func (s *Server) handleListRecoveryKeys(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListRecoveryKeys(r.Context(), store.RecoveryKeyQuery{Scope: s.scope(r), Search: cleanText(r.URL.Query().Get("search"), 100)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type revealReq struct {
	Reason string `json:"reason"`
}

func (s *Server) handleRevealRecoveryKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req revealReq
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	k, err := s.st.RecoveryKeyByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !k.InScope(s.scope(r))) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if s.box == nil {
		s.internalError(w, r, errNoSecrets)
		return
	}
	pw, err := s.box.Open(k.Password())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if err := s.st.MarkKeyRevealed(r.Context(), k.ID, u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "security.key.reveal", k.Hostname, true, map[string]any{"keyId": k.KeyID, "volume": k.Volume, "reason": cleanText(req.Reason, 300)})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"keyId": k.KeyID, "password": pw})
}

// ---- alerts ----

func isSecurityAlert(kind string) bool {
	return kind == "threat" || kind == "antivirus" || kind == "firewall" || kind == "encryption"
}

// securityDetails is what a security alert shows: the state and its text
// key, or the names of the active threats.
type securityDetails struct {
	State   string   `json:"state,omitempty"`
	Text    string   `json:"text,omitempty"`
	Threats []string `json:"threats,omitempty"`
}

// securityProblem reports whether a device's security status breaks an
// alert rule of this kind. Linux devices without antivirus do not count:
// most servers run without one.
func securityProblem(kind string, d store.SecurityAlertDevice) (securityDetails, bool) {
	sm := d.Summary
	switch kind {
	case "threat":
		if sm.Threats == 0 {
			return securityDetails{}, false
		}
		var rep agentproto.SecurityReport
		_ = json.Unmarshal(d.Report, &rep)
		det := securityDetails{}
		if rep.Defender != nil {
			for _, t := range rep.Defender.Threats {
				if t.Active && len(det.Threats) < 5 && !slices.Contains(det.Threats, t.Name) {
					det.Threats = append(det.Threats, t.Name)
				}
			}
		}
		if len(det.Threats) == 0 {
			det.Threats = []string{strconv.Itoa(sm.Threats)}
		}
		return det, true
	case "antivirus":
		if sm.Antivirus == "outdated" || sm.Antivirus == "disabled" || (sm.Antivirus == "none" && d.Platform == "windows") {
			return securityDetails{State: sm.Antivirus, Text: "security.av." + sm.Antivirus}, true
		}
	case "firewall":
		if sm.Firewall == "off" || sm.Firewall == "partial" {
			return securityDetails{State: sm.Firewall, Text: "security.fw." + sm.Firewall}, true
		}
	case "encryption":
		if sm.Encryption == "none" || sm.Encryption == "suspended" || sm.Encryption == "partial" {
			return securityDetails{State: sm.Encryption, Text: "security.enc." + sm.Encryption}, true
		}
	}
	return securityDetails{}, false
}
