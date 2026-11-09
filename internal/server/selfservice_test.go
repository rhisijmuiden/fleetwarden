package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func findItem(c *agentproto.SelfServiceCatalog, name string) agentproto.SelfServiceItem {
	for _, i := range c.Items {
		if i.Name == name {
			return i
		}
	}
	return agentproto.SelfServiceItem{}
}

// TestSelfService drives the tray's requests through a real agent: the
// catalog, an install without and one with approval (rejected, asked again,
// approved), a help request with a reply, and "install updates now".
func TestSelfService(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	// Three uploaded packages: one for everybody, one that needs approval
	// and is only for the Office group, and one the device already has.
	pkg := func(file, name, detect string) store.SoftwarePackage {
		var up uploadResult
		if code := admin.upload(file, []byte("pretend MSI "+name), &up); code != 201 {
			t.Fatalf("upload %s: %d", file, code)
		}
		var p store.SoftwarePackage
		if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": name, "source": "upload", "fileSha256": up.SHA256,
			"fileName": up.Name, "installerKind": "msi", "detectName": detect, "publisher": "Corp"}, &p); code != 201 {
			t.Fatalf("package %s: %d", name, code)
		}
		return p
	}
	lob := pkg("lob.msi", "LOB App", "LOB App")
	visio := pkg("visio.msi", "Visio", "Microsoft Visio")
	zip := pkg("7zip.msi", "7-Zip", "7-Zip")

	var office, other store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Office", UpdateRing: 1}, &office)
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Lab", UpdateRing: 1}, &other)

	var it1, it2, it3, it4 store.SelfServiceItem
	if code := admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": lob.ID, "category": "Business", "description": "Our line-of-business app"}, &it1); code != 201 ||
		it1.Name() != "LOB App" || !it1.Enabled || it1.Platform != "windows" {
		t.Fatalf("add item: %d %+v", code, it1)
	}
	if code := admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": lob.ID}, nil); code != 409 {
		t.Errorf("duplicate item: %d", code)
	}
	if code := admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": visio.ID, "groupIds": []int64{9999}}, nil); code != 400 {
		t.Errorf("unknown group: %d", code)
	}
	if code := admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": visio.ID, "displayName": "Microsoft Visio", "approval": true,
		"groupIds": []int64{office.ID}}, &it2); code != 201 || len(it2.Groups) != 1 || it2.Groups[0].Name != "Office" {
		t.Fatalf("add approval item: %d %+v", code, it2)
	}
	admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": zip.ID}, &it3)
	// An item for another group stays hidden from the device.
	other7 := pkg("other.msi", "Lab Tool", "Lab Tool")
	admin.do("POST", "/api/v1/selfservice/items", map[string]any{"packageId": other7.ID, "groupIds": []int64{other.ID}}, &it4)

	_, info := e.createToken(admin, office.ID)
	fs := newFakeSoftware()
	fs.byFile["lob.msi"] = [2]string{"LOB App", "1.0"}
	fs.byFile["visio.msi"] = [2]string{"Microsoft Visio", "16.0"}
	a := newTestAgent(t, e, info.Token, "PC-SELF", false)
	fp := &fakePatches{missing: []agentproto.PatchUpdate{{ID: "aaaa-1111", Title: "Security Update (KB5050002)", KB: "KB5050002", Category: "security"}}}
	fn := &fakeNotifier{}
	a.Software, a.Patches, a.Notify, a.Minute = fs, fp, fn, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-SELF")
	setPlatform(t, d.ID, "windows")

	const user = `CORP\jan`
	ask := func(req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
		t.Helper()
		req.User, req.Lang = user, "nl-NL"
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

	r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
	if r.Error != "" || len(r.Catalog.Items) != 3 || !r.Catalog.Software || !r.Catalog.Help || r.Catalog.Updates || r.Catalog.ProductName == "" {
		t.Fatalf("catalog: %+v", r)
	}
	if i := findItem(r.Catalog, "LOB App"); i.State != agentproto.ItemAvailable || i.Category != "Business" || i.Description != "Our line-of-business app" || i.Approval {
		t.Errorf("LOB item: %+v", i)
	}
	if i := findItem(r.Catalog, "7-Zip"); i.State != agentproto.ItemInstalled {
		t.Errorf("7-Zip is in the inventory: %+v", i)
	}
	if i := findItem(r.Catalog, "Microsoft Visio"); !i.Approval || i.State != agentproto.ItemAvailable {
		t.Errorf("Visio item: %+v", i)
	}
	if i := findItem(r.Catalog, "Lab Tool"); i.ID != 0 {
		t.Errorf("an item of another group is visible: %+v", i)
	}
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it4.ID}); r.Error != agentproto.SelfServiceErrNotFound {
		t.Errorf("install of a hidden item: %+v", r.Error)
	}

	// An install without approval runs right away.
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it1.ID})
	if r.Error != "" || !strings.Contains(r.Message, "LOB App") || !strings.Contains(r.Message, "geïnstalleerd") {
		t.Fatalf("install: %+v", r)
	}
	if s := findItem(r.Catalog, "LOB App").State; s != agentproto.ItemQueued && s != agentproto.ItemInstalling && s != agentproto.ItemInstalled {
		t.Errorf("state after install: %s", s)
	}
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it1.ID}); r.Error != agentproto.SelfServiceErrBusy &&
		findItem(r.Catalog, "LOB App").State != agentproto.ItemInstalled {
		t.Errorf("second install while the first runs: %+v", r.Error)
	}
	waitFor(t, "LOB App installed", func() bool {
		e.srv.softwareTick(ctx)
		return findItem(ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog}).Catalog, "LOB App").State == agentproto.ItemInstalled
	})
	if fs.count() != 1 {
		t.Errorf("installs: %d", fs.count())
	}

	// With approval: the request waits, is rejected, asked again and approved.
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it2.ID, Reason: "Floor plans for the new office"})
	if r.Error != "" || findItem(r.Catalog, "Microsoft Visio").State != agentproto.ItemPending {
		t.Fatalf("request: %+v", r)
	}
	var count store.SelfServiceCounts
	if code := admin.do("GET", "/api/v1/selfservice/count", nil, &count); code != 200 || count.PendingRequests != 1 {
		t.Errorf("count: %d %+v", code, count)
	}
	var reqs struct{ Items []store.SelfServiceRequest }
	if code := admin.do("GET", "/api/v1/selfservice/requests?state=pending", nil, &reqs); code != 200 || len(reqs.Items) != 1 ||
		reqs.Items[0].Username != user || reqs.Items[0].Reason != "Floor plans for the new office" || reqs.Items[0].Hostname != "PC-SELF" {
		t.Fatalf("pending requests: %d %+v", code, reqs)
	}
	if code := admin.do("POST", fmt.Sprintf("/api/v1/selfservice/requests/%d/reject", reqs.Items[0].ID), map[string]any{"note": "No licence left"}, nil); code != 204 {
		t.Fatalf("reject: %d", code)
	}
	if code := admin.do("POST", fmt.Sprintf("/api/v1/selfservice/requests/%d/approve", reqs.Items[0].ID), nil, nil); code != 409 {
		t.Errorf("approve after reject: %d", code)
	}
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
	if i := findItem(r.Catalog, "Microsoft Visio"); i.State != agentproto.ItemRejected || i.Message != "No licence left" {
		t.Errorf("rejected item: %+v", i)
	}
	ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it2.ID, Reason: "We bought one"})
	admin.do("GET", "/api/v1/selfservice/requests?state=pending", nil, &reqs)
	if len(reqs.Items) != 1 {
		t.Fatalf("pending after asking again: %+v", reqs)
	}
	var approved map[string]int64
	if code := admin.do("POST", fmt.Sprintf("/api/v1/selfservice/requests/%d/approve", reqs.Items[0].ID), map[string]any{"note": "ok"}, &approved); code != 200 || approved["deploymentId"] == 0 {
		t.Fatalf("approve: %d %+v", code, approved)
	}
	waitFor(t, "Visio installed", func() bool {
		e.srv.softwareTick(ctx)
		return findItem(ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog}).Catalog, "Microsoft Visio").State == agentproto.ItemInstalled
	})
	var dep deploymentView
	if code := admin.do("GET", fmt.Sprintf("/api/v1/deployments/%d", approved["deploymentId"]), nil, &dep); code != 200 || len(dep.Jobs) != 1 ||
		!strings.Contains(dep.Deployment.RequestedBy, "self-service") {
		t.Errorf("deployment of the request: %d %+v", code, dep.Deployment)
	}

	// Help: the request shows in the web interface, the reply in the tray.
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpHelp, Help: &agentproto.HelpRequest{Subject: " "}}); r.Error != agentproto.SelfServiceErrInvalid {
		t.Errorf("help without subject: %+v", r.Error)
	}
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpHelp, Help: &agentproto.HelpRequest{Subject: "Printer does not print",
		Message: "Since this morning\nthe printer on floor 2 is offline.", Contact: "06-12345678"}})
	if r.Error != "" || len(r.Catalog.HelpRequests) != 1 || r.Catalog.HelpRequests[0].State != agentproto.HelpOpen || !strings.Contains(r.Message, "hulpvraag") {
		t.Fatalf("help: %+v", r)
	}
	var help struct{ Items []store.HelpRequest }
	if code := admin.do("GET", "/api/v1/selfservice/help?state=active", nil, &help); code != 200 || len(help.Items) != 1 || help.Items[0].Contact != "06-12345678" ||
		!strings.Contains(help.Items[0].Message, "floor 2") || !help.Items[0].Online || help.Items[0].Username != user {
		t.Fatalf("help list: %d %+v", code, help)
	}
	var updated store.HelpRequest
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/selfservice/help/%d", help.Items[0].ID), map[string]any{"state": "in_progress",
		"reply": "We are sending a new toner."}, &updated); code != 200 || updated.AssignedTo == "" || updated.State != "in_progress" {
		t.Fatalf("take help request: %d %+v", code, updated)
	}
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
	if h := r.Catalog.HelpRequests[0]; h.State != agentproto.HelpInProgress || h.Reply != "We are sending a new toner." {
		t.Errorf("help in the tray: %+v", h)
	}
	// Another user of the same device does not see it.
	if r := a.SelfService(ctx, agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog, User: `CORP\piet`}); r.Catalog == nil || len(r.Catalog.HelpRequests) != 0 {
		t.Errorf("help of another user: %+v", r.Catalog)
	}

	// Updates: off without a patch policy; with one the user installs the
	// approved updates now, without the "updates are ready" pop-up.
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpUpdates}); r.Error != agentproto.SelfServiceErrDisabled {
		t.Errorf("updates without a policy: %+v", r.Error)
	}
	pol := allDayPolicy("Office")
	pol["reboot"] = "never"
	var pv policyView
	if code := admin.do("POST", "/api/v1/patches/policies", pol, &pv); code != 201 {
		t.Fatalf("policy: %d", code)
	}
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/groups/%d", office.ID), map[string]any{"name": "Office", "updateRing": 1, "patchPolicyId": pv.ID}, nil); code != 200 {
		t.Fatalf("assign policy: %d", code)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/scan", nil, nil); code != 202 {
		t.Fatalf("scan: %d", code)
	}
	waitFor(t, "approved update in the tray", func() bool {
		r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
		return r.Catalog.Updates && r.Catalog.PendingUpdates == 1
	})
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpUpdates})
	if r.Error != "" || !strings.Contains(r.Message, "1 updates") {
		t.Fatalf("install updates now: %+v", r)
	}
	var runs []store.PatchRun
	waitFor(t, "self-service patch run", func() bool {
		admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
		return len(runs) == 1 && runs[0].State == agentproto.PatchCompleted
	})
	if runs[0].Trigger != "selfservice" || !slices.Equal(runs[0].Updates, []string{"aaaa-1111"}) || !strings.Contains(runs[0].RequestedBy, user) {
		t.Errorf("run: %+v", runs[0])
	}
	fn.mu.Lock()
	prompts := len(fn.prompts)
	fn.mu.Unlock()
	if prompts != 0 {
		t.Errorf("the user was asked %d times after asking for the updates", prompts)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/scan", nil, nil); code != 202 {
		t.Fatalf("scan: %d", code)
	}
	waitFor(t, "no updates left", func() bool {
		r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
		return r.Catalog.PendingUpdates == 0
	})
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpUpdates}); r.Error != agentproto.SelfServiceErrNothing {
		t.Errorf("updates when up to date: %+v", r.Error)
	}

	// Settings: turning the catalog off hides it in the tray.
	var set selfServiceSettings
	if code := admin.do("PUT", "/api/v1/selfservice/settings", map[string]any{"software": false, "updates": true, "help": true,
		"recipients": []string{"not an address"}}, nil); code != 400 {
		t.Errorf("bad recipient: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/selfservice/settings", map[string]any{"software": false, "updates": true, "help": true, "helpText": "Call 1234",
		"recipients": []string{"it@corp.example"}, "language": "nl"}, &set); code != 200 || set.Software || set.Language != "nl" {
		t.Fatalf("settings: %d %+v", code, set)
	}
	r = ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpCatalog})
	if r.Catalog.Software || len(r.Catalog.Items) != 0 || r.Catalog.HelpText != "Call 1234" {
		t.Errorf("catalog off: %+v", r.Catalog)
	}
	if r := ask(agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpInstall, ItemID: it1.ID}); r.Error != agentproto.SelfServiceErrDisabled {
		t.Errorf("install with the catalog off: %+v", r.Error)
	}

	// Removing an item; a read-only user sees the lists but cannot change them.
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/selfservice/items/%d", it3.ID), nil, nil); code != 204 {
		t.Errorf("remove item: %d", code)
	}
	var items selfServiceItemsView
	if code := admin.do("GET", "/api/v1/selfservice/items", nil, &items); code != 200 || len(items.Items) != 3 {
		t.Errorf("items: %d %d", code, len(items.Items))
	}
	if i := slices.IndexFunc(items.Items, func(i store.SelfServiceItem) bool { return i.ID == it1.ID }); i < 0 || items.Items[i].Requests != 1 || items.Items[i].Installs != 1 {
		t.Errorf("item counts: %+v", items.Items)
	}
	var created userWithPassword
	admin.do("POST", "/api/v1/users", createUserReq{Username: "viewer", Role: "readonly"}, &created)
	ro := e.client()
	ro.login("viewer", created.OneTimePassword)
	ro.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil)
	if code := ro.do("GET", "/api/v1/selfservice/requests", nil, nil); code != 200 {
		t.Errorf("read-only list: %d", code)
	}
	if code := ro.do("PUT", fmt.Sprintf("/api/v1/selfservice/help/%d", help.Items[0].ID), map[string]any{"state": "closed"}, nil); code != http.StatusForbidden {
		t.Errorf("read-only close: %d", code)
	}
}
