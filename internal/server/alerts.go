package server

// Alerts: rules that open an alert when a device has been offline too long
// or a disk is running full, and close it again when the problem is gone.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/reports"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

var deviceKinds = []string{"workstation", "server", "hypervisor"}

const maxAlertRecipients = 20

// diskHysteresis keeps a disk alert open until the free space is this many
// percentage points above the threshold, so it doesn't open and close all
// the time around the limit.
const diskHysteresis = 1

// offlineDetails and diskDetails are what an alert shows.
type offlineDetails struct {
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type diskDetails struct {
	Mount     string `json:"mount"`
	SizeBytes int64  `json:"sizeBytes"`
	FreeBytes int64  `json:"freeBytes"`
	Percent   int    `json:"percent"`
}

func freePercent(v store.ReportVolume) int {
	if v.SizeBytes <= 0 {
		return 100
	}
	return int(v.FreeBytes * 100 / v.SizeBytes)
}

// ruleApplies reports whether a rule looks at a device.
func ruleApplies(r store.AlertRule, d store.AlertDevice) bool {
	return (len(r.GroupIDs) == 0 || slices.Contains(r.GroupIDs, d.GroupID)) &&
		(len(r.DeviceKinds) == 0 || slices.Contains(r.DeviceKinds, d.Kind))
}

type alertKey struct {
	rule    int64
	device  string
	subject string
}

type firing struct {
	dev     store.AlertDevice
	subject string
	details any
}

// alertTick opens and closes alerts and mails the recipients of the rules.
func (s *Server) alertTick(ctx context.Context) {
	now := s.now()
	if err := s.st.CloseAlertsOfDisabledRules(ctx, now); err != nil {
		s.log.Warn("alerts", "err", err)
		return
	}
	all, err := s.st.ListAlertRules(ctx)
	if err != nil {
		s.log.Warn("alert rules", "err", err)
		return
	}
	var rules []store.AlertRule
	var ids []int64
	for _, r := range all {
		if r.Enabled {
			rules = append(rules, r)
			ids = append(ids, r.ID)
		}
	}
	if len(rules) == 0 {
		return
	}
	devices, err := s.st.AlertDevices(ctx)
	if err != nil {
		s.log.Warn("alert devices", "err", err)
		return
	}
	openList, err := s.st.OpenAlertsOf(ctx, ids)
	if err != nil {
		s.log.Warn("open alerts", "err", err)
		return
	}
	open := map[alertKey]store.Alert{}
	for _, a := range openList {
		open[alertKey{a.RuleID, a.DeviceID, a.Subject}] = a
	}
	// After a restart the agents need a moment to reconnect; until then a
	// device's last contact counts from the start of the server.
	since := s.workersStarted
	var security map[string]store.SecurityAlertDevice
	for _, r := range rules {
		if isSecurityAlert(r.Kind) && security == nil {
			if security, err = s.st.SecurityAlertDevices(ctx); err != nil {
				s.log.Warn("alert security status", "err", err)
				return
			}
		}
	}
	for _, r := range rules {
		fire := map[alertKey]firing{}
		for _, d := range devices {
			if !ruleApplies(r, d) {
				continue
			}
			switch r.Kind {
			case "offline":
				seen := d.LastSeenAt
				if since.After(seen) {
					seen = since
				}
				if !s.hub.online(d.ID) && now.Sub(seen) >= time.Duration(r.Threshold)*time.Minute {
					fire[alertKey{r.ID, d.ID, ""}] = firing{d, "", offlineDetails{d.LastSeenAt}}
				}
			case "disk":
				for _, v := range d.Volumes {
					if v.SizeBytes < minVolumeBytes {
						continue
					}
					p := freePercent(v)
					k := alertKey{r.ID, d.ID, v.Mount}
					_, isOpen := open[k]
					if p < r.Threshold || (isOpen && p < r.Threshold+diskHysteresis) {
						fire[k] = firing{d, v.Mount, diskDetails{v.Mount, v.SizeBytes, v.FreeBytes, p}}
					}
				}
			case "threat", "antivirus", "firewall", "encryption":
				if sec, ok := security[d.ID]; ok {
					if det, bad := securityProblem(r.Kind, sec); bad {
						fire[alertKey{r.ID, d.ID, ""}] = firing{d, "", det}
					}
				}
			}
		}
		var opened, resolved []store.Alert
		for k, f := range fire {
			raw, _ := json.Marshal(f.details)
			if a, ok := open[k]; ok {
				if err := s.st.UpdateAlertDetails(ctx, a.ID, raw); err != nil {
					s.log.Warn("alert details", "err", err)
				}
				continue
			}
			a := store.Alert{RuleID: r.ID, RuleName: r.Name, DeviceID: f.dev.ID, Hostname: f.dev.Hostname, GroupID: f.dev.GroupID,
				GroupName: f.dev.GroupName, Kind: r.Kind, Subject: f.subject, Details: raw, OpenedAt: now}
			id, err := s.st.OpenAlert(ctx, a)
			if err != nil {
				s.log.Warn("open alert", "err", err)
				continue
			}
			if id != 0 {
				a.ID = id
				opened = append(opened, a)
			}
		}
		for k, a := range open {
			if k.rule != r.ID {
				continue
			}
			if _, ok := fire[k]; ok {
				continue
			}
			ok, err := s.st.ResolveAlert(ctx, a.ID, now)
			if err != nil {
				s.log.Warn("resolve alert", "err", err)
				continue
			}
			if ok {
				a.ResolvedAt = &now
				resolved = append(resolved, a)
			}
		}
		if len(opened) > 0 {
			s.log.Info("alerts opened", "rule", r.Name, "count", len(opened))
			s.mailAlerts(ctx, r, "alert", opened, devices, now)
		}
		if len(resolved) > 0 && r.NotifyResolved {
			s.mailAlerts(ctx, r, "alert_resolved", resolved, devices, now)
		}
	}
}

// alertText describes an alert in a mail or the PDF-free parts of the UI.
func alertText(lang string, a store.Alert, devices []store.AlertDevice, now time.Time, resolved bool) string {
	switch a.Kind {
	case "offline":
		if resolved {
			return reports.T(lang, "alert.back.offline")
		}
		var d offlineDetails
		_ = json.Unmarshal(a.Details, &d)
		return reports.T(lang, "alert.offline", reports.Duration(lang, now.Sub(d.LastSeenAt)), reports.FormatDateTime(lang, d.LastSeenAt))
	case "threat", "antivirus", "firewall", "encryption":
		if resolved {
			return reports.T(lang, "alert.back."+a.Kind)
		}
		var d securityDetails
		_ = json.Unmarshal(a.Details, &d)
		if a.Kind == "threat" {
			return reports.T(lang, "alert.threat", strings.Join(d.Threats, ", "))
		}
		return reports.T(lang, "alert."+a.Kind, reports.T(lang, d.Text))
	default:
		var d diskDetails
		_ = json.Unmarshal(a.Details, &d)
		if resolved {
			// The current free space, rather than the space when it was low.
			for _, dev := range devices {
				if dev.ID != a.DeviceID {
					continue
				}
				for _, v := range dev.Volumes {
					if v.Mount == a.Subject {
						return reports.T(lang, "alert.back.disk", a.Subject, freePercent(v))
					}
				}
			}
			return reports.T(lang, "alert.back.disk", a.Subject, d.Percent)
		}
		return reports.T(lang, "alert.disk", d.Mount, d.Percent, reports.Bytes(lang, d.FreeBytes), reports.Bytes(lang, d.SizeBytes))
	}
}

// mailAlerts queues one message per recipient with every alert of a tick.
func (s *Server) mailAlerts(ctx context.Context, r store.AlertRule, kind string, alerts []store.Alert, devices []store.AlertDevice, now time.Time) {
	if len(r.Recipients) == 0 {
		return
	}
	if cfg, err := s.loadMail(ctx); err != nil || !cfg.Enabled {
		return
	}
	slices.SortFunc(alerts, func(a, b store.Alert) int {
		return strings.Compare(strings.ToLower(a.Hostname+a.Subject), strings.ToLower(b.Hostname+b.Subject))
	})
	lang := reports.Lang(r.Language)
	tone := "danger"
	if kind == "alert_resolved" {
		tone = "success"
	}
	var facts []mail.Fact
	for _, a := range alerts {
		facts = append(facts, mail.Fact{Label: a.Hostname + " (" + a.GroupName + ")", Value: alertText(lang, a, devices, now, kind == "alert_resolved"),
			Tone: tone})
	}
	summary := alerts[0].Hostname
	if a := alerts[0]; a.Kind == "disk" {
		summary += " " + a.Subject
	}
	if len(alerts) > 1 {
		summary = fmt.Sprintf("%s (+%d)", summary, len(alerts)-1)
	}
	summary = r.Name + ", " + summary
	b := s.loadBranding(ctx)
	var g generalSettings
	_ = s.st.GetSetting(ctx, keyGeneral, &g)
	for _, to := range r.Recipients {
		msg := mail.RenderFacts(kind, lang, to, mailBrandOf(b, g), map[string]string{"name": r.Name, "summary": summary}, facts)
		if _, err := s.st.EnqueueMail(ctx, store.Mail{Kind: kind, To: to, Subject: msg.Subject, BodyText: msg.Text, BodyHTML: msg.HTML}); err != nil {
			s.log.Warn("alert mail", "err", err)
			return
		}
	}
}

// ---- handlers ----

func (s *Server) alertRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermAlertsManage, 0, h) }
	mux.Handle("GET /api/v1/alerts", view(s.handleListAlerts))
	mux.Handle("GET /api/v1/alerts/count", view(s.handleCountAlerts))
	mux.Handle("POST /api/v1/alerts/{id}/acknowledge", manage(s.handleAcknowledgeAlert))
	mux.Handle("GET /api/v1/alerts/rules", manage(s.handleListAlertRules))
	mux.Handle("POST /api/v1/alerts/rules", manage(s.handleCreateAlertRule))
	mux.Handle("PUT /api/v1/alerts/rules/{id}", manage(s.handleUpdateAlertRule))
	mux.Handle("DELETE /api/v1/alerts/rules/{id}", manage(s.handleDeleteAlertRule))
}

