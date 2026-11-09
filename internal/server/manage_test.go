package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// fakeManage is the agent's process, service and event log backend.
type fakeManage struct {
	mu      sync.Mutex
	killed  []int
	actions []string
	query   agentproto.LogQuery
	root    string
}

func (f *fakeManage) Processes(context.Context) ([]agentproto.Process, error) {
	return []agentproto.Process{
		{PID: 4, Name: "System", Protected: true},
		{PID: 4242, Name: "notepad.exe", User: `PC-01\anna`, CPU: 1.5, Memory: 20 << 20},
		{PID: os.Getpid(), Name: "fleetwarden-agent.exe"},
	}, nil
}

func (f *fakeManage) KillProcess(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid != 4242 {
		return &agentErr{agentproto.ManageErrNotFound}
	}
	f.killed = append(f.killed, pid)
	return nil
}

func (f *fakeManage) Services(context.Context) ([]agentproto.Service, error) {
	return []agentproto.Service{
		{Name: "Spooler", DisplayName: "Print Spooler", State: "running", StartType: "automatic", CanStop: true},
		{Name: "FleetWardenAgent", DisplayName: "FleetWarden Agent", State: "running", StartType: "automatic", CanStop: true},
	}, nil
}

func (f *fakeManage) ServiceAction(_ context.Context, name, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, name+":"+action)
	return nil
}

func (f *fakeManage) LogSources(context.Context) ([]agentproto.LogSource, error) {
	return []agentproto.LogSource{{Name: "System", Records: 10}, {Name: "Application"}}, nil
}

func (f *fakeManage) QueryLog(_ context.Context, q agentproto.LogQuery) ([]agentproto.LogEvent, bool, error) {
	f.mu.Lock()
	f.query = q
	f.mu.Unlock()
	ev := []agentproto.LogEvent{
		{Time: time.Now().UTC(), Level: 2, Provider: "Service Control Manager", EventID: 7031, Message: "The Print Spooler service terminated unexpectedly."},
		{Time: time.Now().UTC(), Level: 4, Provider: "Kernel-General", EventID: 1, Message: "The system time has changed."},
	}
	return ev[:min(len(ev), q.Max)], len(ev) > q.Max, nil
}

func (f *fakeManage) Roots() ([]agentproto.FileEntry, error) {
	return []agentproto.FileEntry{{Name: "root", Path: f.root, Dir: true, Drive: "fixed"}}, nil
}

// agentErr mimics the agent's coded errors through the public result.
type agentErr struct{ code string }

func (e *agentErr) Error() string { return e.code }

func jsonUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
}

