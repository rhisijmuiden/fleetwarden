// Package updates checks GitHub releases for new server versions and hands
// update requests to the privileged updater (fleetwarden-update.service).
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Channels.
const (
	ChannelProduction = "production"
	ChannelTest       = "test"
)

// ValidChannel reports whether c is a known channel.
func ValidChannel(c string) bool { return c == ChannelProduction || c == ChannelTest }

// versionRE matches release versions the updater accepts.
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-test\.[0-9]+)?$`)

// ValidVersion reports whether v is a production or test release version.
func ValidVersion(v string) bool { return versionRE.MatchString(v) }

// Compare compares two versions like semver (-1, 0, 1). Invalid versions sort lowest.
func Compare(a, b string) int { return semver.Compare("v"+a, "v"+b) }

// Release is a published server version.
type Release struct {
	Version     string    `json:"version"`
	Channel     string    `json:"channel"`
	PublishedAt time.Time `json:"publishedAt"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Checker looks up releases on GitHub and caches the answer.
type Checker struct {
	Repo    string // owner/name
	APIBase string // https://api.github.com
	HTTP    *http.Client
	TTL     time.Duration

	mu      sync.Mutex
	fetched time.Time
	cache   []ghRelease
}

// NewChecker returns a checker for the FleetWarden repository.
func NewChecker(repo string) *Checker {
	return &Checker{Repo: repo, APIBase: "https://api.github.com", HTTP: &http.Client{Timeout: 20 * time.Second}, TTL: 10 * time.Minute}
}

func (c *Checker) releases(ctx context.Context, force bool) ([]ghRelease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.cache != nil && time.Since(c.fetched) < c.TTL {
		return c.cache, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/repos/%s/releases?per_page=50", c.APIBase, c.Repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fleetwarden-server")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var out []ghRelease
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	c.cache, c.fetched = out, time.Now()
	return out, nil
}

// Latest returns the newest release of a channel that ships a server package.
// The test channel follows pre-releases (vX.Y.Z-test.N), production follows releases.
func (c *Checker) Latest(ctx context.Context, channel string, force bool) (*Release, error) {
	all, err := c.releases(ctx, force)
	if err != nil {
		return nil, err
	}
	var best *Release
	for _, r := range all {
		v := strings.TrimPrefix(r.TagName, "v")
		if r.Draft || !ValidVersion(v) {
			continue
		}
		isTest := strings.Contains(v, "-test.")
		if (channel == ChannelTest) != (r.Prerelease && isTest) {
			continue
		}
		if !hasServerPackage(r) {
			continue
		}
		if best == nil || Compare(v, best.Version) > 0 {
			notes := r.Body
			if len(notes) > 4000 {
				notes = notes[:4000]
			}
			best = &Release{Version: v, Channel: channel, PublishedAt: r.PublishedAt, URL: r.HTMLURL, Notes: notes}
		}
	}
	return best, nil
}

func hasServerPackage(r ghRelease) bool {
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, "fleetwarden-server_") && strings.HasSuffix(a.Name, "_amd64.deb") {
			return true
		}
	}
	return false
}

// Asset is a file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// AgentRelease is a published release with agent packages.
type AgentRelease struct {
	Version     string    `json:"version"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"publishedAt"`
	Assets      []Asset   `json:"assets"` // agent packages and SHA256SUMS
}

// AgentReleases lists releases that ship agent packages, newest first.
func (c *Checker) AgentReleases(ctx context.Context, force bool) ([]AgentRelease, error) {
	all, err := c.releases(ctx, force)
	if err != nil {
		return nil, err
	}
	var out []AgentRelease
	for _, r := range all {
		v := strings.TrimPrefix(r.TagName, "v")
		if r.Draft || !ValidVersion(v) {
			continue
		}
		ar := AgentRelease{Version: v, Prerelease: r.Prerelease || strings.Contains(v, "-"), PublishedAt: r.PublishedAt}
		pkgs := 0
		for _, a := range r.Assets {
			if a.Name == "SHA256SUMS" || (strings.HasPrefix(a.Name, "fleetwarden-agent_"+v+"_") &&
				(strings.HasSuffix(a.Name, ".deb") || strings.HasSuffix(a.Name, ".msi"))) {
				ar.Assets = append(ar.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
				if a.Name != "SHA256SUMS" {
					pkgs++
				}
			}
		}
		if pkgs > 0 {
			out = append(out, ar)
		}
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// Request is the file the server writes for the updater.
type Request struct {
	Version     string    `json:"version"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
}

// ErrBusy is returned when an update is already pending or running.
var ErrBusy = errors.New("an update is already in progress")

// RequestUpdate writes the update request for the privileged updater, which a
// systemd path unit starts when the file appears.
func RequestUpdate(dir string, r Request, status Status) error {
	if !ValidVersion(r.Version) {
		return fmt.Errorf("invalid version %q", r.Version)
	}
	if status.Running() {
		return ErrBusy
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(dir, "request.json")
	if _, err := os.Stat(path); err == nil {
		return ErrBusy
	}
	raw, _ := json.Marshal(r)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Pending reports whether a request is waiting for the updater.
func Pending(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "request.json"))
	return err == nil
}

// Status is written by the updater while it works.
type Status struct {
	ID          string    `json:"id"`
	State       string    `json:"state"` // downloading, backup, installing, verifying, rolling-back, completed, rolled-back, failed
	FromVersion string    `json:"fromVersion"`
	ToVersion   string    `json:"toVersion"`
	Message     string    `json:"message"`
	StartedAt   time.Time `json:"startedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Running reports whether the updater is still busy. A status that has not
// changed for 30 minutes is treated as stale (the updater died).
func (s Status) Running() bool {
	switch s.State {
	case "downloading", "backup", "installing", "verifying", "rolling-back":
		return time.Since(s.UpdatedAt) < 30*time.Minute
	}
	return false
}

// ReadStatus reads the updater status; a missing file gives an empty status.
func ReadStatus(dir string) (Status, error) {
	var s Status
	raw, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(raw, &s)
}
