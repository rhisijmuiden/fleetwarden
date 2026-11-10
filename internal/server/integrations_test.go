package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestIntegrationsAPI(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	var mu sync.Mutex
	var auths, bodies []string
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		bodies = append(bodies, r.Method+" "+r.URL.Path+" "+string(b))
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/tickets":
			_, _ = io.WriteString(w, `{"tickets":[]}`)
		case "POST /api/v2/tickets":
			_, _ = io.WriteString(w, `{"ticket":{"id":4821}}`)
		case "POST /hook":
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer desk.Close()

	var kinds struct {
		Kinds []struct {
			Kind string `json:"kind"`
		} `json:"kinds"`
		Variables []string `json:"variables"`
	}
	if code := admin.do("GET", "/api/v1/integrations/kinds", nil, &kinds); code != 200 || len(kinds.Kinds) != 9 || len(kinds.Variables) == 0 {
		t.Fatalf("kinds: %d %+v", code, kinds)
	}

	form := map[string]any{"name": "Freshservice", "kind": "freshservice", "enabled": true,
		"settings": map[string]string{"url": desk.URL, "groupId": "42", "fallbackRequester": "desk@acme.nl", "unknown": "x"},
		"secrets":  map[string]string{"apiKey": "key"}, "events": []string{"alert.opened"}}
	for _, bad := range []map[string]any{
		{"name": "x", "kind": "nope"},
		{"name": "", "kind": "freshservice", "settings": map[string]string{"url": desk.URL}, "secrets": map[string]string{"apiKey": "k"}},
		{"name": "x", "kind": "freshservice", "settings": map[string]string{"url": desk.URL}},
		{"name": "x", "kind": "freshservice", "settings": map[string]string{"url": desk.URL}, "secrets": map[string]string{"apiKey": "k"}, "events": []string{"device.enrolled"}},
		{"name": "x", "kind": "freshservice", "settings": map[string]string{"url": desk.URL}, "secrets": map[string]string{"apiKey": "k"}, "alertRuleIds": []int64{999}},
		{"name": "x", "kind": "custom", "custom": map[string]any{"url": "ftp://x"}},
	} {
		if code := admin.do("POST", "/api/v1/integrations", bad, nil); code != 400 {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}
	var created integrationView
	if code := admin.do("POST", "/api/v1/integrations", form, &created); code != 201 || created.ID == 0 || !created.SecretsSet["apiKey"] ||
		created.Settings["unknown"] != "" || !created.ServiceDesk {
		t.Fatalf("create: %d %+v", code, created)
	}
	if code := admin.do("POST", "/api/v1/integrations", form, nil); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	// Secrets never leave the server.
	var raw json.RawMessage
	admin.do("GET", "/api/v1/integrations", nil, &raw)
	if strings.Contains(string(raw), "key") && strings.Contains(string(raw), `"apiKey":"`) {
		t.Errorf("secret in list: %s", raw)
	}
	stored, _ := e.st.IntegrationByID(context.Background(), created.ID)
	if stored.Secrets["apiKey"] == "key" || stored.Secrets["apiKey"] == "" {
		t.Errorf("secret not sealed: %q", stored.Secrets["apiKey"])
	}

	// Update without the secret keeps it; the test uses it.
	form["id"] = created.ID
	form["secrets"] = map[string]string{}
	form["name"] = "Freshservice IT"
	if code := admin.do("PUT", "/api/v1/integrations/"+itoa(created.ID), form, nil); code != 200 {
		t.Fatalf("update: %d", code)
	}
	var res integrationResult
	if code := admin.do("POST", "/api/v1/integrations/test", form, &res); code != 200 || !res.OK || len(res.Calls) != 1 {
		t.Fatalf("test: %d %+v", code, res)
	}
	mu.Lock()
	if auths[len(auths)-1] != "Basic a2V5Olg=" {
		t.Errorf("auth = %q", auths[len(auths)-1])
	}
	mu.Unlock()
	if code := admin.do("POST", "/api/v1/integrations/"+itoa(created.ID)+"/test-ticket", nil, &res); code != 200 || !res.OK || res.Ticket.Number != "#4821" {
		t.Fatalf("test ticket: %d %+v", code, res)
	}
	var calls []store.IntegrationCall
	if code := admin.do("GET", "/api/v1/integrations/"+itoa(created.ID)+"/calls", nil, &calls); code != 200 || len(calls) != 2 ||
		calls[0].TicketNumber != "#4821" || calls[0].Event != "test_ticket" || !calls[0].OK {
		t.Fatalf("calls: %d %+v", code, calls)
	}
	var one integrationView
	if admin.do("GET", "/api/v1/integrations/"+itoa(created.ID), nil, &one); one.LastOK == nil || !*one.LastOK {
		t.Errorf("last result: %+v", one)
	}

	// A wrong key shows the service desk's error.
	form["secrets"] = map[string]string{"apiKey": "other"}
	form["settings"] = map[string]string{"url": desk.URL + "/wrong"}
	if code := admin.do("POST", "/api/v1/integrations/test", form, &res); code != 200 || res.OK || !strings.Contains(res.Error, "404") {
		t.Fatalf("failing test: %d %+v", code, res)
	}

	// A custom API reacts to a new device in the background.
	hook := map[string]any{"name": "Hook", "kind": "custom", "enabled": true, "events": []string{"device.enrolled"},
		"custom":  map[string]any{"method": "POST", "url": desk.URL + "/hook", "auth": "bearer", "body": `{"host":"{{device.hostname}}"}`},
		"secrets": map[string]string{"secret": "tok"}}
	var hv integrationView
	if code := admin.do("POST", "/api/v1/integrations", hook, &hv); code != 201 || hv.ServiceDesk {
		t.Fatalf("custom: %d %+v", code, hv)
	}
	e.srv.integrationEnrolled(context.Background(), store.Device{ID: "00000000-0000-0000-0000-000000000001", Hostname: "PC-7", Platform: "windows"}, "Office")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _ := e.st.ListIntegrationCalls(context.Background(), hv.ID, 10)
		if len(c) == 1 {
			if !c[0].OK || c[0].Event != "device.enrolled" {
				t.Fatalf("hook call: %+v", c[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook not called")
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	last := bodies[len(bodies)-1]
	mu.Unlock()
	if last != `POST /hook {"host":"PC-7"}` {
		t.Errorf("hook body: %s", last)
	}

	// In use by self-service: cannot be deleted.
	var ss selfServiceSettings
	admin.do("GET", "/api/v1/selfservice/settings", nil, &ss)
	_ = e.st.PutSetting(context.Background(), keySelfService, selfServiceSettings{TicketIntegration: created.ID, Recipients: []string{}, Language: "en"})
	if code := admin.do("DELETE", "/api/v1/integrations/"+itoa(created.ID), nil, nil); code != 409 {
		t.Errorf("delete in use: %d", code)
	}
	_ = e.st.PutSetting(context.Background(), keySelfService, ss)
	if code := admin.do("DELETE", "/api/v1/integrations/"+itoa(created.ID), nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code := admin.do("GET", "/api/v1/integrations/"+itoa(created.ID), nil, nil); code != 404 {
		t.Errorf("deleted still there: %d", code)
	}
}
