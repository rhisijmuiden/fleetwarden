package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/drivercat"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// redirectTransport sends every request to the test server, keeping the
// path, as if it were the vendor's host.
type redirectTransport struct{ to *url.URL }

func (rt redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host, c.Host = rt.to.Scheme, rt.to.Host, rt.to.Host
	res, err := http.DefaultTransport.RoundTrip(c)
	if res != nil {
		res.Request = r
	}
	return res, err
}

func readCatalog(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "drivercat", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// utf16Replace replaces text in a UTF-16 (little endian, BOM) document.
func utf16Replace(raw []byte, old, new string) []byte {
	u := make([]uint16, (len(raw)-2)/2)
	for i := range u {
		u[i] = uint16(raw[2+2*i]) | uint16(raw[3+2*i])<<8
	}
	s := strings.ReplaceAll(string(utf16.Decode(u)), old, new)
	out := []byte{0xff, 0xfe}
	for _, c := range utf16.Encode([]rune(s)) {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func TestVendorDrivers(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	// The vendors' catalogs, as the vendors publish them (the cabinets are
	// served unpacked; the test unpacks nothing).
	dellModel := readCatalog(t, "dell-model.xml")
	idx, err := drivercat.ParseDellIndex(readCatalog(t, "dell-index.xml"))
	if err != nil {
		t.Fatal(err)
	}
	dellIndex := utf16Replace(readCatalog(t, "dell-index.xml"), idx["075D"].SHA256, sum256(string(dellModel)))
	lenovoPkg := readCatalog(t, "lenovo-package.xml")
	lenovoCat := readCatalog(t, "lenovo-catalog.xml")
	refs, _ := drivercat.ParseLenovoCatalog(lenovoCat)
	lenovoCat = bytes.ReplaceAll(lenovoCat, []byte(refs[0].SHA256), []byte(sum256(string(lenovoPkg))))
	files := map[string][]byte{
		"/catalog/CatalogIndexPC.cab":               dellIndex,
		"/" + idx["075D"].Path:                      dellModel,
		"/ref/8723/8723_64_11.0.24h2.cab":           readCatalog(t, "hp-ref.xml"),
		"/catalog/21AH_Win11.xml":                   lenovoCat,
		"/pccbbs/mobiles/n3buj24w_2_.xml":           lenovoPkg,
		"/pub/softpaq/sp174001-174500/sp174024.exe": []byte("not the real SoftPaq"),
	}
	var hits []string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if b, ok := files[r.URL.Path]; ok {
			w.Write(b) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(vendor.Close)
	to, _ := url.Parse(vendor.URL)
	e.srv.drivers.client = &http.Client{Transport: redirectTransport{to}}
	e.srv.vendors.extractCab = func(_ context.Context, b []byte) ([]byte, error) { return b, nil }

	// Three Windows PCs, one per vendor, each with an older BIOS.
	_, info := e.createToken(admin, 1)
	pcs := map[string]agentproto.Hardware{
		"PC-DELL":   {Manufacturer: "Dell Inc.", Model: "Precision 5720 AIO", SystemSKU: "075D", BIOSVersion: "1.0.3"},
		"PC-HP":     {Manufacturer: "HP", Model: "HP EliteBook 840 G7", Baseboard: "8723", BIOSVersion: "S70 Ver. 01.23.00"},
		"PC-LENOVO": {Manufacturer: "LENOVO", Model: "21AHCTO1WW", BIOSVersion: "N3BET65W (1.45 )"},
		"PC-OTHER":  {Manufacturer: "Dell Inc.", Model: "Precision 5720 AIO", SystemSKU: "075D", BIOSVersion: "2.2.0"},
	}
	ids := map[string]string{}
	for host, hw := range pcs {
		a := newTestAgent(t, e, info.Token, host, true)
		a.MachineID = "machine-" + host
		a.Collect = func(context.Context) (agentproto.Inventory, error) {
			return agentproto.Inventory{Hostname: host, OS: agentproto.OSInfo{Name: "Microsoft Windows 11 Pro", Version: "24H2"}, Hardware: hw}, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := a.Run(ctx); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		cancel()
		var list deviceList
		admin.do("GET", "/api/v1/devices?search="+host, nil, &list)
		if list.Total != 1 || list.Items[0].InventoryAt == nil {
			t.Fatalf("%s: %+v", host, list)
		}
		ids[host] = list.Items[0].ID
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), "UPDATE devices SET platform = 'windows'"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := e.srv.syncVendorCatalogs(ctx, true); err != nil {
		t.Fatalf("sync: %v (hits %v)", err, hits)
	}
	var st vendorStatus
	if code := admin.do("GET", "/api/v1/drivers/vendors", nil, &st); code != 200 || len(st.Catalogs) != 3 || st.LastRun == nil {
		t.Fatalf("status: %d %+v", code, st)
	}
	for _, c := range st.Catalogs {
		if c.Error != "" || c.ItemCount == 0 {
			t.Errorf("catalog %s/%s: %+v", c.Vendor, c.Key, c)
		}
	}

	var avail []store.DriverUpdate
	if code := admin.do("GET", "/api/v1/drivers?state=available&kind=firmware", nil, &avail); code != 200 || len(avail) != 3 {
		t.Fatalf("available: %d %+v", code, avail)
	}
	bySource := map[string]store.DriverUpdate{}
	for _, u := range avail {
		bySource[u.Source] = u
		if u.Devices != 1 || !u.Reboot || u.Class != "BIOS" || len(u.Files) != 1 || len(u.Files[0].SHA256) != 64 {
			t.Errorf("%s: %+v", u.Source, u)
		}
	}
	if u := bySource["dell"]; u.Version != "2.2.0" || u.Installer != "dell" || !strings.HasPrefix(u.Files[0].URL, drivercat.DellBase) {
		t.Errorf("dell: %+v", u)
	}
	if u := bySource["hp"]; u.SourceID != "sp174024" || u.Command != "HpFirmwareUpdRec.exe -b -s" || u.Installer != "hp" {
		t.Errorf("hp: %+v", u)
	}
	if u := bySource["lenovo"]; u.SourceID != "n3buj24w" || u.Installer != "lenovo" || len(u.ExitCodes) != 2 {
		t.Errorf("lenovo: %+v", u)
	}
	var dd deviceDriversView
	if code := admin.do("GET", "/api/v1/devices/"+ids["PC-HP"]+"/drivers", nil, &dd); code != 200 || len(dd.Drivers) != 1 ||
		dd.Drivers[0].CurrentVersion != "S70 Ver. 01.23.00" {
		t.Errorf("HP device: %d %+v", code, dd)
	}
	admin.do("GET", "/api/v1/devices/"+ids["PC-OTHER"]+"/drivers", nil, &dd)
	if len(dd.Drivers) != 0 {
		t.Errorf("a device with the newest BIOS is offered %+v", dd.Drivers)
	}

	// The download is checked against the vendor's checksum.
	hp := bySource["hp"]
	if code := admin.do("POST", "/api/v1/drivers/state", driverStateReq{IDs: []int64{hp.ID}, Action: "add"}, nil); code != 200 {
		t.Fatalf("add: %d", code)
	}
	waitFor(t, "HP download", func() bool {
		u, _ := e.st.DriverByID(ctx, hp.ID)
		return u.State == store.DriverQueued && u.Error != "" || u.State == store.DriverFailed
	})
	if u, _ := e.st.DriverByID(ctx, hp.ID); u.Error == "" || u.State != store.DriverFailed {
		t.Errorf("tampered SoftPaq: %+v", u)
	}

	// Switching a vendor off takes its updates away.
	if code := admin.do("PUT", "/api/v1/drivers/vendors", vendorSettings{HP: true, Lenovo: true}, &st); code != 200 || st.Settings.Dell {
		t.Fatalf("settings: %d %+v", code, st)
	}
	waitFor(t, "Dell switched off", func() bool {
		admin.do("GET", "/api/v1/devices/"+ids["PC-DELL"]+"/drivers", nil, &dd)
		return len(dd.Drivers) == 0
	})
}

func zipWith(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, n := range names {
		w, _ := z.Create(n)
		w.Write([]byte("content of " + n)) //nolint:errcheck
	}
	z.Close()
	return b.Bytes()
}

func (c *client) uploadTo(path, name string, body []byte, out any) int {
	c.e.t.Helper()
	req, _ := http.NewRequest("POST", c.e.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("X-Requested-With", "fleetwarden")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-File-Name", name)
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out) //nolint:errcheck
	}
	return res.StatusCode
}

func TestDriverUploads(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-UPLOAD", true)
	a.MachineID = "machine-upload"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "UPDATE devices SET platform = 'windows'"); err != nil {
		t.Fatal(err)
	}
	var list deviceList
	admin.do("GET", "/api/v1/devices?search=PC-UPLOAD", nil, &list)
	dev := list.Items[0]

	// Only archives with a driver, cabinets and programs.
	if code := admin.uploadTo("/api/v1/drivers/uploads/file", "readme.zip", zipWith(t, "readme.txt"), nil); code != 400 {
		t.Errorf("zip without .inf: %d", code)
	}
	if code := admin.uploadTo("/api/v1/drivers/uploads/file", "driver.inf", []byte("x"), nil); code != 400 {
		t.Errorf("bare .inf: %d", code)
	}
	pkg := zipWith(t, "net/e1000.inf", "net/e1000.sys", "net/e1000.cat")
	var up uploadResult
	if code := admin.uploadTo("/api/v1/drivers/uploads/file", "Intel NIC 1.2.zip", pkg, &up); code != 201 || up.SHA256 != sum256(string(pkg)) {
		t.Fatalf("upload: %d %+v", code, up)
	}

	req := driverUploadReq{SHA256: up.SHA256, Name: up.Name, Platform: dev.Platform, Kind: "driver", Title: "Intel NIC",
		Version: "1.2", Manufacturer: "dell", Model: "latitude"}
	var u store.DriverUpdate
	if code := admin.do("POST", "/api/v1/drivers/uploads", req, &u); code != 201 || u.State != store.DriverReady ||
		u.Source != "upload" || u.Installer != agentproto.InstallerINF || u.Files[0].Stored != up.SHA256 {
		t.Fatalf("create: %d %+v", code, u)
	}
	if code := admin.do("POST", "/api/v1/drivers/uploads", req, nil); code != 409 {
		t.Errorf("same package twice: %d", code)
	}
	var dd deviceDriversView
	admin.do("GET", "/api/v1/devices/"+dev.ID+"/drivers", nil, &dd)
	if len(dd.Drivers) != 1 || dd.Drivers[0].ID != u.ID {
		t.Fatalf("offered: %+v", dd.Drivers)
	}
	jobs, err := e.srv.deviceDriverJobs(ctx, dev.ID, nil, []int64{u.ID})
	if err != nil || len(jobs) != 1 || jobs[0].Files[0].SHA256 != up.SHA256 || jobs[0].Installer != u.Installer {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	var uploads []store.DriverUpdate
	if code := admin.do("GET", "/api/v1/drivers?source=upload", nil, &uploads); code != 200 || len(uploads) != 1 {
		t.Errorf("uploads: %d %+v", code, uploads)
	}

	// A target that does not match takes it away.
	req.Model = "OptiPlex"
	if code := admin.do("PUT", "/api/v1/drivers/uploads/"+itoa(u.ID), req, &u); code != 200 || u.Target.Model != "OptiPlex" {
		t.Fatalf("update: %d %+v", code, u)
	}
	admin.do("GET", "/api/v1/devices/"+dev.ID+"/drivers", nil, &dd)
	if len(dd.Drivers) != 0 {
		t.Errorf("still offered: %+v", dd.Drivers)
	}
	// Uploads cannot be rejected or taken out like vendor updates.
	var res map[string]int
	admin.do("POST", "/api/v1/drivers/state", driverStateReq{IDs: []int64{u.ID}, Action: "remove"}, &res)
	if res["changed"] != 0 {
		t.Errorf("state change on an upload: %+v", res)
	}
	if code := admin.do("DELETE", "/api/v1/drivers/uploads/"+itoa(u.ID), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.srv.uploadsDir(), up.SHA256)); !os.IsNotExist(err) {
		t.Errorf("file kept: %v", err)
	}
}
