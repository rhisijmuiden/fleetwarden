package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/db"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/secrets"
	"github.com/rhisijmuiden/fleetwarden/internal/sso"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
)

func init() { auth.Argon2Params.Memory, auth.Argon2Params.Time = 1024, 1 }

// testDB returns a store on an empty schema. Integration tests need
// FW_TEST_DATABASE_URL (CI provides a PostgreSQL service).
func testDB(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("FW_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("FW_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return store.New(pool)
}

type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, _ mail.Config, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

type env struct {
	t       *testing.T
	srv     *Server
	ts      *httptest.Server
	st      *store.Store
	cfg     config.Server
	mailer  *fakeMailer
	adminPW string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := testDB(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)
	trusted, _ := config.ParsePrefixes("127.0.0.1/32")
	envFile := filepath.Join(t.TempDir(), "server.env")
	os.WriteFile(envFile, []byte("FW_LISTEN=127.0.0.1:8080\n"), 0o640)
	cfg := config.Server{WebDir: dir, TrustedProxies: trusted, DataDir: t.TempDir(), UpdateStatusDir: t.TempDir(),
		DatabaseURL: os.Getenv("FW_TEST_DATABASE_URL"), EnvFile: envFile}
	box, err := secrets.LoadOrCreate(cfg.SecretKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	pw, err := EnsureAdmin(context.Background(), st, cfg.InitialPasswordPath())
	if err != nil || pw == "" {
		t.Fatalf("EnsureAdmin: %q %v", pw, err)
	}
	m := &fakeMailer{}
	// The catalog points at a closed port, so no test reaches the internet.
	wg := &catalog.Winget{Dir: t.TempDir(), BaseURL: "http://127.0.0.1:1"}
	srv := New(cfg, Deps{Store: st, DB: st, Secrets: box, Mailer: m, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Winget: wg})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, srv: srv, ts: ts, st: st, cfg: cfg, mailer: m, adminPW: pw}
}

type client struct {
	e *env
	c *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) do(method, path string, body any, out any) int {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, rd)
	req.Header.Set("X-Requested-With", "fleetwarden")
	req.Header.Set("Content-Type", "application/json")
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.e.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return res.StatusCode
}

func (c *client) login(user, pw string) (int, loginResp) {
	var r loginResp
	code := c.do("POST", "/api/v1/auth/login", loginReq{Username: user, Password: pw}, &r)
	return code, r
}

// signedInAdmin returns a client for the admin after the forced password change.
func (e *env) signedInAdmin() (*client, string) {
	c := e.client()
	if code, _ := c.login("admin", e.adminPW); code != 200 {
		e.t.Fatalf("admin login: %d", code)
	}
	newPW := "a-brand-new-password"
	if code := c.do("POST", "/api/v1/me/password", map[string]string{"current": e.adminPW, "new": newPW}, nil); code != 200 {
		e.t.Fatalf("password change: %d", code)
	}
	return c, newPW
}

func TestFirstLoginForcesPasswordChange(t *testing.T) {
	e := newEnv(t)
	if _, err := os.Stat(e.cfg.InitialPasswordPath()); err != nil {
		t.Fatal("initial password file missing")
	}
	c := e.client()
	if code, _ := c.login("admin", "wrong-password-123"); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	code, r := c.login("ADMIN", e.adminPW)
	if code != 200 || r.User == nil || !r.User.MustChangePassword || !r.User.BreakGlass {
		t.Fatalf("login: %d %+v", code, r.User)
	}
	if code := c.do("GET", "/api/v1/users", nil, nil); code != 403 {
		t.Errorf("API before password change: %d, want 403", code)
	}
	var apiE apiErr
	if code := c.do("POST", "/api/v1/me/password", map[string]string{"current": e.adminPW, "new": "short"}, &apiE); code != 400 || apiE.Error != "password_too_short" {
		t.Errorf("short password: %d %v", code, apiE)
	}
	if code := c.do("POST", "/api/v1/me/password", map[string]string{"current": e.adminPW, "new": "a-brand-new-password"}, nil); code != 200 {
		t.Fatalf("password change: %d", code)
	}
	if _, err := os.Stat(e.cfg.InitialPasswordPath()); !os.IsNotExist(err) {
		t.Error("initial password file not removed after the change")
	}
	var users []store.User
	if code := c.do("GET", "/api/v1/users", nil, &users); code != 200 || len(users) != 1 {
		t.Fatalf("users: %d %v", code, users)
	}
	// The old one-time password no longer works.
	if code, _ := e.client().login("admin", e.adminPW); code != 401 {
		t.Errorf("old password still works: %d", code)
	}
	var audit []store.AuditEntry
	c.do("GET", "/api/v1/audit", nil, &audit)
	if len(audit) != 4 || audit[1].Action != "user.password_change" || audit[0].Success {
		t.Errorf("audit log: %+v", audit)
	}
}

