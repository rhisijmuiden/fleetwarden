package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ---- TOPdesk (incidents API) ----

type topdesk struct {
	c Config
	h *client
}

func (x *topdesk) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/tas/api/version", nil, nil)
}

func nameRef(v string) any {
	if v == "" {
		return nil
	}
	return map[string]string{"name": v}
}

func (x *topdesk) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	subject := x.c.Subject(t)
	if r := []rune(subject); len(r) > 80 { // TOPdesk's brief description is at most 80 characters
		subject = string(r[:79]) + "…"
	}
	body := map[string]any{
		"briefDescription": subject,
		// TOPdesk accepts a small set of HTML tags in the request text.
		"request": t.body(),
	}
	if email := orFallback(t.RequesterEmail, x.c); email != "" {
		body["callerLookup"] = map[string]string{"email": email}
	} else {
		body["caller"] = map[string]string{"dynamicName": orDefault(t.RequesterName, "FleetWarden")}
	}
	for k, v := range map[string]string{"callType": x.c.Setting("callType"), "entryType": x.c.Setting("entryType"),
		"category": orDefault(t.Category, x.c.Setting("category")), "priority": x.c.Priority(t.Urgency)} {
		if v != "" {
			body[k] = nameRef(v)
		}
	}
	if g := x.c.Setting("operatorGroup"); g != "" {
		body["operatorGroup"] = map[string]string{"name": g}
	}
	var out struct {
		ID     string `json:"id"`
		Number string `json:"number"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/tas/api/incidents", body, &out); err != nil {
		return Ref{}, err
	}
	return Ref{ID: out.ID, Number: out.Number, URL: x.h.base + "/tas/secure/incident?unid=" + url.QueryEscape(out.ID)}, nil
}

func (x *topdesk) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPatch, "/tas/api/incidents/id/"+url.PathEscape(ref.ID),
		map[string]any{"action": text, "actionInvisibleForCaller": true}, nil)
}

func (x *topdesk) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		Closed           bool `json:"closed"`
		ProcessingStatus struct {
			Name string `json:"name"`
		} `json:"processingStatus"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/tas/api/incidents/id/"+url.PathEscape(ref.ID), nil, &out); err != nil {
		return Status{}, err
	}
	return Status{State: out.ProcessingStatus.Name, Closed: out.Closed}, nil
}

// ---- Freshservice (API v2) ----

type freshservice struct {
	c Config
	h *client
}

func (x *freshservice) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/api/v2/tickets?per_page=1", nil, nil)
}

var freshStates = map[int]string{2: "Open", 3: "Pending", 4: "Resolved", 5: "Closed"}

func (x *freshservice) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	body := map[string]any{
		"subject":     x.c.Subject(t),
		"description": t.body(),
		"status":      2,
		"source":      2, // portal
		"tags":        []string{"fleetwarden"},
	}
	email := orFallback(t.RequesterEmail, x.c)
	if email == "" {
		return Ref{}, errors.New("no requester: the user has no e-mail address and no fallback requester is set")
	}
	body["email"] = email
	if p, err := strconv.Atoi(x.c.Priority(t.Urgency)); err == nil {
		body["priority"] = p
	}
	if g := x.c.Setting("groupId"); g != "" {
		body["group_id"] = num(g)
	}
	if w := x.c.Setting("workspaceId"); w != "" {
		body["workspace_id"] = num(w)
	}
	if c := orDefault(t.Category, x.c.Setting("category")); c != "" {
		body["category"] = c
	}
	var out struct {
		Ticket struct {
			ID any `json:"id"`
		} `json:"ticket"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/api/v2/tickets", body, &out); err != nil {
		return Ref{}, err
	}
	id := str(out.Ticket.ID)
	return Ref{ID: id, Number: "#" + id, URL: x.h.base + "/a/tickets/" + id}, nil
}

func (x *freshservice) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPost, "/api/v2/tickets/"+url.PathEscape(ref.ID)+"/notes",
		map[string]any{"body": "<pre>" + htmlEscape(text) + "</pre>", "private": true}, nil)
}

func (x *freshservice) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		Ticket struct {
			Status int `json:"status"`
		} `json:"ticket"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/api/v2/tickets/"+url.PathEscape(ref.ID), nil, &out); err != nil {
		return Status{}, err
	}
	st := freshStates[out.Ticket.Status]
	if st == "" {
		st = strconv.Itoa(out.Ticket.Status)
	}
	return Status{State: st, Closed: out.Ticket.Status == 4 || out.Ticket.Status == 5}, nil
}

// ---- Jira Service Management ----

type jsm struct {
	c Config
	h *client
}

func (x *jsm) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/rest/servicedeskapi/servicedesk/"+url.PathEscape(x.c.Setting("serviceDeskId")), nil, nil)
}

