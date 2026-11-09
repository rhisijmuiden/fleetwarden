package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/rhisijmuiden/fleetwarden/internal/gwproto"
)

// releaseVersion matches the versions the updater accepts (as the server's).
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-test\.[0-9]+)?$`)

func (g *Gateway) updatable() bool {
	if g.cfg.UpdateDir == "" {
		return false
	}
	st, err := os.Stat(g.cfg.UpdateDir)
	return err == nil && st.IsDir()
}

func (g *Gateway) updateStatus() *gwproto.UpdateStatus {
	if g.cfg.UpdateStatusDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(g.cfg.UpdateStatusDir, "status.json"))
	if err != nil {
		return nil
	}
	var st gwproto.UpdateStatus
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	return &st
}

// handleUpdate passes the paired server's update request to the root
// updater (fleetwarden-gateway-update.path) as a version number only.
func (g *Gateway) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req gwproto.UpdateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || !releaseVersion.MatchString(req.Version) {
		jsonError(w, http.StatusBadRequest, "invalid_version")
		return
	}
	if !g.updatable() {
		jsonError(w, http.StatusConflict, "not_updatable")
		return
	}
	raw, _ := json.Marshal(req)
	if err := writeFile(filepath.Join(g.cfg.UpdateDir, "request.json"), raw); err != nil {
		g.log.Error("update request", "err", err)
		jsonError(w, http.StatusInternalServerError, "internal")
		return
	}
	g.log.Info("update requested by the management server", "version", req.Version)
	writeJSON(w, http.StatusAccepted, map[string]string{"version": req.Version})
}
