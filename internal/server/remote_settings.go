package server

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const keyRemote = "remote"

// remoteSettings configure remote desktop sessions server-wide.
type remoteSettings struct {
	// DirectOff turns the direct path (WebRTC) off, so every session goes
	// over the relay. Stored inverted, so a missing setting means on.
	DirectOff bool `json:"directOff"`
	// STUNServers let the direct path cross NAT ("stun:host:port"). Without
	// them it only works where the browser can reach the device's own
	// addresses (the local network, a VPN).
	STUNServers []string `json:"stunServers"`
}

// remoteSettingsView is the API shape.
type remoteSettingsView struct {
	Direct      bool     `json:"direct"`
	STUNServers []string `json:"stunServers"`
}

const maxSTUNServers = 5

func (s *Server) loadRemoteSettings(ctx context.Context) remoteSettings {
	var c remoteSettings
	_ = s.st.GetSetting(ctx, keyRemote, &c)
	if c.STUNServers == nil {
		c.STUNServers = []string{}
	}
	return c
}

// validSTUN accepts stun:host or stun:host:port (and stuns:).
func validSTUN(u string) bool {
	rest, ok := strings.CutPrefix(u, "stun:")
	if !ok {
		rest, ok = strings.CutPrefix(u, "stuns:")
	}
	if !ok || rest == "" || strings.ContainsAny(rest, "/?# ") {
		return false
	}
	host := rest
	if h, p, err := net.SplitHostPort(rest); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
		host = h
	} else if strings.Count(rest, ":") > 0 && net.ParseIP(rest) == nil {
		return false
	}
	return host != "" && len(host) <= 253
}

func (s *Server) handleGetRemoteSettings(w http.ResponseWriter, r *http.Request) {
	c := s.loadRemoteSettings(r.Context())
	writeJSON(w, http.StatusOK, remoteSettingsView{Direct: !c.DirectOff, STUNServers: c.STUNServers})
}

func (s *Server) handlePutRemoteSettings(w http.ResponseWriter, r *http.Request) {
	var req remoteSettingsView
	if !decode(w, r, &req) {
		return
	}
	var stun []string
	for _, u := range req.STUNServers {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if !validSTUN(u) {
			apiError(w, http.StatusBadRequest, "invalid_stun_server")
			return
		}
		stun = append(stun, u)
	}
	if len(stun) > maxSTUNServers {
		apiError(w, http.StatusBadRequest, "too_many_stun_servers")
		return
	}
	if stun == nil {
		stun = []string{}
	}
	c := remoteSettings{DirectOff: !req.Direct, STUNServers: stun}
	if err := s.st.PutSetting(r.Context(), keyRemote, c); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.remote", "remote", true, map[string]any{"direct": req.Direct, "stunServers": stun})
	writeJSON(w, http.StatusOK, remoteSettingsView{Direct: req.Direct, STUNServers: stun})
}
