package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// OAuth 2.1 for the MCP server, as the MCP authorization spec describes:
// protected resource metadata (RFC 9728), authorization server metadata
// (RFC 8414), dynamic client registration (RFC 7591) and the authorization
// code flow with PKCE (S256) for public clients. The user approves an
// assistant on a page of the web interface while signed in, so SSO, TOTP and
// the break-glass rules apply unchanged.

const (
	mcpAccessTTL  = time.Hour
	mcpRefreshTTL = 30 * 24 * time.Hour // a connection ends after 30 days without use
	mcpCodeTTL    = 5 * time.Minute
	mcpMaxClients = 1000
	mcpConsentURL = "/authorize-assistant"
)

type mcpCode struct {
	clientID    string
	redirectURI string
	challenge   string
	userID      int64
	access      string
	expires     time.Time
}

type mcpCodes struct {
	mu sync.Mutex
	m  map[string]mcpCode
}

// take returns a code once.
func (c *mcpCodes) take(code string, now time.Time) (mcpCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[code]
	delete(c.m, code)
	for k, x := range c.m {
		if now.After(x.expires) {
			delete(c.m, k)
		}
	}
	return v, ok && now.Before(v.expires)
}

func (c *mcpCodes) put(code string, v mcpCode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]mcpCode{}
	}
	c.m[code] = v
}

func (s *Server) mcpOAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleMCPResourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.handleMCPResourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleMCPServerMeta)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", s.handleMCPServerMeta)
	mux.HandleFunc("POST /oauth/register", s.handleMCPRegister)
	mux.HandleFunc("GET /oauth/authorize", s.handleMCPAuthorize)
	mux.HandleFunc("POST /oauth/token", s.handleMCPToken)
	mux.HandleFunc("POST /oauth/revoke", s.handleMCPRevoke)
}

// mcpOAuthOn answers 404 unless the MCP server and its OAuth are on.
func (s *Server) mcpOAuthOn(w http.ResponseWriter, r *http.Request) (mcpSettings, bool) {
	m := s.loadMCPSettings(r.Context())
	if !m.Enabled || !m.OAuth {
		apiError(w, http.StatusNotFound, "mcp_oauth_disabled")
		return m, false
	}
	return m, true
}

func (s *Server) handleMCPResourceMeta(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	base := s.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + mcpPath,
		"resource_name":            s.loadBranding(r.Context()).ProductName,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         mcpLevelNames,
	})
}

func (s *Server) handleMCPServerMeta(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	base := s.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     base,
		"authorization_endpoint":                     base + "/oauth/authorize",
		"token_endpoint":                             base + "/oauth/token",
		"registration_endpoint":                      base + "/oauth/register",
		"revocation_endpoint":                        base + "/oauth/revoke",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                           mcpLevelNames,
	})
}

// oauthError answers in the RFC 6749 format.
func oauthError(w http.ResponseWriter, code int, errCode, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, map[string]string{"error": errCode, "error_description": desc})
}

// validRedirect allows https, http on the loopback address (desktop apps)
// and private-use schemes such as cursor:// (RFC 8252).
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || len(raw) > 2000 {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	case "", "javascript", "data", "file", "vbscript", "blob", "ftp", "ws", "wss":
		return false
	}
	return true
}

func (s *Server) handleMCPRegister(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	ip := ClientIP(r)
	if s.mcpRegs.Blocked(ip) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many registrations from this address")
		return
	}
	s.mcpRegs.Fail(ip) // counts every registration
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "give one to ten redirect URIs")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must use https, http on localhost or an app scheme")
			return
		}
	}
	if n, err := s.st.CountMCPClients(r.Context()); err != nil || n >= mcpMaxClients {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "too many registered clients")
		return
	}
	name := cleanText(req.ClientName, 100)
	if name == "" {
		name = "AI assistant"
	}
	c, err := s.st.CreateMCPClient(r.Context(), store.MCPClient{ID: "fwc_" + newToken()[:24], Name: name, RedirectURIs: req.RedirectURIs}, ip)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": c.ID, "client_name": c.Name, "redirect_uris": c.RedirectURIs, "client_id_issued_at": c.CreatedAt.Unix(),
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
	})
}