func TestCSRFAndAuthRequired(t *testing.T) {
	e := newEnv(t)
	res, err := http.Post(e.ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 403 {
		t.Errorf("POST without CSRF header: %d", res.StatusCode)
	}
	if code := e.client().do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Errorf("me without session: %d", code)
	}
}

func TestLocalRBAC(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	var created userWithPassword
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: "Viewer", Role: "readonly", Email: "v@example.com"}, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if created.OneTimePassword == "" || created.User.Username != "viewer" {
		t.Fatalf("created: %+v", created)
	}
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: "viewer", Role: "readonly"}, nil); code != 409 {
		t.Errorf("duplicate username: %d", code)
	}
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: "x y", Role: "readonly"}, nil); code != 400 {
		t.Errorf("bad username: %d", code)
	}
	viewer := e.client()
	viewer.login("viewer", created.OneTimePassword)
	if code := viewer.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil); code != 200 {
		t.Fatalf("viewer password change: %d", code)
	}
	var me meResp
	viewer.do("GET", "/api/v1/auth/me", nil, &me)
	if me.Role != "readonly" || len(me.Permissions) != 2 {
		t.Errorf("viewer: %+v", me)
	}
	for _, p := range []string{"/api/v1/users", "/api/v1/settings/mail", "/api/v1/audit", "/api/v1/system/update"} {
		if code := viewer.do("GET", p, nil, nil); code != 403 {
			t.Errorf("readonly GET %s: %d, want 403", p, code)
		}
	}
	// Promote to technician; the session is ended so the new role applies cleanly.
	role := "technician"
	if code := admin.do("PATCH", "/api/v1/users/"+itoa(created.User.ID), updateUserReq{Role: &role}, nil); code != 200 {
		t.Fatalf("promote: %d", code)
	}
	if code := viewer.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Errorf("session survived role change: %d", code)
	}
	// The last administrator cannot be demoted or disabled.
	admins, _ := e.st.ListUsers(context.Background())
	var adminID int64
	for _, u := range admins {
		if u.BreakGlass {
			adminID = u.ID
		}
	}
	if code := admin.do("DELETE", "/api/v1/users/"+itoa(adminID), nil, nil); code != 400 {
		t.Errorf("delete self: %d", code)
	}
	tru := true
	var ae apiErr
	if code := admin.do("PATCH", "/api/v1/users/"+itoa(adminID), updateUserReq{Disabled: &tru}, &ae); code != 400 {
		t.Errorf("disable self: %d %v", code, ae)
	}
	// Disable and delete the technician.
	if code := admin.do("PATCH", "/api/v1/users/"+itoa(created.User.ID), updateUserReq{Disabled: &tru}, nil); code != 200 {
		t.Errorf("disable: %d", code)
	}
	if code, _ := e.client().login("viewer", "another-secret-pass"); code != 401 {
		t.Errorf("disabled user logged in: %d", code)
	}
	if code := admin.do("DELETE", "/api/v1/users/"+itoa(created.User.ID), nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func jsonStr(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTOTP(t *testing.T) {
	e := newEnv(t)
	admin, pw := e.signedInAdmin()
	var setup map[string]string
	if code := admin.do("POST", "/api/v1/me/totp/setup", nil, &setup); code != 200 || setup["secret"] == "" {
		t.Fatalf("setup: %d", code)
	}
	now := time.Now()
	code0, _ := auth.TOTPCode(setup["secret"], now.Unix()/30-1)
	if code := admin.do("POST", "/api/v1/me/totp/enable", map[string]string{"code": "000000"}, nil); code != 400 {
		t.Errorf("enable with wrong code: %d", code)
	}
	if code := admin.do("POST", "/api/v1/me/totp/enable", map[string]string{"code": code0}, nil); code != 200 {
		t.Fatalf("enable: %d", code)
	}
	c := e.client()
	code, r := c.login("admin", pw)
	if code != 200 || !r.MFARequired {
		t.Fatalf("login with TOTP: %d %+v", code, r)
	}
	if code := c.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Errorf("me before TOTP: %d", code)
	}
	// The code used to enable TOTP cannot be replayed.
	if code := c.do("POST", "/api/v1/auth/totp", map[string]string{"code": code0}, nil); code != 401 {
		t.Errorf("replayed code: %d", code)
	}
	code1, _ := auth.TOTPCode(setup["secret"], now.Unix()/30)
	if code := c.do("POST", "/api/v1/auth/totp", map[string]string{"code": code1}, nil); code != 200 {
		t.Fatalf("totp: %d", code)
	}
	if code := c.do("GET", "/api/v1/auth/me", nil, nil); code != 200 {
		t.Errorf("me after TOTP: %d", code)
	}
	// Console reset clears TOTP and forces a new password.
	newPW, err := ResetAdmin(context.Background(), e.st, e.cfg.InitialPasswordPath())
	if err != nil {
		t.Fatal(err)
	}
	if code := c.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Errorf("session survived reset-admin: %d", code)
	}
	if code, r := e.client().login("admin", newPW); code != 200 || r.MFARequired || !r.User.MustChangePassword {
		t.Errorf("login after reset: %d %+v", code, r)
	}
}

