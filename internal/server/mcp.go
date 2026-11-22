package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// The MCP server lets AI assistants such as Claude read and manage the fleet
// through the Model Context Protocol (streamable HTTP on /mcp). It is off by
// default. An assistant uses a token that acts as one user: a personal token
// made in the web interface, or an OAuth connection the user approved. Every
// tool calls the HTTP API as that user, so roles and device-group scopes
// apply, and every call is written to the audit log.

const (
	keyMCP       = "mcp"
	mcpMaxResult = 120_000 // bytes of tool output passed to the assistant
	mcpPath      = "/mcp"
)

type mcpSettings struct {
	Enabled bool `json:"enabled"`
	// MaxLevel is the most a token may do: read, write or full.
	MaxLevel      string   `json:"maxLevel"`
	DisabledTools []string `json:"disabledTools"`
	// OAuth lets assistants such as claude.ai connect with a sign-in instead
	// of a pasted token.
	OAuth bool `json:"oauth"`
}

func (s *Server) loadMCPSettings(ctx context.Context) mcpSettings {
	m := mcpSettings{MaxLevel: "read", OAuth: true, DisabledTools: []string{}}
	_ = s.st.GetSetting(ctx, keyMCP, &m)
	if _, ok := parseMCPLevel(m.MaxLevel); !ok {
		m.MaxLevel = "read"
	}
	if m.DisabledTools == nil {
		m.DisabledTools = []string{}
	}
	return m
}

func (s *Server) mcpRoutes(mux *http.ServeMux) {
	admin := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSettingsManage, 0, h) }
	user := func(h http.HandlerFunc) http.Handler { return s.authed("", 0, h) }
	mux.Handle("GET /api/v1/mcp/settings", admin(s.handleGetMCPSettings))
	mux.Handle("PUT /api/v1/mcp/settings", admin(s.handlePutMCPSettings))
	mux.Handle("GET /api/v1/mcp/info", user(s.handleMCPInfo))
	mux.Handle("GET /api/v1/mcp/tokens", user(s.handleListMCPTokens))
	mux.Handle("POST /api/v1/mcp/tokens", user(s.handleCreateMCPToken))
	mux.Handle("DELETE /api/v1/mcp/tokens/{id}", user(s.handleDeleteMCPToken))
	mux.Handle("GET /api/v1/mcp/oauth/request", user(s.handleMCPOAuthRequest))
	mux.Handle("POST /api/v1/mcp/oauth/approve", user(s.handleMCPOAuthApprove))
	s.mcpOAuthRoutes(mux)
	mux.Handle(mcpPath, s.mcpHandler())
}

// ---- settings and tokens ----

type mcpToolInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Group       string `json:"group"`
	Level       string `json:"level"`
	Permission  string `json:"permission"`
}

type mcpSettingsView struct {
	mcpSettings
	URL   string        `json:"url"`
	Tools []mcpToolInfo `json:"tools"`
}

func (s *Server) handleGetMCPSettings(w http.ResponseWriter, r *http.Request) {
	tools := []mcpToolInfo{}
	for _, t := range mcpTools() {
		tools = append(tools, mcpToolInfo{Name: t.name, Title: t.title, Description: t.desc, Group: t.group, Level: t.level.String(), Permission: string(t.perm)})
	}
	writeJSON(w, http.StatusOK, mcpSettingsView{mcpSettings: s.loadMCPSettings(r.Context()), URL: s.baseURL(r) + mcpPath, Tools: tools})
}

