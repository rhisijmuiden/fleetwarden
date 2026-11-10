package server

// Integrations: service desk connectors and custom API calls. An
// administrator adds them under Administration > Integrations. Self-service
// tickets use one as their destination; alerts and new devices can trigger
// them. Every HTTP request is logged per integration for 30 days.

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
	"github.com/rhisijmuiden/fleetwarden/internal/integrations"
	"github.com/rhisijmuiden/fleetwarden/internal/reports"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	maxIntegrations      = 100
	integrationCallsDays = 30
	integrationWorkers   = 4
	// Events that trigger integrations.
	eventAlertOpened    = "alert.opened"
	eventDeviceEnrolled = "device.enrolled"
)

var integrationEvents = []string{eventAlertOpened, eventDeviceEnrolled}

func (s *Server) integrationRoutes(mux *http.ServeMux) {
	h := func(f http.HandlerFunc) http.Handler { return s.authed(auth.PermSettingsManage, 0, f) }
	mux.Handle("GET /api/v1/integrations/kinds", h(s.handleIntegrationKinds))
	mux.Handle("GET /api/v1/integrations", h(s.handleListIntegrations))
	mux.Handle("POST /api/v1/integrations", h(s.handleCreateIntegration))
	mux.Handle("POST /api/v1/integrations/test", h(s.handleTestIntegration))
	mux.Handle("GET /api/v1/integrations/{id}", h(s.handleGetIntegration))
	mux.Handle("PUT /api/v1/integrations/{id}", h(s.handleUpdateIntegration))
	mux.Handle("DELETE /api/v1/integrations/{id}", h(s.handleDeleteIntegration))
	mux.Handle("POST /api/v1/integrations/{id}/test-ticket", h(s.handleTestTicket))
	mux.Handle("GET /api/v1/integrations/{id}/calls", h(s.handleIntegrationCalls))
}

// integrationView is an integration without its secrets.
type integrationView struct {
	store.Integration
	SecretsSet map[string]bool `json:"secretsSet"`
	// ServiceDesk is false for a custom API.
	ServiceDesk bool `json:"serviceDesk"`
}

func integrationViewOf(i store.Integration) integrationView {
	v := integrationView{Integration: i, SecretsSet: map[string]bool{}}
	for k, val := range i.Secrets {
		v.SecretsSet[k] = val != ""
	}
	if info, ok := integrations.Info(integrations.Kind(i.Kind)); ok {
		v.ServiceDesk = info.ServiceDesk
	}
	if len(v.Custom) == 0 {
		v.Custom = json.RawMessage("null")
	}
	return v
}

func (s *Server) handleIntegrationKinds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": integrations.Kinds, "variables": integrations.Variables, "events": integrationEvents})
}

func (s *Server) handleListIntegrations(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListIntegrations(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]integrationView, 0, len(list))
	for _, i := range list {
		out = append(out, integrationViewOf(i))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadIntegration(w http.ResponseWriter, r *http.Request) (store.Integration, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.Integration{}, false
	}
	i, err := s.st.IntegrationByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return i, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return i, false
	}
	return i, true
}

func (s *Server) handleGetIntegration(w http.ResponseWriter, r *http.Request) {
	if i, ok := s.loadIntegration(w, r); ok {
		writeJSON(w, http.StatusOK, integrationViewOf(i))
	}
}

// integrationReq is the form. Empty secrets keep the stored value; a secret
// in Clear is removed.
type integrationReq struct {
	ID           int64                   `json:"id"`
	Name         string                  `json:"name"`
	Kind         string                  `json:"kind"`
	Enabled      bool                    `json:"enabled"`
	Settings     map[string]string       `json:"settings"`
	Secrets      map[string]string       `json:"secrets"`
	Clear        []string                `json:"clear"`
	Custom       *integrations.CustomAPI `json:"custom"`
	Events       []string                `json:"events"`
	AlertRuleIDs []int64                 `json:"alertRuleIds"`
}

