package server

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{1,63}$`)

type roleInfo struct {
	Role        auth.Role         `json:"role"`
	Permissions []auth.Permission `json:"permissions"`
}

func (s *Server) handleRoles(w http.ResponseWriter, _ *http.Request) {
	out := make([]roleInfo, 0, len(auth.Roles))
	for _, r := range auth.Roles {
		out = append(out, roleInfo{r, auth.PermissionsOf(r)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if users == nil {
		users = []store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

type createUserReq struct {
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	Password    string  `json:"password"`
	Scoped      bool    `json:"scoped"`
	ScopeGroups []int64 `json:"scopeGroups"`
}

type userWithPassword struct {
	User            store.User `json:"user"`
	OneTimePassword string     `json:"oneTimePassword,omitempty"`
}

// handleCreateUser creates a local account. Without a password a one-time
// password is generated that must be changed at the first login.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.Email = strings.TrimSpace(req.Email)
	if !usernameRE.MatchString(req.Username) {
		apiError(w, http.StatusBadRequest, "invalid_username")
		return
	}
	if !auth.ValidRole(auth.Role(req.Role)) {
		apiError(w, http.StatusBadRequest, "invalid_role")
		return
	}
	if req.Email != "" && !mail.ValidAddress(req.Email) {
		apiError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	oneTime := ""
	if req.Password == "" {
		oneTime = auth.GeneratePassword()
		req.Password = oneTime
	} else if err := auth.ValidatePassword(req.Password, req.Username); err != nil {
		passwordError(w, err)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, err := s.st.CreateUser(r.Context(), store.User{
		Username: req.Username, DisplayName: strings.TrimSpace(req.DisplayName), Email: req.Email, Source: "local",
		PasswordHash: hash, MustChangePassword: true, Role: req.Role,
	})
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "username_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if req.Scoped && req.Role != string(auth.RoleAdministrator) {
		if err := s.st.SetUserScope(r.Context(), u.ID, true, req.ScopeGroups); err != nil {
			s.internalError(w, r, err)
			return
		}
		u, _ = s.st.UserByID(r.Context(), u.ID)
	}
	s.audit(r, "user.create", u.Username, true, map[string]any{"role": u.Role, "scoped": u.Scoped, "groups": u.ScopeGroups})
	writeJSON(w, http.StatusCreated, userWithPassword{User: u, OneTimePassword: oneTime})
}

type updateUserReq struct {
	DisplayName *string  `json:"displayName"`
	Email       *string  `json:"email"`
	Role        *string  `json:"role"`
	Disabled    *bool    `json:"disabled"`
	Scoped      *bool    `json:"scoped"`
	ScopeGroups *[]int64 `json:"scopeGroups"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req updateUserReq
	if !decode(w, r, &req) {
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	me, _ := currentUser(r)
	changes := map[string]any{}
	next := u
	if req.DisplayName != nil {
		next.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Email != nil {
		next.Email = strings.TrimSpace(*req.Email)
		if next.Email != "" && !mail.ValidAddress(next.Email) {
			apiError(w, http.StatusBadRequest, "invalid_email")
			return
		}
	}
	if req.Role != nil && *req.Role != u.Role {
		if u.Source != "local" {
			apiError(w, http.StatusBadRequest, "role_managed_by_entra")
			return
		}
		if !auth.ValidRole(auth.Role(*req.Role)) {
			apiError(w, http.StatusBadRequest, "invalid_role")
			return
		}
		if u.BreakGlass {
			apiError(w, http.StatusBadRequest, "break_glass_protected")
			return
		}
		next.Role = *req.Role
		changes["role"] = next.Role
	}
	if req.Disabled != nil && *req.Disabled != u.Disabled {
		if u.ID == me.ID {
			apiError(w, http.StatusBadRequest, "cannot_disable_self")
			return
		}
		next.Disabled = *req.Disabled
		changes["disabled"] = next.Disabled
	}
	// Never leave the system without an enabled administrator.
	if u.Role == string(auth.RoleAdministrator) && !u.Disabled && (next.Role != u.Role || next.Disabled) {
		n, err := s.st.CountActiveAdmins(r.Context(), u.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if n == 0 {
			apiError(w, http.StatusBadRequest, "last_admin")
			return
		}
	}
	if err := s.st.UpdateProfile(r.Context(), u.ID, next.DisplayName, next.Email, next.Role, next.Disabled); err != nil {
		s.internalError(w, r, err)
		return
	}
	if req.Scoped != nil || req.ScopeGroups != nil {
		if u.Source != "local" {
			apiError(w, http.StatusBadRequest, "role_managed_by_entra")
			return
		}
		scoped, groups := u.Scoped, u.ScopeGroups
		if req.Scoped != nil {
			scoped = *req.Scoped
		}
		if req.ScopeGroups != nil {
			groups = *req.ScopeGroups
		}
		if next.Role == string(auth.RoleAdministrator) {
			scoped = false // administrators always see every group
		}
		if err := s.st.SetUserScope(r.Context(), u.ID, scoped, groups); err != nil {
			s.internalError(w, r, err)
			return
		}
		changes["scoped"], changes["groups"] = scoped, groups
	}
	if next.Disabled || next.Role != u.Role {
		_ = s.st.DeleteUserSessions(r.Context(), u.ID, "")
	}
	s.audit(r, "user.update", u.Username, true, changes)
	updated, _ := s.st.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	me, _ := currentUser(r)
	switch {
	case u.ID == me.ID:
		apiError(w, http.StatusBadRequest, "cannot_delete_self")
		return
	case u.BreakGlass:
		apiError(w, http.StatusBadRequest, "break_glass_protected")
		return
	}
	if u.Role == string(auth.RoleAdministrator) && !u.Disabled {
		if n, err := s.st.CountActiveAdmins(r.Context(), u.ID); err != nil || n == 0 {
			apiError(w, http.StatusBadRequest, "last_admin")
			return
		}
	}
	if err := s.st.DeleteUser(r.Context(), u.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "user.delete", u.Username, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req struct {
		ResetTOTP bool `json:"resetTotp"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if u.Source != "local" {
		apiError(w, http.StatusBadRequest, "not_local_account")
		return
	}
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.SetPassword(r.Context(), u.ID, hash, true); err != nil {
		s.internalError(w, r, err)
		return
	}
	if req.ResetTOTP {
		if err := s.st.SetTOTP(r.Context(), u.ID, "", false); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	_ = s.st.DeleteUserSessions(r.Context(), u.ID, "")
	s.audit(r, "user.password_reset", u.Username, true, map[string]any{"resetTotp": req.ResetTOTP})
	u, _ = s.st.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, userWithPassword{User: u, OneTimePassword: pw})
}