func (s *Server) handlePutMCPSettings(w http.ResponseWriter, r *http.Request) {
	var req mcpSettings
	if !decode(w, r, &req) {
		return
	}
	if _, ok := parseMCPLevel(req.MaxLevel); !ok {
		apiError(w, http.StatusBadRequest, "invalid_level")
		return
	}
	disabled := []string{}
	for _, n := range req.DisabledTools {
		if mcpToolNames()[n] && !slices.Contains(disabled, n) {
			disabled = append(disabled, n)
		}
	}
	req.DisabledTools = disabled
	if err := s.st.PutSetting(r.Context(), keyMCP, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.mcp", "", true, map[string]any{"enabled": req.Enabled, "maxLevel": req.MaxLevel, "oauth": req.OAuth, "disabledTools": disabled})
	s.handleGetMCPSettings(w, r)
}

type mcpInfo struct {
	Enabled  bool   `json:"enabled"`
	MaxLevel string `json:"maxLevel"`
	OAuth    bool   `json:"oauth"`
	URL      string `json:"url"`
}

func (s *Server) handleMCPInfo(w http.ResponseWriter, r *http.Request) {
	m := s.loadMCPSettings(r.Context())
	writeJSON(w, http.StatusOK, mcpInfo{Enabled: m.Enabled, MaxLevel: m.MaxLevel, OAuth: m.OAuth, URL: s.baseURL(r) + mcpPath})
}

// handleListMCPTokens lists the user's own tokens; administrators pass all=1
// for everyone's.
func (s *Server) handleListMCPTokens(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	uid := u.ID
	if r.URL.Query().Get("all") == "1" {
		if !auth.Role(u.Role).Has(auth.PermSettingsManage) {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		uid = 0
	}
	list, err := s.st.ListMCPTokens(r.Context(), uid)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

type mcpTokenReq struct {
	Name        string `json:"name"`
	Access      string `json:"access"`
	ExpiresDays int    `json:"expiresDays"` // 0: never
}

type mcpTokenCreated struct {
	store.MCPToken
	Secret string `json:"secret"`
}

func (s *Server) handleCreateMCPToken(w http.ResponseWriter, r *http.Request) {
	var req mcpTokenReq
	if !decode(w, r, &req) {
		return
	}
	m := s.loadMCPSettings(r.Context())
	if !m.Enabled {
		apiError(w, http.StatusConflict, "mcp_disabled")
		return
	}
	lvl, ok := parseMCPLevel(req.Access)
	maxLvl, _ := parseMCPLevel(m.MaxLevel)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_level")
		return
	}
	if lvl > maxLvl {
		apiError(w, http.StatusBadRequest, "level_not_allowed")
		return
	}
	name := cleanText(req.Name, 100)
	if name == "" {
		apiError(w, http.StatusBadRequest, "name_required")
		return
	}
	if req.ExpiresDays < 0 || req.ExpiresDays > 365 {
		apiError(w, http.StatusBadRequest, "invalid_token_expiry")
		return
	}
	u, _ := currentUser(r)
	secret := "fwm_" + newToken()
	t := store.MCPToken{UserID: u.ID, Name: name, Kind: "personal", Access: lvl.String(), Prefix: secret[:12]}
	if req.ExpiresDays > 0 {
		exp := s.now().AddDate(0, 0, req.ExpiresDays)
		t.ExpiresAt = &exp
	}
	t, err := s.st.CreateMCPToken(r.Context(), t, secret, "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	t.Username = u.Username
	s.audit(r, "mcp.token.create", name, true, map[string]any{"access": t.Access, "expiresDays": req.ExpiresDays})
	writeJSON(w, http.StatusCreated, mcpTokenCreated{MCPToken: t, Secret: secret})
}

func (s *Server) handleDeleteMCPToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	u, _ := currentUser(r)
	owner := u.ID
	if auth.Role(u.Role).Has(auth.PermSettingsManage) {
		owner = 0 // administrators revoke anyone's token
	}
	t, err := s.st.MCPTokenByID(r.Context(), id)
	if err == nil && owner != 0 && t.UserID != owner {
		err = store.ErrNotFound
	}
	if err == nil {
		err = s.st.DeleteMCPToken(r.Context(), id, owner)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "mcp.token.revoke", t.Name, true, map[string]any{"kind": t.Kind, "owner": t.Username})
	w.WriteHeader(http.StatusNoContent)
}

// ---- the MCP endpoint ----

const mcpInstructions = `FleetWarden is a self-hosted endpoint manager for Windows and Linux devices (agents report inventory, updates, vulnerabilities and security status).
Start with get_overview, then list_devices or the other list tools. Tools that take a device accept its ID or host name.
You act as the FleetWarden user who owns this token: you only see the device groups that user may see, and tools for which the user lacks a permission are not offered.
Tools that change something are only present when the administrator and the token allow it. Destructive tools (restart_device, service_action, kill_process) need confirm set to the exact host name: always ask the user before you call them.
Every tool call is written to the FleetWarden audit log with the user and the token's name.`

// mcpPrincipal is the authenticated caller of one MCP request.
type mcpPrincipal struct {
	user  store.User
	token store.MCPToken
	level mcpLevel
	tools []mcpTool
}

type mcpPrincipalKey struct{}

func (s *Server) mcpHandler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, ok := r.Context().Value(mcpPrincipalKey{}).(*mcpPrincipal)
		if !ok {
			return nil
		}
		return s.mcpServerFor(r, p)
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Requests usually come through a reverse proxy on the same host with
		// the public host name; tokens protect the endpoint, not the address.
		DisableLocalhostProtection: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		m := s.loadMCPSettings(r.Context())
		if !m.Enabled {
			apiError(w, http.StatusNotFound, "mcp_disabled")
			return
		}
		p, ok := s.mcpAuthenticate(w, r, m)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpPrincipalKey{}, p)))
	})
}

func (s *Server) mcpUnauthorized(w http.ResponseWriter, r *http.Request, m mcpSettings, desc string) {
	v := `Bearer realm="FleetWarden", error="invalid_token", error_description="` + desc + `"`
	if m.OAuth {
		v += `, resource_metadata="` + s.baseURL(r) + `/.well-known/oauth-protected-resource` + mcpPath + `"`
	}
	w.Header().Set("WWW-Authenticate", v)
	apiError(w, http.StatusUnauthorized, "unauthenticated")
}