func TestBreakGlassNetwork(t *testing.T) {
	e := newEnv(t)
	admin, pw := e.signedInAdmin()
	var ae apiErr
	if code := admin.do("PUT", "/api/v1/settings/authentication", authSettingsView{BreakGlassNetworks: []string{"10.0.0.0/8"}}, &ae); code != 400 || ae.Error != "network_excludes_you" {
		t.Fatalf("self lock-out not prevented: %d %v", code, ae)
	}
	if code := admin.do("PUT", "/api/v1/settings/authentication", authSettingsView{BreakGlassNetworks: []string{"127.0.0.1"}}, nil); code != 200 {
		t.Fatalf("save: %d", code)
	}
	// Through a trusted proxy from outside the allowed network.
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/auth/login", strings.NewReader(jsonStr(loginReq{Username: "admin", Password: pw})))
	req.Header.Set("X-Requested-With", "fleetwarden")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != 403 {
		t.Errorf("break-glass from outside: %d", res.StatusCode)
	}
	if code, _ := e.client().login("admin", pw); code != 200 {
		t.Errorf("break-glass from inside: %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for i := 0; i < 8; i++ {
		c.login("admin", "wrong-password-xx")
	}
	if code, _ := c.login("admin", e.adminPW); code != 429 {
		t.Errorf("after 8 failures: %d, want 429", code)
	}
}

func TestMailSettingsAndQueue(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	cfg := mail.Config{Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls", Auth: "plain",
		Username: "fw@example.com", Password: "smtp-secret", FromAddress: "fw@example.com", FromName: "FW"}
	var view mailView
	if code := admin.do("PUT", "/api/v1/settings/mail", cfg, &view); code != 200 || !view.PasswordSet || view.Password != "" {
		t.Fatalf("save: %d %+v", code, view)
	}
	// The password is stored encrypted.
	var raw map[string]any
	e.st.GetSetting(context.Background(), keyMail, &raw)
	if raw["password"] == "smtp-secret" || !strings.HasPrefix(raw["password"].(string), "v1:") {
		t.Errorf("password stored as %v", raw["password"])
	}
	// Saving without a password keeps the stored one.
	cfg.Password = ""
	admin.do("PUT", "/api/v1/settings/mail", cfg, &view)
	if got, _ := e.srv.loadMail(context.Background()); got.Password != "smtp-secret" {
		t.Errorf("password lost: %q", got.Password)
	}
	if code := admin.do("POST", "/api/v1/settings/mail/test", map[string]any{"to": "me@example.com", "host": "smtp.example.com", "port": 587,
		"security": "starttls", "auth": "plain", "username": "fw@example.com", "fromAddress": "fw@example.com"}, nil); code != 200 {
		t.Fatalf("test mail: %d", code)
	}
	if len(e.mailer.sent) != 1 || e.mailer.sent[0].To != "me@example.com" {
		t.Fatalf("sent: %+v", e.mailer.sent)
	}
	e.mailer.err = errors.New("535 authentication failed")
	var ae apiErr
	if code := admin.do("POST", "/api/v1/settings/mail/test", map[string]any{"to": "me@example.com", "host": "smtp.example.com", "port": 587,
		"security": "starttls", "auth": "plain", "username": "fw@example.com", "fromAddress": "fw@example.com"}, &ae); code != 502 || !strings.Contains(ae.Detail, "535") {
		t.Errorf("failing test mail: %d %+v", code, ae)
	}
	// Queue: fails first, then succeeds after a retry.
	e.srv.enqueue(context.Background(), "update_success", "nl", "a@example.com", mail.Brand{}, map[string]string{"version": "1.0.0"})
	e.srv.ProcessMailQueue(context.Background())
	var log []store.Mail
	admin.do("GET", "/api/v1/mail/log", nil, &log)
	if len(log) != 3 || log[0].Status != "queued" || log[0].Attempts != 1 {
		t.Fatalf("log after failure: %+v", log)
	}
	if !strings.Contains(log[0].LastError, "535") || log[1].Status != "failed" || log[2].Status != "sent" {
		t.Errorf("mail log: %+v", log)
	}
	if code := admin.do("POST", "/api/v1/mail/log/"+itoa(log[1].ID)+"/retry", nil, nil); code != 204 {
		t.Errorf("retry failed mail: %d", code)
	}
}

func TestBranding(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	if code := admin.do("PUT", "/api/v1/settings/branding", brandingSettings{ProductName: "Acme Fleet", PrimaryColor: "#ff0000"}, nil); code != 200 {
		t.Fatalf("save: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/settings/branding", brandingSettings{PrimaryColor: "red"}, nil); code != 400 {
		t.Errorf("bad colour: %d", code)
	}
	upload := func(body string) int {
		req, _ := http.NewRequest("PUT", e.ts.URL+"/api/v1/settings/branding/logo", strings.NewReader(body))
		req.Header.Set("X-Requested-With", "fleetwarden")
		res, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := upload("hello"); code != 400 {
		t.Errorf("non-image upload: %d", code)
	}
	if code := upload(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`); code != 200 {
		t.Fatalf("svg upload: %d", code)
	}
	var b brandingView
	e.client().do("GET", "/api/v1/branding", nil, &b)
	if b.ProductName != "Acme Fleet" || !b.HasLogo || b.PrimaryColor != "#ff0000" {
		t.Errorf("public branding: %+v", b)
	}
	res, _ := http.Get(e.ts.URL + "/api/v1/branding/logo")
	if res.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("logo headers: %v", res.Header)
	}
}

func TestUpdateFlow(t *testing.T) {
	e := newEnv(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v99.0.0","prerelease":false,"assets":[{"name":"fleetwarden-server_99.0.0_amd64.deb"}]},
			{"tag_name":"v99.0.0-test.1","prerelease":true,"assets":[{"name":"fleetwarden-server_99.0.0-test.1_amd64.deb"}]}]`))
	}))
	defer gh.Close()
	e.srv.updates.APIBase = gh.URL
	admin, _ := e.signedInAdmin()
	var v updateView
	if code := admin.do("PUT", "/api/v1/system/channel", map[string]string{"channel": "production"}, &v); code != 200 || v.Channel != "production" {
		t.Fatalf("channel: %d %+v", code, v)
	}
	if !v.UpdateAvailable || v.Latest.Version != "99.0.0" {
		t.Fatalf("update not offered: %+v", v)
	}
	if code := admin.do("POST", "/api/v1/system/update/apply", map[string]string{"version": "99.0.0-test.1"}, nil); code != 400 {
		t.Errorf("version from the other channel accepted: %d", code)
	}
	if code := admin.do("POST", "/api/v1/system/update/apply", map[string]string{"version": "99.0.0"}, &v); code != 202 || !v.Pending {
		t.Fatalf("apply: %d %+v", code, v)
	}
	raw, err := os.ReadFile(filepath.Join(e.cfg.UpdateRequestDir(), "request.json"))
	if err != nil || !strings.Contains(string(raw), `"99.0.0"`) {
		t.Fatalf("request file: %s %v", raw, err)
	}
	if code := admin.do("POST", "/api/v1/system/update/apply", map[string]string{"version": "99.0.0"}, nil); code != 409 {
		t.Errorf("second apply: %d", code)
	}
	// The updater reports a rollback; the server audits it once and mails admins.
	status := updates.Status{ID: "u1", State: "rolled-back", FromVersion: "0.1.0", ToVersion: "99.0.0", Message: "health check failed", UpdatedAt: time.Now()}
	sraw, _ := json.Marshal(status)
	os.WriteFile(filepath.Join(e.cfg.UpdateStatusDir, "status.json"), sraw, 0o644)
	email := "admin@example.com"
	admins, _ := e.st.ListUsers(context.Background())
	e.st.UpdateProfile(context.Background(), admins[0].ID, "Admin", email, "administrator", false)
	e.srv.reportUpdateOutcome(context.Background())
	e.srv.reportUpdateOutcome(context.Background())
	var log []store.Mail
	admin.do("GET", "/api/v1/mail/log", nil, &log)
	if len(log) != 1 || log[0].Kind != "update_failed" || log[0].To != email {
		t.Errorf("update mail: %+v", log)
	}
}

