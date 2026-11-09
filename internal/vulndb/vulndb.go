// Package vulndb reads the public vulnerability sources FleetWarden uses
// (phase 13) and matches what is installed on a device against them:
//
//   - NVD (nist.gov) data feeds: every CVE with its CVSS score, and the
//     affected version ranges of the Windows programs FleetWarden knows
//     (see Products);
//   - CISA's Known Exploited Vulnerabilities catalog;
//   - the Debian security tracker: per source package and release the
//     version that fixes a CVE (Debian and Proxmox VE);
//   - Microsoft's security update guide (MSRC CVRF): per Windows release
//     the build and the update (KB) that fix a CVE.
//
// The parsers stream: the sources are hundreds of megabytes uncompressed.
package vulndb

import (
	"strings"
	"time"
)

// Severity levels, from a CVSS base score.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityNone     = "none" // no score known
)

// SeverityOf rates a CVSS base score with the CVSS v3 bands.
func SeverityOf(score float64) string {
	switch {
	case score >= 9:
		return SeverityCritical
	case score >= 7:
		return SeverityHigh
	case score >= 4:
		return SeverityMedium
	case score > 0:
		return SeverityLow
	}
	return SeverityNone
}

// SeverityRank orders severities, critical highest.
func SeverityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	}
	return 0
}

// ValidCVE reports whether v looks like a CVE identifier.
func ValidCVE(v string) bool {
	if !strings.HasPrefix(v, "CVE-") || len(v) < 13 || len(v) > 24 {
		return false
	}
	parts := strings.Split(v[4:], "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) < 4 {
		return false
	}
	for _, p := range parts {
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

// Clip shortens a text to at most n bytes on a rune boundary.
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func parseTime(v string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			if t.Year() < 1990 {
				return nil
			}
			return &t
		}
	}
	return nil
}
