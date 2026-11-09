package vulndb

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// KEV is an entry of CISA's Known Exploited Vulnerabilities catalog.
type KEV struct {
	CVE        string
	Name       string
	Added      *time.Time
	Due        *time.Time
	Ransomware bool
}

// ParseKEV reads the KEV catalog (known_exploited_vulnerabilities.json).
func ParseKEV(r io.Reader) ([]KEV, error) {
	var doc struct {
		Vulnerabilities []struct {
			CVE               string `json:"cveID"`
			VulnerabilityName string `json:"vulnerabilityName"`
			DateAdded         string `json:"dateAdded"`
			DueDate           string `json:"dueDate"`
			Ransomware        string `json:"knownRansomwareCampaignUse"`
		} `json:"vulnerabilities"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("kev catalog: %w", err)
	}
	if len(doc.Vulnerabilities) == 0 {
		return nil, fmt.Errorf("kev catalog: no vulnerabilities")
	}
	out := make([]KEV, 0, len(doc.Vulnerabilities))
	for _, v := range doc.Vulnerabilities {
		if !ValidCVE(v.CVE) {
			continue
		}
		out = append(out, KEV{CVE: v.CVE, Name: Clip(v.VulnerabilityName, 300), Added: parseTime(v.DateAdded),
			Due: parseTime(v.DueDate), Ransomware: v.Ransomware == "Known"})
	}
	return out, nil
}
