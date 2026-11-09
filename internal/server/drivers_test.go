package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// fakeDrivers is a driver backend that offers a fixed list and records
// what it installs (with the content of the files it got).
type fakeDrivers struct {
	mu        sync.Mutex
	offered   []agentproto.DriverUpdate
	installed []string // job IDs
	contents  map[string]string
	manage    bool
}

func (f *fakeDrivers) Scan(context.Context) ([]agentproto.DriverUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.offered), nil
}

func (f *fakeDrivers) Install(_ context.Context, job agentproto.DriverInstall, files []string) (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contents == nil {
		f.contents = map[string]string{}
	}
	for _, p := range files {
		raw, err := os.ReadFile(p)
		if err != nil {
			return false, "", err
		}
		f.contents[filepath.Base(p)] = string(raw)
	}
	f.installed = append(f.installed, job.ID)
	// Windows Update no longer offers an installed driver.
	f.offered = slices.DeleteFunc(f.offered, func(d agentproto.DriverUpdate) bool { return strings.EqualFold(d.SourceID, job.SourceID) })
	return false, "installed", nil
}

func (f *fakeDrivers) Configure(manage bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manage = manage
	return nil
}

func sum256(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func TestDriverRepository(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	// The vendor: a driver cabinet and a firmware file that does not match
	// its published checksum.
	const cab = "MSCF fake driver cabinet"
	var vendorHits sync.Map
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := vendorHits.LoadOrStore(r.URL.Path, new(int))
		*(n.(*int))++
		switch r.URL.Path {
		case "/d/intel-display.cab":
			w.Write([]byte(cab)) //nolint:errcheck
		case "/fw/bios.cab":
			w.Write([]byte("tampered firmware")) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(vendor.Close)
	e.srv.drivers.allowHost = func(_, host string) bool { return host == "127.0.0.1" }

	pol := allDayPolicy("Drivers")
	pol["drivers"], pol["firmware"] = true, false
	var pv policyView
	if code := admin.do("POST", "/api/v1/patches/policies", pol, &pv); code != 201 || !pv.Drivers || pv.Firmware {
		t.Fatalf("create policy: %d %+v", code, pv)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": pv.ID}, nil); code != 200 {
		t.Fatalf("assign policy: %d", code)
	}

	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-DRIVERS", false)
	fd := &fakeDrivers{offered: []agentproto.DriverUpdate{
		{Source: agentproto.DriverSourceWindowsUpdate, SourceID: "AAAA-1111", Kind: "driver", Title: "Intel - Display - 31.0.101.5590",
			Vendor: "Intel", Class: "Display", Version: "31.0.101.5590", HardwareID: `PCI\VEN_8086&DEV_A7A0`, Device: "Intel(R) Iris(R) Xe",
			CurrentVersion: "31.0.101.4502", Files: []agentproto.DriverFile{{URL: vendor.URL + "/d/intel-display.cab", SHA256: sum256(cab)}}},
		{Source: agentproto.DriverSourceFwupd, SourceID: strings.Repeat("b", 64), Kind: "firmware", Title: "System Firmware 1.20",
			Version: "1.20", DeviceID: "fwdev1", Files: []agentproto.DriverFile{{Name: "bios.cab", URL: vendor.URL + "/fw/bios.cab",
				SHA256: sum256("genuine firmware")}}},
		// Not from a known download host: dropped.
		{Source: agentproto.DriverSourceWindowsUpdate, SourceID: "CCCC-3333", Title: "Elsewhere",
			Files: []agentproto.DriverFile{{URL: "https://downloads.example.net/x.cab"}}},
	}}
	fp := &fakePatches{}
	a.Patches, a.Drivers, a.Minute = fp, fd, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-DRIVERS")
	waitFor(t, "driver settings", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return fd.manage
	})

	e.srv.patchTick(ctx) // asks for a scan
	var dv deviceDriversView
	waitFor(t, "driver scan", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/drivers", nil, &dv)
		return dv.ScanAt != nil && len(dv.Drivers) == 2
	})
	var avail []store.DriverUpdate
	if code := admin.do("GET", "/api/v1/drivers?state=available", nil, &avail); code != 200 || len(avail) != 2 || avail[0].Devices != 1 {
		t.Fatalf("available: %d %+v", code, avail)
	}
	var drv, fw store.DriverUpdate
	for _, u := range avail {
		if u.Kind == "driver" {
			drv = u
		} else {
			fw = u
		}
	}
	if drv.SourceID != "aaaa-1111" || drv.Files[0].Name != "intel-display.cab" || fw.Platform == "" {
		t.Errorf("catalog entries: %+v %+v", drv, fw)
	}

	// Nothing is in the repository yet: the window starts no job and the
	// vendor was never contacted.
	e.srv.patchTick(ctx)
	time.Sleep(200 * time.Millisecond)
	var runs []store.PatchRun
	admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
	if len(runs) != 0 {
		t.Fatalf("job without repository updates: %+v", runs)
	}
	if _, ok := vendorHits.Load("/d/intel-display.cab"); ok {
		t.Fatal("downloaded before it was added to the repository")
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/drivers/install", map[string]any{"ids": []int64{drv.ID}}, nil); code != 400 {
		t.Errorf("install before the repository has it: %d", code)
	}
	if code := admin.do("POST", "/api/v1/drivers/state", map[string]any{"ids": []int64{drv.ID}, "action": "approve"}, nil); code != 400 {
		t.Errorf("unknown action: %d", code)
	}

	// Add both to the repository.
	if code := admin.do("POST", "/api/v1/drivers/state", map[string]any{"ids": []int64{drv.ID, fw.ID}, "action": "add"}, nil); code != 200 {
		t.Fatalf("add: %d", code)
	}
	waitFor(t, "downloads", func() bool {
		a, _ := e.st.DriverByID(ctx, drv.ID)
		b, _ := e.st.DriverByID(ctx, fw.ID)
		drv, fw = a, b
		return drv.State == store.DriverReady && fw.State == store.DriverFailed
	})
	if drv.StoredBytes != int64(len(cab)) || drv.Files[0].Stored != sum256(cab) || drv.DecidedBy != "admin" {
		t.Errorf("stored driver: %+v", drv)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "repository", sum256(cab))); err != nil {
		t.Errorf("repository file: %v", err)
	}
	if !strings.Contains(fw.Error, "SHA-256") || fw.Attempts != driverDownloadTries {
		t.Errorf("failed firmware: %+v", fw)
	}
	if n, _ := vendorHits.Load("/fw/bios.cab"); *(n.(*int)) != driverDownloadTries {
		t.Errorf("firmware download attempts: %d", *(n.(*int)))
	}
	var sum driverSummary
	if code := admin.do("GET", "/api/v1/drivers/summary", nil, &sum); code != 200 || sum.Stats.Ready != 1 || sum.Stats.Failed != 1 ||
		sum.Stats.StoredBytes != int64(len(cab)) {
		t.Errorf("summary: %d %+v", code, sum)
	}

	// Agents only get repository files with an open job, and only signed.
	if res, err := http.Get(e.ts.URL + agentproto.PathRepository + sum256(cab)); err != nil || res.StatusCode != 401 {
		t.Errorf("unsigned repository download: %v %v", err, res)
	}

	// The window: the driver is installed from the repository.
	e.srv.patchTick(ctx)
	waitFor(t, "driver job", func() bool {
		admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
		return len(runs) == 1 && runs[0].State == agentproto.PatchCompleted
	})
	if want := agentproto.DriverResultID(drv.ID); !slices.Equal(runs[0].Updates, []string{want}) || runs[0].Installed != 1 {
		t.Errorf("run: %+v", runs[0])
	}
	fd.mu.Lock()
	got := fd.contents["intel-display.cab"]
	fd.mu.Unlock()
	if got != cab {
		t.Errorf("installed file content: %q", got)
	}
	if n, _ := vendorHits.Load("/d/intel-display.cab"); *(n.(*int)) != 1 {
		t.Errorf("vendor downloads: %d (agents must not fetch from the vendor)", *(n.(*int)))
	}
	waitFor(t, "rescan after the job", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/drivers", nil, &dv)
		return len(dv.Drivers) == 1
	})

	// Remove the driver from the repository: its file goes.
	if code := admin.do("POST", "/api/v1/drivers/state", map[string]any{"ids": []int64{drv.ID}, "action": "remove"}, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	waitFor(t, "repository clean-up", func() bool {
		_, err := os.Stat(filepath.Join(e.cfg.DataDir, "repository", sum256(cab)))
		return os.IsNotExist(err)
	})
	var repo []store.DriverUpdate
	if code := admin.do("GET", "/api/v1/drivers?state=repository", nil, &repo); code != 200 || len(repo) != 0 {
		t.Errorf("repository after removal: %d %+v", code, repo)
	}
	// Reject the firmware: it leaves the available list.
	if code := admin.do("POST", "/api/v1/drivers/state", map[string]any{"ids": []int64{fw.ID}, "action": "reject"}, nil); code != 200 {
		t.Fatalf("reject: %d", code)
	}
	if code := admin.do("GET", "/api/v1/drivers?state=available", nil, &avail); code != 200 || len(avail) != 0 {
		t.Errorf("available after reject: %d %+v", code, avail)
	}
}

func TestDriverURLs(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		source, in, want string
	}{
		{"windows-update", "http://download.windowsupdate.com/d/msdownload/update/driver/drvs/2024/x.cab", "https://download.windowsupdate.com/d/msdownload/update/driver/drvs/2024/x.cab"},
		{"windows-update", "https://catalog.s.download.windowsupdate.com/c/x.cab", "https://catalog.s.download.windowsupdate.com/c/x.cab"},
		{"windows-update", "https://evilwindowsupdate.com/x.cab", ""},
		{"windows-update", "https://fwupd.org/downloads/x.cab", ""},
		{"fwupd", "https://fwupd.org/downloads/abc-bios.cab", "https://fwupd.org/downloads/abc-bios.cab"},
		{"fwupd", "https://cdn.fwupd.org/downloads/abc-bios.cab", "https://cdn.fwupd.org/downloads/abc-bios.cab"},
		{"fwupd", "https://user:pw@fwupd.org/x.cab", ""},
		{"fwupd", "file:///etc/passwd", ""},
		{"other", "https://fwupd.org/x.cab", ""},
	} {
		got, ok := s.driverURL(c.source, c.in)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("%s %s: %q %v, want %q", c.source, c.in, got, ok, c.want)
		}
	}
}
