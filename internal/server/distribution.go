package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const (
	distTokenTTL       = 24 * time.Hour
	distOnlineFor      = 3 * time.Minute // a server that reported within this is online
	distManifestCache  = 30 * time.Second
	distMaxPerAgent    = 3
	distAssignKey      = "agent.distribution"
	distMaxBandwidth   = 10_000_000 // kbit/s (10 Gbit/s)
	distMaxNetworks    = 50
	distInstallerRepo  = "rhisijmuiden/fleetwarden"
	distReportBodySize = 256 << 10
)

// distState is what the server keeps in memory about distribution servers.
type distState struct {
	mu        sync.Mutex
	manifest  distproto.Manifest
	paths     map[string]string // sha256 -> file on disk
	builtAt   time.Time
	agents    map[int64]int // devices assigned, from the last assignment round
	assignMu  sync.Mutex
	lastRound time.Time
}

// ---- manifest: what every distribution server keeps ----

// distManifest returns the files distribution servers keep: uploaded
// installers, driver files in the repository and agent packages.
func (s *Server) distManifest(ctx context.Context) (distproto.Manifest, map[string]string, error) {
	d := &s.dist
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paths != nil && s.now().Sub(d.builtAt) < distManifestCache {
		return d.manifest, d.paths, nil
	}
	rows, err := s.st.DistributableFiles(ctx)
	if err != nil {
		return distproto.Manifest{}, nil, err
	}
	files := []distproto.File{}
	paths := map[string]string{}
	add := func(f distproto.File, path string) {
		if _, ok := paths[f.SHA256]; ok {
			return
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return
		}
		f.Size = st.Size()
		paths[f.SHA256] = path
		files = append(files, f)
	}
	for _, r := range rows {
		if !distproto.ValidSHA(r.SHA256) {
			continue
		}
		switch r.Kind {
		case "software":
			add(distproto.File{SHA256: r.SHA256, Kind: "software"}, filepath.Join(s.softwareDir(), r.SHA256))
		case "driver":
			p := filepath.Join(s.repositoryDir(), r.SHA256)
			if _, err := os.Stat(p); err != nil {
				p = filepath.Join(s.uploadsDir(), r.SHA256)
			}
			add(distproto.File{SHA256: r.SHA256, Kind: "driver"}, p)
		}
	}
	for _, p := range s.agentPackages() {
		sum := strings.ToLower(p.SHA256)
		if distproto.ValidSHA(sum) && p.path != "" {
			if _, ok := paths[sum]; !ok {
				add(distproto.File{SHA256: sum, Kind: "agent", Name: p.Name}, p.path)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].SHA256 < files[j].SHA256 })
	d.manifest = distproto.Manifest{Version: distproto.ManifestVersion(files), Files: files}
	d.paths, d.builtAt = paths, s.now()
	return d.manifest, d.paths, nil
}

// invalidateDistManifest makes the next request rebuild the manifest.
func (s *Server) invalidateDistManifest() {
	s.dist.mu.Lock()
	s.dist.paths = nil
	s.dist.mu.Unlock()
}

// ---- requests from distribution servers ----

func distIdentity(id int64) string { return distproto.IDPrefix + strconv.FormatInt(id, 10) }

// distFromRequest checks a request signed by a distribution server.
func (s *Server) distFromRequest(w http.ResponseWriter, r *http.Request) (store.DistributionServer, bool) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return store.DistributionServer{}, false
	}
	ident, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(agentproto.HeaderAuth))
	var d store.DistributionServer
	if err == nil {
		id, perr := strconv.ParseInt(strings.TrimPrefix(ident, distproto.IDPrefix), 10, 64)
		if perr != nil || !strings.HasPrefix(ident, distproto.IDPrefix) {
			err = errors.New("not a distribution server")
		} else if d, err = s.st.DistributionServerByID(r.Context(), id); err == nil &&
			!agentproto.VerifyRequest(ed25519.PublicKey(d.PublicKey), ident, r.Method, r.URL.Path, ts, sig, s.now(), 5*time.Minute) {
			err = errors.New("bad signature")
		}
	}
	if err != nil {
		s.agentFails.Fail(ip)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return d, false
	}
	return d, true
}