// mcpClientRedirect checks the client and its redirect URI. Without a
// redirect URI the only registered one is used.
func (s *Server) mcpClientRedirect(r *http.Request, clientID, redirect string) (store.MCPClient, string, bool) {
	c, err := s.st.MCPClientByID(r.Context(), clientID)
	if err != nil {
		return c, "", false
	}
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	return c, redirect, slices.Contains(c.RedirectURIs, redirect)
}

func withQuery(raw string, q url.Values) string {
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + q.Encode()
}

// handleMCPAuthorize checks the request and sends the browser to the consent
// page of the web interface (which asks for a sign-in first).
func (s *Server) handleMCPAuthorize(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	q := r.URL.Query()
	_, redirect, ok := s.mcpClientRedirect(r, q.Get("client_id"), q.Get("redirect_uri"))
	if !ok {
		// Never redirect to an address that was not registered.
		http.Error(w, "Unknown client or redirect URI. Remove the connector in your assistant and add it again.", http.StatusBadRequest)
		return
	}
	fail := func(code, desc string) {
		v := url.Values{"error": {code}, "error_description": {desc}, "iss": {s.baseURL(r)}}
		if st := q.Get("state"); st != "" {
			v.Set("state", st)
		}
		http.Redirect(w, r, withQuery(redirect, v), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only the code flow is supported")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "PKCE with S256 is required")
		return
	}
	v := url.Values{}
	for _, k := range []string{"client_id", "code_challenge", "state", "scope", "resource"} {
		if x := q.Get(k); x != "" {
			v.Set(k, x)
		}
	}
	v.Set("redirect_uri", redirect)
	http.Redirect(w, r, mcpConsentURL+"?"+v.Encode(), http.StatusFound)
}

// requestedLevel is the highest level in the scope parameter, capped by the
// settings; without a scope the settings' maximum.
func requestedLevel(scope string, m mcpSettings) mcpLevel {
	maxLvl, _ := parseMCPLevel(m.MaxLevel)
	want := mcpLevel(-1)
	for _, sc := range strings.Fields(scope) {
		if l, ok := parseMCPLevel(sc); ok && l > want {
			want = l
		}
	}
	if want < 0 || want > maxLvl {
		return maxLvl
	}
	return want
}

type mcpConsentView struct {
	ClientName   string `json:"clientName"`
	RedirectHost string `json:"redirectHost"`
	Requested    string `json:"requested"`
	MaxLevel     string `json:"maxLevel"`
}

func (s *Server) handleMCPOAuthRequest(w http.ResponseWriter, r *http.Request) {
	m, ok := s.mcpOAuthOn(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	c, redirect, ok := s.mcpClientRedirect(r, q.Get("client_id"), q.Get("redirect_uri"))
	if !ok {
		apiError(w, http.StatusBadRequest, "unknown_client")
		return
	}
	host := redirect
	if u, err := url.Parse(redirect); err == nil {
		host = u.Scheme + "://" + u.Host
	}
	writeJSON(w, http.StatusOK, mcpConsentView{ClientName: c.Name, RedirectHost: host, Requested: requestedLevel(q.Get("scope"), m).String(), MaxLevel: m.MaxLevel})
}

type mcpApproveReq struct {
	ClientID      string `json:"clientId"`
	RedirectURI   string `json:"redirectUri"`
	CodeChallenge string `json:"codeChallenge"`
	State         string `json:"state"`
	Access        string `json:"access"`
	Deny          bool   `json:"deny"`
}

// handleMCPOAuthApprove records the user's answer and returns where the
// browser goes next: back to the assistant with a code or an error.
func (s *Server) handleMCPOAuthApprove(w http.ResponseWriter, r *http.Request) {
	m, ok := s.mcpOAuthOn(w, r)
	if !ok {
		return
	}
	var req mcpApproveReq
	if !decode(w, r, &req) {
		return
	}
	c, redirect, ok := s.mcpClientRedirect(r, req.ClientID, req.RedirectURI)
	if !ok || req.CodeChallenge == "" {
		apiError(w, http.StatusBadRequest, "unknown_client")
		return
	}
	u, _ := currentUser(r)
	v := url.Values{"iss": {s.baseURL(r)}}
	if req.State != "" {
		v.Set("state", req.State)
	}
	if req.Deny {
		v.Set("error", "access_denied")
		s.audit(r, "mcp.oauth.deny", c.Name, true, nil)
		writeJSON(w, http.StatusOK, map[string]string{"redirect": withQuery(redirect, v)})
		return
	}
	lvl, ok := parseMCPLevel(req.Access)
	if maxLvl, _ := parseMCPLevel(m.MaxLevel); !ok || lvl > maxLvl {
		apiError(w, http.StatusBadRequest, "level_not_allowed")
		return
	}
	code := newToken()
	s.mcpCodes.put(code, mcpCode{clientID: c.ID, redirectURI: redirect, challenge: req.CodeChallenge, userID: u.ID,
		access: lvl.String(), expires: s.now().Add(mcpCodeTTL)})
	v.Set("code", code)
	s.audit(r, "mcp.oauth.approve", c.Name, true, map[string]any{"access": lvl.String(), "redirect": redirect})
	writeJSON(w, http.StatusOK, map[string]string{"redirect": withQuery(redirect, v)})
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

type mcpTokenResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (s *Server) handleMCPToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body must be form encoded")
		return
	}
	ip := ClientIP(r)
	if s.mcpFails.Blocked(ip) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many failed attempts")
		return
	}
	f := r.PostForm
	access, refresh := "fwo_"+newToken(), "fwr_"+newToken()
	now := s.now()
	exp, rexp := now.Add(mcpAccessTTL), now.Add(mcpRefreshTTL)
	var t store.MCPToken
	switch f.Get("grant_type") {
	case "authorization_code":
		code, ok := s.mcpCodes.take(f.Get("code"), now)
		if !ok || code.clientID != f.Get("client_id") || (f.Get("redirect_uri") != "" && code.redirectURI != f.Get("redirect_uri")) ||
			!pkceOK(f.Get("code_verifier"), code.challenge) {
			s.mcpFails.Fail(ip)
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is unknown, used, expired or does not match")
			return
		}
		c, err := s.st.MCPClientByID(r.Context(), code.clientID)
		if err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_client", "unknown client")
			return
		}
		t, err = s.st.CreateMCPToken(r.Context(), store.MCPToken{UserID: code.userID, Name: c.Name, Kind: "oauth", ClientID: &c.ID,
			Access: code.access, Prefix: access[:12], ExpiresAt: &exp, RefreshExpiresAt: &rexp}, access, refresh)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		u, _ := s.st.UserByID(r.Context(), code.userID)
		s.auditAs(r, &u, "", "mcp.oauth.connect", c.Name, true, map[string]any{"access": code.access, "tokenId": t.ID})
	case "refresh_token":
		var err error
		t, err = s.st.RefreshMCPToken(r.Context(), f.Get("refresh_token"), f.Get("client_id"), access, refresh, exp, rexp)
		if errors.Is(err, store.ErrNotFound) {
			s.mcpFails.Fail(ip)
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is unknown, used or expired")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, mcpTokenResp{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(mcpAccessTTL.Seconds()),
		RefreshToken: refresh, Scope: t.Access})
}

// handleMCPRevoke ends a connection by its access or refresh token (RFC 7009:
// always 200).
func (s *Server) handleMCPRevoke(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mcpOAuthOn(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err == nil && r.PostForm.Get("token") != "" {
		if err := s.st.RevokeMCPRefresh(r.Context(), r.PostForm.Get("token")); err != nil {
			s.log.Warn("revoke MCP token", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}
