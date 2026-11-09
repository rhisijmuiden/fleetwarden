package agent

// fwupd (LVFS) firmware updates on Linux. The agent asks fwupd which
// firmware updates the devices can get (fwupdmgr get-updates --json), but
// installs only cabinet files from FleetWarden's repository
// (fwupdmgr install FILE DEVICE-ID). fwupd itself only fetches its metadata.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type fwupdDevices struct {
	Devices []fwupdDevice `json:"Devices"`
}

type fwupdDevice struct {
	Name     string         `json:"Name"`
	DeviceID string         `json:"DeviceId"`
	GUIDs    []string       `json:"Guid"`
	Plugin   string         `json:"Plugin"`
	Vendor   string         `json:"Vendor"`
	Version  string         `json:"Version"`
	Flags    []string       `json:"Flags"`
	Releases []fwupdRelease `json:"Releases"`
}

type fwupdRelease struct {
	AppstreamID string   `json:"AppstreamId"`
	Name        string   `json:"Name"`
	Summary     string   `json:"Summary"`
	Description string   `json:"Description"`
	Version     string   `json:"Version"`
	Filename    string   `json:"Filename"`
	Checksums   []string `json:"Checksum"`
	Size        int64    `json:"Size"`
	Created     int64    `json:"Created"`
	Vendor      string   `json:"Vendor"`
	URI         string   `json:"Uri"`
	Locations   []string `json:"Locations"`
	Flags       []string `json:"Flags"`
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// plainText turns fwupd's release notes (simple HTML) into text.
func plainText(v string) string {
	v = strings.NewReplacer("</p>", "\n", "<li>", "\n- ", "<br>", "\n", "<br/>", "\n").Replace(v)
	v = html.UnescapeString(tagRE.ReplaceAllString(v, ""))
	lines := strings.Split(v, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// parseFwupdUpdates turns `fwupdmgr get-updates --json` into driver
// updates: per device the newest release.
func parseFwupdUpdates(raw []byte) ([]agentproto.DriverUpdate, error) {
	var doc fwupdDevices
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := []agentproto.DriverUpdate{}
	for _, d := range doc.Devices {
		if len(d.Releases) == 0 || d.DeviceID == "" {
			continue
		}
		rel := d.Releases[0] // fwupd lists the newest release first
		link := rel.URI
		if len(rel.Locations) > 0 {
			link = rel.Locations[0]
		}
		if link == "" {
			continue
		}
		f := agentproto.DriverFile{URL: link, Size: rel.Size}
		for _, c := range rel.Checksums {
			c = strings.ToLower(strings.TrimSpace(c))
			switch len(c) {
			case 64:
				f.SHA256 = c
			case 40:
				f.SHA1 = c
			}
		}
		f.Name = path.Base(rel.Filename)
		if f.Name == "" || f.Name == "." || f.Name == "/" {
			if u, err := url.Parse(link); err == nil {
				f.Name = path.Base(u.Path)
			}
		}
		id := f.SHA256
		if id == "" {
			id = f.SHA1
		}
		if id == "" {
			id = rel.AppstreamID + "-" + rel.Version
		}
		title := d.Name
		if rel.Name != "" && !strings.EqualFold(rel.Name, d.Name) {
			title = rel.Name
		}
		u := agentproto.DriverUpdate{
			Source: agentproto.DriverSourceFwupd, SourceID: id, Kind: agentproto.DriverKindFirmware,
			Title: strings.TrimSpace(title + " " + rel.Version), Vendor: firstNonEmpty(rel.Vendor, d.Vendor), Class: d.Plugin,
			Version: rel.Version, SizeBytes: rel.Size, Device: d.Name, DeviceID: d.DeviceID, CurrentVersion: d.Version,
			Description:    firstNonEmpty(plainText(rel.Description), rel.Summary),
			RebootRequired: d.Plugin == "uefi_capsule" || hasFlag(rel.Flags, "needs-reboot") || hasFlag(d.Flags, "needs-reboot"),
			Files:          []agentproto.DriverFile{f},
		}
		if len(d.GUIDs) > 0 {
			u.HardwareID = d.GUIDs[0]
		}
		if rel.Created > 0 {
			u.ReleasedAt = time.Unix(rel.Created, 0).UTC()
		}
		out = append(out, u)
	}
	return out, nil
}

func hasFlag(flags []string, f string) bool {
	for _, v := range flags {
		if v == f {
			return true
		}
	}
	return false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// fwupdBackend runs fwupdmgr. Run is replaced by tests.
type fwupdBackend struct {
	Run func(ctx context.Context, args ...string) ([]byte, int, error)
}

func runFwupdmgr(ctx context.Context, args ...string) ([]byte, int, error) {
	exe, err := exec.LookPath("fwupdmgr")
	if err != nil {
		return nil, -1, errFwupdMissing
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(cmd.Environ(), "LANG=C.UTF-8", "LC_ALL=C.UTF-8")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

var errFwupdMissing = errors.New("fwupd is not installed")

func (b fwupdBackend) run(ctx context.Context, args ...string) ([]byte, int, error) {
	if b.Run != nil {
		return b.Run(ctx, args...)
	}
	return runFwupdmgr(ctx, args...)
}

// Scan refreshes fwupd's metadata and lists the firmware updates. Without
// fwupd there is nothing to report.
func (b fwupdBackend) Scan(ctx context.Context) ([]agentproto.DriverUpdate, error) {
	if _, _, err := b.run(ctx, "refresh"); errors.Is(err, errFwupdMissing) {
		return []agentproto.DriverUpdate{}, nil
	}
	out, code, err := b.run(ctx, "get-updates", "--json")
	if err != nil {
		return nil, err
	}
	i := strings.IndexByte(string(out), '{')
	if i < 0 {
		if code == 2 || code == 0 { // 2: nothing to do
			return []agentproto.DriverUpdate{}, nil
		}
		return nil, errors.New("fwupd: " + firstLine(string(out)))
	}
	return parseFwupdUpdates(out[i:])
}

func firstLine(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = v[:i]
	}
	if len(v) > 300 {
		v = v[:300]
	}
	return v
}

// Install flashes the cabinet file on the device it was offered for.
func (b fwupdBackend) Install(ctx context.Context, job agentproto.DriverInstall, files []string) (bool, string, error) {
	if job.Installer != "" && job.Installer != agentproto.InstallerFwupd {
		return false, "", fmt.Errorf("installer %q is not supported on Linux", job.Installer)
	}
	if len(files) != 1 {
		return false, "", errors.New("a firmware update needs exactly one cabinet file")
	}
	args := []string{"install", "--assume-yes", "--no-reboot-check", files[0]}
	if job.DeviceID != "" {
		args = append(args, job.DeviceID)
	}
	out, code, err := b.run(ctx, args...)
	if err != nil {
		return false, "", err
	}
	if code != 0 {
		return false, "", errors.New("fwupd: " + lastLine(string(out)))
	}
	reboot := false
	if raw, _, err := b.run(ctx, "get-devices", "--json"); err == nil {
		if i := strings.IndexByte(string(raw), '{'); i >= 0 {
			var doc fwupdDevices
			if json.Unmarshal(raw[i:], &doc) == nil {
				for _, d := range doc.Devices {
					if hasFlag(d.Flags, "needs-reboot") {
						reboot = true
					}
				}
			}
		}
	}
	return reboot, lastLine(string(out)), nil
}

func lastLine(v string) string {
	lines := strings.Split(strings.TrimSpace(v), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return firstLine(l)
		}
	}
	return ""
}

// Configure has nothing to do: fwupd never installs by itself.
func (fwupdBackend) Configure(bool) error { return nil }
