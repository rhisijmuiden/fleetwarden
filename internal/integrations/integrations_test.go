package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fake records requests and answers by "METHOD path".
type fake struct {
	mu      sync.Mutex
	reqs    []*http.Request
	bodies  map[string]string
	answers map[string]string
}

func newFake(answers map[string]string) (*fake, *httptest.Server) {
	f := &fake{answers: answers, bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		f.bodies[key] = string(b)
		f.mu.Unlock()
		a, ok := f.answers[key]
		if !ok {
			http.Error(w, `{"error":"unexpected"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, a)
	}))
	return f, srv
}

func (f *fake) body(t *testing.T, key string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.bodies[key]
	if !ok {
		t.Fatalf("no request %s; got %v", key, f.bodies)
	}
	var v map[string]any
	if strings.HasPrefix(strings.TrimSpace(raw), "[") {
		var a []map[string]any
		if err := json.Unmarshal([]byte(raw), &a); err != nil || len(a) != 1 {
			t.Fatalf("body of %s: %v %s", key, err, raw)
		}
		return a[0]
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("body of %s: %v %s", key, err, raw)
	}
	return v
}

var ticket = Ticket{Subject: "Outlook crashes", Text: "It crashes.", HTML: "<p>It crashes.</p>", RequesterEmail: "karin@acme.nl",
	RequesterName: "Karin", Urgency: "high", Hostname: "LT-0142", DeviceURL: "https://fw.example/devices/x"}

func TestServiceDesks(t *testing.T) {
	cases := []struct {
		kind     Kind
		settings map[string]string
		secrets  map[string]string
		answers  map[string]string
		create   string
		check    func(t *testing.T, b map[string]any)
		ref      Ref
		note     string
		status   string
		closed   bool
	}{
		{
			kind: TOPdesk, settings: map[string]string{"username": "op", "priorityHigh": "P1", "operatorGroup": "Werkplek"},
			secrets: map[string]string{"password": "pw"},
			answers: map[string]string{"GET /tas/api/version": `{"version":"3"}`, "POST /tas/api/incidents": `{"id":"u-1","number":"I 2411 001"}`,
				"PATCH /tas/api/incidents/id/u-1": `{}`, "GET /tas/api/incidents/id/u-1": `{"closed":true,"processingStatus":{"name":"Afgemeld"}}`},
			create: "POST /tas/api/incidents",
			check: func(t *testing.T, b map[string]any) {
				if b["briefDescription"] != "[LT-0142] Outlook crashes" || b["callerLookup"].(map[string]any)["email"] != "karin@acme.nl" ||
					b["priority"].(map[string]any)["name"] != "P1" || b["operatorGroup"].(map[string]any)["name"] != "Werkplek" {
					t.Errorf("topdesk body %v", b)
				}
			},
			ref: Ref{ID: "u-1", Number: "I 2411 001"}, note: "PATCH /tas/api/incidents/id/u-1", status: "Afgemeld", closed: true,
		},
		{
			kind: Freshservice, settings: map[string]string{"groupId": "42"}, secrets: map[string]string{"apiKey": "k"},
			answers: map[string]string{"GET /api/v2/tickets": `{"tickets":[]}`, "POST /api/v2/tickets": `{"ticket":{"id":4821}}`,
				"POST /api/v2/tickets/4821/notes": `{}`, "GET /api/v2/tickets/4821": `{"ticket":{"status":3}}`},
			create: "POST /api/v2/tickets",
			check: func(t *testing.T, b map[string]any) {
				if b["email"] != "karin@acme.nl" || b["priority"] != float64(3) || b["group_id"] != float64(42) || b["subject"] != "[LT-0142] Outlook crashes" {
					t.Errorf("freshservice body %v", b)
				}
			},
			ref: Ref{ID: "4821", Number: "#4821"}, note: "POST /api/v2/tickets/4821/notes", status: "Pending",
		},
		{
			kind: JSM, settings: map[string]string{"email": "a@acme.nl", "serviceDeskId": "1", "requestTypeId": "7"}, secrets: map[string]string{"apiToken": "t"},
			answers: map[string]string{"GET /rest/servicedeskapi/servicedesk/1": `{}`, "POST /rest/servicedeskapi/request": `{"issueId":"10","issueKey":"IT-5","_links":{"web":"https://x/IT-5"}}`,
				"POST /rest/servicedeskapi/request/IT-5/comment": `{}`, "GET /rest/servicedeskapi/request/IT-5": `{"currentStatus":{"status":"Done","statusCategory":"DONE"}}`},
			create: "POST /rest/servicedeskapi/request",
			check: func(t *testing.T, b map[string]any) {
				if b["raiseOnBehalfOf"] != "karin@acme.nl" || b["requestTypeId"] != "7" || b["requestFieldValues"].(map[string]any)["description"] != "It crashes." {
					t.Errorf("jsm body %v", b)
				}
			},
			ref: Ref{ID: "IT-5", Number: "IT-5", URL: "https://x/IT-5"}, note: "POST /rest/servicedeskapi/request/IT-5/comment", status: "Done", closed: true,
		},
		{
			kind: ServiceNow, settings: map[string]string{"username": "u"}, secrets: map[string]string{"password": "p"},
			answers: map[string]string{"GET /api/now/table/incident": `{"result":[]}`, "GET /api/now/table/sys_user": `{"result":[{"sys_id":"usr1"}]}`,
				"POST /api/now/table/incident":     `{"result":{"sys_id":"s1","number":"INC0010001"}}`,
				"PATCH /api/now/table/incident/s1": `{}`, "GET /api/now/table/incident/s1": `{"result":{"state":"2"}}`},
			create: "POST /api/now/table/incident",
			check: func(t *testing.T, b map[string]any) {
				if b["caller_id"] != "usr1" || b["urgency"] != "1" {
					t.Errorf("servicenow body %v", b)
				}
			},
			ref: Ref{ID: "s1", Number: "INC0010001"}, note: "PATCH /api/now/table/incident/s1", status: "In Progress",
		},
		{
			kind: Zendesk, settings: map[string]string{"email": "a@acme.nl"}, secrets: map[string]string{"apiToken": "t"},
			answers: map[string]string{"GET /api/v2/users/me.json": `{}`, "POST /api/v2/tickets.json": `{"ticket":{"id":77}}`,
				"PUT /api/v2/tickets/77.json": `{}`, "GET /api/v2/tickets/77.json": `{"ticket":{"status":"solved"}}`},
			create: "POST /api/v2/tickets.json",
			check: func(t *testing.T, b map[string]any) {
				tk := b["ticket"].(map[string]any)
				if tk["priority"] != "high" || tk["requester"].(map[string]any)["email"] != "karin@acme.nl" {
					t.Errorf("zendesk body %v", b)
				}
			},
			ref: Ref{ID: "77", Number: "#77"}, note: "PUT /api/v2/tickets/77.json", status: "solved", closed: true,
		},
		{
			kind: Halo, settings: map[string]string{"clientId": "c"}, secrets: map[string]string{"clientSecret": "s"},
			answers: map[string]string{"POST /auth/token": `{"access_token":"tok","expires_in":3600}`, "POST /api/Tickets": `{"id":123}`,
				"POST /api/Actions": `{}`, "GET /api/Tickets/123": `{"status_id":2,"status_name":"In Progress"}`},
			create: "POST /api/Tickets",
			check: func(t *testing.T, b map[string]any) {
				if b["priority_id"] != float64(2) || b["user_emailaddress"] != "karin@acme.nl" {
					t.Errorf("halo body %v", b)
				}
			},
			ref: Ref{ID: "123", Number: "123"}, note: "POST /api/Actions", status: "In Progress",
		},
		{
			kind: Zammad, secrets: map[string]string{"token": "t"},
			answers: map[string]string{"GET /api/v1/users/me": `{}`, "POST /api/v1/tickets": `{"id":9,"number":"31009"}`,
				"POST /api/v1/ticket_articles": `{}`, "GET /api/v1/tickets/9": `{"state":"closed"}`},
			create: "POST /api/v1/tickets",
			check: func(t *testing.T, b map[string]any) {
				if b["customer_id"] != "guess:karin@acme.nl" || b["group"] != "Users" || b["priority_id"] != float64(3) {
					t.Errorf("zammad body %v", b)
				}
			},
			ref: Ref{ID: "9", Number: "31009"}, note: "POST /api/v1/ticket_articles", status: "closed", closed: true,
		},
		{
			kind: GLPI, secrets: map[string]string{"userToken": "u", "appToken": "a"},
			answers: map[string]string{"GET /initSession": `{"session_token":"sess"}`, "GET /killSession": `{}`, "POST /Ticket": `{"id":5}`,
				"POST /ITILFollowup": `{}`, "GET /Ticket/5": `{"status":2}`},
			create: "POST /Ticket",
			check: func(t *testing.T, b map[string]any) {
				in := b["input"].(map[string]any)
				if in["urgency"] != float64(4) || in["name"] != "[LT-0142] Outlook crashes" {
					t.Errorf("glpi body %v", b)
				}
			},
			ref: Ref{ID: "5", Number: "5"}, note: "POST /ITILFollowup", status: "Assigned",
		},
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			f, srv := newFake(c.answers)
			defer srv.Close()
			settings := map[string]string{"url": srv.URL}
			for k, v := range c.settings {
				settings[k] = v
			}
			var calls []Call
			conn, err := New(Config{Kind: c.kind, Settings: settings, Secrets: c.secrets}, Options{Record: func(cl Call) { calls = append(calls, cl) }})
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Test(ctx); err != nil {
				t.Fatalf("test: %v", err)
			}
			ref, err := conn.CreateTicket(ctx, ticket)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if ref.ID != c.ref.ID || ref.Number != c.ref.Number || (c.ref.URL != "" && ref.URL != c.ref.URL) || ref.URL == "" {
				t.Errorf("ref = %+v, want %+v", ref, c.ref)
			}
			c.check(t, f.body(t, c.create))
			if err := conn.AddNote(ctx, ref, "Chat started by Mark"); err != nil {
				t.Fatalf("note: %v", err)
			}
			f.body(t, c.note)
			st, err := conn.Status(ctx, ref)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if st.State != c.status || st.Closed != c.closed {
				t.Errorf("status = %+v, want %s closed=%v", st, c.status, c.closed)
			}
			if len(calls) < 4 {
				t.Errorf("recorded %d calls", len(calls))
			}
		})
	}
}

func TestAuthHeaders(t *testing.T) {
	_, srv := newFake(map[string]string{"GET /api/v2/tickets": `{}`})
	defer srv.Close()
	var got string
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "{}")
	})
	conn, _ := New(Config{Kind: Freshservice, Settings: map[string]string{"url": srv.URL}, Secrets: map[string]string{"apiKey": "key"}}, Options{})
	if err := conn.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "Basic a2V5Olg=" { // key:X
		t.Errorf("auth = %q", got)
	}
}

func TestErrorsAndValidation(t *testing.T) {
	if err := (Config{Kind: Freshservice, Settings: map[string]string{"url": "https://x"}}).Validate(); err == nil {
		t.Error("missing API key accepted")
	}
	if err := (Config{Kind: Freshservice, Settings: map[string]string{"url": "ftp://x"}, Secrets: map[string]string{"apiKey": "k"}}).Validate(); err == nil {
		t.Error("ftp URL accepted")
	}
	if err := (Config{Kind: "nope"}).Validate(); err == nil {
		t.Error("unknown kind accepted")
	}
	_, srv := newFake(map[string]string{})
	defer srv.Close()
	conn, _ := New(Config{Kind: Zendesk, Settings: map[string]string{"url": srv.URL, "email": "a@b.c"}, Secrets: map[string]string{"apiToken": "t"}}, Options{})
	err := conn.Test(context.Background())
	var he *HTTPError
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	if e, ok := err.(*HTTPError); !ok || e.Status != 404 {
		t.Errorf("not an HTTPError: %T", err)
	}
	_ = he
	// No requester anywhere: Freshservice refuses before calling.
	conn, _ = New(Config{Kind: Freshservice, Settings: map[string]string{"url": srv.URL}, Secrets: map[string]string{"apiKey": "k"}}, Options{})
	if _, err := conn.CreateTicket(context.Background(), Ticket{Subject: "x"}); err == nil || !strings.Contains(err.Error(), "requester") {
		t.Errorf("err = %v", err)
	}
}

func TestRender(t *testing.T) {
	vars := map[string]string{"a": `say "hi"` + "\n", "b": "x&y"}
	if got := Render(`{"t":"{{a}}","q":{{ a | json }}}`, vars, EscapeJSON); got != `{"t":"say \"hi\"\n","q":"say \"hi\"\n"}` {
		t.Errorf("json = %s", got)
	}
	if got := Render("https://x/?q={{b}}&r={{ b | raw }}", vars, EscapeQuery); got != "https://x/?q=x%26y&r=x&y" {
		t.Errorf("query = %s", got)
	}
	if got := Render("<v>{{b}}</v>{{missing}}", vars, EscapeXML); got != "<v>x&amp;y</v>" {
		t.Errorf("xml = %s", got)
	}
}

func TestJSONPath(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"ticket":{"number":"HD-1","list":[{"id":4}]}}`), &doc)
	if str(JSONPath(doc, "$.ticket.number")) != "HD-1" || str(JSONPath(doc, "ticket.list[0].id")) != "4" || JSONPath(doc, "$.nope.x") != nil {
		t.Error("JSONPath")
	}
}

func TestCustomAPI(t *testing.T) {
	var gotBody, gotSig, gotKey, gotCT, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotSig, gotKey, gotCT, gotPath = string(b), r.Header.Get("X-FleetWarden-Signature"), r.Header.Get("X-Source"), r.Header.Get("Content-Type"), r.URL.RawQuery
		_, _ = io.WriteString(w, `{"ticket":{"number":"HD-20931","link":"https://hd/20931"}}`)
	}))
	defer srv.Close()
	api := &CustomAPI{Method: "post", URL: srv.URL + "/tickets?host={{device.hostname}}", Auth: "hmac",
		Headers: []Header{{Name: "X-Source", Value: "fw-{{event}}"}},
		Body:    `{"title":"{{ticket.subject}}","html":{{report.html | json}}}`, NumberPath: "$.ticket.number", URLPath: "ticket.link"}
	c := Config{Kind: Custom, Custom: api, Secrets: map[string]string{"secret": "s3cret"}}
	conn, err := New(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := conn.CreateTicket(context.Background(), Ticket{Subject: `A "quote"`, HTML: "<p>x</p>", Hostname: "PC 1", Vars: map[string]string{"event": "ticket"}})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Number != "HD-20931" || ref.URL != "https://hd/20931" {
		t.Errorf("ref = %+v", ref)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil || sent["title"] != `A "quote"` || sent["html"] != "<p>x</p>" {
		t.Errorf("body = %q", gotBody)
	}
	if !strings.HasPrefix(gotSig, "sha256=") || gotKey != "fw-ticket" || gotCT != "application/json" || gotPath != "host=PC+1" {
		t.Errorf("sig=%q key=%q ct=%q query=%q", gotSig, gotKey, gotCT, gotPath)
	}
	bad := []CustomAPI{
		{Method: "TRACE", URL: srv.URL},
		{URL: "file:///etc/passwd"},
		{URL: srv.URL, Headers: []Header{{Name: "Authorization", Value: "x"}}},
		{URL: srv.URL, Headers: []Header{{Name: "X-A", Value: "a\r\nB: c"}}},
		{URL: srv.URL, Auth: "oauth2"},
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("bad[%d] accepted", i)
		}
	}
}
