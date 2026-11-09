package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const sessionCookie = "fw_session"

type authFlags int

// allowPending lets a user who must still change their password reach a route.
const allowPending authFlags = 1

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func currentUser(r *http.Request) (store.User, bool) {
	u, ok := r.Context().Value(userKey).(store.User)
	return u, ok
}

func currentToken(r *http.Request) string {
	t, _ := r.Context().Value(tokenKey).(string)
	return t
}

// authed wraps a handler that needs a signed-in user with permission perm
// (empty: any signed-in user).
func (s *Server) authed(perm auth.Permission, flags authFlags, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			apiError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		sess, err := s.st.SessionByToken(r.Context(), c.Value)
		if errors.Is(err, store.ErrNotFound) || (err == nil && s.now().Sub(sess.LastSeenAt) > sessionIdle) {
			s.clearSessionCookie(w, r)
			apiError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if sess.MFAPending {
			apiError(w, http.StatusUnauthorized, "mfa_required")
			return
		}
		u, err := s.st.UserByID(r.Context(), sess.UserID)
		if err != nil || u.Disabled {
			_ = s.st.DeleteSession(r.Context(), c.Value)
			s.clearSessionCookie(w, r)
			apiError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		if u.MustChangePassword && flags&allowPending == 0 {
			apiError(w, http.StatusForbidden, "password_change_required")
			return
		}
		if perm != "" && !auth.Role(u.Role).Has(perm) {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		if s.now().Sub(sess.LastSeenAt) > time.Minute {
			_ = s.st.TouchSession(r.Context(), c.Value)
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, tokenKey, c.Value)
		h(w, r.WithContext(ctx))
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
}

// startSession signs a user in. With mfaPending the session only allows the TOTP step.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User, mfaPending bool) error {
	token := newToken()
	expires := s.now().Add(sessionAbsolute)
	if mfaPending {
		expires = s.now().Add(mfaWindow)
	}
	if err := s.st.CreateSession(r.Context(), token, u.ID, mfaPending, expires, ClientIP(r), r.UserAgent()); err != nil {
		return err
	}
	if !mfaPending {
		_ = s.st.TouchLogin(r.Context(), u.ID)
	}
	s.setSessionCookie(w, r, token, s.now().Add(sessionAbsolute))
	return nil
}

// ---- public auth endpoints ----

type authConfig struct {
	SSOEnabled        bool   `json:"ssoEnabled"`
	LocalLoginAllowed bool   `json:"localLoginAllowed"`
	ProductName       string `json:"productName"`
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	ssoCfg, _ := s.loadSSO(r.Context())
	b := s.loadBranding(r.Context())
	writeJSON(w, http.StatusOK, authConfig{SSOEnabled: ssoCfg.Enabled, LocalLoginAllowed: true, ProductName: b.ProductName})
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResp struct {
	MFARequired bool    `json:"mfaRequired"`
	User        *meResp `json:"user,omitempty"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	ip := ClientIP(r)
	if s.ipFails.Blocked(ip) || s.usrFails.Blocked(req.Username) {
		s.auditAs(r, nil, req.Username, "auth.login", req.Username, false, map[string]any{"reason": "rate_limited"})
		apiError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	fail := func(reason string, u *store.User) {
		s.ipFails.Fail(ip)
		s.usrFails.Fail(req.Username)
		s.auditAs(r, u, req.Username, "auth.login", req.Username, false, map[string]any{"reason": reason})
		apiError(w, http.StatusUnauthorized, "invalid_credentials")
	}
	u, err := s.st.UserByUsername(r.Context(), req.Username)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (u.Source != "local" || u.PasswordHash == "")) {
		auth.VerifyDummy(req.Password)
		fail("unknown_user", nil)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !auth.VerifyPassword(req.Password, u.PasswordHash) {
		fail("wrong_password", &u)
		return
	}
	if u.Disabled {
		fail("disabled", &u)
		return
	}
	if u.BreakGlass && !s.breakGlassAllowed(r) {
		s.auditAs(r, &u, "", "auth.login", u.Username, false, map[string]any{"reason": "network_restricted"})
		apiError(w, http.StatusForbidden, "network_restricted")
		return
	}
	s.usrFails.Reset(req.Username)
	if err := s.startSession(w, r, u, u.TOTPEnabled); err != nil {
		s.internalError(w, r, err)
		return
	}
	if u.TOTPEnabled {
		writeJSON(w, http.StatusOK, loginResp{MFARequired: true})
		return
	}
	s.auditAs(r, &u, "", "auth.login", u.Username, true, map[string]any{"method": "password"})
	me := s.meFor(u)
	writeJSON(w, http.StatusOK, loginResp{User: &me})
}

// breakGlassAllowed applies the optional network restriction of the local
// emergency administrator.
func (s *Server) breakGlassAllowed(r *http.Request) bool {
	a := s.loadAuthSettings(r.Context())
	if len(a.BreakGlassNetworks) == 0 {
		return true
	}
	ip, err := netip.ParseAddr(ClientIP(r))
	if err != nil {
		return false
	}
	for _, n := range a.BreakGlassNetworks {
		if p, err := netip.ParsePrefix(n); err == nil && p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func (s *Server) handleLoginTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	sess, err := s.st.SessionByToken(r.Context(), c.Value)
	if err != nil || !sess.MFAPending {
		apiError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	u, err := s.st.UserByID(r.Context(), sess.UserID)
	if err != nil || u.Disabled {
		apiError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	ip := ClientIP(r)
	if s.ipFails.Blocked(ip) || s.usrFails.Blocked(u.Username) {
		apiError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	secret, err := s.box.Open(u.TOTPSecret)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	step, ok := auth.VerifyTOTP(secret, req.Code, s.now(), u.TOTPLastStep)
	if ok {
		ok, err = s.st.UseTOTPStep(r.Context(), u.ID, step)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if !ok {
		s.ipFails.Fail(ip)
		s.usrFails.Fail(u.Username)
		s.auditAs(r, &u, "", "auth.login", u.Username, false, map[string]any{"reason": "wrong_totp"})
		apiError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	// Replace the pending session with a fresh one so the pre-MFA token is useless.
	_ = s.st.DeleteSession(r.Context(), c.Value)
	if err := s.startSession(w, r, u, false); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.usrFails.Reset(u.Username)
	s.auditAs(r, &u, "", "auth.login", u.Username, true, map[string]any{"method": "password+totp"})
	me := s.meFor(u)
	writeJSON(w, http.StatusOK, loginResp{User: &me})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if sess, err := s.st.SessionByToken(r.Context(), c.Value); err == nil {
			if u, err := s.st.UserByID(r.Context(), sess.UserID); err == nil {
				s.auditAs(r, &u, "", "auth.logout", u.Username, true, nil)
			}
		}
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// ---- current user ----

type meResp struct {
	store.User
	Permissions []auth.Permission `json:"permissions"`
}

func (s *Server) meFor(u store.User) meResp {
	p := auth.PermissionsOf(auth.Role(u.Role))
	if p == nil {
		p = []auth.Permission{}
	}
	return meResp{User: u, Permissions: p}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	writeJSON(w, http.StatusOK, s.meFor(u))
}

func (s *Server) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Language string `json:"language"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Language != "en" && req.Language != "nl" {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	u, _ := currentUser(r)
	if err := s.st.SetLanguage(r.Context(), u.ID, req.Language); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func passwordError(w http.ResponseWriter, err error) {
	apiError(w, http.StatusBadRequest, err.Error())
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, _ := currentUser(r)
	if u.Source != "local" {
		apiError(w, http.StatusBadRequest, "not_local_account")
		return
	}
	if s.usrFails.Blocked(u.Username) {
		apiError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	if !auth.VerifyPassword(req.Current, u.PasswordHash) {
		s.usrFails.Fail(u.Username)
		s.audit(r, "user.password_change", u.Username, false, map[string]any{"reason": "wrong_password"})
		apiError(w, http.StatusBadRequest, "wrong_password")
		return
	}
	if err := auth.ValidatePassword(req.New, u.Username); err != nil {
		passwordError(w, err)
		return
	}
	if req.New == req.Current {
		apiError(w, http.StatusBadRequest, "password_unchanged")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.SetPassword(r.Context(), u.ID, hash, false); err != nil {
		s.internalError(w, r, err)
		return
	}
	// Sign out other sessions; keep this one.
	_ = s.st.DeleteUserSessions(r.Context(), u.ID, currentToken(r))
	if u.BreakGlass {
		if err := os.Remove(s.cfg.InitialPasswordPath()); err != nil && !os.IsNotExist(err) {
			s.log.Warn("could not remove the initial password file", "err", err)
		}
	}
	s.audit(r, "user.password_change", u.Username, true, nil)
	u.MustChangePassword = false
	writeJSON(w, http.StatusOK, s.meFor(u))
}

// ---- TOTP enrolment ----

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if u.Source != "local" {
		apiError(w, http.StatusBadRequest, "not_local_account")
		return
	}
	if u.TOTPEnabled {
		apiError(w, http.StatusConflict, "totp_already_enabled")
		return
	}
	secret := auth.NewTOTPSecret()
	sealed, err := s.box.Seal(secret)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.SetTOTP(r.Context(), u.ID, sealed, false); err != nil {
		s.internalError(w, r, err)
		return
	}
	issuer := s.loadBranding(r.Context()).ProductName
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": auth.TOTPURI(issuer, u.Username, secret)})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, _ := currentUser(r)
	if u.TOTPEnabled || u.TOTPSecret == "" {
		apiError(w, http.StatusConflict, "totp_not_pending")
		return
	}
	secret, err := s.box.Open(u.TOTPSecret)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	step, ok := auth.VerifyTOTP(secret, req.Code, s.now(), 0)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_code")
		return
	}
	if err := s.st.SetTOTP(r.Context(), u.ID, u.TOTPSecret, true); err != nil {
		s.internalError(w, r, err)
		return
	}
	_, _ = s.st.UseTOTPStep(r.Context(), u.ID, step)
	s.audit(r, "user.totp_enable", u.Username, true, nil)
	u.TOTPEnabled = true
	writeJSON(w, http.StatusOK, s.meFor(u))
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, _ := currentUser(r)
	if s.usrFails.Blocked(u.Username) {
		apiError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	if !auth.VerifyPassword(req.Password, u.PasswordHash) {
		s.usrFails.Fail(u.Username)
		s.audit(r, "user.totp_disable", u.Username, false, map[string]any{"reason": "wrong_password"})
		apiError(w, http.StatusBadRequest, "wrong_password")
		return
	}
	if err := s.st.SetTOTP(r.Context(), u.ID, "", false); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "user.totp_disable", u.Username, true, nil)
	u.TOTPEnabled = false
	writeJSON(w, http.StatusOK, s.meFor(u))
}
