package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/debpatch"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
)

// agentPackage is an agent installer the server can hand out: shipped with
// the server package (bundled) or downloaded/uploaded into the releases folder.
type agentPackage struct {
	Version  string    `json:"version"`
	Platform string    `json:"platform"` // windows, linux
	Arch     string    `json:"arch"`     // amd64, arm64
	Kind     string    `json:"kind"`     // msi, deb
	Name     string    `json:"name"`
	SHA256   string    `json:"sha256"`
	Size     int64     `json:"size"`
	Source   string    `json:"source"` // bundled, github, upload
	Added    time.Time `json:"added"`
	path     string
}

var agentFileRE = regexp.MustCompile(`^fleetwarden-agent_([0-9]+\.[0-9]+\.[0-9]+(?:-[a-z]+\.[0-9]+)?)_(amd64|arm64|x64)\.(deb|msi)$`)

// parseAgentFile recognises an agent installer by its file name.
func parseAgentFile(name string) (agentPackage, bool) {
	m := agentFileRE.FindStringSubmatch(name)
	if m == nil {
		return agentPackage{}, false
	}
	p := agentPackage{Version: m[1], Arch: m[2], Kind: m[3], Name: name}
	switch {
	case p.Kind == "msi" && p.Arch == "x64":
		p.Platform, p.Arch = "windows", "amd64"
	case p.Kind == "deb" && p.Arch != "x64":
		p.Platform = "linux"
	default:
		return agentPackage{}, false
	}
	return p, true
}

// target is the download name the install scripts ask for.
func (p agentPackage) target() string {
	if p.Kind == "msi" {
		return "windows-x64.msi"
	}
	return "linux-" + p.Arch + ".deb"
}

// downloadName is the file name a browser saves.
func (p agentPackage) downloadName() string { return p.Name }

func (p agentPackage) prerelease() bool { return strings.Contains(p.Version, "-") }

type pkgCatalog struct {
	mu    sync.Mutex
	cache map[string]hashEntry // path -> hash for unchanged files
}

type hashEntry struct {
	size  int64
	mtime time.Time
	sum   string
}

