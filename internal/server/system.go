package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/updates"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

type updateSettings struct {
	Channel      string `json:"channel"`
	LastReported string `json:"lastReported"`
}

func (s *Server) loadUpdateSettings(ctx context.Context) updateSettings {
	var u updateSettings
	_ = s.st.GetSetting(ctx, keyUpdates, &u)
	if !updates.ValidChannel(u.Channel) {
		u.Channel = version.Channel
		if !updates.ValidChannel(u.Channel) {
			u.Channel = updates.ChannelProduction
		}
	}
	return u
}

type updateView struct {
	Current         version.Info     `json:"current"`
	Channel         string           `json:"channel"`
	Latest          *updates.Release `json:"latest"`
	UpdateAvailable bool             `json:"updateAvailable"`
	CheckError      string           `json:"checkError,omitempty"`
	Status          updates.Status   `json:"status"`
	Pending         bool             `json:"pending"`
	CheckedAt       time.Time        `json:"checkedAt"`
}

func (s *Server) updateView(r *http.Request, force bool) updateView {
	us := s.loadUpdateSettings(r.Context())
	v := updateView{Current: version.Get(), Channel: us.Channel, CheckedAt: s.now()}
	latest, err := s.updates.Latest(r.Context(), us.Channel, force)
	if err != nil {
		v.CheckError = err.Error()
	}
	v.Latest = latest
	v.UpdateAvailable = latest != nil && updates.Compare(latest.Version, version.Version) > 0
	v.Status, _ = updates.ReadStatus(s.cfg.UpdateStatusDir)
	v.Pending = updates.Pending(s.cfg.UpdateRequestDir())
	return v
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateView(r, false))
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	v := s.updateView(r, true)
	s.audit(r, "system.update_check", v.Channel, v.CheckError == "", map[string]any{"available": v.UpdateAvailable})
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleSetChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !updates.ValidChannel(req.Channel) {
		apiError(w, http.StatusBadRequest, "invalid_channel")
		return
	}
	us := s.loadUpdateSettings(r.Context())
	from := us.Channel
	us.Channel = req.Channel
	if err := s.st.PutSetting(r.Context(), keyUpdates, us); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "system.channel", req.Channel, true, map[string]any{"from": from})
	writeJSON(w, http.StatusOK, s.updateView(r, false))
}

func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if !decode(w, r, &req) {
		return
	}
	v := s.updateView(r, false)
	// Only the newest release of the selected channel may be installed, and
	// never an older version: migrations are forward-only.
	if v.Latest == nil || v.Latest.Version != req.Version {
		apiError(w, http.StatusBadRequest, "version_not_offered")
		return
	}
	if !v.UpdateAvailable {
		apiError(w, http.StatusBadRequest, "no_update")
		return
	}
	u, _ := currentUser(r)
	err := updates.RequestUpdate(s.cfg.UpdateRequestDir(), updates.Request{Version: req.Version, RequestedBy: u.Username, RequestedAt: s.now()}, v.Status)
	if errors.Is(err, updates.ErrBusy) {
		apiError(w, http.StatusConflict, "update_in_progress")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "system.update", req.Version, true, map[string]any{"from": version.Version, "channel": v.Channel})
	writeJSON(w, http.StatusAccepted, s.updateView(r, false))
}

// reportUpdateOutcome audits and mails the result of a finished update once.
// It runs at startup and periodically, because a successful update restarts the server.
func (s *Server) reportUpdateOutcome(ctx context.Context) {
	st, err := updates.ReadStatus(s.cfg.UpdateStatusDir)
	if err != nil || st.ID == "" || st.Running() || st.State == "" {
		return
	}
	us := s.loadUpdateSettings(ctx)
	if us.LastReported == st.ID {
		return
	}
	us.LastReported = st.ID
	if err := s.st.PutSetting(ctx, keyUpdates, us); err != nil {
		return
	}
	ok := st.State == "completed"
	_ = s.st.Audit(ctx, auditSystem("system.update_result", st.ToVersion, ok, map[string]any{
		"state": st.State, "from": st.FromVersion, "message": st.Message}))
	kind := "update_success"
	if !ok {
		kind = "update_failed"
	}
	emails, _ := s.st.AdminEmails(ctx)
	b := s.loadBranding(ctx)
	var g generalSettings
	_ = s.st.GetSetting(ctx, keyGeneral, &g)
	for _, to := range emails {
		s.enqueue(ctx, kind, "en", to, mailBrandOf(b, g), map[string]string{"version": st.ToVersion, "detail": st.Message})
	}
}
