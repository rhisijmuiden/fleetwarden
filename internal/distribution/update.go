package distribution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
)

// releaseVersion matches the versions the updater accepts.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-test\.[0-9]+)?$`)

func (s *Server) updatable() bool {
	if s.cfg.UpdateDir == "" {
		return false
	}
	st, err := os.Stat(s.cfg.UpdateDir)
	return err == nil && st.IsDir()
}

func (s *Server) updateStatus() *distproto.Update {
	if s.cfg.UpdateStatusDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.cfg.UpdateStatusDir, "status.json"))
	if err != nil {
		return nil
	}
	var st distproto.Update
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	return &st
}

// requestUpdate passes the management server's request to the root updater
// (fleetwarden-distribution-update.path) as a version number only.
func (s *Server) requestUpdate(version string) {
	if version == s.version || !releaseVersion.MatchString(version) || !s.updatable() {
		return
	}
	path := filepath.Join(s.cfg.UpdateDir, "request.json")
	if _, err := os.Stat(path); err == nil {
		return // already asked
	}
	raw, _ := json.Marshal(map[string]string{"version": version})
	if err := writeFile(path, raw); err != nil {
		s.log.Error("update request", "err", err)
		return
	}
	s.log.Info("update requested by the management server", "version", version)
}
