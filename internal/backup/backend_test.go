package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// exercise runs the same checks against every backend, then a backup and
// restore through it.
func exercise(t *testing.T, be Backend) {
	t.Helper()
	ctx := context.Background()
	if _, err := be.Get(ctx, "nope/x"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Get missing: %v", err)
	}
	if list, err := be.List(ctx, "nope"); err != nil || len(list) != 0 {
		t.Fatalf("List missing: %v %v", list, err)
	}
	big := randomData(9, 5<<20) // above the OneDrive simple-upload limit
	if err := be.Put(ctx, "a/b/one", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(ctx, "a/b/one", []byte("hello2")); err != nil {
		t.Fatal("overwrite:", err)
	}
	if err := be.Put(ctx, "a/b/big", big); err != nil {
		t.Fatal(err)
	}
	got, err := be.Get(ctx, "a/b/one")
	if err != nil || string(got) != "hello2" {
		t.Fatalf("Get: %q %v", got, err)
	}
	if got, _ := be.Get(ctx, "a/b/big"); !bytes.Equal(got, big) {
		t.Fatal("big object differs")
	}
	list, err := be.List(ctx, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "big,one" {
		t.Fatalf("List: %v", names)
	}
	if list, _ := be.List(ctx, "a"); len(list) != 1 || !list[0].Dir {
		t.Fatalf("List dirs: %+v", list)
	}
	if err := be.Delete(ctx, "a/b/one"); err != nil {
		t.Fatal(err)
	}
	if err := be.Delete(ctx, "a/b/one"); err != nil {
		t.Fatal("delete twice:", err)
	}
	if err := be.RemoveAll(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := be.List(ctx, "a"); len(list) != 0 {
		t.Fatalf("after RemoveAll: %+v", list)
	}

	r, err := OpenRepo(ctx, be, "pass", true)
	if err != nil {
		t.Fatal(err)
	}
	data := randomData(10, 3<<20)
	chain := NewChainID("abcdef01", time.Now())
	if _, err := r.Write(ctx, chain, KindFull, time.Now(), bytes.NewReader(data), Manifest{}, nil); err != nil {
		t.Fatal(err)
	}
	r2, err := OpenRepo(ctx, be, "pass", false)
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := r2.Snapshots(ctx)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("%v %v", snaps, err)
	}
	if !bytes.Equal(readAll(t, r2, snaps[0]), data) {
		t.Fatal("restore differs")
	}
	if err := r2.DeleteChain(ctx, chain); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBackend(t *testing.T) {
	be, err := Open(context.Background(), Location{Type: TypeLocal, Path: "loc"}, Options{LocalBase: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, be)
}

func TestLocationValidate(t *testing.T) {
	for _, l := range []Location{
		{Type: "nfs"},
		{Type: TypeLocal},
		{Type: TypeLocal, Path: "../etc"},
		{Type: TypeSFTP, Host: "h", Port: 22},
		{Type: TypeOneDrive, TenantID: "x", ClientID: "y", ClientSecret: "s", DriveUser: "u"},
	} {
		l.Normalize()
		if l.Validate() == nil {
			t.Errorf("%+v accepted", l)
		}
	}
	l := Location{Type: "SFTP", Host: " h ", Username: "u", Path: "/backups/fw/"}
	l.Normalize()
	if l.Validate() != nil || l.Port != 22 || l.Path != "backups/fw" || l.Host != "h" {
		t.Fatalf("%+v", l)
	}
}

// ---- SFTP: an in-process SSH server with the SFTP subsystem ----

func sftpServer(t *testing.T, root string) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == "backup" && string(pw) == "secret" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, reqs, _ := nch.Accept()
					go func() {
						for req := range reqs {
							ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp"
							req.Reply(ok, nil)
							if ok {
								srv, _ := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root))
								srv.Serve()
								ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

func TestSFTPBackend(t *testing.T) {
	root := t.TempDir()
	addr, key := sftpServer(t, root)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	l := Location{Type: TypeSFTP, Host: host, Port: port, Username: "backup", Password: "secret", Path: "fleetwarden"}
	ctx := context.Background()
	// Unknown host key: a TrustError with the key to pin.
	_, err := Open(ctx, l, Options{})
	var te *TrustError
	if !errors.As(err, &te) || te.Kind != "ssh" || te.Mismatch || te.Fingerprint != ssh.FingerprintSHA256(key) {
		t.Fatalf("expected trust error, got %v", err)
	}
	l.HostKey = te.Value
	be, err := Open(ctx, l, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	exercise(t, be)
	if _, err := os.Stat(root + "/fleetwarden/fleetwarden-backup.json"); err != nil {
		t.Fatal("repository not in the configured folder:", err)
	}
	// A changed host key is refused.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	op, _ := ssh.NewPublicKey(other)
	l.HostKey = string(ssh.MarshalAuthorizedKey(op))
	if _, err := Open(ctx, l, Options{}); !errors.As(err, &te) || !te.Mismatch {
		t.Fatalf("changed key accepted: %v", err)
	}
	// Wrong password.
	l.HostKey, l.Password = te.Value, "wrong"
	if _, err := Open(ctx, l, Options{}); err == nil {
		t.Fatal("wrong password accepted")
	}
}

func TestGenerateSSHKey(t *testing.T) {
	priv, pub, err := GenerateSSHKey("fleetwarden")
	if err != nil {
		t.Fatal(err)
	}
	got, err := PublicKeyOf(priv)
	if err != nil || !strings.HasPrefix(pub, got) || !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Fatalf("%q %q %v", pub, got, err)
	}
}

// ---- OneDrive: a fake Microsoft Graph ----

type fakeGraph struct {
	mu    sync.Mutex
	files map[string][]byte // path below the drive root
	srv   *httptest.Server
}

func newFakeGraph(t *testing.T) *fakeGraph {
	g := &fakeGraph{files: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_secret") != "s3cret" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("PUT /upload/{id...}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(400) // pre-authenticated URL: no token
			return
		}
		p, _ := url.PathUnescape(r.PathValue("id"))
		body, _ := io.ReadAll(r.Body)
		var from, to, total int
		if _, err := fmtSscanf(r.Header.Get("Content-Range"), &from, &to, &total); err != nil {
			w.WriteHeader(400)
			return
		}
		g.mu.Lock()
		cur := g.files[p+".partial"]
		cur = append(cur[:from], body...)
		if to+1 == total {
			g.files[p] = cur
			delete(g.files, p+".partial")
		} else {
			g.files[p+".partial"] = cur
		}
		g.mu.Unlock()
		w.WriteHeader(202)
	})
	mux.HandleFunc("GET /download/{id...}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(400)
			return
		}
		p, _ := url.PathUnescape(r.PathValue("id"))
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Write(g.files[p])
	})
	mux.HandleFunc("/v1.0/users/{user}/drive/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if r.PathValue("user") != "backup@example.com" {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"code":"itemNotFound","message":"user"}}`)
			return
		}
		g.drive(w, r, r.PathValue("rest"))
	})
	mux.HandleFunc("GET /v1.0/users/{user}/drive", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if r.PathValue("user") != "backup@example.com" {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"code":"ResourceNotFound","message":"User not found"}}`)
			return
		}
		io.WriteString(w, `{"id":"drive1"}`)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func fmtSscanf(s string, a, b, c *int) (int, error) {
	s = strings.TrimPrefix(s, "bytes ")
	rng, tot, ok := strings.Cut(s, "/")
	f, l, ok2 := strings.Cut(rng, "-")
	if !ok || !ok2 {
		return 0, errors.New("bad range")
	}
	var err error
	if *a, err = strconv.Atoi(f); err != nil {
		return 0, err
	}
	if *b, err = strconv.Atoi(l); err != nil {
		return 0, err
	}
	*c, err = strconv.Atoi(tot)
	return 3, err
}

func (g *fakeGraph) drive(w http.ResponseWriter, r *http.Request, rest string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	notFound := func() {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"code":"itemNotFound","message":"not found"}}`)
	}
	var p, op string
	switch {
	case rest == "root/children":
		op = "children"
	case strings.HasPrefix(rest, "root:/"):
		raw := strings.TrimPrefix(r.URL.EscapedPath()[strings.Index(r.URL.EscapedPath(), "root:/")+6:], "")
		if i := strings.LastIndex(raw, ":"); i >= 0 {
			p, op = raw[:i], strings.TrimPrefix(raw[i+1:], "/")
		} else {
			p = raw
		}
		p, _ = url.PathUnescape(p)
	default:
		notFound()
		return
	}
	isDir := func(d string) bool {
		for k := range g.files {
			if strings.HasPrefix(k, d+"/") {
				return true
			}
		}
		return false
	}
	switch {
	case r.Method == "PUT" && op == "content":
		body, _ := io.ReadAll(r.Body)
		if len(body) > smallUpload {
			w.WriteHeader(413)
			return
		}
		g.files[p] = body
		w.WriteHeader(201)
		io.WriteString(w, `{}`)
	case r.Method == "POST" && op == "createUploadSession":
		json.NewEncoder(w).Encode(map[string]string{"uploadUrl": g.srv.URL + "/upload/" + url.PathEscape(p)})
	case r.Method == "GET" && op == "content":
		if _, ok := g.files[p]; !ok {
			notFound()
			return
		}
		w.Header().Set("Location", g.srv.URL+"/download/"+url.PathEscape(p))
		w.WriteHeader(302)
	case r.Method == "GET" && op == "children":
		if p != "" && !isDir(p) {
			notFound()
			return
		}
		seen := map[string]bool{}
		var items []map[string]any
		prefix := ""
		if p != "" {
			prefix = p + "/"
		}
		for k, v := range g.files {
			if !strings.HasPrefix(k, prefix) || strings.HasSuffix(k, ".partial") {
				continue
			}
			name, sub, nested := strings.Cut(strings.TrimPrefix(k, prefix), "/")
			_ = sub
			if seen[name] {
				continue
			}
			seen[name] = true
			if nested {
				items = append(items, map[string]any{"name": name, "folder": map[string]int{"childCount": 1}})
			} else {
				items = append(items, map[string]any{"name": name, "size": len(v)})
			}
		}
		// Page through results like Graph does.
		sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		resp := map[string]any{}
		end := min(skip+2, len(items))
		resp["value"] = items[min(skip, len(items)):end]
		if end < len(items) {
			q := r.URL.Query()
			q.Set("skip", strconv.Itoa(end))
			resp["@odata.nextLink"] = g.srv.URL + r.URL.Path + "?" + q.Encode()
		}
		json.NewEncoder(w).Encode(resp)
	case r.Method == "DELETE" && op == "":
		found := false
		for k := range g.files {
			if k == p || strings.HasPrefix(k, p+"/") {
				delete(g.files, k)
				found = true
			}
		}
		if !found {
			notFound()
			return
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(400)
	}
	_ = path.Base
}