func (x *jsm) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	// JSM's description is plain text (wiki markup); the HTML report would show
	// as tags.
	fields := map[string]any{"summary": x.c.Subject(t), "description": t.Text}
	if p := x.c.Priority(t.Urgency); p != "" {
		fields["priority"] = map[string]string{"name": p}
	}
	body := map[string]any{
		"serviceDeskId":      x.c.Setting("serviceDeskId"),
		"requestTypeId":      x.c.Setting("requestTypeId"),
		"requestFieldValues": fields,
	}
	if email := orFallback(t.RequesterEmail, x.c); email != "" {
		body["raiseOnBehalfOf"] = email
	}
	var out struct {
		IssueID  string `json:"issueId"`
		IssueKey string `json:"issueKey"`
		Links    struct {
			Web string `json:"web"`
		} `json:"_links"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/rest/servicedeskapi/request", body, &out); err != nil {
		return Ref{}, err
	}
	return Ref{ID: out.IssueKey, Number: out.IssueKey, URL: orDefault(out.Links.Web, x.h.base+"/browse/"+out.IssueKey)}, nil
}

func (x *jsm) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPost, "/rest/servicedeskapi/request/"+url.PathEscape(ref.ID)+"/comment",
		map[string]any{"body": text, "public": false}, nil)
}

func (x *jsm) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		CurrentStatus struct {
			Status         string `json:"status"`
			StatusCategory string `json:"statusCategory"`
		} `json:"currentStatus"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/rest/servicedeskapi/request/"+url.PathEscape(ref.ID), nil, &out); err != nil {
		return Status{}, err
	}
	return Status{State: out.CurrentStatus.Status, Closed: out.CurrentStatus.StatusCategory == "DONE"}, nil
}

// ---- ServiceNow (Table API, incident) ----

type servicenow struct {
	c Config
	h *client
}

func (x *servicenow) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/api/now/table/incident?sysparm_limit=1&sysparm_fields=sys_id", nil, nil)
}

var snStates = map[string]string{"1": "New", "2": "In Progress", "3": "On Hold", "6": "Resolved", "7": "Closed", "8": "Canceled"}

func (x *servicenow) caller(ctx context.Context, email string) string {
	if email == "" {
		return ""
	}
	var out struct {
		Result []struct {
			SysID string `json:"sys_id"`
		} `json:"result"`
	}
	q := url.Values{"sysparm_query": {"email=" + email}, "sysparm_fields": {"sys_id"}, "sysparm_limit": {"1"}}
	if err := x.h.json(ctx, http.MethodGet, "/api/now/table/sys_user?"+q.Encode(), nil, &out); err != nil || len(out.Result) == 0 {
		return ""
	}
	return out.Result[0].SysID
}

func (x *servicenow) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	body := map[string]any{
		"short_description": x.c.Subject(t),
		"description":       t.Text,
		"contact_type":      "self-service",
	}
	if id := x.caller(ctx, orFallback(t.RequesterEmail, x.c)); id != "" {
		body["caller_id"] = id
	}
	if p := x.c.Priority(t.Urgency); p != "" {
		body["urgency"] = p
	}
	if g := x.c.Setting("assignmentGroup"); g != "" {
		body["assignment_group"] = g
	}
	if c := orDefault(t.Category, x.c.Setting("category")); c != "" {
		body["category"] = c
	}
	if t.DeviceURL != "" {
		body["work_notes"] = "FleetWarden: " + t.DeviceURL
	}
	var out struct {
		Result struct {
			SysID  string `json:"sys_id"`
			Number string `json:"number"`
		} `json:"result"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/api/now/table/incident", body, &out); err != nil {
		return Ref{}, err
	}
	return Ref{ID: out.Result.SysID, Number: out.Result.Number,
		URL: x.h.base + "/nav_to.do?uri=" + url.QueryEscape("incident.do?sys_id="+out.Result.SysID)}, nil
}

func (x *servicenow) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPatch, "/api/now/table/incident/"+url.PathEscape(ref.ID), map[string]any{"work_notes": text}, nil)
}

func (x *servicenow) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		Result struct {
			State string `json:"state"`
		} `json:"result"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/api/now/table/incident/"+url.PathEscape(ref.ID)+"?sysparm_fields=state", nil, &out); err != nil {
		return Status{}, err
	}
	st := orDefault(snStates[out.Result.State], out.Result.State)
	return Status{State: st, Closed: out.Result.State == "6" || out.Result.State == "7" || out.Result.State == "8"}, nil
}

// ---- Zendesk ----

type zendesk struct {
	c Config
	h *client
}

func (x *zendesk) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/api/v2/users/me.json", nil, nil)
}