// ---- Entra SSO against a fake identity provider ----

type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	tenant string
	client string
	claims map[string]any
	nonce  string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: key, tenant: "11111111-2222-3333-4444-555555555555", client: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	iss := func() string { return f.srv.URL + "/" + f.tenant + "/v2.0" }
	mux.HandleFunc("/"+f.tenant+"/v2.0/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss(), "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		if r.Form.Get("grant_type") == "client_credentials" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "pkce", 400)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		c := map[string]any{"iss": iss(), "aud": f.client, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": f.nonce, "tid": f.tenant, "oid": "99999999-0000-0000-0000-000000000001", "preferred_username": "Jane@Contoso.com", "name": "Jane"}
		for k, v := range f.claims {
			c[k] = v
		}
		tok, _ := jwt.Signed(signer).Claims(c).Serialize()
		json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": 3600, "id_token": tok})
	})
	t.Cleanup(f.srv.Close)
	return f
}

func TestEntraSSO(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIdP(t)
	old := sso.Authority
	sso.Authority = idp.srv.URL
	defer func() { sso.Authority = old }()

	admin, _ := e.signedInAdmin()
	settings := authSettingsView{SSOEnabled: true, TenantID: idp.tenant, ClientID: idp.client, ClientSecret: "s3cret"}
	if code := admin.do("PUT", "/api/v1/settings/authentication", settings, nil); code != 200 {
		t.Fatalf("save sso: %d", code)
	}
	var ae apiErr
	if code := admin.do("POST", "/api/v1/settings/authentication/test", authSettingsView{}, &ae); code != 200 {
		t.Fatalf("test sso: %d %+v", code, ae)
	}
	group := "0b0b0b0b-0000-0000-0000-000000000001"
	if code := admin.do("POST", "/api/v1/settings/role-mappings", store.RoleMapping{Claim: "group", Value: group, Role: "technician"}, nil); code != 201 {
		t.Fatalf("mapping: %d", code)
	}
	admin.do("POST", "/api/v1/settings/role-mappings", store.RoleMapping{Claim: "role", Value: "FleetWarden.Admin", Role: "administrator"}, nil)

	signIn := func(claims map[string]any) (*client, string) {
		c := e.client()
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/auth/sso/start", nil)
		res, err := c.c.Do(req)
		if err != nil || res.StatusCode != 302 {
			t.Fatalf("start: %v %v", res, err)
		}
		loc, _ := url.Parse(res.Header.Get("Location"))
		if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") || loc.Query().Get("code_challenge") == "" {
			t.Fatalf("authorize URL %s", loc)
		}
		idp.nonce, idp.claims = loc.Query().Get("nonce"), claims
		cb := loc.Query().Get("redirect_uri") + "?code=abc&state=" + url.QueryEscape(loc.Query().Get("state"))
		res, err = c.c.Get(cb)
		if err != nil {
			t.Fatal(err)
		}
		return c, res.Header.Get("Location")
	}

	if _, loc := signIn(map[string]any{"groups": []string{"other"}}); loc != "/login?error=sso_no_role" {
		t.Errorf("unmapped user: %s", loc)
	}
	c, loc := signIn(map[string]any{"groups": []string{group}})
	if loc != "/" {
		t.Fatalf("mapped user: %s", loc)
	}
	var me meResp
	if code := c.do("GET", "/api/v1/auth/me", nil, &me); code != 200 || me.Role != "technician" || me.Source != "entra" || me.Username != "jane@contoso.com" {
		t.Fatalf("me: %d %+v", code, me)
	}
	// App role wins (more privileged) and is re-evaluated on the next login.
	c, _ = signIn(map[string]any{"groups": []string{group}, "roles": []string{"FleetWarden.Admin"}})
	c.do("GET", "/api/v1/auth/me", nil, &me)
	if me.Role != "administrator" {
		t.Errorf("role after app role: %s", me.Role)
	}
	// Removed from everything in Entra: no access any more.
	if _, loc := signIn(map[string]any{}); loc != "/login?error=sso_no_role" {
		t.Errorf("removed user: %s", loc)
	}
	// Tampered state is rejected.
	c2 := e.client()
	res, _ := c2.c.Get(e.ts.URL + "/api/v1/auth/sso/callback?code=abc&state=forged")
	if res.Header.Get("Location") != "/login?error=sso_expired" {
		t.Errorf("forged state: %s", res.Header.Get("Location"))
	}
	// Entra users cannot sign in with a password.
	if code, _ := e.client().login("jane@contoso.com", "anything-at-all"); code != 401 {
		t.Errorf("password login for entra user: %d", code)
	}
}
