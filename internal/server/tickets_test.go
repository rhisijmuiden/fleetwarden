package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// TestTrayTickets sends tickets from the tray to a service desk, to Outlook
// and by e-mail, with the device report and a link back to the device.
func TestTrayTickets(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	bg := context.Background()

	var g generalSettings
	_ = e.st.GetSetting(bg, keyGeneral, &g)
	g.PublicURL = "https://fw.example.com/"
	if err := e.st.PutSetting(bg, keyGeneral, g); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var created, notes []string
	status := 2
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v2/tickets":
			created = append(created, string(b))
			_, _ = io.WriteString(w, `{"ticket":{"id":7001}}`)
		case r.Method == "POST" && r.URL.Path == "/api/v2/tickets/7001/notes":
			notes = append(notes, string(b))
			_, _ = io.WriteString(w, `{"conversation":{"id":1}}`)
		case r.Method == "GET" && r.URL.Path == "/api/v2/tickets/7001":
			fmt.Fprintf(w, `{"ticket":{"id":7001,"status":%d}}`, status)
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer desk.Close()

	var integ integrationView
	if code := admin.do("POST", "/api/v1/integrations", map[string]any{"name": "Freshservice", "kind": "freshservice", "enabled": true,
		"settings": map[string]string{"url": desk.URL, "fallbackRequester": "desk@acme.nl"}, "secrets": map[string]string{"apiKey": "k"}}, &integ); code != 201 {
		t.Fatalf("integration: %d", code)
	}

	var grp store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Office", UpdateRing: 1}, &grp)
	_, info := e.createToken(admin, grp.ID)
	a := newTestAgent(t, e, info.Token, "PC-TICKET", false)
	a.Minute = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(bg, 90*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-TICKET")
	setPlatform(t, d.ID, "windows")

	// Settings are checked.
	base := map[string]any{"help": true, "language": "en", "recipients": []string{}}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	for _, bad := range []map[string]any{
		with("destination", "pigeon"),
		with("destination", "outlook", "ticketAddress", "nope"),
		with("destination", "integration", "ticketIntegration", 9999),
		with("report", []string{"shoe size"}),
		with("linkActions", []string{"format"}),
		with("requireMessage", true, "minMessage", 9000),
	} {
		if code := admin.do("PUT", "/api/v1/selfservice/settings", bad, nil); code != 400 {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}
	var saved selfServiceSettings
	if code := admin.do("PUT", "/api/v1/selfservice/settings", with("destination", "integration", "ticketIntegration", integ.ID,
		"requireMessage", true, "minMessage", 20, "askUrgency", true, "askCategory", true, "categories", []string{"Hardware", " Software ", "Hardware"},
		"showSent", true, "report", []string{"reboot", "user", "network"}, "linkDevice", true, "linkActions", []string{"scan", "remote"}), &saved); code != 200 ||
		len(saved.Categories) != 2 || strings.Join(saved.Report, ",") != "user,network,reboot" || strings.Join(saved.LinkActions, ",") != "remote,scan" {
		t.Fatalf("settings: %d %+v", code, saved)
	}

	const user = `CORP\jan`
	ask := func(req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
		t.Helper()
		req.User, req.Lang = user, "en-US"
		var r agentproto.SelfServiceReply
		waitFor(t, "self-service reply", func() bool {
			r = a.SelfService(ctx, req)
			return r.Error != agentproto.SelfServiceErrOffline && r.Error != agentproto.SelfServiceErrNoService
		})
		if r.Catalog == nil {
			t.Fatalf("%s: no catalog in the reply %+v", req.Op, r)
		}
		return r
	}

	// The tray gets the form and what will be sent.
	r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
	f := r.Catalog.Ticket
	if f == nil || !f.RequireMessage || f.MinMessage != 20 || !f.AskUrgency || len(f.Categories) != 2 || f.Destination != agentproto.TicketIntegration ||
		f.DestinationName != "Freshservice" || len(f.Sent) == 0 {
		t.Fatalf("ticket form: %+v", f)
	}

	help := func(h agentproto.HelpRequest) agentproto.SelfServiceReply {
		return ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpHelp, Help: &h})
	}
	for _, bad := range []agentproto.HelpRequest{
		{Subject: "Outlook", Message: "too short"},
		{Subject: "Outlook", Message: "Outlook closes right after it starts.", Urgency: "panic"},
		{Subject: "Outlook", Message: "Outlook closes right after it starts.", Category: "Coffee"},
	} {
		if r := help(bad); r.Error != agentproto.SelfServiceErrInvalid {
			t.Errorf("%+v accepted: %+v", bad, r)
		}
	}
	r = help(agentproto.HelpRequest{Subject: "Outlook crashes", Message: "Outlook closes right after it starts.", Urgency: "high",
		Category: "Software", Email: "jan@acme.nl"})
	if r.Error != "" || r.Outlook != nil {
		t.Fatalf("ticket: %+v", r)
	}
	var h store.HelpRequest
	waitFor(t, "ticket number", func() bool {
		var list struct{ Items []store.HelpRequest }
		admin.do("GET", "/api/v1/selfservice/help?state=active", nil, &list)
		if len(list.Items) == 1 {
			h = list.Items[0]
		}
		return h.TicketNumber != ""
	})
	if h.TicketNumber != "#7001" || h.TicketID != "7001" || h.Urgency != "high" || h.Category != "Software" || h.Email != "jan@acme.nl" ||
		h.Destination != "integration" || h.TicketError != "" || len(h.Report) == 0 {
		t.Fatalf("help request: %+v", h)
	}
	var rep ticketReport
	if err := json.Unmarshal(h.Report, &rep); err != nil || rep.Hostname != "PC-TICKET" || len(rep.Links) != 3 {
		t.Fatalf("report: %v %+v", err, rep)
	}
	want := fmt.Sprintf("https://fw.example.com/devices/%s?action=remote&ticket=%d", d.ID, h.ID)
	if rep.Links[1].URL != want {
		t.Errorf("link = %q, want %q", rep.Links[1].URL, want)
	}
	mu.Lock()
	body := created[0]
	mu.Unlock()
	var sent struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
		Email       string `json:"email"`
		Priority    int    `json:"priority"`
	}
	_ = json.Unmarshal([]byte(body), &sent)
	if sent.Subject != "[PC-TICKET] Outlook crashes" || sent.Email != "jan@acme.nl" || sent.Priority != 3 ||
		!strings.Contains(sent.Description, "Outlook closes") || !strings.Contains(sent.Description, "PC-TICKET") ||
		!strings.Contains(sent.Description, "action=scan") {
		t.Errorf("ticket body: %s", body)
	}
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog}); r.Catalog.HelpRequests[0].Number != "#7001" {
		t.Errorf("number in the tray: %+v", r.Catalog.HelpRequests[0])
	}

	// A reply of the technician is added to the ticket as a note.
	admin.do("PUT", fmt.Sprintf("/api/v1/selfservice/help/%d", h.ID), map[string]any{"state": "in_progress", "reply": "Repairing Office now."}, nil)
	waitFor(t, "note", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notes) == 1 && strings.Contains(notes[0], "Repairing Office now.")
	})

	// The service desk closes the ticket: the help request closes too.
	mu.Lock()
	status = 5
	mu.Unlock()
	e.srv.ticketSyncTick(bg)
	got, _ := e.st.HelpRequestByID(bg, h.ID)
	if got.State != agentproto.HelpClosed || got.TicketState == "" {
		t.Errorf("after sync: %+v", got)
	}

	// The preview uses the form, not the saved settings.
	var pv ticketPreview
	if code := admin.do("POST", "/api/v1/selfservice/ticket-preview", map[string]any{"deviceId": d.ID,
		"settings": with("destination", "outlook", "ticketAddress", "it@acme.nl", "report", []string{"user", "os"})}, &pv); code != 200 ||
		pv.Hostname != "PC-TICKET" || pv.Target != "it@acme.nl" || !pv.PublicURL || !strings.Contains(pv.HTML, "PC-TICKET") || len(pv.Report.Sections) == 0 {
		t.Fatalf("preview: %d %+v", code, pv)
	}

	// Outlook: the tray gets a draft with the report attached.
	admin.do("PUT", "/api/v1/selfservice/settings", with("destination", "outlook", "ticketAddress", "it@acme.nl"), nil)
	r = help(agentproto.HelpRequest{Subject: "Mouse broken", Message: "x"})
	if r.Error != "" || r.Outlook == nil || r.Outlook.To[0] != "it@acme.nl" || r.Outlook.Subject != "[PC-TICKET] Mouse broken" ||
		!strings.HasSuffix(r.Outlook.AttachmentName, ".html") || !strings.Contains(r.Outlook.Attachment, "<html") {
		t.Fatalf("outlook: %+v", r)
	}

	// Mail: FleetWarden mails the service desk.
	e.configureMail(admin)
	admin.do("PUT", "/api/v1/selfservice/settings", with("destination", "mail", "ticketAddress", "desk@acme.nl"), nil)
	if r := help(agentproto.HelpRequest{Subject: "Screen flickers"}); r.Error != "" || r.Outlook != nil {
		t.Fatalf("mail: %+v", r)
	}
	mails, _ := e.st.ListMail(bg, 10)
	found := false
	for _, m := range mails {
		if m.Kind == "ticket" && m.To == "desk@acme.nl" && m.Subject == "[PC-TICKET] Screen flickers" {
			found = true
		}
	}
	if !found {
		t.Errorf("no ticket mail: %+v", mails)
	}
}