func (x *zendesk) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	tk := map[string]any{
		"subject": x.c.Subject(t),
		"comment": map[string]any{"html_body": t.body()},
		"tags":    []string{"fleetwarden"},
	}
	if email := orFallback(t.RequesterEmail, x.c); email != "" {
		tk["requester"] = map[string]string{"email": email, "name": orDefault(t.RequesterName, email)}
	}
	if p := x.c.Priority(t.Urgency); p != "" {
		tk["priority"] = p
	}
	if g := x.c.Setting("groupId"); g != "" {
		tk["group_id"] = num(g)
	}
	var out struct {
		Ticket struct {
			ID any `json:"id"`
		} `json:"ticket"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/api/v2/tickets.json", map[string]any{"ticket": tk}, &out); err != nil {
		return Ref{}, err
	}
	id := str(out.Ticket.ID)
	return Ref{ID: id, Number: "#" + id, URL: x.h.base + "/agent/tickets/" + id}, nil
}

func (x *zendesk) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPut, "/api/v2/tickets/"+url.PathEscape(ref.ID)+".json",
		map[string]any{"ticket": map[string]any{"comment": map[string]any{"body": text, "public": false}}}, nil)
}

func (x *zendesk) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		Ticket struct {
			Status string `json:"status"`
		} `json:"ticket"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/api/v2/tickets/"+url.PathEscape(ref.ID)+".json", nil, &out); err != nil {
		return Status{}, err
	}
	return Status{State: out.Ticket.Status, Closed: out.Ticket.Status == "solved" || out.Ticket.Status == "closed"}, nil
}

// ---- HaloITSM ----

type halo struct {
	c Config
	h *client
}

func (x *halo) Test(ctx context.Context) error {
	_, err := x.h.token.get(ctx)
	return err
}

func (x *halo) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	tk := map[string]any{
		"summary":      x.c.Subject(t),
		"details_html": t.body(),
	}
	if email := orFallback(t.RequesterEmail, x.c); email != "" {
		tk["user_emailaddress"] = email
	}
	if v := x.c.Setting("ticketTypeId"); v != "" {
		tk["tickettype_id"] = num(v)
	}
	if v := x.c.Priority(t.Urgency); v != "" {
		tk["priority_id"] = num(v)
	}
	if v := x.c.Setting("team"); v != "" {
		tk["team"] = v
	}
	var out map[string]any
	if err := x.h.json(ctx, http.MethodPost, "/api/Tickets", []any{tk}, &out); err != nil {
		return Ref{}, err
	}
	id := str(out["id"])
	if id == "" {
		return Ref{}, errors.New("unexpected answer: no ticket id")
	}
	return Ref{ID: id, Number: id, URL: x.h.base + "/ticket?id=" + url.QueryEscape(id)}, nil
}

func (x *halo) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPost, "/api/Actions", []any{map[string]any{
		"ticket_id": num(ref.ID), "outcome": "Private Note", "note_html": "<pre>" + htmlEscape(text) + "</pre>", "hiddenfromuser": true,
	}}, nil)
}

func (x *halo) Status(ctx context.Context, ref Ref) (Status, error) {
	var out map[string]any
	if err := x.h.json(ctx, http.MethodGet, "/api/Tickets/"+url.PathEscape(ref.ID), nil, &out); err != nil {
		return Status{}, err
	}
	st := orDefault(str(out["status_name"]), str(out["status_id"]))
	// Status 9 is "Closed" in a default HaloITSM; the ticket also carries the
	// time it was closed when that applies.
	closed := str(out["status_id"]) == "9" || (str(out["dateclosed"]) != "" && !strings.HasPrefix(str(out["dateclosed"]), "1900"))
	return Status{State: st, Closed: closed}, nil
}

// ---- Zammad ----

type zammad struct {
	c Config
	h *client
}

func (x *zammad) Test(ctx context.Context) error {
	return x.h.json(ctx, http.MethodGet, "/api/v1/users/me", nil, nil)
}

func (x *zammad) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	email := orFallback(t.RequesterEmail, x.c)
	if email == "" {
		return Ref{}, errors.New("no customer: the user has no e-mail address and no fallback requester is set")
	}
	body := map[string]any{
		"title":       x.c.Subject(t),
		"group":       x.c.Setting("group"),
		"customer_id": "guess:" + email,
		"tags":        "fleetwarden",
		"article": map[string]any{
			"subject": x.c.Subject(t), "body": t.body(), "type": "note", "internal": false, "content_type": "text/html",
		},
	}
	if p := x.c.Priority(t.Urgency); p != "" {
		body["priority_id"] = num(p)
	}
	var out struct {
		ID     any    `json:"id"`
		Number string `json:"number"`
	}
	if err := x.h.json(ctx, http.MethodPost, "/api/v1/tickets", body, &out); err != nil {
		return Ref{}, err
	}
	id := str(out.ID)
	return Ref{ID: id, Number: orDefault(out.Number, id), URL: x.h.base + "/#ticket/zoom/" + id}, nil
}

