package server

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/sso"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Setting keys.
const (
	keySSO      = "sso"
	keyAuth     = "auth"
	keyMail     = "mail"
	keyBranding = "branding"
	keyGeneral  = "general"
	keyUpdates  = "updates"
)

// ssoSettings is stored with the client secret encrypted.
type ssoSettings struct {
	Enabled      bool   `json:"enabled"`
	TenantID     string `json:"tenantId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type authSettings struct {
	BreakGlassNetworks []string `json:"breakGlassNetworks"`
}

type generalSettings struct {
	PublicURL string `json:"publicUrl"`
}

func (s *Server) loadSSO(ctx context.Context) (ssoSettings, error) {
	var c ssoSettings
	err := s.st.GetSetting(ctx, keySSO, &c)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return c, err
}

func (s *Server) ssoConfig(c ssoSettings) (sso.Config, error) {
	secret, err := s.box.Open(c.ClientSecret)
	return sso.Config{TenantID: c.TenantID, ClientID: c.ClientID, ClientSecret: secret}, err
}

func (s *Server) loadAuthSettings(ctx context.Context) authSettings {
	var a authSettings
	_ = s.st.GetSetting(ctx, keyAuth, &a)
	return a
}

// ---- authentication settings (Entra SSO + break-glass restriction) ----

type authSettingsView struct {
	SSOEnabled         bool     `json:"ssoEnabled"`
	TenantID           string   `json:"tenantId"`
	ClientID           string   `json:"clientId"`
	ClientSecret       string   `json:"clientSecret,omitempty"` // write-only
	ClientSecretSet    bool     `json:"clientSecretSet"`
	RedirectURI        string   `json:"redirectUri"`
	BreakGlassNetworks []string `json:"breakGlassNetworks"`
	ClientIP           string   `json:"clientIp"`
}

func (s *Server) redirectURI(r *http.Request) string {
	return s.baseURL(r) + "/api/v1/auth/sso/callback"
}

func (s *Server) handleGetAuthSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadSSO(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	a := s.loadAuthSettings(r.Context())
	if a.BreakGlassNetworks == nil {
		a.BreakGlassNetworks = []string{}
	}
	writeJSON(w, http.StatusOK, authSettingsView{
		SSOEnabled: c.Enabled, TenantID: c.TenantID, ClientID: c.ClientID, ClientSecretSet: c.ClientSecret != "",
		RedirectURI: s.redirectURI(r), BreakGlassNetworks: a.BreakGlassNetworks, ClientIP: ClientIP(r),
	})
}

func (s *Server) handlePutAuthSettings(w http.ResponseWriter, r *http.Request) {
	var req authSettingsView
	if !decode(w, r, &req) {
		return
	}
	cur, err := s.loadSSO(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	next := ssoSettings{Enabled: req.SSOEnabled, TenantID: strings.TrimSpace(strings.ToLower(req.TenantID)),
		ClientID: strings.TrimSpace(strings.ToLower(req.ClientID)), ClientSecret: cur.ClientSecret}
	if req.ClientSecret != "" {
		if next.ClientSecret, err = s.box.Seal(req.ClientSecret); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if (next.TenantID != "" && !sso.ValidGUID(next.TenantID)) || (next.ClientID != "" && !sso.ValidGUID(next.ClientID)) {
		apiError(w, http.StatusBadRequest, "invalid_guid")
		return
	}
	if next.Enabled && (next.TenantID == "" || next.ClientID == "" || next.ClientSecret == "") {
		apiError(w, http.StatusBadRequest, "sso_incomplete")
		return
	}
	nets := []string{}
	for _, n := range req.BreakGlassNetworks {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.Contains(n, "/") {
			if a, err := netip.ParseAddr(n); err == nil {
				n = netip.PrefixFrom(a, a.BitLen()).String()
			}
		}
		p, err := netip.ParsePrefix(n)
		if err != nil {
			apiErrorDetail(w, http.StatusBadRequest, "invalid_network", n)
			return
		}
		nets = append(nets, p.Masked().String())
	}
	// Do not let an administrator lock themselves out of the emergency account
	// from where they are now.
	if len(nets) > 0 {
		ip, err := netip.ParseAddr(ClientIP(r))
		inside := false
		for _, n := range nets {
			if p, _ := netip.ParsePrefix(n); err == nil && p.Contains(ip.Unmap()) {
				inside = true
			}
		}
		if !inside {
			apiError(w, http.StatusBadRequest, "network_excludes_you")
			return
		}
	}
	if err := s.st.PutSetting(r.Context(), keySSO, next); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.PutSetting(r.Context(), keyAuth, authSettings{BreakGlassNetworks: nets}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.authentication", "sso", true, map[string]any{
		"ssoEnabled": next.Enabled, "tenantId": next.TenantID, "clientId": next.ClientID,
		"secretChanged": req.ClientSecret != "", "breakGlassNetworks": nets,
	})
	s.handleGetAuthSettings(w, r)
}

func (s *Server) handleTestSSO(w http.ResponseWriter, r *http.Request) {
	var req authSettingsView
	if !decode(w, r, &req) {
		return
	}
	cur, err := s.loadSSO(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cfg, err := s.ssoConfig(cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Test what is in the form, falling back to the stored secret.
	if req.TenantID != "" {
		cfg.TenantID = strings.TrimSpace(strings.ToLower(req.TenantID))
	}
	if req.ClientID != "" {
		cfg.ClientID = strings.TrimSpace(strings.ToLower(req.ClientID))
	}
	if req.ClientSecret != "" {
		cfg.ClientSecret = req.ClientSecret
	}
	if err := s.sso.Test(r.Context(), cfg); err != nil {
		s.audit(r, "settings.authentication.test", "sso", false, map[string]any{"error": err.Error()})
		apiErrorDetail(w, http.StatusBadGateway, "sso_test_failed", err.Error())
		return
	}
	s.audit(r, "settings.authentication.test", "sso", true, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListMappings(w http.ResponseWriter, r *http.Request) {
	m, err := s.st.ListRoleMappings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleCreateMapping(w http.ResponseWriter, r *http.Request) {
	var req store.RoleMapping
	if !decode(w, r, &req) {
		return
	}
	req.Value = strings.TrimSpace(req.Value)
	if (req.Claim != "group" && req.Claim != "role") || req.Value == "" || len(req.Value) > 200 || !auth.ValidRole(auth.Role(req.Role)) {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if req.Claim == "group" && !sso.ValidGUID(req.Value) {
		apiError(w, http.StatusBadRequest, "invalid_guid")
		return
	}
	if req.Claim == "group" {
		req.Value = strings.ToLower(req.Value)
	}
	scoped := req.Scoped && req.Role != string(auth.RoleAdministrator)
	m, err := s.st.CreateRoleMapping(r.Context(), req)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "mapping_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.SetMappingScope(r.Context(), m.ID, scoped, req.Groups); err != nil {
		s.internalError(w, r, err)
		return
	}
	m.Scoped, m.Groups = scoped, req.Groups
	if !scoped || m.Groups == nil {
		m.Groups = []int64{}
	}
	s.audit(r, "settings.role_mapping.create", m.Claim+":"+m.Value, true, map[string]any{"role": m.Role, "scoped": scoped, "groups": m.Groups})
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleDeleteMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.st.DeleteRoleMapping(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.role_mapping.delete", r.PathValue("id"), true, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- general ----

func (s *Server) handleGetGeneral(w http.ResponseWriter, r *http.Request) {
	var g generalSettings
	_ = s.st.GetSetting(r.Context(), keyGeneral, &g)
	writeJSON(w, http.StatusOK, map[string]string{"publicUrl": g.PublicURL, "detectedUrl": s.detectedURL(r)})
}

func (s *Server) detectedURL(r *http.Request) string {
	scheme, _ := r.Context().Value(schemeKey).(string)
	host, _ := r.Context().Value(hostKey).(string)
	return scheme + "://" + host
}

func (s *Server) handlePutGeneral(w http.ResponseWriter, r *http.Request) {
	var req generalSettings
	if !decode(w, r, &req) {
		return
	}
	req.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if req.PublicURL != "" {
		u, err := url.Parse(req.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			apiError(w, http.StatusBadRequest, "invalid_url")
			return
		}
	}
	if err := s.st.PutSetting(r.Context(), keyGeneral, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.general", "publicUrl", true, map[string]any{"publicUrl": req.PublicURL})
	s.broadcastEndpoints(r.Context())
	s.handleGetGeneral(w, r)
}
