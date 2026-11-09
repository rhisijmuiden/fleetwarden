package vulndb

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// DebianEntry is the state of a CVE for a source package in one Debian
// release, from the security tracker.
type DebianEntry struct {
	Release string // codename: bookworm, trixie
	Package string // source package
	CVE     string
	// Status is open (no fix yet) or resolved (FixedVersion fixes it).
	Status       string
	FixedVersion string
	Urgency      string // low, medium, high, "not yet assigned"
	// NoDSA marks a minor issue Debian fixes in a point release, if at all.
	NoDSA   bool
	Summary string
}

type debRelease struct {
	Status       string `json:"status"`
	FixedVersion string `json:"fixed_version"`
	Urgency      string `json:"urgency"`
	NoDSA        string `json:"nodsa"`
}

type debCVE struct {
	Description string                `json:"description"`
	Releases    map[string]debRelease `json:"releases"`
}

// ParseDebian reads the security tracker's JSON (tracker/data/json) and
// calls fn for each CVE of the asked releases that can affect a system:
// entries Debian rates unimportant, never affected (fixed in version 0) or
// not yet triaged are left out.
func ParseDebian(r io.Reader, releases map[string]bool, fn func(DebianEntry) error) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		pkg, err := readKey(dec)
		if err != nil {
			return err
		}
		var cves map[string]debCVE
		if err := dec.Decode(&cves); err != nil {
			return fmt.Errorf("debian tracker, package %s: %w", pkg, err)
		}
		for id, c := range cves {
			if !ValidCVE(id) {
				continue
			}
			for rel, st := range c.Releases {
				if !releases[rel] {
					continue
				}
				e := DebianEntry{Release: rel, Package: pkg, CVE: id, Status: st.Status, FixedVersion: st.FixedVersion,
					Urgency: st.Urgency, NoDSA: st.NoDSA != ""}
				switch {
				case st.Urgency == "unimportant" || strings.HasSuffix(st.Urgency, "*"):
					continue
				case st.Status == "resolved" && (st.FixedVersion == "0" || st.FixedVersion == ""):
					continue
				case st.Status == "open":
					e.FixedVersion = ""
				case st.Status != "resolved":
					continue // undetermined
				}
				e.Summary = Clip(c.Description, 600)
				if err := fn(e); err != nil {
					return err
				}
			}
		}
	}
	return expectDelim(dec, '}')
}

// Debian releases by major version; Proxmox VE follows Debian.
var debianCodenames = map[string]string{"10": "buster", "11": "bullseye", "12": "bookworm", "13": "trixie", "14": "forky"}

var codenameRE = regexp.MustCompile(`\((buster|bullseye|bookworm|trixie|forky)\)`)
var debMajorRE = regexp.MustCompile(`Debian GNU/Linux (\d+)`)

// DebianRelease finds the release codename of a Debian or Proxmox VE
// device from its OS name ("Debian GNU/Linux 13 (trixie)",
// "Proxmox VE 9.0 (Debian GNU/Linux 13 (trixie))") and version. It answers
// "" for other systems.
func DebianRelease(osName, osVersion string) string {
	if !strings.Contains(osName, "Debian") {
		return ""
	}
	if m := codenameRE.FindStringSubmatch(osName); m != nil {
		return m[1]
	}
	if m := debMajorRE.FindStringSubmatch(osName); m != nil {
		return debianCodenames[m[1]]
	}
	major, _, _ := strings.Cut(osVersion, ".")
	return debianCodenames[major]
}

// SupportedDebianReleases are the releases whose tracker data is kept.
var SupportedDebianReleases = []string{"bullseye", "bookworm", "trixie", "forky"}
