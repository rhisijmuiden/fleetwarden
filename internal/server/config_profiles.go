package server

import (
	"errors"
	"net/http"
	"slices"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Configuration profiles bundle configurations and assign them to device
// groups.

type profileReq struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Enabled     *bool   `json:"enabled"`
	GroupIDs    []int64 `json:"groupIds"`
	ConfigIDs   []int64 `json:"configIds"`
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListConfigProfiles(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) loadProfile(w http.ResponseWriter, r *http.Request) (store.ConfigProfile, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.ConfigProfile{}, false
	}
	p, err := s.st.ConfigProfileByID(r.Context(), id, s.scope(r))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return p, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return p, false
	}
	return p, true
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.loadProfile(w, r); ok {
		writeJSON(w, http.StatusOK, p)
	}
}

func (s *Server) profileFromReq(w http.ResponseWriter, r *http.Request, req profileReq, p *store.ConfigProfile) bool {
	p.Name, p.Description = cleanText(req.Name, 200), cleanText(req.Description, 1000)
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if p.Name == "" {
		apiError(w, http.StatusBadRequest, "invalid_name")
		return false
	}
	p.GroupIDs, p.ConfigIDs = slices.Compact(slices.Sorted(slices.Values(req.GroupIDs))), slices.Compact(slices.Sorted(slices.Values(req.ConfigIDs)))
	for _, g := range p.GroupIDs {
		if _, err := s.st.GroupByID(r.Context(), g); err != nil {
			apiError(w, http.StatusBadRequest, "unknown_group")
			return false
		}
	}
	for _, id := range p.ConfigIDs {
		if _, err := s.st.ConfigurationByID(r.Context(), id, store.Scope{All: true}); err != nil {
			apiError(w, http.StatusBadRequest, "unknown_configuration")
			return false
		}
	}
	return true
}

func (s *Server) saveProfileResult(w http.ResponseWriter, r *http.Request, p store.ConfigProfile, err error, action string, status int) {
	switch {
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "profile_exists")
		return
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, action, p.Name, true, map[string]any{"id": p.ID, "enabled": p.Enabled, "groups": p.GroupIDs, "configurations": p.ConfigIDs})
	s.pushConfigsSoon()
	writeJSON(w, status, p)
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req profileReq
	if !decode(w, r, &req) {
		return
	}
	p := store.ConfigProfile{Enabled: true}
	if !s.profileFromReq(w, r, req, &p) {
		return
	}
	u, _ := currentUser(r)
	p.CreatedBy = u.Username
	p, err := s.st.CreateConfigProfile(r.Context(), p)
	s.saveProfileResult(w, r, p, err, "config.profile.create", http.StatusCreated)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProfile(w, r)
	if !ok {
		return
	}
	var req profileReq
	if !decode(w, r, &req) {
		return
	}
	if !s.profileFromReq(w, r, req, &p) {
		return
	}
	u, _ := currentUser(r)
	p.UpdatedBy = u.Username
	p, err := s.st.UpdateConfigProfile(r.Context(), p)
	s.saveProfileResult(w, r, p, err, "config.profile.update", http.StatusOK)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProfile(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteConfigProfile(r.Context(), p.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "config.profile.delete", p.Name, true, map[string]any{"id": p.ID})
	s.pushConfigsSoon()
	w.WriteHeader(http.StatusNoContent)
}