func TestOneDriveBackend(t *testing.T) {
	g := newFakeGraph(t)
	o := Options{GraphURL: g.srv.URL + "/v1.0", TokenURL: g.srv.URL + "/token"}
	l := Location{Type: TypeOneDrive, TenantID: "11111111-1111-1111-1111-111111111111", ClientID: "22222222-2222-2222-2222-222222222222",
		ClientSecret: "s3cret", DriveUser: "backup@example.com", Path: "Backups/Fleet Warden"}
	ctx := context.Background()
	be, err := Open(ctx, l, o)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, be)
	bad := l
	bad.ClientSecret = "wrong"
	if _, err := Open(ctx, bad, o); err == nil {
		t.Fatal("wrong secret accepted")
	}
	bad = l
	bad.DriveUser = "nobody@example.com"
	if _, err := Open(ctx, bad, o); err == nil {
		t.Fatal("unknown user accepted")
	}
}

// ---- FTP and FTPS: needs a server, see CONTRIBUTING.md ----

func TestFTPBackend(t *testing.T) {
	addr := os.Getenv("FW_TEST_FTP_ADDR") // host:port of a plain FTP server, user fwtest / fwtest
	if addr == "" {
		t.Skip("FW_TEST_FTP_ADDR not set")
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	l := Location{Type: TypeFTP, Host: host, Port: port, Username: "fwtest", Password: "fwtest", Path: "ftp-test"}
	be, err := Open(context.Background(), l, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	exercise(t, be)
}

func TestFTPSBackend(t *testing.T) {
	addr := os.Getenv("FW_TEST_FTPS_ADDR") // explicit TLS with a self-signed certificate
	if addr == "" {
		t.Skip("FW_TEST_FTPS_ADDR not set")
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	l := Location{Type: TypeFTPS, Host: host, Port: port, Username: "fwtest", Password: "fwtest", Path: "ftps-test"}
	ctx := context.Background()
	_, err := Open(ctx, l, Options{})
	var te *TrustError
	if !errors.As(err, &te) || te.Kind != "tls" {
		t.Fatalf("expected a trust error for the self-signed certificate, got %v", err)
	}
	l.TLSFingerprint = te.Value
	be, err := Open(ctx, l, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer be.Close()
	exercise(t, be)
}