func (c *pkgCatalog) hash(path string, fi os.FileInfo, sums map[string]string) string {
	if v, ok := sums[filepath.Base(path)]; ok {
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.cache[path]; ok && e.size == fi.Size() && e.mtime.Equal(fi.ModTime()) {
		return e.sum
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if c.cache == nil {
		c.cache = map[string]hashEntry{}
	}
	c.cache[path] = hashEntry{fi.Size(), fi.ModTime(), sum}
	return sum
}

func readSums(dir string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && len(fields[0]) == 64 {
			out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return out
}

// releaseMeta is stored next to downloaded or uploaded agent packages.
type releaseMeta struct {
	Source string    `json:"source"`
	Added  time.Time `json:"added"`
}

func (c *pkgCatalog) scanDir(dir, source string) []agentPackage {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	added := time.Time{}
	var meta releaseMeta
	if raw, err := os.ReadFile(filepath.Join(dir, "release.json")); err == nil && json.Unmarshal(raw, &meta) == nil {
		source, added = meta.Source, meta.Added
	}
	sums := readSums(dir)
	var out []agentPackage
	for _, e := range entries {
		p, ok := parseAgentFile(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		p.path = filepath.Join(dir, e.Name())
		p.Size, p.Source, p.Added = fi.Size(), source, added
		if p.Added.IsZero() {
			p.Added = fi.ModTime()
		}
		p.SHA256 = c.hash(p.path, fi, sums)
		out = append(out, p)
	}
	return out
}

type agentPackages []agentPackage

// agentPackages lists every agent installer available on this server.
func (s *Server) agentPackages() agentPackages {
	var out agentPackages
	if s.cfg.AgentDir != "" {
		out = append(out, s.pkgs.scanDir(s.cfg.AgentDir, "bundled")...)
	}
	if dirs, err := os.ReadDir(s.cfg.AgentReleasesDir()); err == nil {
		for _, d := range dirs {
			if d.IsDir() && updates.ValidVersion(d.Name()) {
				out = append(out, s.pkgs.scanDir(filepath.Join(s.cfg.AgentReleasesDir(), d.Name()), "release")...)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := updates.Compare(out[i].Version, out[j].Version); c != 0 {
			return c > 0
		}
		return out[i].target() < out[j].target()
	})
	return out
}

func (ps agentPackages) all() agentPackages {
	if ps == nil {
		return agentPackages{}
	}
	return ps
}

// allowed reports whether a version may go to agents on a channel: the
// production channel only takes releases, the test channel takes both.
func channelAllows(channel, v string) bool {
	return channel == updates.ChannelTest || !strings.Contains(v, "-")
}

// latest returns the newest package for a target on a channel. A production
// server without any release package falls back to the newest package.
func (ps agentPackages) latest(target, channel string) (agentPackage, bool) {
	var fallback *agentPackage
	for i, p := range ps { // sorted newest first
		if p.target() != target {
			continue
		}
		if channelAllows(channel, p.Version) {
			return p, true
		}
		if fallback == nil {
			fallback = &ps[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return agentPackage{}, false
}

// version returns the package of an exact version for a target.
func (ps agentPackages) version(target, v string) (agentPackage, bool) {
	for _, p := range ps {
		if p.target() == target && p.Version == v {
			return p, true
		}
	}
	return agentPackage{}, false
}

var installTargets = []string{"windows-x64.msi", "linux-amd64.deb", "linux-arm64.deb"}

func (ps agentPackages) latestPerTarget(channel string) []agentPackage {
	var out []agentPackage
	for _, t := range installTargets {
		if p, ok := ps.latest(t, channel); ok {
			out = append(out, p)
		}
	}
	return out
}

// groupChannel is the agent release channel of a group: its own setting or
// the server's channel.
func (s *Server) groupChannel(r *http.Request, groupID int64) string {
	if g, err := s.st.GroupByID(r.Context(), groupID); err == nil && g.AgentChannel != nil {
		return *g.AgentChannel
	}
	return s.loadUpdateSettings(r.Context()).Channel
}

// ---- downloads for the Add client wizard and install scripts ----

// enrollFile is written into a downloaded .deb; the package's postinst
// enrolls with it.
const enrollFile = "/usr/share/fleetwarden-agent/enroll.json"

type enrollConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// tokenFromQuery validates the ?t= enrollment token of a public download.
func (s *Server) tokenFromQuery(w http.ResponseWriter, r *http.Request) (store.EnrollmentToken, string, bool) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return store.EnrollmentToken{}, "", false
	}
	plain := strings.TrimSpace(r.URL.Query().Get("t"))
	t, err := s.st.TokenByValue(r.Context(), plain)
	if err != nil || !t.Usable(s.now()) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return t, "", false
		}
		s.agentFails.Fail(ip)
		http.Error(w, "invalid, expired or revoked enrollment token", http.StatusUnauthorized)
		return t, "", false
	}
	return t, plain, true
}

func (s *Server) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	t, plain, ok := s.tokenFromQuery(w, r)
	if !ok {
		return
	}
	target := r.PathValue("file")
	pkg, ok := s.agentPackages().latest(target, s.groupChannel(r, t.GroupID))
	if !ok {
		http.Error(w, "no agent package for "+target+" is available on this server", http.StatusNotFound)
		return
	}
	raw, err := os.ReadFile(pkg.path)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	cfg := enrollConfig{Server: s.baseURL(r), Token: plain}
	switch pkg.Kind {
	case "deb":
		js, _ := json.Marshal(cfg)
		if out, err := debpatch.AddFiles(raw, []debpatch.File{{Path: enrollFile, Mode: 0o600, Data: js}}); err == nil {
			raw = out
		} else {
			s.log.Warn("bake token into agent package", "file", pkg.Name, "err", err)
		}
	case "msi":
		if out, err := s.bakeMSI(r.Context(), raw, cfg); err == nil {
			raw = out
		} else {
			s.log.Warn("bake token into agent installer", "file", pkg.Name, "err", err)
		}
	}
	s.auditAs(r, nil, "agent", "agent.download", pkg.Name, true, map[string]any{"token": t.Prefix, "group": t.GroupName})
	ctype := "application/vnd.debian.binary-package"
	if pkg.Kind == "msi" {
		ctype = "application/x-msi"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", pkg.downloadName()))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}