func (s *Server) mcpAuthenticate(w http.ResponseWriter, r *http.Request, m mcpSettings) (*mcpPrincipal, bool) {
	ip := ClientIP(r)
	secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	secret = strings.TrimSpace(secret)
	if !found || secret == "" {
		s.mcpUnauthorized(w, r, m, "a bearer token is required")
		return nil, false
	}
	if s.mcpFails.Blocked(ip) {
		apiError(w, http.StatusTooManyRequests, "too_many_attempts")
		return nil, false
	}
	t, err := s.st.MCPTokenBySecret(r.Context(), secret)
	if errors.Is(err, store.ErrNotFound) {
		s.mcpFails.Fail(ip)
		s.mcpUnauthorized(w, r, m, "the token is unknown, revoked or expired")
		return nil, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	if t.Kind == "oauth" && !m.OAuth {
		s.mcpUnauthorized(w, r, m, "OAuth connections are turned off")
		return nil, false
	}
	u, err := s.st.UserByID(r.Context(), t.UserID)
	if err != nil || u.Disabled {
		s.mcpUnauthorized(w, r, m, "the user of this token is disabled")
		return nil, false
	}
	if u.MustChangePassword {
		apiError(w, http.StatusForbidden, "password_change_required")
		return nil, false
	}
	lvl, _ := parseMCPLevel(t.Access)
	if maxLvl, _ := parseMCPLevel(m.MaxLevel); maxLvl < lvl {
		lvl = maxLvl
	}
	if t.LastUsedAt == nil || s.now().Sub(*t.LastUsedAt) > time.Minute || t.LastIP != ip {
		_ = s.st.TouchMCPToken(r.Context(), t.ID, ip)
	}
	p := &mcpPrincipal{user: u, token: t, level: lvl}
	role := auth.Role(u.Role)
	for _, tool := range mcpTools() {
		if tool.level <= lvl && !slices.Contains(m.DisabledTools, tool.name) && (tool.perm == "" || role.Has(tool.perm)) {
			p.tools = append(p.tools, tool)
		}
	}
	return p, true
}

// mcpServerFor builds the MCP server of one request with the tools the
// caller may use.
func (s *Server) mcpServerFor(r *http.Request, p *mcpPrincipal) *mcp.Server {
	b := s.loadBranding(r.Context())
	srv := mcp.NewServer(&mcp.Implementation{Name: "fleetwarden", Title: b.ProductName, Version: version.Version},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	for _, t := range p.tools {
		tool := t
		destructive := tool.level == mcpFull
		srv.AddTool(&mcp.Tool{
			Name: tool.name, Title: tool.title, Description: tool.desc, InputSchema: tool.schema(),
			Annotations: &mcp.ToolAnnotations{Title: tool.title, ReadOnlyHint: tool.level == mcpRead,
				DestructiveHint: &destructive, IdempotentHint: tool.level == mcpRead, OpenWorldHint: new(bool)},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.runMCPTool(ctx, r, p, tool, req.Params.Arguments), nil
		})
	}
	return srv
}

func (s *Server) runMCPTool(ctx context.Context, r *http.Request, p *mcpPrincipal, tool mcpTool, raw json.RawMessage) *mcp.CallToolResult {
	args := mcpArgs{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcpError("the arguments must be a JSON object")
		}
	}
	c := &mcpCall{s: s, user: p.user, token: p.token, ctx: ctx, req: r}
	for _, k := range tool.required {
		if _, ok := args[k]; !ok {
			return mcpError("argument " + k + " is required")
		}
	}
	out, err := tool.run(c, args)
	details := map[string]any{"token": p.token.Name, "tokenId": p.token.ID, "kind": p.token.Kind, "args": mcpAuditArgs(args)}
	if err != nil {
		details["error"] = truncate(err.Error(), 300)
	}
	ar := r.WithContext(context.WithValue(r.Context(), userKey, p.user))
	s.audit(ar, "mcp.tool", tool.name, err == nil, details)
	if err != nil {
		return mcpError(err.Error())
	}
	text, err := json.Marshal(out)
	if err != nil {
		return mcpError("could not encode the result")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: mcpCut(text)}}}
}

func mcpError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// mcpCut keeps a result within what an assistant can use.
func mcpCut(b []byte) string {
	if len(b) <= mcpMaxResult {
		return string(b)
	}
	cut := b[:mcpMaxResult]
	for !utf8.Valid(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return string(cut) + "\n… [result cut at " + strconv.Itoa(mcpMaxResult) + " bytes; narrow the request with filters or a limit]"
}

// mcpAuditArgs keeps the arguments short in the audit log.
func mcpAuditArgs(a mcpArgs) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		if len(out) >= 12 {
			break
		}
		switch x := v.(type) {
		case string:
			out[k] = truncate(x, 200)
		case []any:
			if len(x) > 20 {
				x = append(x[:20:20], "…")
			}
			out[k] = x
		case float64, bool, nil:
			out[k] = x
		default:
			out[k] = "…"
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
