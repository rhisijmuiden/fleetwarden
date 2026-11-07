package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// loadMail returns the mail configuration with secrets decrypted.
func (s *Server) loadMail(ctx context.Context) (mail.Config, error) {
	var c mail.Config
	err := s.st.GetSetting(ctx, keyMail, &c)
	if errors.Is(err, store.ErrNotFound) {
		return mail.Config{Port: 587, Security: mail.SecuritySTARTTLS, Auth: mail.AuthPlain}, nil
	}
	if err != nil {
		return c, err
	}
	if c.Password, err = s.box.Open(c.Password); err != nil {
		return c, err
	}
	c.OAuthSecret, err = s.box.Open(c.OAuthSecret)
	return c, err
}

type mailView struct {
	mail.Config
	PasswordSet    bool `json:"passwordSet"`
	OAuthSecretSet bool `json:"oauthSecretSet"`
}

func viewOf(c mail.Config) mailView {
	v := mailView{Config: c, PasswordSet: c.Password != "", OAuthSecretSet: c.OAuthSecret != ""}
	v.Password, v.OAuthSecret = "", ""
	return v
}

func (s *Server) handleGetMail(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadMail(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(c))
}

// mergeMail applies a form to the stored configuration: empty secrets keep the
// stored value.
func mergeMail(cur, req mail.Config) mail.Config {
	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	req.FromAddress = strings.TrimSpace(req.FromAddress)
	req.OAuthTenant = strings.TrimSpace(req.OAuthTenant)
	req.OAuthClient = strings.TrimSpace(req.OAuthClient)
	if req.Password == "" {
		req.Password = cur.Password
	}
	if req.OAuthSecret == "" {
		req.OAuthSecret = cur.OAuthSecret
	}
	return req
}

func (s *Server) handlePutMail(w http.ResponseWriter, r *http.Request) {
	var req mail.Config
	if !decode(w, r, &req) {
		return
	}
	cur, err := s.loadMail(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	next := mergeMail(cur, req)
	if next.Enabled {
		if err := next.Validate(); err != nil {
			apiErrorDetail(w, http.StatusBadRequest, "invalid_mail_config", err.Error())
			return
		}
	}
	stored := next
	if stored.Password, err = s.box.Seal(next.Password); err != nil {
		s.internalError(w, r, err)
		return
	}
	if stored.OAuthSecret, err = s.box.Seal(next.OAuthSecret); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.PutSetting(r.Context(), keyMail, stored); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.mail", next.Host, true, map[string]any{"enabled": next.Enabled, "host": next.Host, "port": next.Port,
		"security": next.Security, "auth": next.Auth, "from": next.FromAddress, "secretChanged": req.Password != "" || req.OAuthSecret != ""})
	writeJSON(w, http.StatusOK, viewOf(next))
}

func (s *Server) mailBrand(r *http.Request) mail.Brand {
	b := s.loadBranding(r.Context())
	return mail.Brand{Product: b.ProductName, Color: b.PrimaryColor, URL: s.baseURL(r)}
}

// handleTestMail sends a test message right away with the settings in the form
// (not yet saved), so the administrator sees the SMTP error directly.
func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		mail.Config
		To string `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.To = strings.TrimSpace(req.To)
	if !mail.ValidAddress(req.To) {
		apiError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	cur, err := s.loadMail(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cfg := mergeMail(cur, req.Config)
	cfg.Enabled = true
	if err := cfg.Validate(); err != nil {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_mail_config", err.Error())
		return
	}
	u, _ := currentUser(r)
	msg := mail.Render("test", u.Language, req.To, s.mailBrand(r), nil)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	sendErr := s.mailer.Send(ctx, cfg, msg)
	_ = s.st.RecordMail(r.Context(), store.Mail{Kind: "test", To: msg.To, Subject: msg.Subject, BodyText: msg.Text, BodyHTML: msg.HTML}, sendErr)
	if sendErr != nil {
		s.audit(r, "mail.test", req.To, false, map[string]any{"error": sendErr.Error()})
		apiErrorDetail(w, http.StatusBadGateway, "mail_send_failed", sendErr.Error())
		return
	}
	s.audit(r, "mail.test", req.To, true, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMailLog(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListMail(r.Context(), 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleMailRetry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.st.RetryMail(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "mail.retry", r.PathValue("id"), true, nil)
	w.WriteHeader(http.StatusNoContent)
}

// enqueue renders and queues a message for the background worker.
func (s *Server) enqueue(ctx context.Context, kind, lang, to string, brand mail.Brand, vars map[string]string) {
	msg := mail.Render(kind, lang, to, brand, vars)
	if _, err := s.st.EnqueueMail(ctx, store.Mail{Kind: kind, To: to, Subject: msg.Subject, BodyText: msg.Text, BodyHTML: msg.HTML}); err != nil {
		s.log.Error("enqueue mail", "kind", kind, "err", err)
	}
}