// alertView is an alert with its description in the user's language.
type alertView struct {
	store.Alert
	Text string `json:"text"`
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.AlertFilter{Open: q.Get("state") != "all", DeviceID: q.Get("device"), Limit: limit}
	list, err := s.st.ListAlerts(r.Context(), s.scope(r), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	lang := reports.Lang(u.Language)
	now := s.now()
	out := make([]alertView, 0, len(list))
	for _, a := range list {
		at := now
		if a.ResolvedAt != nil {
			at = *a.ResolvedAt
		}
		out = append(out, alertView{a, alertText(lang, a, nil, at, false)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
}

func (s *Server) handleCountAlerts(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountOpenAlerts(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"open": n})
}

func (s *Server) handleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	a, err := s.st.AlertByID(r.Context(), id)
	if err == nil && !s.scope(r).Allows(a.GroupID) {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if err := s.st.AcknowledgeAlert(r.Context(), id, u.Username, s.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "alert.acknowledge", a.Hostname, true, map[string]any{"rule": a.RuleName, "subject": a.Subject})
	a, _ = s.st.AlertByID(r.Context(), id)
	writeJSON(w, http.StatusOK, a)
}

// rulesAllowed: rules look at every device, so only users who see every
// group may change them.
func (s *Server) rulesAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.scope(r).All {
		apiError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

func (s *Server) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAllowed(w, r) {
		return
	}
	list, err := s.st.ListAlertRules(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": list})
}

// validAlertRule cleans a rule from the web interface and returns an error
// code when it is not valid.
func (s *Server) validAlertRule(ctx context.Context, r *store.AlertRule) string {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len([]rune(r.Name)) > 80 {
		return "invalid_name"
	}
	switch r.Kind {
	case "offline":
		if r.Threshold < 5 || r.Threshold > 30*24*60 {
			return "invalid_threshold"
		}
	case "disk":
		if r.Threshold < 1 || r.Threshold > 90 {
			return "invalid_threshold"
		}
	case "threat", "antivirus", "firewall", "encryption":
		r.Threshold = 0 // the security status decides
	default:
		return "invalid_kind"
	}
	for _, k := range r.DeviceKinds {
		if !slices.Contains(deviceKinds, k) {
			return "invalid_kind"
		}
	}
	var kinds []string
	for _, k := range deviceKinds { // fixed order
		if slices.Contains(r.DeviceKinds, k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == len(deviceKinds) {
		kinds = nil
	}
	r.DeviceKinds = kinds
	if r.Language != "nl" {
		r.Language = "en"
	}
	var rcpt []string
	for _, a := range r.Recipients {
		a = strings.TrimSpace(a)
		if a == "" || slices.Contains(rcpt, a) {
			continue
		}
		if !mail.ValidAddress(a) {
			return "invalid_email"
		}
		rcpt = append(rcpt, a)
	}
	if len(rcpt) > maxAlertRecipients {
		return "invalid_recipients"
	}
	r.Recipients = rcpt
	if len(r.GroupIDs) > 0 {
		groups, err := s.st.ListGroups(ctx, store.Scope{All: true})
		if err != nil {
			return "internal"
		}
		for _, id := range r.GroupIDs {
			if !slices.ContainsFunc(groups, func(g store.DeviceGroup) bool { return g.ID == id }) {
				return "invalid_group"
			}
		}
	}
	return ""
}

func (s *Server) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAllowed(w, r) {
		return
	}
	var req store.AlertRule
	if !decode(w, r, &req) {
		return
	}
	if code := s.validAlertRule(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	u, _ := currentUser(r)
	req.CreatedBy = u.Username
	created, err := s.st.CreateAlertRule(r.Context(), req)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "alert.rule_create", created.Name, true, map[string]any{"kind": created.Kind, "threshold": created.Threshold,
		"recipients": created.Recipients})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAllowed(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req store.AlertRule
	if !decode(w, r, &req) {
		return
	}
	req.ID = id
	if code := s.validAlertRule(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	err := s.st.UpdateAlertRule(r.Context(), req)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "name_taken")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "alert.rule_update", req.Name, true, map[string]any{"enabled": req.Enabled, "kind": req.Kind, "threshold": req.Threshold,
		"recipients": req.Recipients})
	got, _ := s.st.AlertRuleByID(r.Context(), id)
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAllowed(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	cur, err := s.st.AlertRuleByID(r.Context(), id)
	if err == nil {
		err = s.st.DeleteAlertRule(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "alert.rule_delete", cur.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}