func (x *zammad) AddNote(ctx context.Context, ref Ref, text string) error {
	return x.h.json(ctx, http.MethodPost, "/api/v1/ticket_articles", map[string]any{
		"ticket_id": num(ref.ID), "body": text, "type": "note", "internal": true, "content_type": "text/plain",
	}, nil)
}

func (x *zammad) Status(ctx context.Context, ref Ref) (Status, error) {
	var out struct {
		State string `json:"state"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/api/v1/tickets/"+url.PathEscape(ref.ID)+"?expand=true", nil, &out); err != nil {
		return Status{}, err
	}
	return Status{State: out.State, Closed: out.State == "closed" || out.State == "merged"}, nil
}

// ---- GLPI (REST API v1, apirest.php) ----

type glpi struct {
	c Config
	h *client
}

// session opens an API session and returns the request headers and a
// function that ends it.
func (x *glpi) session(ctx context.Context) (*client, func(), error) {
	hd := map[string]string{"Authorization": "user_token " + x.c.Secrets["userToken"]}
	if a := x.c.Secrets["appToken"]; a != "" {
		hd["App-Token"] = a
	}
	x.h.header = hd
	var out struct {
		SessionToken string `json:"session_token"`
	}
	if err := x.h.json(ctx, http.MethodGet, "/initSession", nil, &out); err != nil {
		return nil, nil, err
	}
	if out.SessionToken == "" {
		return nil, nil, errors.New("GLPI gave no session token")
	}
	s := &client{http: x.h.http, record: x.h.record, base: x.h.base, header: map[string]string{"Session-Token": out.SessionToken}}
	if a := x.c.Secrets["appToken"]; a != "" {
		s.header["App-Token"] = a
	}
	return s, func() { _ = s.json(context.WithoutCancel(ctx), http.MethodGet, "/killSession", nil, nil) }, nil
}

func (x *glpi) Test(ctx context.Context) error {
	_, end, err := x.session(ctx)
	if err == nil {
		end()
	}
	return err
}

var glpiStates = map[string]string{"1": "New", "2": "Assigned", "3": "Planned", "4": "Waiting", "5": "Solved", "6": "Closed"}

func (x *glpi) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	s, end, err := x.session(ctx)
	if err != nil {
		return Ref{}, err
	}
	defer end()
	in := map[string]any{"name": x.c.Subject(t), "content": t.body(), "type": 1}
	if p := x.c.Priority(t.Urgency); p != "" {
		in["urgency"] = num(p)
	}
	if c := x.c.Setting("categoryId"); c != "" {
		in["itilcategories_id"] = num(c)
	}
	if email := orFallback(t.RequesterEmail, x.c); email != "" {
		in["_users_id_requester_notif"] = map[string]any{"use_notification": 1, "alternative_email": []string{email}}
	}
	var out struct {
		ID any `json:"id"`
	}
	if err := s.json(ctx, http.MethodPost, "/Ticket", map[string]any{"input": in}, &out); err != nil {
		return Ref{}, err
	}
	id := str(out.ID)
	front := strings.TrimSuffix(x.h.base, "/apirest.php")
	return Ref{ID: id, Number: id, URL: front + "/front/ticket.form.php?id=" + url.QueryEscape(id)}, nil
}

func (x *glpi) AddNote(ctx context.Context, ref Ref, text string) error {
	s, end, err := x.session(ctx)
	if err != nil {
		return err
	}
	defer end()
	return s.json(ctx, http.MethodPost, "/ITILFollowup", map[string]any{"input": map[string]any{
		"itemtype": "Ticket", "items_id": num(ref.ID), "content": text, "is_private": 1,
	}}, nil)
}

func (x *glpi) Status(ctx context.Context, ref Ref) (Status, error) {
	s, end, err := x.session(ctx)
	if err != nil {
		return Status{}, err
	}
	defer end()
	var out struct {
		Status any `json:"status"`
	}
	if err := s.json(ctx, http.MethodGet, "/Ticket/"+url.PathEscape(ref.ID), nil, &out); err != nil {
		return Status{}, err
	}
	st := str(out.Status)
	return Status{State: orDefault(glpiStates[st], st), Closed: st == "5" || st == "6"}, nil
}

// ---- helpers ----

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orFallback(email string, c Config) string {
	return orDefault(strings.TrimSpace(email), c.Setting("fallbackRequester"))
}