// handleDistEnroll uses a one-time token from the web interface.
func (s *Server) handleDistEnroll(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var req distproto.EnrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || len(req.PublicKey) != ed25519.PublicKeySize || req.Token == "" {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	cert := strings.ToLower(strings.TrimSpace(req.CertSHA256))
	if !distproto.ValidSHA(cert) {
		apiError(w, http.StatusBadRequest, "bad_certificate")
		return
	}
	port := req.Port
	if port < 1 || port > 65535 {
		port = distproto.DefaultPort
	}
	u := ""
	if a := firstLANAddress(req.Addresses); a != "" {
		u = "https://" + net.JoinHostPort(a, strconv.Itoa(port))
	}
	d, err := s.st.EnrollDistributionServer(r.Context(), hashDistToken(req.Token), store.DistributionEnroll{
		PublicKey: req.PublicKey, Hostname: cleanText(req.Hostname, 100), Version: cleanText(req.Version, 50), CertSHA256: cert, URL: u,
	})
	if errors.Is(err, store.ErrNotFound) {
		s.agentFails.Fail(ip)
		s.auditAs(r, nil, "distribution", "distribution.enroll", cleanText(req.Hostname, 100), false, map[string]any{"error": "invalid_token"})
		apiError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "key_in_use")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.auditAs(r, nil, "distribution", "distribution.enroll", d.Name, true, map[string]any{"hostname": d.Hostname, "url": d.URL, "version": d.Version})
	s.log.Info("distribution server enrolled", "name", d.Name, "hostname", d.Hostname, "url", d.URL)
	writeJSON(w, http.StatusOK, distproto.EnrollResponse{ID: distIdentity(d.ID), Name: d.Name})
}

// firstLANAddress picks the address agents most likely reach: a private
// IPv4 address, else any global one.
func firstLANAddress(addrs []string) string {
	var fallback string
	for _, a := range addrs {
		ip, err := netip.ParseAddr(strings.TrimSpace(a))
		if err != nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		ip = ip.Unmap()
		if ip.Is4() && ip.IsPrivate() {
			return ip.String()
		}
		if fallback == "" {
			fallback = ip.String()
		}
	}
	return fallback
}

func (s *Server) handleDistStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distFromRequest(w, r)
	if !ok {
		return
	}
	var st distproto.Status
	raw, err := io.ReadAll(io.LimitReader(r.Body, distReportBodySize))
	if err != nil || json.Unmarshal(raw, &st) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	cert := strings.ToLower(st.CertSHA256)
	if !distproto.ValidSHA(cert) {
		cert = d.CertSHA256
	}
	st.ReportedAt = s.now().UTC()
	if len(st.Recent) > 20 {
		st.Recent = st.Recent[:20]
	}
	if len(st.Errors) > 10 {
		st.Errors = st.Errors[:10]
	}
	clean, _ := json.Marshal(st)
	ver := cleanText(st.Version, 50)
	wasOnline := d.LastSeenAt != nil && s.now().Sub(*d.LastSeenAt) < distOnlineFor
	if err := s.st.DistributionSeen(r.Context(), d.ID, ver, cleanText(st.Hostname, 100), cert, clean); err != nil {
		s.internalError(w, r, err)
		return
	}
	reply := distproto.StatusReply{Enabled: d.Enabled, BandwidthKbps: d.BandwidthKbps}
	if d.UpdateVersion != "" && d.UpdateVersion != ver {
		// Sent once; pressing Update again asks again.
		reply.Update = d.UpdateVersion
		_ = s.st.RequestDistributionUpdate(r.Context(), d.ID, "")
	}
	if d.Enabled {
		m, _, err := s.distManifest(r.Context())
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		reply.Manifest = m.Version
	}
	if !wasOnline || cert != d.CertSHA256 {
		go s.assignDistribution(context.WithoutCancel(r.Context())) // agents learn about it now, not in a minute
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleDistManifest(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distFromRequest(w, r)
	if !ok {
		return
	}
	if !d.Enabled {
		apiError(w, http.StatusForbidden, "disabled")
		return
	}
	m, _, err := s.distManifest(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleDistContent sends a file in the manifest to a distribution server.
func (s *Server) handleDistContent(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distFromRequest(w, r)
	if !ok {
		return
	}
	if !d.Enabled {
		http.Error(w, "disabled", http.StatusForbidden)
		return
	}
	sum := strings.ToLower(r.PathValue("sha"))
	_, paths, err := s.distManifest(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	path, ok := paths[sum]
	if !ok {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.invalidateDistManifest()
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, sum, st.ModTime(), f)
}

// handleDistAuthorize decides, as for a direct download, whether the agent
// that asked a distribution server may have the file. The answer names the
// file by its hash, so the distribution server serves exactly that.
func (s *Server) handleDistAuthorize(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distFromRequest(w, r)
	if !ok {
		return
	}
	if !d.Enabled {
		apiError(w, http.StatusForbidden, "disabled")
		return
	}
	path := r.Header.Get(distproto.HeaderAgentPath)
	if !distproto.Served(path) {
		apiError(w, http.StatusBadRequest, "bad_path")
		return
	}
	devID, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(distproto.HeaderAgentAuth))
	if err != nil {
		apiError(w, http.StatusForbidden, "bad_signature")
		return
	}
	dev, err := s.st.DeviceByID(r.Context(), devID)
	if err != nil || !agentproto.VerifyRequest(dev.PublicKey, dev.ID, http.MethodGet, path, ts, sig, s.now(), 5*time.Minute) {
		apiError(w, http.StatusForbidden, "bad_signature")
		return
	}
	_, paths, err := s.distManifest(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var sum string
	switch {
	case strings.HasPrefix(path, agentproto.PathSoftware):
		sum = strings.ToLower(strings.TrimPrefix(path, agentproto.PathSoftware))
		if ok, err := s.st.DeviceMayDownload(r.Context(), dev.ID, sum); err != nil || !ok {
			sum = ""
		}
	case strings.HasPrefix(path, agentproto.PathRepository):
		sum = strings.ToLower(strings.TrimPrefix(path, agentproto.PathRepository))
		if ok, err := s.st.DeviceMayDownloadDriver(r.Context(), dev.ID, sum); err != nil || !ok {
			sum = ""
		}
	case strings.HasPrefix(path, agentproto.PathDownload):
		name := strings.TrimPrefix(path, agentproto.PathDownload)
		for _, p := range s.agentPackages() {
			if p.Name == name {
				sum = strings.ToLower(p.SHA256)
				break
			}
		}
	}
	p, ok := paths[sum]
	if sum == "" || !ok {
		apiError(w, http.StatusNotFound, "no_such_file")
		return
	}
	st, err := os.Stat(p)
	if err != nil {
		apiError(w, http.StatusNotFound, "no_such_file")
		return
	}
	writeJSON(w, http.StatusOK, distproto.AuthorizeReply{SHA256: sum, Size: st.Size()})
}

// ---- which agents use which distribution server ----

// distOnline reports whether agents may be sent to the server.
func (s *Server) distOnline(d store.DistributionServer) bool {
	return d.Enabled && d.Enrolled && d.URL != "" && distproto.ValidSHA(d.CertSHA256) &&
		d.LastSeenAt != nil && s.now().Sub(*d.LastSeenAt) < distOnlineFor
}

type distCandidate struct {
	srv      store.DistributionServer
	networks []netip.Prefix
	groups   []int64
}

func newDistCandidates(servers []store.DistributionServer) []distCandidate {
	out := []distCandidate{}
	for _, d := range servers {
		c := distCandidate{srv: d, groups: d.GroupIDs}
		for _, n := range d.Networks {
			if p, err := netip.ParsePrefix(n); err == nil {
				c.networks = append(c.networks, p.Masked())
			}
		}
		out = append(out, c)
	}
	return out
}

// distServersFor picks the servers for a device: the most specific network
// that holds one of its addresses first, then group membership.
func distServersFor(dev store.PowerDevice, cands []distCandidate) []agentproto.DistributionServer {
	var addrs []netip.Addr
	for _, ip := range newPowerNet(dev).ips {
		if a, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, a.Unmap())
		}
	}
	for _, v := range dev.LocalIPs {
		if a, err := netip.ParseAddr(v); err == nil {
			addrs = append(addrs, a.Unmap())
		}
	}
	if a, err := netip.ParseAddr(dev.RemoteIP); err == nil {
		addrs = append(addrs, a.Unmap())
	}
	type hit struct {
		c    distCandidate
		bits int
	}
	var hits []hit
	for _, c := range cands {
		best := -1
		for _, n := range c.networks {
			for _, a := range addrs {
				if n.Contains(a) && n.Bits() > best {
					best = n.Bits()
				}
			}
		}
		if best < 0 && slices.Contains(c.groups, dev.GroupID) {
			best = 0
		}
		if best >= 0 {
			hits = append(hits, hit{c, best})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].bits != hits[j].bits {
			return hits[i].bits > hits[j].bits
		}
		return strings.ToLower(hits[i].c.srv.Name) < strings.ToLower(hits[j].c.srv.Name)
	})
	out := []agentproto.DistributionServer{}
	for _, h := range hits {
		if len(out) == distMaxPerAgent {
			break
		}
		out = append(out, agentproto.DistributionServer{ID: h.c.srv.ID, Name: h.c.srv.Name, URL: h.c.srv.URL, CertSHA256: h.c.srv.CertSHA256})
	}
	return out
}

// onlineDistCandidates returns the servers agents may use now.
func (s *Server) onlineDistCandidates(ctx context.Context) ([]distCandidate, []store.DistributionServer, error) {
	all, err := s.st.ListDistributionServers(ctx)
	if err != nil {
		return nil, nil, err
	}
	var on []store.DistributionServer
	for _, d := range all {
		if s.distOnline(d) {
			on = append(on, d)
		}
	}
	return newDistCandidates(on), all, nil
}

// assignDistribution tells every connected agent which distribution
// servers to use (only when that changed) and counts them per server.
func (s *Server) assignDistribution(ctx context.Context) {
	if s.st == nil {
		return
	}
	s.dist.assignMu.Lock()
	defer s.dist.assignMu.Unlock()
	cands, _, err := s.onlineDistCandidates(ctx)
	if err != nil {
		s.log.Warn("distribution servers", "err", err)
		return
	}
	counts := map[int64]int{}
	ids := s.hub.onlineIDs()
	var devs []store.PowerDevice
	if len(ids) > 0 {
		if devs, err = s.st.PowerDevices(ctx, store.PowerDeviceQuery{IDs: ids}); err != nil {
			s.log.Warn("distribution servers", "err", err)
			return
		}
	}
	for _, dev := range devs {
		list := distServersFor(dev, cands)
		for _, d := range list {
			counts[d.ID]++
		}
		s.hub.sendChanged(dev.ID, distAssignKey, agentproto.TypeDistribution, agentproto.Distribution{Servers: list})
	}
	s.dist.mu.Lock()
	s.dist.agents, s.dist.lastRound = counts, s.now()
	s.dist.mu.Unlock()
}

// sendDistribution tells one agent, right after it connected.
func (s *Server) sendDistribution(ctx context.Context, id string) {
	cands, _, err := s.onlineDistCandidates(ctx)
	if err != nil {
		return
	}
	devs, err := s.st.PowerDevices(ctx, store.PowerDeviceQuery{IDs: []string{id}})
	if err != nil || len(devs) != 1 {
		return
	}
	s.hub.sendChanged(id, distAssignKey, agentproto.TypeDistribution, agentproto.Distribution{Servers: distServersFor(devs[0], cands)})
}

func (s *Server) distributionTick(ctx context.Context) {
	s.invalidateDistManifest()
	s.assignDistribution(ctx)
}

// ---- API ----

func (s *Server) distributionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+distproto.PathEnroll, s.handleDistEnroll)
	mux.HandleFunc("POST "+distproto.PathStatus, s.handleDistStatus)
	mux.HandleFunc("GET "+distproto.PathManifest, s.handleDistManifest)
	mux.HandleFunc("GET "+distproto.PathContent+"{sha}", s.handleDistContent)
	mux.HandleFunc("GET "+distproto.PathAuthorize, s.handleDistAuthorize)

	adm := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSettingsManage, 0, h) }
	mux.Handle("GET /api/v1/distribution", adm(s.handleListDistribution))
	mux.Handle("POST /api/v1/distribution", adm(s.handleCreateDistribution))
	mux.Handle("PUT /api/v1/distribution/{id}", adm(s.handleUpdateDistribution))
	mux.Handle("POST /api/v1/distribution/{id}/token", adm(s.handleDistributionToken))
	mux.Handle("DELETE /api/v1/distribution/{id}", adm(s.handleDeleteDistribution))
	mux.Handle("POST /api/v1/distribution/{id}/update", s.authed(auth.PermSystemUpdate, 0, s.handleDistributionSoftwareUpdate))
}

