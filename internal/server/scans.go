package server

import (
	"context"
	"net/http"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

const keyScans = "scans"

// Scan interval limits, in minutes. Inventory is cheap; a Windows Update
// scan is not, so it runs at most once an hour.
const (
	minInventoryMinutes = 15
	minPatchScanMinutes = 60
	maxScanMinutes      = 7 * 24 * 60
	defaultScanMinutes  = 6 * 60
)

// scanSettings are the server-wide defaults for how often agents send their
// inventory and scan for missing updates. A device group may override both.
type scanSettings struct {
	InventoryMinutes int `json:"inventoryMinutes"`
	PatchScanMinutes int `json:"patchScanMinutes"`
}

func (c scanSettings) withDefaults() scanSettings {
	if c.InventoryMinutes == 0 {
		c.InventoryMinutes = defaultScanMinutes
	}
	if c.PatchScanMinutes == 0 {
		c.PatchScanMinutes = defaultScanMinutes
	}
	return c
}

// effective applies a group's overrides.
func (c scanSettings) effective(inventory, patch *int) scanSettings {
	if inventory != nil {
		c.InventoryMinutes = *inventory
	}
	if patch != nil {
		c.PatchScanMinutes = *patch
	}
	return c
}

func validInventoryMinutes(m int) bool { return m >= minInventoryMinutes && m <= maxScanMinutes }
func validPatchScanMinutes(m int) bool { return m >= minPatchScanMinutes && m <= maxScanMinutes }

func (s *Server) loadScanSettings(ctx context.Context) scanSettings {
	var c scanSettings
	_ = s.st.GetSetting(ctx, keyScans, &c)
	return c.withDefaults()
}

// pushAgentConfig sends the inventory interval when it changed for the
// session. Patch scans are asked for by the server, so they need nothing here.
func (s *Server) pushAgentConfig(id string, c scanSettings) {
	s.hub.sendChanged(id, agentConfigKey, agentproto.TypeAgentConfig, agentproto.AgentConfig{InventoryMinutes: c.InventoryMinutes})
}

const agentConfigKey = "agent.config"

func (s *Server) handleGetScans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadScanSettings(r.Context()))
}

func (s *Server) handlePutScans(w http.ResponseWriter, r *http.Request) {
	var req scanSettings
	if !decode(w, r, &req) {
		return
	}
	if !validInventoryMinutes(req.InventoryMinutes) || !validPatchScanMinutes(req.PatchScanMinutes) {
		apiError(w, http.StatusBadRequest, "invalid_scan_interval")
		return
	}
	if err := s.st.PutSetting(r.Context(), keyScans, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.scans", "scans", true, map[string]any{"inventoryMinutes": req.InventoryMinutes, "patchScanMinutes": req.PatchScanMinutes})
	// Online agents get the new interval on the next scheduler tick, within
	// a minute.
	writeJSON(w, http.StatusOK, req)
}

// inventoryDue reports whether a device's inventory is older than its interval.
func inventoryDue(at *time.Time, now time.Time, minutes int) bool {
	return at == nil || now.Sub(*at) > time.Duration(minutes)*time.Minute
}
