package vulndb

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MSRCDoc is a monthly release of Microsoft's security update guide.
type MSRCDoc struct {
	ID      string // 2026-Nov
	Title   string
	Updated time.Time
}

var msrcIDRE = regexp.MustCompile(`^(\d{4})-(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)$`)

// MSRCMonth is the first day of a release's month, for sorting.
func MSRCMonth(id string) (time.Time, bool) {
	m := msrcIDRE.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-Jan", m[1]+"-"+m[2])
	return t, err == nil
}

// ParseMSRCUpdates reads the list of releases (cvrf/v3.0/updates) and keeps
// the monthly security updates.
func ParseMSRCUpdates(r io.Reader) ([]MSRCDoc, error) {
	var doc struct {
		Value []struct {
			ID                 string `json:"ID"`
			DocumentTitle      string `json:"DocumentTitle"`
			CurrentReleaseDate string `json:"CurrentReleaseDate"`
		} `json:"value"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("msrc updates: %w", err)
	}
	var out []MSRCDoc
	for _, v := range doc.Value {
		if _, ok := MSRCMonth(v.ID); !ok || !strings.Contains(v.DocumentTitle, "Security Updates") {
			continue
		}
		d := MSRCDoc{ID: v.ID, Title: v.DocumentTitle}
		if t := parseTime(v.CurrentReleaseDate); t != nil {
			d.Updated = *t
		}
		out = append(out, d)
	}
	return out, nil
}

// MSRCVuln is a CVE of a monthly release.
type MSRCVuln struct {
	CVE       string
	Title     string
	Score     float64
	Vector    string
	Severity  string // critical, high, medium, low from Microsoft's rating
	Exploited bool
	Fixes     []MSRCFix
}

// MSRCFix is a security update for a product. Product is set for Windows
// releases only (they are matched by build); other products are matched by
// the update a device is missing (KB).
type MSRCFix struct {
	Product    string
	KB         string
	FixedBuild string
	Supersedes string
}

type cvrfVuln struct {
	CVE   string `json:"CVE"`
	Title struct {
		Value string `json:"Value"`
	} `json:"Title"`
	Threats []struct {
		Type        int `json:"Type"`
		Description struct {
			Value string `json:"Value"`
		} `json:"Description"`
	} `json:"Threats"`
	CVSSScoreSets []struct {
		BaseScore float64 `json:"BaseScore"`
		Vector    string  `json:"Vector"`
	} `json:"CVSSScoreSets"`
	Remediations []struct {
		Type        int `json:"Type"`
		Description struct {
			Value string `json:"Value"`
		} `json:"Description"`
		ProductID    []string `json:"ProductID"`
		FixedBuild   string   `json:"FixedBuild"`
		Supercedence string   `json:"Supercedence"`
	} `json:"Remediations"`
}

var kbRE = regexp.MustCompile(`^(?:KB)?(\d{6,8})$`)

func cleanKB(v string) string {
	m := kbRE.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return ""
	}
	return m[1]
}

// msrcRating maps Microsoft's severity rating.
func msrcRating(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical":
		return SeverityCritical
	case "important":
		return SeverityHigh
	case "moderate":
		return SeverityMedium
	case "low":
		return SeverityLow
	}
	return ""
}

// ParseCVRF reads a monthly release (cvrf/v3.0/cvrf/<id>, JSON) and calls
// fn for each CVE that has a security update.
func ParseCVRF(r io.Reader, fn func(MSRCVuln) error) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	products := map[string]string{}
	var pending []cvrfVuln // vulnerabilities listed before the product tree
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "ProductTree":
			var pt struct {
				FullProductName []struct {
					ProductID string `json:"ProductID"`
					Value     string `json:"Value"`
				} `json:"FullProductName"`
			}
			if err := dec.Decode(&pt); err != nil {
				return fmt.Errorf("cvrf product tree: %w", err)
			}
			for _, p := range pt.FullProductName {
				products[p.ProductID] = p.Value
			}
			for _, v := range pending {
				if err := emitCVRF(v, products, fn); err != nil {
					return err
				}
			}
			pending = nil
		case "Vulnerability":
			if err := expectDelim(dec, '['); err != nil {
				return err
			}
			for dec.More() {
				var v cvrfVuln
				if err := dec.Decode(&v); err != nil {
					return fmt.Errorf("cvrf vulnerability: %w", err)
				}
				if len(products) == 0 {
					pending = append(pending, v)
					continue
				}
				if err := emitCVRF(v, products, fn); err != nil {
					return err
				}
			}
			if err := expectDelim(dec, ']'); err != nil {
				return err
			}
		default:
			if err := skipValue(dec); err != nil {
				return err
			}
		}
	}
	return nil
}

func emitCVRF(v cvrfVuln, products map[string]string, fn func(MSRCVuln) error) error {
	if !ValidCVE(v.CVE) {
		return nil
	}
	out := MSRCVuln{CVE: v.CVE, Title: Clip(v.Title.Value, 300)}
	for _, t := range v.Threats {
		switch t.Type {
		case 3:
			if s := msrcRating(t.Description.Value); SeverityRank(s) > SeverityRank(out.Severity) {
				out.Severity = s
			}
		case 1:
			if strings.Contains(t.Description.Value, "Exploited:Yes") {
				out.Exploited = true
			}
		}
	}
	for _, c := range v.CVSSScoreSets {
		if c.BaseScore > out.Score {
			out.Score, out.Vector = c.BaseScore, Clip(c.Vector, 200)
		}
	}
	seen := map[MSRCFix]bool{}
	for _, rm := range v.Remediations {
		if rm.Type != 2 { // vendor fix
			continue
		}
		kb := cleanKB(rm.Description.Value)
		if kb == "" {
			continue
		}
		sup := cleanKB(rm.Supercedence)
		build := ""
		if _, _, ok := ParseFixedBuild(rm.FixedBuild); ok {
			build = strings.TrimSpace(rm.FixedBuild)
		}
		generic := false
		for _, id := range rm.ProductID {
			name := products[id]
			if IsWindowsProduct(name) {
				f := MSRCFix{Product: name, KB: kb, FixedBuild: build, Supersedes: sup}
				if !seen[f] {
					seen[f] = true
					out.Fixes = append(out.Fixes, f)
				}
			} else {
				generic = true
			}
		}
		if generic {
			f := MSRCFix{KB: kb, Supersedes: sup}
			if !seen[f] {
				seen[f] = true
				out.Fixes = append(out.Fixes, f)
			}
		}
	}
	if len(out.Fixes) == 0 {
		return nil
	}
	return fn(out)
}

// ParseFixedBuild reads "10.0.26100.9711" as build 26100, revision 9711.
func ParseFixedBuild(v string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 4 || (parts[0] != "10" && parts[0] != "6") {
		return 0, 0, false
	}
	b, err1 := strconv.Atoi(parts[2])
	u, err2 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return b, u, true
}

// ParseOSBuild reads a device's build ("26100.2314", also "10.0.26100.2314").
func ParseOSBuild(v string) (int, int, bool) {
	v = strings.TrimSpace(v)
	if strings.Count(v, ".") == 3 {
		return ParseFixedBuild(v)
	}
	b, u, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, false
	}
	bn, err1 := strconv.Atoi(b)
	un, err2 := strconv.Atoi(u)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return bn, un, true
}

var windowsProductRE = regexp.MustCompile(`^Windows (?:1[01] Version \w+ for (?:x64-based|ARM64-based|32-bit) Systems|Server (?:2008 R2|2012 R2|2012|2016|2019|2022|2025)(?: \(Server Core installation\))?|Server, version \w+(?: \(Server Core installation\))?)$`)

// IsWindowsProduct reports whether an MSRC product is a Windows release.
func IsWindowsProduct(name string) bool { return windowsProductRE.MatchString(name) }

var serverYearRE = regexp.MustCompile(`Windows Server (2008 R2|2012 R2|2012|2016|2019|2022|2025)`)
var winVersionRE = regexp.MustCompile(`^(\d{2}H\d|\d{4})$`)

// WindowsProduct is the MSRC product name of a Windows device from its OS
// name ("Windows 11 Pro", "Windows Server 2022 Standard"), version ("24H2")
// and architecture (amd64, arm64, 386). It answers "" when unknown.
func WindowsProduct(osName, osVersion, arch string) string {
	if m := serverYearRE.FindStringSubmatch(osName); m != nil {
		return "Windows Server " + m[1]
	}
	family := ""
	switch {
	case strings.Contains(osName, "Windows 11"):
		family = "Windows 11"
	case strings.Contains(osName, "Windows 10"):
		family = "Windows 10"
	default:
		return ""
	}
	ver := strings.ToUpper(strings.TrimSpace(osVersion))
	if !winVersionRE.MatchString(ver) {
		return ""
	}
	sys := "x64-based Systems"
	switch strings.ToLower(arch) {
	case "arm64", "aarch64":
		sys = "ARM64-based Systems"
	case "386", "x86", "i386":
		sys = "32-bit Systems"
	}
	return family + " Version " + ver + " for " + sys
}