type distView struct {
	store.DistributionServer
	State  string            `json:"state"` // pending, online, offline, disabled
	Agents int               `json:"agents"`
	Status *distproto.Status `json:"status"`
}

type distListView struct {
	Servers       []distView `json:"servers"`
	ManifestFiles int        `json:"manifestFiles"`
	ManifestBytes int64      `json:"manifestBytes"`
	Manifest      string     `json:"manifest"`
	Version       string     `json:"version"`
	Channel       string     `json:"channel"`
	ServerURL     string     `json:"serverUrl"`
	Port          int        `json:"port"`
}

func (s *Server) distViewOf(d store.DistributionServer) distView {
	v := distView{DistributionServer: d}
	switch {
	case !d.Enrolled:
		v.State = "pending"
	case !d.Enabled:
		v.State = "disabled"
	case d.LastSeenAt != nil && s.now().Sub(*d.LastSeenAt) < distOnlineFor:
		v.State = "online"
	default:
		v.State = "offline"
	}
	if len(d.Status) > 0 {
		var st distproto.Status
		if json.Unmarshal(d.Status, &st) == nil {
			v.Status = &st
		}
	}
	s.dist.mu.Lock()
	v.Agents = s.dist.agents[d.ID]
	s.dist.mu.Unlock()
	return v
}

