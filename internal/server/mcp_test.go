package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpSession connects an MCP client with a token, the way Claude does.
func (e *env) mcpSession(ctx context.Context, token string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}}}, nil)
}

func toolNames(t *testing.T, ctx context.Context, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range res.Tools {
		out = append(out, x.Name)
	}
	return out
}

func callTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPServer(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Off by default: no endpoint, no tokens.
	res, err := http.Post(e.ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil || res.StatusCode != 404 {
		t.Fatalf("disabled endpoint: %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	if code := admin.do("POST", "/api/v1/mcp/tokens", mcpTokenReq{Name: "Claude", Access: "read"}, nil); code != 409 {
		t.Errorf("token while disabled: %d", code)
	}

	var sv mcpSettingsView
	if code := admin.do("PUT", "/api/v1/mcp/settings", mcpSettings{Enabled: true, MaxLevel: "read", OAuth: true, DisabledTools: []string{"get_report", "nope"}}, &sv); code != 200 ||
		!sv.Enabled || len(sv.DisabledTools) != 1 || !strings.HasSuffix(sv.URL, "/mcp") || len(sv.Tools) < 30 {
		t.Fatalf("enable: %d %+v", code, sv.mcpSettings)
	}
	if code := admin.do("POST", "/api/v1/mcp/tokens", mcpTokenReq{Name: "Claude", Access: "write"}, nil); code != 400 {
		t.Errorf("write token above the maximum: %d", code)
	}
	var tok mcpTokenCreated
	if code := admin.do("POST", "/api/v1/mcp/tokens", mcpTokenReq{Name: "Claude Desktop", Access: "read", ExpiresDays: 30}, &tok); code != 201 ||
		!strings.HasPrefix(tok.Secret, "fwm_") || tok.ExpiresAt == nil {
		t.Fatalf("create token: %d %+v", code, tok)
	}

	// Without or with a wrong token: 401 that points at the OAuth metadata.
	req, _ := http.NewRequest("POST", e.ts.URL+"/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer fwm_wrong")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Errorf("wrong token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}

	// A device to work with.
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-MCP1", false)
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-MCP1")

	cs, err := e.mcpSession(ctx, tok.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if cs.InitializeResult().Instructions == "" {
		t.Error("no server instructions")
	}
	names := toolNames(t, ctx, cs)
	for _, want := range []string{"get_overview", "list_devices", "get_device", "list_vulnerabilities", "list_processes", "get_audit_log"} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %s missing from %v", want, names)
		}
	}
	for _, no := range []string{"get_report", "request_scan", "restart_device"} {
		if slices.Contains(names, no) {
			t.Errorf("tool %s offered with a read token", no)
		}
	}
	out, isErr := callTool(t, ctx, cs, "list_devices", map[string]any{"search": "PC-MCP"})
	if isErr || !strings.Contains(out, d.ID) {
		t.Errorf("list_devices: %v %s", isErr, out)
	}
	out, isErr = callTool(t, ctx, cs, "get_device", map[string]any{"device": "pc-mcp1"})
	if isErr || !strings.Contains(out, `"hostname":"PC-MCP1"`) {
		t.Errorf("get_device by host name: %v %s", isErr, out)
	}
	if out, isErr = callTool(t, ctx, cs, "get_device", map[string]any{"device": "PC-NONE"}); !isErr || !strings.Contains(out, "no device") {
		t.Errorf("unknown device: %v %s", isErr, out)
	}
	if out, isErr = callTool(t, ctx, cs, "get_overview", nil); isErr || !strings.Contains(out, `"devices"`) {
		t.Errorf("overview: %v %s", isErr, out)
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "request_scan", Arguments: map[string]any{"device": d.ID, "kind": "inventory"}}); err == nil {
		t.Error("a tool that is not offered ran")
	}

	// Every call is in the audit log under the user, with the token's name.
	entries, err := e.st.ListAudit(ctx, store.AuditQuery{Search: "mcp.tool", Limit: 50})
	if err != nil || len(entries) < 4 {
		t.Fatalf("audit: %d %v", len(entries), err)
	}
	if entries[0].Actor != "admin" || entries[0].Details["token"] != "Claude Desktop" {
		t.Errorf("audit entry: %+v", entries[0])
	}

	// Write and full levels.
	admin.do("PUT", "/api/v1/mcp/settings", mcpSettings{Enabled: true, MaxLevel: "full", OAuth: true}, nil)
	var full mcpTokenCreated
	admin.do("POST", "/api/v1/mcp/tokens", mcpTokenReq{Name: "Ops", Access: "full"}, &full)
	fs, err := e.mcpSession(ctx, full.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	names = toolNames(t, ctx, fs)
	if !slices.Contains(names, "restart_device") || !slices.Contains(names, "request_scan") {
		t.Errorf("full token tools: %v", names)
	}
	if out, isErr = callTool(t, ctx, fs, "request_scan", map[string]any{"device": "PC-MCP1", "kind": "inventory"}); isErr {
		t.Errorf("request_scan: %s", out)
	}
	if out, isErr = callTool(t, ctx, fs, "restart_device", map[string]any{"device": "PC-MCP1", "action": "restart", "confirm": "PC-OTHER"}); !isErr || !strings.Contains(out, "confirm") {
		t.Errorf("restart without the right confirm: %v %s", isErr, out)
	}
	// The read token stays read-only even now the maximum is higher.
	if slices.Contains(toolNames(t, ctx, cs), "request_scan") {
		t.Error("read token got write tools")
	}

	// A read-only user sees fewer tools; a token never exceeds the role.
	ro := e.newUserClient(admin, "viewer", "readonly")
	var rt mcpTokenCreated
	if code := ro.do("POST", "/api/v1/mcp/tokens", mcpTokenReq{Name: "Mine", Access: "full"}, &rt); code != 201 {
		t.Fatalf("readonly token: %d", code)
	}
	rs, err := e.mcpSession(ctx, rt.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	names = toolNames(t, ctx, rs)
	for _, no := range []string{"list_processes", "get_audit_log", "request_scan", "restart_device", "deploy_software"} {
		if slices.Contains(names, no) {
			t.Errorf("readonly user got %s", no)
		}
	}
	var mine struct{ Tokens []store.MCPToken }
	ro.do("GET", "/api/v1/mcp/tokens", nil, &mine)
	if len(mine.Tokens) != 1 {
		t.Errorf("own tokens: %+v", mine.Tokens)
	}
	if code := ro.do("GET", "/api/v1/mcp/tokens?all=1", nil, nil); code != 403 {
		t.Errorf("readonly lists all: %d", code)
	}
	if code := ro.do("DELETE", "/api/v1/mcp/tokens/"+itoa(tok.ID), nil, nil); code != 404 {
		t.Errorf("readonly revokes admin token: %d", code)
	}
	var all struct{ Tokens []store.MCPToken }
	admin.do("GET", "/api/v1/mcp/tokens?all=1", nil, &all)
	if len(all.Tokens) != 3 || all.Tokens[0].LastUsedAt == nil {
		t.Errorf("all tokens: %+v", all.Tokens)
	}

	// Revoked: the next request fails.
	if code := admin.do("DELETE", "/api/v1/mcp/tokens/"+itoa(tok.ID), nil, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if _, err := cs.ListTools(ctx, nil); err == nil {
		t.Error("revoked token still works")
	}

	// Turning the server off closes the endpoint for every token.
	admin.do("PUT", "/api/v1/mcp/settings", mcpSettings{Enabled: false, MaxLevel: "full"}, nil)
	if _, err := fs.ListTools(ctx, nil); err == nil {
		t.Error("disabled server still answers")
	}
}

func TestMCPOAuth(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	getJSON := func(path string, out any) int {
		res, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(raw, out)
		return res.StatusCode
	}
	var meta map[string]any
	if code := getJSON("/.well-known/oauth-authorization-server", &meta); code != 404 {
		t.Errorf("metadata while disabled: %d", code)
	}
	admin.do("PUT", "/api/v1/mcp/settings", mcpSettings{Enabled: true, MaxLevel: "write", OAuth: true}, nil)
	if code := getJSON("/.well-known/oauth-protected-resource/mcp", &meta); code != 200 || meta["resource"] != e.ts.URL+"/mcp" {
		t.Fatalf("resource metadata: %d %v", code, meta)
	}
	if code := getJSON("/.well-known/oauth-authorization-server", &meta); code != 200 || meta["registration_endpoint"] != e.ts.URL+"/oauth/register" {
		t.Fatalf("server metadata: %d %v", code, meta)
	}

	post := func(path, ctype, body string, out any) int {
		res, err := http.Post(e.ts.URL+path, ctype, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(raw, out)
		return res.StatusCode
	}
	if code := post("/oauth/register", "application/json", `{"client_name":"Evil","redirect_uris":["http://evil.example/cb"]}`, nil); code != 400 {
		t.Errorf("plain http redirect accepted: %d", code)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	const cb = "https://claude.ai/api/mcp/auth_callback"
	if code := post("/oauth/register", "application/json", `{"client_name":"Claude","redirect_uris":["`+cb+`"]}`, &reg); code != 201 || reg.ClientID == "" {
		t.Fatalf("register: %d %+v", code, reg)
	}

	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {cb}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "state": {"xyz"}, "scope": {"read"}}
	nc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := nc.Get(e.ts.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, mcpConsentURL+"?") {
		t.Fatalf("authorize: %d %s", res.StatusCode, loc)
	}
	bad := q
	bad.Set("redirect_uri", "https://evil.example/cb")
	if res, _ := nc.Get(e.ts.URL + "/oauth/authorize?" + bad.Encode()); res.StatusCode != 400 {
		t.Errorf("unregistered redirect: %d", res.StatusCode)
	}

	var cv mcpConsentView
	if code := admin.do("GET", "/api/v1/mcp/oauth/request?client_id="+reg.ClientID+"&redirect_uri="+url.QueryEscape(cb)+"&scope=read", nil, &cv); code != 200 ||
		cv.ClientName != "Claude" || cv.Requested != "read" || cv.RedirectHost != "https://claude.ai" {
		t.Fatalf("consent request: %d %+v", code, cv)
	}
	if code := admin.do("POST", "/api/v1/mcp/oauth/approve", mcpApproveReq{ClientID: reg.ClientID, RedirectURI: cb, CodeChallenge: challenge, Access: "full"}, nil); code != 400 {
		t.Errorf("approve above the maximum: %d", code)
	}
	var ap struct{ Redirect string }
	if code := admin.do("POST", "/api/v1/mcp/oauth/approve", mcpApproveReq{ClientID: reg.ClientID, RedirectURI: cb, CodeChallenge: challenge, State: "xyz", Access: "read"}, &ap); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	ru, _ := url.Parse(ap.Redirect)
	code := ru.Query().Get("code")
	if !strings.HasPrefix(ap.Redirect, cb+"?") || code == "" || ru.Query().Get("state") != "xyz" {
		t.Fatalf("redirect: %s", ap.Redirect)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {reg.ClientID}, "redirect_uri": {cb}, "code_verifier": {"wrong" + verifier}}
	var tr mcpTokenResp
	if c := post("/oauth/token", "application/x-www-form-urlencoded", form.Encode(), &tr); c != 400 {
		t.Errorf("wrong verifier: %d", c)
	}
	// A code works once, so a failed exchange used it up: approve again.
	admin.do("POST", "/api/v1/mcp/oauth/approve", mcpApproveReq{ClientID: reg.ClientID, RedirectURI: cb, CodeChallenge: challenge, Access: "read"}, &ap)
	ru, _ = url.Parse(ap.Redirect)
	form.Set("code", ru.Query().Get("code"))
	form.Set("code_verifier", verifier)
	if c := post("/oauth/token", "application/x-www-form-urlencoded", form.Encode(), &tr); c != 200 || !strings.HasPrefix(tr.AccessToken, "fwo_") || tr.Scope != "read" {
		t.Fatalf("token: %d %+v", c, tr)
	}
	if c := post("/oauth/token", "application/x-www-form-urlencoded", form.Encode(), nil); c != 400 {
		t.Errorf("code reused: %d", c)
	}

	cs, err := e.mcpSession(ctx, tr.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if out, isErr := callTool(t, ctx, cs, "list_groups", nil); isErr {
		t.Errorf("list_groups over OAuth: %s", out)
	}
	cs.Close()

	// Refresh rotates both tokens; the old ones stop working.
	old := tr
	rf := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {reg.ClientID}}
	if c := post("/oauth/token", "application/x-www-form-urlencoded", rf.Encode(), &tr); c != 200 || tr.AccessToken == old.AccessToken {
		t.Fatalf("refresh: %d", c)
	}
	if c := post("/oauth/token", "application/x-www-form-urlencoded", rf.Encode(), nil); c != 400 {
		t.Errorf("refresh token reused: %d", c)
	}
	if _, err := e.mcpSession(ctx, old.AccessToken); err == nil {
		t.Error("old access token still works")
	}
	var all struct{ Tokens []store.MCPToken }
	admin.do("GET", "/api/v1/mcp/tokens", nil, &all)
	if len(all.Tokens) != 1 || all.Tokens[0].Kind != "oauth" || all.Tokens[0].Name != "Claude" {
		t.Errorf("connections: %+v", all.Tokens)
	}
	// Revocation by the assistant ends the connection.
	post("/oauth/revoke", "application/x-www-form-urlencoded", url.Values{"token": {tr.RefreshToken}}.Encode(), nil)
	if _, err := e.mcpSession(ctx, tr.AccessToken); err == nil {
		t.Error("revoked connection still works")
	}
}
