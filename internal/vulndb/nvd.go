package vulndb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// CVE is one entry of the NVD feeds.
type CVE struct {
	ID        string
	Summary   string
	Published *time.Time
	Modified  *time.Time
	Score     float64 // CVSS base score, 0 when NVD has none
	Severity  string
	Vector    string
	Rejected  bool
	// Ranges are the affected versions of the products asked for.
	Ranges []Range
}

// Range is an affected version range of a product ("vendor:product" as in
// the CPE). Version is an exact version; otherwise the bounds apply, empty
// ones are open.
type Range struct {
	Product   string
	Version   string
	StartIncl string
	StartExcl string
	EndIncl   string
	EndExcl   string
}

// Affects reports whether version v lies in the range; cmp compares versions.
func (r Range) Affects(v string, cmp func(a, b string) int) bool {
	if v == "" {
		return false
	}
	if r.Version != "" {
		return cmp(v, r.Version) == 0
	}
	if r.StartIncl == "" && r.StartExcl == "" && r.EndIncl == "" && r.EndExcl == "" {
		return false // "every version": too vague to report
	}
	if r.StartIncl != "" && cmp(v, r.StartIncl) < 0 {
		return false
	}
	if r.StartExcl != "" && cmp(v, r.StartExcl) <= 0 {
		return false
	}
	if r.EndIncl != "" && cmp(v, r.EndIncl) > 0 {
		return false
	}
	if r.EndExcl != "" && cmp(v, r.EndExcl) >= 0 {
		return false
	}
	return true
}

// Fixed is the first version without the vulnerability, when the range says.
func (r Range) Fixed() string {
	if r.EndExcl != "" {
		return r.EndExcl
	}
	return ""
}

type nvdItem struct {
	CVE struct {
		ID           string `json:"id"`
		Published    string `json:"published"`
		LastModified string `json:"lastModified"`
		VulnStatus   string `json:"vulnStatus"`
		Descriptions []struct {
			Lang  string `json:"lang"`
			Value string `json:"value"`
		} `json:"descriptions"`
		Metrics        map[string][]nvdMetric `json:"metrics"`
		Configurations []struct {
			Nodes []struct {
				CPEMatch []struct {
					Vulnerable            bool   `json:"vulnerable"`
					Criteria              string `json:"criteria"`
					VersionStartIncluding string `json:"versionStartIncluding"`
					VersionStartExcluding string `json:"versionStartExcluding"`
					VersionEndIncluding   string `json:"versionEndIncluding"`
					VersionEndExcluding   string `json:"versionEndExcluding"`
				} `json:"cpeMatch"`
			} `json:"nodes"`
		} `json:"configurations"`
	} `json:"cve"`
}

type nvdMetric struct {
	Type     string `json:"type"` // Primary, Secondary
	CVSSData struct {
		BaseScore    float64 `json:"baseScore"`
		VectorString string  `json:"vectorString"`
	} `json:"cvssData"`
}

// metricOrder is the preference between CVSS versions.
var metricOrder = []string{"cvssMetricV31", "cvssMetricV40", "cvssMetricV30", "cvssMetricV2"}

// ParseNVD reads an NVD CVE data feed (JSON 2.0, uncompressed) and calls fn
// for each CVE. want selects the products ("vendor:product") whose version
// ranges are kept; nil keeps none.
func ParseNVD(r io.Reader, want func(product string) bool, fn func(CVE) error) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	found := false
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		if key != "vulnerabilities" {
			if err := skipValue(dec); err != nil {
				return err
			}
			continue
		}
		found = true
		if err := expectDelim(dec, '['); err != nil {
			return err
		}
		for dec.More() {
			var it nvdItem
			if err := dec.Decode(&it); err != nil {
				return fmt.Errorf("nvd feed: %w", err)
			}
			c, ok := it.toCVE(want)
			if !ok {
				continue
			}
			if err := fn(c); err != nil {
				return err
			}
		}
		if err := expectDelim(dec, ']'); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("nvd feed: no vulnerabilities")
	}
	return nil
}

func (it nvdItem) toCVE(want func(string) bool) (CVE, bool) {
	c := CVE{ID: it.CVE.ID, Published: parseTime(it.CVE.Published), Modified: parseTime(it.CVE.LastModified),
		Rejected: it.CVE.VulnStatus == "Rejected"}
	if !ValidCVE(c.ID) {
		return c, false
	}
	for _, d := range it.CVE.Descriptions {
		if d.Lang == "en" {
			c.Summary = Clip(d.Value, 600)
			break
		}
	}
	for _, key := range metricOrder {
		ms := it.CVE.Metrics[key]
		if len(ms) == 0 {
			continue
		}
		best := ms[0]
		for _, m := range ms {
			if m.Type == "Primary" {
				best = m
				break
			}
		}
		if best.CVSSData.BaseScore > 0 {
			c.Score, c.Vector = best.CVSSData.BaseScore, Clip(best.CVSSData.VectorString, 200)
			break
		}
	}
	c.Severity = SeverityOf(c.Score)
	if want == nil {
		return c, true
	}
	seen := map[Range]bool{}
	for _, cfg := range it.CVE.Configurations {
		for _, n := range cfg.Nodes {
			for _, m := range n.CPEMatch {
				if !m.Vulnerable {
					continue
				}
				product, ver, ok := cpeProduct(m.Criteria)
				if !ok || !want(product) {
					continue
				}
				rg := Range{Product: product, StartIncl: m.VersionStartIncluding, StartExcl: m.VersionStartExcluding,
					EndIncl: m.VersionEndIncluding, EndExcl: m.VersionEndExcluding}
				if ver != "*" && ver != "-" && ver != "" {
					rg = Range{Product: product, Version: ver}
				}
				if !seen[rg] {
					seen[rg] = true
					c.Ranges = append(c.Ranges, rg)
				}
			}
		}
	}
	return c, true
}

// cpeProduct reads "vendor:product" and the version from a CPE 2.3 name of
// an application ("cpe:2.3:a:google:chrome:*:*:...").
func cpeProduct(cpe string) (string, string, bool) {
	parts := splitCPE(cpe)
	if len(parts) < 6 || parts[0] != "cpe" || parts[1] != "2.3" || parts[2] != "a" {
		return "", "", false
	}
	return unescapeCPE(parts[3]) + ":" + unescapeCPE(parts[4]), unescapeCPE(parts[5]), true
}

func splitCPE(v string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '\\' && i+1 < len(v):
			b.WriteByte(v[i])
			b.WriteByte(v[i+1])
			i++
		case v[i] == ':':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(v[i])
		}
	}
	return append(out, b.String())
}

func unescapeCPE(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// ---- JSON streaming helpers ----

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("unexpected %v, want %v", tok, want)
	}
	return nil
}

func readKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	k, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("unexpected %v, want a key", tok)
	}
	return k, nil
}

func skipValue(dec *json.Decoder) error {
	var raw json.RawMessage
	return dec.Decode(&raw)
}