func (s *Server) handleListDistribution(w http.ResponseWriter, r *http.Request) {
	all, err := s.st.ListDistributionServers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	m, _, err := s.distManifest(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := distListView{Servers: []distView{}, ManifestFiles: len(m.Files), Manifest: m.Version, Version: version.Version,
		Channel: version.Get().Channel, ServerURL: s.baseURL(r), Port: distproto.DefaultPort}
	for _, f := range m.Files {
		out.ManifestBytes += f.Size
	}
	for _, d := range all {
		out.Servers = append(out.Servers, s.distViewOf(d))
	}
	writeJSON(w, http.StatusOK, out)
}

type distReq struct {
	Name          string   `json:"name"`
	URL           string   `json:"url"`
	Networks      []string `json:"networks"`
	GroupIDs      []int64  `json:"groupIds"`
	BandwidthKbps int      `json:"bandwidthKbps"`
	Enabled       *bool    `json:"enabled"`
}

// normalizeNetworks accepts CIDRs and single addresses.
func normalizeNetworks(in []string) ([]string, bool) {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		var p netip.Prefix
		if a, err := netip.ParseAddr(v); err == nil {
			p = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		} else if p, err = netip.ParsePrefix(v); err != nil {
			return nil, false
		}
		p = p.Masked()
		if s := p.String(); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, len(out) <= distMaxNetworks
}

// normalizeDistURL validates the HTTPS address agents use.
func normalizeDistURL(v string) (string, bool) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", true
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(distproto.DefaultPort))
	}
	return "https://" + u.Host, true
}

