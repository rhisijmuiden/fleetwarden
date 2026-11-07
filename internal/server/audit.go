package server

import (
	"net/http"
	"strconv"

	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.st.ListAudit(r.Context(), store.AuditQuery{Before: before, Search: q.Get("q"), Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func auditSystem(action, target string, ok bool, details map[string]any) store.AuditEntry {
	return store.AuditEntry{Actor: "system", Action: action, Target: target, Success: ok, Details: details}
}

func mailBrandOf(b brandingSettings, g generalSettings) mail.Brand {
	return mail.Brand{Product: b.ProductName, Color: b.PrimaryColor, URL: g.PublicURL}
}
