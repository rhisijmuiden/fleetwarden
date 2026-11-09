package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestParseAssetDate(t *testing.T) {
	for in, want := range map[string]string{"": "", "2027-03-31": "2027-03-31", "31-3-2027": "2027-03-31", "31/03/2027": "2027-03-31",
		"1.2.2027": "2027-02-01", "2027-03-31T00:00:00": "2027-03-31", "2027/03/31": "2027-03-31"} {
		if got, ok := parseAssetDate(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"31-31-2027", "tomorrow", "1899-01-01", "03/31/2027"} {
		if _, ok := parseAssetDate(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
	now := time.Date(2026, 11, 9, 23, 30, 0, 0, time.Local)
	if d := daysUntil(now, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)); d != 1 {
		t.Errorf("daysUntil: %d", d)
	}
	if st := warrantyState("2026-11-08", now, 30); st != "expired" {
		t.Errorf("state: %s", st)
	}
	if st := warrantyState("2026-12-01", now, 30); st != "expiring" {
		t.Errorf("state: %s", st)
	}
}

func TestAssets(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx := context.Background()

	office := []store.SoftwareRow{{Name: "Microsoft 365 Apps for business - nl-nl", Version: "16.0", Publisher: "Microsoft Corporation"}}
	pc1 := seedDevice(t, e.st, 1, "PC-1", "windows", nil, office, nil)
	pc2 := seedDevice(t, e.st, 1, "PC-2.corp.example", "windows", nil, append(office, store.SoftwareRow{Name: "Adobe Acrobat (64-bit)", Publisher: "Adobe"}), nil)
	pc3 := seedDevice(t, e.st, 1, "PC-3", "windows", nil, office, nil)
	if err := e.st.SaveInventory(ctx, pc1, store.InventoryUpdate{Hostname: "PC-1", Kind: "workstation", Serial: "ABC123", Manufacturer: "Dell Inc.",
		Model: "Latitude 5440", Hardware: []byte("{}"), Software: office}); err != nil {
		t.Fatal(err)
	}

	// One device by hand.
	var a store.DeviceAsset
	if code := admin.do("GET", "/api/v1/devices/"+pc1+"/asset", nil, &a); code != 200 || a.Owner != "" {
		t.Fatalf("empty asset: %d %+v", code, a)
	}
	today := time.Now()
	soon := today.AddDate(0, 0, 20).Format(time.DateOnly)
	in := store.DeviceAsset{Owner: " Anna de Vries ", Location: "Utrecht", AssetTag: "IT-0001", WarrantyEnd: soon, PurchaseDate: "1-2-2023"}
	if code := admin.do("PUT", "/api/v1/devices/"+pc1+"/asset", in, &a); code != 200 || a.Owner != "Anna de Vries" ||
		a.PurchaseDate != "2023-02-01" || a.WarrantyEnd != soon || a.UpdatedBy != "admin" {
		t.Fatalf("put asset: %d %+v", code, a)
	}
	if code := admin.do("PUT", "/api/v1/devices/"+pc1+"/asset", store.DeviceAsset{WarrantyEnd: "never"}, nil); code != 400 {
		t.Errorf("bad date: %d", code)
	}

	// Bulk: only the location changes, the rest stays.
	var res map[string]int
	if code := admin.do("PUT", "/api/v1/assets", map[string]any{"ids": []string{pc1, pc2}, "fields": map[string]string{"location": "Amersfoort"}},
		&res); code != 200 || res["updated"] != 2 {
		t.Fatalf("bulk: %d %v", code, res)
	}
	admin.do("GET", "/api/v1/devices/"+pc1+"/asset", nil, &a)
	if a.Location != "Amersfoort" || a.Owner != "Anna de Vries" {
		t.Errorf("after bulk: %+v", a)
	}
	if code := admin.do("PUT", "/api/v1/assets", map[string]any{"ids": []string{pc1}, "fields": map[string]string{"hostname": "x"}}, nil); code != 400 {
		t.Errorf("bulk unknown field: %d", code)
	}

	// CSV import from a supplier (Dutch headings, semicolons): serial
	// first, then the short host name; a check first, then apply.
	csvText := "\ufeffServienummer;Serienummer;Hostnaam;Garantie tot;Eigenaar;Leverancier\n" +
		";abc123;;31-12-2030;;Bechtle\n" +
		";;pc-2;2025-01-01;Piet;Bechtle\n" +
		";;PC-404;2030-01-01;;\n" +
		";;PC-3;31-31-2030;;\n" +
		";;PC-1;;;\n"
	var imp importResult
	if code := admin.do("POST", "/api/v1/assets/import", importReq{CSV: csvText}, &imp); code != 200 || imp.Applied || imp.Updated != 2 {
		t.Fatalf("import check: %d %+v", code, imp)
	}
	statuses := []string{}
	for _, r := range imp.Rows {
		statuses = append(statuses, r.Status)
	}
	if strings.Join(statuses, ",") != "update,update,not_found,invalid,invalid" || len(imp.Ignored) != 1 || imp.Ignored[0] != "Servienummer" {
		t.Errorf("import rows: %v ignored %v", statuses, imp.Ignored)
	}
	if imp.Rows[0].DeviceID != pc1 || imp.Rows[0].Changes["warrantyEnd"] != "2030-12-31" || imp.Rows[4].Error != "duplicate" {
		t.Errorf("import row: %+v %+v", imp.Rows[0], imp.Rows[4])
	}
	admin.do("GET", "/api/v1/devices/"+pc1+"/asset", nil, &a)
	if a.WarrantyEnd != soon {
		t.Errorf("check changed data: %+v", a)
	}
	if code := admin.do("POST", "/api/v1/assets/import", importReq{CSV: csvText, Apply: true}, &imp); code != 200 || !imp.Applied {
		t.Fatalf("import: %d %+v", code, imp)
	}
	admin.do("GET", "/api/v1/devices/"+pc1+"/asset", nil, &a)
	if a.WarrantyEnd != "2030-12-31" || a.Supplier != "Bechtle" || a.Owner != "Anna de Vries" {
		t.Errorf("imported: %+v", a)
	}
	if code := admin.do("POST", "/api/v1/assets/import", importReq{CSV: "foo;bar\n1;2\n"}, nil); code != 400 {
		t.Errorf("csv without columns: %d", code)
	}

	// The register with the warranty states.
	var list assetList
	if code := admin.do("GET", "/api/v1/assets", nil, &list); code != 200 || list.Summary.Total != 3 || list.Summary.Expired != 1 ||
		list.Summary.Valid != 1 || list.Summary.Unknown != 1 {
		t.Fatalf("assets: %d %+v", code, list.Summary)
	}
	admin.do("GET", "/api/v1/assets?warranty=expired", nil, &list)
	if len(list.Items) != 1 || list.Items[0].ID != pc2 || list.Items[0].Owner != "Piet" {
		t.Errorf("expired: %+v", list.Items)
	}
	admin.do("GET", "/api/v1/assets?search=bechtle", nil, &list)
	if len(list.Items) != 2 {
		t.Errorf("search: %+v", list.Items)
	}

	// Export, and the export imports again without changes.
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/assets/export?lang=en", nil)
	resp, err := admin.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "PC-1,ABC123,Dell Inc.,Latitude 5440,Default,IT-0001,Anna de Vries,Amersfoort,,Bechtle,,2023-02-01,2030-12-31") {
		t.Errorf("export:\n%s", body)
	}
	admin.do("POST", "/api/v1/assets/import", importReq{CSV: string(body)}, &imp)
	if imp.Updated != 0 {
		t.Errorf("re-import changes: %+v", imp.Rows)
	}

	// Licences.
	key := "AAAA-BBBB"
	lic := map[string]any{"name": "Microsoft 365 Business", "vendor": "Microsoft", "matchName": "Microsoft 365 Apps*", "seats": 2,
		"key": key, "expiresOn": today.AddDate(0, 0, 10).Format(time.DateOnly), "cost": 12.5}
	var l store.SoftwareLicense
	if code := admin.do("POST", "/api/v1/licenses", lic, &l); code != 201 || l.InUse != 3 || !l.HasKey || l.KeyEnc != "" {
		t.Fatalf("create licence: %d %+v", code, l)
	}
	if code := admin.do("POST", "/api/v1/licenses", lic, nil); code != 409 {
		t.Errorf("duplicate licence: %d", code)
	}
	for _, bad := range []map[string]any{{"name": "x", "matchName": "*", "seats": 1}, {"name": "", "matchName": "a", "seats": 1},
		{"name": "y", "matchName": "a", "seats": -1}} {
		if code := admin.do("POST", "/api/v1/licenses", bad, nil); code != 400 {
			t.Errorf("bad licence %v: %d", bad, code)
		}
	}
	var acro store.SoftwareLicense
	admin.do("POST", "/api/v1/licenses", map[string]any{"name": "Acrobat", "matchName": "adobe acrobat*", "matchPublisher": "Adobe", "seats": 5}, &acro)
	if acro.InUse != 1 {
		t.Errorf("acrobat: %+v", acro)
	}
	var prev map[string]any
	if code := admin.do("POST", "/api/v1/licenses/preview", map[string]string{"matchName": "*365*"}, &prev); code != 200 || prev["devices"].(float64) != 3 {
		t.Errorf("preview: %d %v", code, prev)
	}
	var devs struct{ Devices []store.LicenseInstall }
	admin.do("GET", "/api/v1/licenses/"+itoa(l.ID)+"/devices", nil, &devs)
	if len(devs.Devices) != 3 || devs.Devices[0].Hostname != "PC-1" || devs.Devices[0].Owner != "Anna de Vries" {
		t.Errorf("licence devices: %+v", devs.Devices)
	}
	var k map[string]string
	if code := admin.do("POST", "/api/v1/licenses/"+itoa(l.ID)+"/key", nil, &k); code != 200 || k["key"] != key {
		t.Errorf("key: %d %v", code, k)
	}
	// Updating without a key keeps it; seats go up.
	lic["seats"] = 3
	delete(lic, "key")
	if code := admin.do("PUT", "/api/v1/licenses/"+itoa(l.ID), lic, &l); code != 200 || !l.HasKey || l.Seats != 3 {
		t.Errorf("update licence: %d %+v", code, l)
	}

	// Read-only users see, but do not change, and never see keys.
	ro := e.newUserClient(admin, "reader", "readonly")
	if code := ro.do("GET", "/api/v1/licenses", nil, nil); code != 200 {
		t.Errorf("read-only licences: %d", code)
	}
	if code := ro.do("POST", "/api/v1/licenses/"+itoa(l.ID)+"/key", nil, nil); code != 403 {
		t.Errorf("read-only key: %d", code)
	}
	if code := ro.do("PUT", "/api/v1/devices/"+pc3+"/asset", store.DeviceAsset{Owner: "x"}, nil); code != 403 {
		t.Errorf("read-only asset: %d", code)
	}
	tech := e.newUserClient(admin, "tech", "technician")
	if code := tech.do("PUT", "/api/v1/devices/"+pc3+"/asset", store.DeviceAsset{Owner: "Kees"}, nil); code != 200 {
		t.Errorf("technician asset: %d", code)
	}

	// The asset report.
	var rep assetsReport
	if code := admin.do("GET", "/api/v1/reports/assets", nil, &rep); code != 200 || rep.Devices != 3 || rep.Expired != 1 ||
		len(rep.Licenses) != 2 || rep.LicensesOver != 0 || rep.LicensesEnd != 1 || len(rep.Warranty) != 1 {
		t.Fatalf("report: %d %+v", code, rep)
	}

	// Warranty alerts: the rule from the migration looks 60 days ahead.
	var rules struct{ Rules []store.AlertRule }
	admin.do("GET", "/api/v1/alerts/rules", nil, &rules)
	var wr store.AlertRule
	for _, r := range rules.Rules {
		if r.Kind == "warranty" {
			wr = r
		}
	}
	if wr.Threshold != 60 || !wr.Enabled {
		t.Fatalf("warranty rule: %+v", wr)
	}
	admin.do("PUT", "/api/v1/devices/"+pc3+"/asset", store.DeviceAsset{Owner: "Kees", WarrantyEnd: today.AddDate(0, 0, 30).Format(time.DateOnly)}, nil)
	e.srv.alertTick(ctx)
	var alerts struct{ Alerts []alertView }
	admin.do("GET", "/api/v1/alerts", nil, &alerts)
	got := map[string]string{}
	for _, a := range alerts.Alerts {
		if a.Kind == "warranty" {
			got[a.DeviceID] = a.Text
		}
	}
	if len(got) != 2 || !strings.HasPrefix(got[pc2], "Warranty ended on") || !strings.Contains(got[pc3], "(in 30 days)") {
		t.Fatalf("warranty alerts: %v", got)
	}
	// Extending the warranty resolves the alert.
	admin.do("PUT", "/api/v1/devices/"+pc3+"/asset", store.DeviceAsset{Owner: "Kees", WarrantyEnd: today.AddDate(2, 0, 0).Format(time.DateOnly)}, nil)
	e.srv.alertTick(ctx)
	admin.do("GET", "/api/v1/alerts", nil, &alerts)
	for _, a := range alerts.Alerts {
		if a.DeviceID == pc3 && a.Kind == "warranty" {
			t.Errorf("still open: %+v", a)
		}
	}

	if code := admin.do("DELETE", "/api/v1/licenses/"+itoa(acro.ID), nil, nil); code != 204 {
		t.Errorf("delete licence: %d", code)
	}
}