func (s *Server) validDistReq(w http.ResponseWriter, r *http.Request, req *distReq, id int64) bool {
	req.Name = cleanText(req.Name, 100)
	nets, okN := normalizeNetworks(req.Networks)
	u, okU := normalizeDistURL(req.URL)
	switch {
	case req.Name == "":
		apiError(w, http.StatusBadRequest, "invalid_name")
		return false
	case !okN:
		apiError(w, http.StatusBadRequest, "invalid_network")
		return false
	case !okU:
		apiError(w, http.StatusBadRequest, "invalid_url")
		return false
	case req.BandwidthKbps < 0 || req.BandwidthKbps > distMaxBandwidth:
		apiError(w, http.StatusBadRequest, "invalid_bandwidth")
		return false
	}
	req.Networks, req.URL = nets, u
	if req.GroupIDs == nil {
		req.GroupIDs = []int64{}
	}
	taken, err := s.st.DistributionNameTaken(r.Context(), req.Name, id)
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	if taken {
		apiError(w, http.StatusConflict, "name_taken")
		return false
	}
	return true
}

func hashDistToken(tok string) []byte {
	h := sha256.Sum256([]byte("fleetwarden-dist-token\x00" + strings.TrimSpace(tok)))
	return h[:]
}

type distTokenView struct {
	Server  distView  `json:"server"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
	Command string    `json:"command"`
}

// distInstallCommand installs the server's own version, so the
// distribution server and the management server match.
func (s *Server) distInstallCommand(r *http.Request, token string) string {
	base := s.baseURL(r)
	v := version.Version
	if releaseVersion.MatchString(v) {
		return "curl -fsSL https://github.com/" + distInstallerRepo + "/releases/download/v" + v + "/install-distribution.sh | sudo bash -s -- --server " +
			base + " --token " + token + " --version " + v
	}
	channel := version.Get().Channel
	branch := "test"
	if channel == "production" {
		branch = "main"
	}
	return "curl -fsSL https://raw.githubusercontent.com/" + distInstallerRepo + "/" + branch + "/installer/install-distribution.sh | sudo bash -s -- --server " +
		base + " --token " + token + " --channel " + channel
}

func (s *Server) newDistToken() (string, []byte, time.Time) {
	tok := "fwd_" + newToken()
	return tok, hashDistToken(tok), s.now().Add(distTokenTTL)
}

func (s *Server) handleCreateDistribution(w http.ResponseWriter, r *http.Request) {
	var req distReq
	if !decode(w, r, &req) || !s.validDistReq(w, r, &req, 0) {
		return
	}
	u, _ := currentUser(r)
	tok, hash, exp := s.newDistToken()
	d, err := s.st.CreateDistributionServer(r.Context(), store.DistributionServer{Name: req.Name, URL: req.URL, Networks: req.Networks,
		GroupIDs: req.GroupIDs, BandwidthKbps: req.BandwidthKbps, CreatedBy: u.Username}, hash, exp)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "distribution.create", d.Name, true, map[string]any{"networks": d.Networks, "groups": d.GroupIDs, "url": d.URL})
	writeJSON(w, http.StatusCreated, distTokenView{Server: s.distViewOf(d), Token: tok, Expires: exp, Command: s.distInstallCommand(r, tok)})
}

func (s *Server) distByPath(w http.ResponseWriter, r *http.Request) (store.DistributionServer, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.DistributionServer{}, false
	}
	d, err := s.st.DistributionServerByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return d, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return d, false
	}
	return d, true
}

func (s *Server) handleUpdateDistribution(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distByPath(w, r)
	if !ok {
		return
	}
	var req distReq
	if !decode(w, r, &req) || !s.validDistReq(w, r, &req, d.ID) {
		return
	}
	if req.URL == "" && d.Enrolled {
		apiError(w, http.StatusBadRequest, "invalid_url")
		return
	}
	d.Name, d.URL, d.Networks, d.GroupIDs, d.BandwidthKbps = req.Name, req.URL, req.Networks, req.GroupIDs, req.BandwidthKbps
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if err := s.st.UpdateDistributionServer(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apiError(w, http.StatusConflict, "name_taken")
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "distribution.update", d.Name, true, map[string]any{"networks": d.Networks, "groups": d.GroupIDs, "url": d.URL,
		"enabled": d.Enabled, "bandwidthKbps": d.BandwidthKbps})
	s.assignDistribution(r.Context())
	d, _ = s.st.DistributionServerByID(r.Context(), d.ID)
	writeJSON(w, http.StatusOK, s.distViewOf(d))
}

// handleDistributionToken makes a new install token, for a first install
// that was not finished in time or to reinstall the machine.
func (s *Server) handleDistributionToken(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distByPath(w, r)
	if !ok {
		return
	}
	tok, hash, exp := s.newDistToken()
	if err := s.st.NewDistributionToken(r.Context(), d.ID, hash, exp); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "distribution.token", d.Name, true, nil)
	d, _ = s.st.DistributionServerByID(r.Context(), d.ID)
	writeJSON(w, http.StatusOK, distTokenView{Server: s.distViewOf(d), Token: tok, Expires: exp, Command: s.distInstallCommand(r, tok)})
}

func (s *Server) handleDeleteDistribution(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distByPath(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteDistributionServer(r.Context(), d.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "distribution.delete", d.Name, true, map[string]any{"url": d.URL})
	s.assignDistribution(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// handleDistributionSoftwareUpdate asks a distribution server to install
// the management server's own version on its next report.
func (s *Server) handleDistributionSoftwareUpdate(w http.ResponseWriter, r *http.Request) {
	d, ok := s.distByPath(w, r)
	if !ok {
		return
	}
	target := version.Version
	if !releaseVersion.MatchString(target) {
		apiError(w, http.StatusConflict, "version_not_offered")
		return
	}
	v := s.distViewOf(d)
	if v.State != "online" {
		apiError(w, http.StatusConflict, "distribution_offline")
		return
	}
	if v.Status == nil || !v.Status.Updatable {
		apiError(w, http.StatusConflict, "distribution_not_updatable")
		return
	}
	if err := s.st.RequestDistributionUpdate(r.Context(), d.ID, target); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "distribution.software_update", d.Name, true, map[string]any{"version": target})
	writeJSON(w, http.StatusAccepted, map[string]string{"version": target})
}