// build merges a form with the stored integration (cur, with sealed secrets)
// and returns the integration to store (sealed) and its configuration (open).
func (s *Server) buildIntegration(ctx context.Context, req integrationReq, cur *store.Integration) (store.Integration, integrations.Config, string, error) {
	info, ok := integrations.Info(integrations.Kind(req.Kind))
	if cur != nil {
		info, ok = integrations.Info(integrations.Kind(cur.Kind)) // the kind never changes
	}
	if !ok {
		return store.Integration{}, integrations.Config{}, "unknown kind", nil
	}
	out := store.Integration{Name: strings.TrimSpace(req.Name), Kind: string(info.Kind), Enabled: req.Enabled,
		Settings: map[string]string{}, Secrets: map[string]string{}, Events: []string{}, AlertRuleIDs: []int64{}}
	if cur != nil {
		out.ID, out.CreatedBy = cur.ID, cur.CreatedBy
	}
	if out.Name == "" || len([]rune(out.Name)) > 100 {
		return out, integrations.Config{}, "name must be 1 to 100 characters", nil
	}
	cfg := integrations.Config{Kind: info.Kind, Settings: map[string]string{}, Secrets: map[string]string{}}
	secretKeys := []string{}
	for _, f := range info.Fields {
		if f.Type == "secret" {
			secretKeys = append(secretKeys, f.Key)
			continue
		}
		v := strings.TrimSpace(req.Settings[f.Key])
		if len(v) > 500 {
			return out, cfg, f.Key + " is too long", nil
		}
		if f.Type == "switch" && v != "" && v != "true" && v != "false" {
			return out, cfg, f.Key + " must be true or false", nil
		}
		if v != "" {
			out.Settings[f.Key] = v
			cfg.Settings[f.Key] = v
		}
	}
	if info.Kind == integrations.Custom {
		secretKeys = []string{"secret"}
		if req.Custom == nil {
			return out, cfg, "custom API settings are missing", nil
		}
		c := *req.Custom
		c.Normalize()
		cfg.Custom = &c
		raw, err := json.Marshal(c)
		if err != nil {
			return out, cfg, "", err
		}
		out.Custom = raw
	}
	for _, k := range secretKeys {
		v := req.Secrets[k]
		if len(v) > 4096 {
			return out, cfg, k + " is too long", nil
		}
		if v == "" && cur != nil && !slices.Contains(req.Clear, k) {
			old, err := s.box.Open(cur.Secrets[k])
			if err != nil {
				return out, cfg, "", err
			}
			v = old
		}
		if v == "" {
			continue
		}
		cfg.Secrets[k] = v
		sealed, err := s.box.Seal(v)
		if err != nil {
			return out, cfg, "", err
		}
		out.Secrets[k] = sealed
	}
	for _, e := range req.Events {
		if !slices.Contains(integrationEvents, e) {
			return out, cfg, "unknown event " + e, nil
		}
		if e == eventDeviceEnrolled && info.ServiceDesk {
			return out, cfg, "a service desk cannot react to new devices", nil
		}
		if !slices.Contains(out.Events, e) {
			out.Events = append(out.Events, e)
		}
	}
	if len(req.AlertRuleIDs) > 0 {
		rules, err := s.st.ListAlertRules(ctx)
		if err != nil {
			return out, cfg, "", err
		}
		for _, id := range req.AlertRuleIDs {
			if !slices.ContainsFunc(rules, func(r store.AlertRule) bool { return r.ID == id }) {
				return out, cfg, "unknown alert rule " + strconv.FormatInt(id, 10), nil
			}
			if !slices.Contains(out.AlertRuleIDs, id) {
				out.AlertRuleIDs = append(out.AlertRuleIDs, id)
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return out, cfg, err.Error(), nil
	}
	return out, cfg, "", nil
}

func integrationAudit(i store.Integration, secretChanged bool) map[string]any {
	return map[string]any{"name": i.Name, "kind": i.Kind, "enabled": i.Enabled, "settings": i.Settings, "events": i.Events,
		"alertRuleIds": i.AlertRuleIDs, "secretChanged": secretChanged}
}

func secretsGiven(req integrationReq) bool {
	for _, v := range req.Secrets {
		if v != "" {
			return true
		}
	}
	return len(req.Clear) > 0
}

func (s *Server) handleCreateIntegration(w http.ResponseWriter, r *http.Request) {
	var req integrationReq
	if !decode(w, r, &req) {
		return
	}
	list, err := s.st.ListIntegrations(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(list) >= maxIntegrations {
		apiError(w, http.StatusConflict, "too_many_integrations")
		return
	}
	i, _, msg, err := s.buildIntegration(r.Context(), req, nil)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg != "" {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_integration", msg)
		return
	}
	u, _ := currentUser(r)
	i.CreatedBy = u.Username
	id, err := s.st.CreateIntegration(r.Context(), i)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "integration.create", i.Name, true, integrationAudit(i, secretsGiven(req)))
	saved, err := s.st.IntegrationByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, integrationViewOf(saved))
}

func (s *Server) handleUpdateIntegration(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.loadIntegration(w, r)
	if !ok {
		return
	}
	var req integrationReq
	if !decode(w, r, &req) {
		return
	}
	i, _, msg, err := s.buildIntegration(r.Context(), req, &cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg != "" {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_integration", msg)
		return
	}
	if err := s.st.UpdateIntegration(r.Context(), i); errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "integration.update", i.Name, true, integrationAudit(i, secretsGiven(req)))
	saved, err := s.st.IntegrationByID(r.Context(), i.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, integrationViewOf(saved))
}

func (s *Server) handleDeleteIntegration(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.loadIntegration(w, r)
	if !ok {
		return
	}
	if used := s.integrationInUse(r.Context(), cur.ID); used != "" {
		apiErrorDetail(w, http.StatusConflict, "integration_in_use", used)
		return
	}
	if err := s.st.DeleteIntegration(r.Context(), cur.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "integration.delete", cur.Name, true, map[string]any{"kind": cur.Kind})
	w.WriteHeader(http.StatusNoContent)
}

// integrationInUse names what still uses an integration ("" for nothing).
func (s *Server) integrationInUse(ctx context.Context, id int64) string {
	set := s.loadSelfService(ctx)
	if set.TicketIntegration == id {
		return "selfservice"
	}
	return ""
}

// ---- running ----

// callLog collects the requests of one operation.
type callLog struct {
	calls []integrations.Call
}

func (l *callLog) options() integrations.Options {
	return integrations.Options{Record: func(c integrations.Call) { l.calls = append(l.calls, c) }}
}

type callView struct {
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	OK         bool   `json:"ok"`
	DurationMS int    `json:"durationMs"`
	Error      string `json:"error"`
}

func (l *callLog) view() []callView {
	out := []callView{}
	for _, c := range l.calls {
		v := callView{Method: c.Method, URL: c.URL, Status: c.Status, OK: c.Err == nil, DurationMS: int(c.Duration.Milliseconds())}
		if c.Err != nil {
			v.Error = callErrText(c.Err)
		}
		out = append(out, v)
	}
	return out
}

func callErrText(err error) string {
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}

// save writes the calls to the log; ref goes with the last one. A failure
// without any request (a bad template, no requester) is logged too.
func (s *Server) saveCalls(ctx context.Context, id int64, event string, l *callLog, ref integrations.Ref, opErr error) {
	if id == 0 {
		return
	}
	calls := l.calls
	if len(calls) == 0 && opErr != nil {
		calls = []integrations.Call{{Err: opErr}}
	}
	for n, c := range calls {
		row := store.IntegrationCall{IntegrationID: id, Event: event, Method: c.Method, URL: c.URL, Status: c.Status, OK: c.Err == nil,
			DurationMS: int(c.Duration.Milliseconds())}
		if c.Err != nil {
			row.Error = callErrText(c.Err)
		}
		if n == len(calls)-1 {
			row.TicketNumber, row.TicketURL = ref.Number, ref.URL
			if opErr != nil && row.OK {
				row.OK, row.Error = false, callErrText(opErr)
			}
		}
		if err := s.st.RecordIntegrationCall(ctx, row); err != nil {
			s.log.Warn("integration call log", "err", err)
			return
		}
	}
}

// integrationConfig opens a stored integration.
func (s *Server) integrationConfig(i store.Integration) (integrations.Config, error) {
	cfg := integrations.Config{Kind: integrations.Kind(i.Kind), Settings: i.Settings, Secrets: map[string]string{}}
	for k, v := range i.Secrets {
		open, err := s.box.Open(v)
		if err != nil {
			return cfg, err
		}
		cfg.Secrets[k] = open
	}
	if len(i.Custom) > 0 && string(i.Custom) != "null" {
		var c integrations.CustomAPI
		if err := json.Unmarshal(i.Custom, &c); err != nil {
			return cfg, err
		}
		cfg.Custom = &c
	}
	return cfg, nil
}

type integrationResult struct {
	OK     bool              `json:"ok"`
	Error  string            `json:"error"`
	Calls  []callView        `json:"calls"`
	Ticket integrations.Ref  `json:"ticket"`
	Answer string            `json:"answer,omitempty"`
	Vars   map[string]string `json:"-"`
}

// handleTestIntegration tests the form (with the stored secrets of ID when
// they are not filled in), without saving it.
func (s *Server) handleTestIntegration(w http.ResponseWriter, r *http.Request) {
	var req integrationReq
	if !decode(w, r, &req) {
		return
	}
	var cur *store.Integration
	if req.ID != 0 {
		i, err := s.st.IntegrationByID(r.Context(), req.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.internalError(w, r, err)
			return
		}
		if err == nil {
			cur = &i
		}
	}
	if req.Name == "" {
		req.Name = "test"
	}
	_, cfg, msg, err := s.buildIntegration(r.Context(), req, cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg != "" {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_integration", msg)
		return
	}
	var l callLog
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res := integrationResult{}
	conn, err := integrations.New(cfg, l.options())
	if err == nil {
		err = conn.Test(ctx)
	}
	res.OK, res.Calls = err == nil, l.view()
	if err != nil {
		res.Error = callErrText(err)
	}
	if cur != nil {
		s.saveCalls(r.Context(), cur.ID, "test", &l, integrations.Ref{}, err)
	}
	s.audit(r, "integration.test", req.Name, res.OK, map[string]any{"kind": cfg.Kind, "error": res.Error})
	writeJSON(w, http.StatusOK, res)
}

// handleTestTicket creates a test ticket with the saved settings.
func (s *Server) handleTestTicket(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.loadIntegration(w, r)
	if !ok {
		return
	}
	cfg, err := s.integrationConfig(cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	vars := integrations.SampleVars()
	t := integrations.Ticket{
		Subject:        ssText(u.Language, "testTicket"),
		Text:           ssText(u.Language, "testTicketBody"),
		RequesterEmail: u.Email, RequesterName: u.DisplayName, Urgency: "low",
		Hostname: "TEST-PC", DeviceURL: s.baseURL(r) + "/admin/integrations", Vars: vars,
	}
	t.HTML = "<p>" + htmlText(t.Text) + "</p>"
	var l callLog
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res := integrationResult{}
	conn, err := integrations.New(cfg, l.options())
	var ref integrations.Ref
	if err == nil {
		ref, err = conn.CreateTicket(ctx, t)
	}
	res.OK, res.Calls, res.Ticket = err == nil, l.view(), ref
	if err != nil {
		res.Error = callErrText(err)
	}
	s.saveCalls(r.Context(), cur.ID, "test_ticket", &l, ref, err)
	s.audit(r, "integration.test_ticket", cur.Name, res.OK, map[string]any{"ticket": ref.Number, "error": res.Error})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleIntegrationCalls(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.loadIntegration(w, r)
	if !ok {
		return
	}
	list, err := s.st.ListIntegrationCalls(r.Context(), cur.ID, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---- events ----

// retryable reports whether a failed call may succeed later.
func retryable(err error) bool {
	var he *integrations.HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	return !errors.Is(err, integrations.ErrUnsupported)
}

var integrationBackoff = []time.Duration{10 * time.Second, time.Minute}

// runIntegration runs op in the background with retries, one at a time per
// call and at most integrationWorkers at once.
func (s *Server) runIntegration(i store.Integration, event string, op func(ctx context.Context, conn integrations.Connector) (integrations.Ref, error)) {
	s.runIntegrationDone(i, event, op, nil)
}

// runIntegrationDone is runIntegration with a callback that receives the
// outcome of the last attempt exactly once (also when the server stops).
func (s *Server) runIntegrationDone(i store.Integration, event string, op func(ctx context.Context, conn integrations.Connector) (integrations.Ref, error), done func(integrations.Ref, error)) {
	if done == nil {
		done = func(integrations.Ref, error) {}
	}
	if s.bgCtx == nil {
		done(integrations.Ref{}, errors.New("server is stopping"))
		return
	}
	s.intOnce.Do(func() { s.intSem = make(chan struct{}, integrationWorkers) })
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		var ref integrations.Ref
		var err error
		defer func() { done(ref, err) }()
		cfg, cerr := s.integrationConfig(i)
		if cerr != nil {
			err = cerr
			s.log.Warn("integration secrets", "integration", i.Name, "err", err)
			return
		}
		for attempt := 0; ; attempt++ {
			select {
			case s.intSem <- struct{}{}:
			case <-s.bgCtx.Done():
				if err == nil {
					err = s.bgCtx.Err()
				}
				return
			}
			var l callLog
			ctx, cancel := context.WithTimeout(s.bgCtx, 2*time.Minute)
			var conn integrations.Connector
			ref = integrations.Ref{}
			conn, err = integrations.New(cfg, l.options())
			if err == nil {
				ref, err = op(ctx, conn)
			}
			cancel()
			<-s.intSem
			s.saveCalls(context.WithoutCancel(s.bgCtx), i.ID, event, &l, ref, err)
			if err == nil {
				return
			}
			s.log.Warn("integration call failed", "integration", i.Name, "event", event, "attempt", attempt+1, "err", err)
			if attempt >= len(integrationBackoff) || !retryable(err) {
				return
			}
			select {
			case <-time.After(integrationBackoff[attempt]):
			case <-s.bgCtx.Done():
				return
			}
		}
	}()
}

// integrationsFor returns the enabled integrations that react to event.
func (s *Server) integrationsFor(ctx context.Context, event string) []store.Integration {
	list, err := s.st.ListIntegrations(ctx)
	if err != nil {
		s.log.Warn("integrations", "err", err)
		return nil
	}
	var out []store.Integration
	for _, i := range list {
		if i.Enabled && slices.Contains(i.Events, event) {
			out = append(out, i)
		}
	}
	return out
}

// publicURL is the configured public URL ("" when it is not set).
func (s *Server) publicURL(ctx context.Context) string {
	var g generalSettings
	if err := s.st.GetSetting(ctx, keyGeneral, &g); err == nil {
		return strings.TrimRight(g.PublicURL, "/")
	}
	return ""
}

func deviceLink(base, id string) string {
	if base == "" {
		return ""
	}
	return base + "/devices/" + id
}

// integrationAlerts sends newly opened alerts to the integrations that
// react to them: a ticket in a service desk, a call for a custom API.
func (s *Server) integrationAlerts(ctx context.Context, r store.AlertRule, opened []store.Alert, devices []store.AlertDevice, now time.Time) {
	list := s.integrationsFor(ctx, eventAlertOpened)
	if len(list) == 0 {
		return
	}
	base := s.publicURL(ctx)
	lang := reports.Lang(r.Language)
	for _, i := range list {
		if len(i.AlertRuleIDs) > 0 && !slices.Contains(i.AlertRuleIDs, r.ID) {
			continue
		}
		for _, a := range opened {
			text := alertText(lang, a, devices, now, false)
			subject := r.Name
			if a.Subject != "" {
				subject += " (" + a.Subject + ")"
			}
			link := deviceLink(base, a.DeviceID)
			vars := map[string]string{"event": eventAlertOpened, "time": now.UTC().Format(time.RFC3339),
				"device.id": a.DeviceID, "device.hostname": a.Hostname, "device.url": link, "device.group": a.GroupName,
				"alert.title": subject, "alert.rule": r.Name, "alert.kind": a.Kind, "alert.subject": a.Subject,
				"ticket.subject": subject, "ticket.description": text, "ticket.urgency": "normal",
				"report.text": text, "report.html": "<p>" + htmlText(text) + "</p>"}
			body := text
			if link != "" {
				body += "\n\nFleetWarden: " + link
			}
			t := integrations.Ticket{Subject: subject, Text: body, HTML: "<p>" + htmlText(text) + "</p>" + linkHTML(link),
				Urgency: "normal", Hostname: a.Hostname, DeviceURL: link, Vars: vars}
			s.runIntegration(i, eventAlertOpened, func(ctx context.Context, c integrations.Connector) (integrations.Ref, error) {
				return c.CreateTicket(ctx, t)
			})
		}
	}
}

// integrationEnrolled calls the integrations that react to a new device.
func (s *Server) integrationEnrolled(ctx context.Context, d store.Device, group string) {
	list := s.integrationsFor(ctx, eventDeviceEnrolled)
	if len(list) == 0 {
		return
	}
	link := deviceLink(s.publicURL(ctx), d.ID)
	vars := map[string]string{"event": eventDeviceEnrolled, "time": time.Now().UTC().Format(time.RFC3339),
		"device.id": d.ID, "device.hostname": d.Hostname, "device.url": link, "device.os": d.Platform, "device.group": group}
	for _, i := range list {
		s.runIntegration(i, eventDeviceEnrolled, func(ctx context.Context, c integrations.Connector) (integrations.Ref, error) {
			t := integrations.Ticket{Subject: d.Hostname, Hostname: d.Hostname, DeviceURL: link, Vars: vars}
			return c.CreateTicket(ctx, t)
		})
	}
}

func linkHTML(link string) string {
	if link == "" {
		return ""
	}
	return fmt.Sprintf(`<p><a href="%s">FleetWarden</a></p>`, htmlText(link))
}

func htmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;", "\n", "<br>").Replace(s)
}
