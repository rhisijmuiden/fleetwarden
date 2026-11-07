package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/sso"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const ssoCookie = "fw_sso"

// ssoState travels in an encrypted, short-lived cookie between the start of
// the Entra sign-in and the callback.
type ssoState struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	Expires  time.Time `json:"e"`
}

func loginRedirect(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(code), http.StatusFound)
}

func (s *Server) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadSSO(r.Context())
	if err != nil || !c.Enabled {
		loginRedirect(w, r, "sso_disabled")
		return
	}
	cfg, err := s.ssoConfig(c)
	if err != nil {
		s.log.Error("sso config", "err", err)
		loginRedirect(w, r, "sso_error")
		return
	}
	st := ssoState{State: newToken(), Nonce: newToken(), Verifier: oauth2.GenerateVerifier(), Expires: s.now().Add(10 * time.Minute)}
	target, err := s.sso.AuthURL(r.Context(), cfg, s.redirectURI(r), st.State, st.Nonce, st.Verifier)
	if err != nil {
		s.log.Error("sso start", "err", err)
		loginRedirect(w, r, "sso_error")
		return
	}
	raw, _ := json.Marshal(st)
	sealed, err := s.box.Seal(string(raw))
	if err != nil {
		loginRedirect(w, r, "sso_error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: sealed, Path: "/api/v1/auth/sso", MaxAge: 600,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/v1/auth/sso", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.auditAs(r, nil, "", "auth.login", "entra", false, map[string]any{"method": "entra", "reason": e, "description": q.Get("error_description")})
		loginRedirect(w, r, "sso_denied")
		return
	}
	ck, err := r.Cookie(ssoCookie)
	if err != nil {
		loginRedirect(w, r, "sso_expired")
		return
	}
	raw, err := s.box.Open(ck.Value)
	var st ssoState
	if err != nil || json.Unmarshal([]byte(raw), &st) != nil || s.now().After(st.Expires) || q.Get("state") != st.State {
		loginRedirect(w, r, "sso_expired")
		return
	}
	c, err := s.loadSSO(r.Context())
	if err != nil || !c.Enabled {
		loginRedirect(w, r, "sso_disabled")
		return
	}
	cfg, err := s.ssoConfig(c)
	if err != nil {
		loginRedirect(w, r, "sso_error")
		return
	}
	id, err := s.sso.Exchange(r.Context(), cfg, s.redirectURI(r), q.Get("code"), st.Verifier, st.Nonce)
	if err != nil {
		s.log.Warn("sso callback", "err", err)
		s.auditAs(r, nil, "", "auth.login", "entra", false, map[string]any{"method": "entra", "reason": "token", "error": err.Error()})
		loginRedirect(w, r, "sso_error")
		return
	}
	mappings, err := s.st.ListRoleMappings(r.Context())
	if err != nil {
		loginRedirect(w, r, "sso_error")
		return
	}
	// Roles are derived from the token at every login, so removing someone
	// from the Entra group takes effect at their next sign-in.
	role, scope, ok := sso.ResolveRole(id, mappings)
	if !ok {
		reason := "no_role"
		if id.GroupsOverage {
			reason = "groups_overage"
		}
		s.auditAs(r, nil, id.Username, "auth.login", id.Username, false, map[string]any{"method": "entra", "reason": reason})
		loginRedirect(w, r, "sso_no_role")
		return
	}
	u, err := s.upsertSSOUser(r, id, role, scope)
	if errors.Is(err, errDisabled) {
		s.auditAs(r, &u, "", "auth.login", u.Username, false, map[string]any{"method": "entra", "reason": "disabled"})
		loginRedirect(w, r, "account_disabled")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		s.auditAs(r, nil, id.Username, "auth.login", id.Username, false, map[string]any{"method": "entra", "reason": "username_taken"})
		loginRedirect(w, r, "sso_username_taken")
		return
	}
	if err != nil {
		s.log.Error("sso user", "err", err)
		loginRedirect(w, r, "sso_error")
		return
	}
	if err := s.startSession(w, r, u, false); err != nil {
		loginRedirect(w, r, "sso_error")
		return
	}
	s.auditAs(r, &u, "", "auth.login", u.Username, true, map[string]any{"method": "entra", "role": u.Role})
	http.Redirect(w, r, "/", http.StatusFound)
}

var errDisabled = errors.New("account disabled")

func (s *Server) upsertSSOUser(r *http.Request, id sso.Identity, role auth.Role, scope store.Scope) (store.User, error) {
	ctx := r.Context()
	u, err := s.st.UserByExternalID(ctx, id.ExternalID())
	if errors.Is(err, store.ErrNotFound) {
		u, err = s.st.CreateUser(ctx, store.User{
			Username: id.Username, DisplayName: id.Name, Email: id.Email, Source: "entra",
			ExternalID: id.ExternalID(), Role: string(role),
		})
		if err != nil {
			return u, err
		}
		if err := s.st.SetUserScope(ctx, u.ID, !scope.All, scope.Groups); err != nil {
			return u, err
		}
		return s.st.UserByID(ctx, u.ID)
	}
	if err != nil {
		return u, err
	}
	if u.Disabled {
		return u, errDisabled
	}
	if u.Role != string(role) {
		s.auditAs(r, &u, "", "user.role_sync", u.Username, true, map[string]any{"from": u.Role, "to": string(role)})
	}
	if err := s.st.SyncSSOUser(ctx, u.ID, id.Username, id.Name, id.Email, string(role)); err != nil {
		return u, err
	}
	if err := s.st.SetUserScope(ctx, u.ID, !scope.All, scope.Groups); err != nil {
		return u, err
	}
	return s.st.UserByID(ctx, u.ID)
}