func waitOnline(t *testing.T, c *client) string {
	t.Helper()
	var list deviceList
	for range 200 {
		if c.do("GET", "/api/v1/devices?status=online", nil, &list); list.Total == 1 {
			return list.Items[0].ID
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("agent did not come online")
	return ""
}

func waitTransfer(t *testing.T, c *client, id string) transfer {
	t.Helper()
	var tr transfer
	for range 400 {
		c.do("GET", "/api/v1/transfers/"+id, nil, &tr)
		if tr.State == transferReady || tr.State == transferFailed {
			return tr
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("transfer %s: %+v", id, tr)
	return tr
}

func (c *client) raw(method, path string, body io.Reader) (*http.Response, []byte) {
	c.e.t.Helper()
	req, _ := http.NewRequest(method, c.e.ts.URL+path, body)
	req.Header.Set("X-Requested-With", "fleetwarden")
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, data
}

func TestManageWithoutTakeover(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	root := t.TempDir()
	fm := &fakeManage{root: root}
	a := newTestAgent(t, e, info.Token, "PC-01", false)
	a.Manage = fm
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go a.Run(ctx)
	id := waitOnline(t, admin)
	base := "/api/v1/devices/" + id + "/manage"

	// Processes: the agent marks itself.
	var pl processList
	if code := admin.do("GET", base+"/processes", nil, &pl); code != 200 || len(pl.Processes) != 3 {
		t.Fatalf("processes: %d %+v", code, pl)
	}
	for _, p := range pl.Processes {
		if p.PID == os.Getpid() && (!p.Agent || !p.Protected) {
			t.Errorf("agent process not marked: %+v", p)
		}
	}
	if code := admin.do("POST", base+"/processes/4242/kill", map[string]string{"name": "notepad.exe"}, nil); code != 204 || len(fm.killed) != 1 {
		t.Errorf("kill: %d %v", code, fm.killed)
	}
	var ae apiErr
	if code := admin.do("POST", base+"/processes/"+itoa(int64(os.Getpid()))+"/kill", nil, &ae); code != 403 || ae.Error != "manage_protected" {
		t.Errorf("kill the agent: %d %+v", code, ae)
	}
	if code := admin.do("POST", base+"/processes/9/kill", nil, &ae); code != 422 {
		t.Errorf("kill unknown: %d %+v", code, ae)
	}

	// Services.
	var sl struct {
		Services []agentproto.Service `json:"services"`
	}
	if code := admin.do("GET", base+"/services", nil, &sl); code != 200 || len(sl.Services) != 2 || !sl.Services[0].Protected {
		t.Fatalf("services: %d %+v", code, sl)
	}
	if code := admin.do("POST", base+"/services/action", serviceActionReq{Name: "Spooler", Action: "restart"}, nil); code != 204 {
		t.Errorf("restart: %d", code)
	}
	if code := admin.do("POST", base+"/services/action", serviceActionReq{Name: "FleetWardenAgent", Action: "stop"}, &ae); code != 403 {
		t.Errorf("stop the agent: %d %+v", code, ae)
	}
	if code := admin.do("POST", base+"/services/action", serviceActionReq{Name: "Spooler", Action: "explode"}, nil); code != 400 {
		t.Errorf("bad action: %d", code)
	}
	if len(fm.actions) != 1 || fm.actions[0] != "Spooler:restart" {
		t.Errorf("service actions: %v", fm.actions)
	}

	// Event logs.
	var srcs struct {
		Sources []agentproto.LogSource `json:"sources"`
	}
	if code := admin.do("GET", base+"/logs", nil, &srcs); code != 200 || len(srcs.Sources) != 2 {
		t.Errorf("log sources: %d %+v", code, srcs)
	}
	var le logEvents
	if code := admin.do("GET", base+"/logs/events?source=System&level=2&hours=24&max=1&search=spooler", nil, &le); code != 200 || len(le.Events) != 1 || !le.Truncated {
		t.Errorf("events: %d %+v", code, le)
	}
	if fm.query.Source != "System" || fm.query.MaxLevel != 2 || fm.query.Search != "spooler" || fm.query.Since.IsZero() || fm.query.Max != 1 {
		t.Errorf("query at the agent: %+v", fm.query)
	}
	if code := admin.do("GET", base+"/logs/events", nil, nil); code != 400 {
		t.Errorf("events without a log: %d", code)
	}

	// Files: roots, folders, rename, delete.
	var fl fileList
	if code := admin.do("GET", base+"/files", nil, &fl); code != 200 || len(fl.Files) != 1 || fl.Files[0].Path != root {
		t.Fatalf("roots: %d %+v", code, fl)
	}
	sub := filepath.Join(root, "logs")
	if code := admin.do("POST", base+"/files/mkdir", pathReq{Path: sub}, nil); code != 200 {
		t.Fatalf("mkdir: %d", code)
	}
	if code := admin.do("POST", base+"/files/mkdir", pathReq{Path: sub}, &ae); code != 409 {
		t.Errorf("mkdir twice: %d %+v", code, ae)
	}
	if code := admin.do("POST", base+"/files/mkdir", pathReq{Path: "relative"}, &ae); code != 422 || ae.Error != "manage_invalid" {
		t.Errorf("relative path: %d %+v", code, ae)
	}
	big := make([]byte, agentproto.TransferChunk+12345) // two chunks
	rand.Read(big)
	os.WriteFile(filepath.Join(sub, "app.log"), big, 0o644)
	os.WriteFile(filepath.Join(sub, "small.txt"), []byte("hello"), 0o644)
	if code := admin.do("GET", base+"/files?path="+url.QueryEscape(sub), nil, &fl); code != 200 || len(fl.Files) != 2 || fl.Files[0].Name != "app.log" ||
		fl.Files[0].Size != int64(len(big)) {
		t.Fatalf("list: %d %+v", code, fl)
	}
	if code := admin.do("POST", base+"/files/rename", pathReq{Path: filepath.Join(sub, "small.txt"), NewPath: filepath.Join(sub, "app.log")}, &ae); code != 409 {
		t.Errorf("rename over an existing file: %d", code)
	}
	if code := admin.do("POST", base+"/files/rename", pathReq{Path: filepath.Join(sub, "small.txt"), NewPath: filepath.Join(sub, "note.txt")}, nil); code != 200 {
		t.Errorf("rename: %d", code)
	}
	if code := admin.do("POST", base+"/files/delete", pathReq{Path: root}, &ae); code != 403 && code != 409 {
		// root is a temp dir (not a drive root), so it can only fail as "not empty".
		t.Errorf("delete root without recursive: %d %+v", code, ae)
	}

	// Download a file (two chunks).
	var tr transfer
	if code := admin.do("POST", base+"/files/download", pathReq{Path: filepath.Join(sub, "app.log")}, &tr); code != 202 {
		t.Fatalf("download: %d %+v", code, tr)
	}
	tr = waitTransfer(t, admin, tr.ID)
	if tr.State != transferReady || tr.Size != int64(len(big)) || tr.Name != "app.log" {
		t.Fatalf("download: %+v", tr)
	}
	res, data := admin.raw("GET", "/api/v1/transfers/"+tr.ID+"/file", nil)
	if res.StatusCode != 200 || !bytes.Equal(data, big) || !strings.Contains(res.Header.Get("Content-Disposition"), "app.log") {
		t.Errorf("file: %d %d bytes %q", res.StatusCode, len(data), res.Header.Get("Content-Disposition"))
	}

	// Download a folder as a zip file.
	if code := admin.do("POST", base+"/files/download", pathReq{Path: sub}, &tr); code != 202 {
		t.Fatalf("download folder: %d", code)
	}
	tr = waitTransfer(t, admin, tr.ID)
	if tr.State != transferReady || !tr.Zip || tr.Name != "logs.zip" {
		t.Fatalf("zip: %+v", tr)
	}
	_, data = admin.raw("GET", "/api/v1/transfers/"+tr.ID+"/file", nil)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["logs/app.log"] || !names["logs/note.txt"] {
		t.Errorf("zip entries: %v", names)
	}

	// A missing file fails.
	if code := admin.do("POST", base+"/files/download", pathReq{Path: filepath.Join(sub, "nope")}, &tr); code != 202 {
		t.Fatal(code)
	}
	if tr = waitTransfer(t, admin, tr.ID); tr.State != transferFailed || tr.Error != "manage_not_found" {
		t.Errorf("missing file: %+v", tr)
	}

	// Upload a file with a non-ASCII name; a second upload needs overwrite.
	q := "?dir=" + url.QueryEscape(sub) + "&name=" + url.QueryEscape("rapport é.txt")
	res, data = admin.raw("POST", base+"/files/upload"+q, strings.NewReader("contents"))
	if res.StatusCode != 202 {
		t.Fatalf("upload: %d %s", res.StatusCode, data)
	}
	var up transfer
	jsonUnmarshal(t, data, &up)
	if up = waitTransfer(t, admin, up.ID); up.State != transferReady {
		t.Fatalf("upload: %+v", up)
	}
	if got, _ := os.ReadFile(filepath.Join(sub, "rapport é.txt")); string(got) != "contents" {
		t.Errorf("uploaded file: %q", got)
	}
	res, data = admin.raw("POST", base+"/files/upload"+q, strings.NewReader("new"))
	jsonUnmarshal(t, data, &up)
	if up = waitTransfer(t, admin, up.ID); up.State != transferFailed || up.Error != "manage_exists" {
		t.Errorf("upload without overwrite: %+v", up)
	}
	res, data = admin.raw("POST", base+"/files/upload"+q+"&overwrite=1", strings.NewReader("new"))
	jsonUnmarshal(t, data, &up)
	if up = waitTransfer(t, admin, up.ID); up.State != transferReady {
		t.Errorf("overwrite: %+v", up)
	}
	if got, _ := os.ReadFile(filepath.Join(sub, "rapport é.txt")); string(got) != "new" {
		t.Errorf("overwritten file: %q", got)
	}
	if res, _ := admin.raw("POST", base+"/files/upload?dir="+url.QueryEscape(sub)+"&name="+url.QueryEscape("../x"), strings.NewReader("x")); res.StatusCode != 400 {
		t.Errorf("name with a folder: %d", res.StatusCode)
	}

	// Recursive delete.
	if code := admin.do("POST", base+"/files/delete", pathReq{Path: sub}, &ae); code != 409 {
		t.Errorf("delete a full folder: %d %+v", code, ae)
	}
	if code := admin.do("POST", base+"/files/delete", pathReq{Path: sub, Recursive: true}, nil); code != 200 {
		t.Errorf("recursive delete: %d", code)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("folder still there: %v", err)
	}

	// An agent cannot touch another device's transfer, and needs a signature.
	if res, _ := e.client().raw("GET", agentproto.PathTransfer+tr.ID, nil); res.StatusCode != 401 {
		t.Errorf("unsigned transfer fetch: %d", res.StatusCode)
	}

	// The audit log has the actions.
	entries, err := e.st.ListAudit(context.Background(), store.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, en := range entries {
		seen[en.Action] = true
	}
	for _, act := range []string{"device.process.kill", "device.service.restart", "device.file.download", "device.file.upload", "device.file.delete", "device.file.rename", "device.file.mkdir"} {
		if !seen[act] {
			t.Errorf("no audit entry %s", act)
		}
	}

	// Read-only users may not.
	var created userWithPassword
	admin.do("POST", "/api/v1/users", createUserReq{Username: "viewer", Role: "readonly"}, &created)
	viewer := e.client()
	viewer.login("viewer", created.OneTimePassword)
	viewer.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil)
	if code := viewer.do("GET", base+"/processes", nil, nil); code != 403 {
		t.Errorf("readonly processes: %d", code)
	}

	// Offline.
	cancel()
	for range 200 {
		if !e.srv.hub.online(id) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if code := admin.do("GET", base+"/services", nil, &ae); code != 409 || ae.Error != "device_offline" {
		t.Errorf("offline: %d %+v", code, ae)
	}
}
