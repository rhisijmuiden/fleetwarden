package catalog

// Matching installed programs to catalog packages, for the outdated
// software report. Like winget itself, a program from Apps & features is
// found by its normalised display name (and publisher, to tell packages
// with the same name apart).

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode"
)

// Match is the catalog package of an installed program.
type Match struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Latest  string `json:"latest"`
	ARPMax  string `json:"-"` // newest version as Apps & features shows it, when it differs
	matched bool
}

// Outdated reports whether an installed version is older than the newest
// version of the package.
func (m Match) Outdated(installed string) bool {
	if !m.matched || installed == "" || m.Latest == "" {
		return false
	}
	want := m.Latest
	if m.ARPMax != "" {
		want = m.ARPMax
	}
	return CompareVersions(installed, want) < 0
}

var (
	archX64RE  = regexp.MustCompile(`(?i)\b(x64|x86[_-]64|amd64|win64|64[ -]?bits?)\b`)
	archX86RE  = regexp.MustCompile(`(?i)\b(x86|win32|i[36]86|32[ -]?bits?)\b`)
	archArmRE  = regexp.MustCompile(`(?i)\b(arm64|aarch64)\b`)
	versionRE  = regexp.MustCompile(`(?i)\bv?\d+(?:[.,]\d+)+[a-z0-9.+-]*`)
	parenRE    = regexp.MustCompile(`[(\[]([^)\]]*)[)\]]`)
	localeRE   = regexp.MustCompile(`(?i)^[a-z]{2,3}(?:-[a-z]{2,4})?$`)
	legalSufRE = regexp.MustCompile(`(?i)\b(incorporated|inc|corporation|corp|company|co|llc|ltd|limited|gmbh|b\.?v|n\.?v|s\.?a|ag|ab|oy|srl|s\.?r\.?l|pty|plc|kg)\b\.?`)
)

// NormalizeName turns a display name into the form of the catalog's
// norm_names2 table: lower case letters and digits only, without versions,
// languages and architecture, and the architecture apart ("X64", "X86",
// "Arm64" or "").
func NormalizeName(name string) (string, string) {
	s := name
	arch := ""
	switch {
	case archArmRE.MatchString(s):
		arch = "Arm64"
	case archX64RE.MatchString(s):
		arch = "X64"
	case archX86RE.MatchString(s):
		arch = "X86"
	}
	s = archArmRE.ReplaceAllString(s, " ")
	s = archX64RE.ReplaceAllString(s, " ")
	s = archX86RE.ReplaceAllString(s, " ")
	s = versionRE.ReplaceAllString(s, " ")
	// Brackets that only hold a language or nothing else go away.
	s = parenRE.ReplaceAllStringFunc(s, func(g string) string {
		inner := strings.Fields(strings.Trim(g, "()[]"))
		var keep []string
		for _, w := range inner {
			if !localeRE.MatchString(w) {
				keep = append(keep, w)
			}
		}
		return " " + strings.Join(keep, " ") + " "
	})
	return lettersAndDigits(s), arch
}

// NormalizePublisher drops legal suffixes such as Inc. and GmbH.
func NormalizePublisher(p string) string {
	return lettersAndDigits(legalSufRE.ReplaceAllString(p, " "))
}

func lettersAndDigits(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type matchCandidate struct {
	Match
	publishers []string
}

// MatchInstalled finds the package of an installed program. It answers
// false when the catalog does not know the program or cannot tell which
// of several packages it is. Results are cached until the index changes.
func (w *Winget) MatchInstalled(ctx context.Context, name, publisher, version string) (Match, bool) {
	norm, arch := NormalizeName(name)
	if norm == "" {
		return Match{}, false
	}
	key := norm + "\x00" + arch + "\x00" + NormalizePublisher(publisher)
	w.mu.Lock()
	db := w.db
	cands, cached := w.matches[key]
	w.mu.Unlock()
	if db == nil {
		return Match{}, false
	}
	if !cached {
		var err error
		cands, err = lookupCandidates(ctx, db, norm, arch, NormalizePublisher(publisher))
		if err != nil {
			return Match{}, false
		}
		w.mu.Lock()
		if w.db == db {
			if w.matches == nil {
				w.matches = map[string][]matchCandidate{}
			}
			w.matches[key] = cands
		}
		w.mu.Unlock()
	}
	m, ok := pickCandidate(cands, version)
	return m, ok
}

func lookupCandidates(ctx context.Context, db *sql.DB, norm, arch, pub string) ([]matchCandidate, error) {
	names := []string{norm}
	if arch != "" {
		names = []string{norm + "(" + arch + ")", norm}
	}
	var out []matchCandidate
	for _, n := range names {
		rows, err := db.QueryContext(ctx, `SELECT p.id, p.name, p.latest_version, COALESCE(p.arp_max_version, ''),
			COALESCE((SELECT group_concat(np.norm_publisher, char(9)) FROM norm_publishers2 np WHERE np.package = p.rowid), '')
			FROM norm_names2 nn JOIN packages p ON p.rowid = nn.package WHERE nn.norm_name = ?1 ORDER BY length(p.id), p.id`, n)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c matchCandidate
			var pubs string
			if err := rows.Scan(&c.ID, &c.Name, &c.Latest, &c.ARPMax, &pubs); err != nil {
				rows.Close()
				return nil, err
			}
			c.matched = true
			if pubs != "" {
				c.publishers = strings.Split(pubs, "\t")
			}
			out = append(out, c)
		}
		rows.Close()
		if len(out) > 0 {
			break
		}
	}
	if len(out) > 1 && pub != "" {
		var same []matchCandidate
		for _, c := range out {
			for _, p := range c.publishers {
				if p != "" && (strings.HasPrefix(pub, p) || strings.HasPrefix(p, pub)) {
					same = append(same, c)
					break
				}
			}
		}
		if len(same) > 0 {
			out = same
		}
	}
	return out, nil
}

// pickCandidate chooses between packages with the same name: language
// editions with the same newest version are one; otherwise the package
// whose version line (3.12 for Python 3.12.4) fits the installed version.
func pickCandidate(cands []matchCandidate, version string) (Match, bool) {
	if len(cands) == 0 {
		return Match{}, false
	}
	allSame := true
	for _, c := range cands[1:] {
		if CompareVersions(c.Latest, cands[0].Latest) != 0 || CompareVersions(c.ARPMax, cands[0].ARPMax) != 0 {
			allSame = false
			break
		}
	}
	if allSame {
		return cands[0].Match, true
	}
	best, bestLen, tie := -1, 0, false
	for i, c := range cands {
		ref := c.Latest
		if c.ARPMax != "" {
			ref = c.ARPMax
		}
		n := commonParts(ref, version)
		switch {
		case n > bestLen:
			best, bestLen, tie = i, n, false
		case n == bestLen && best >= 0:
			tie = true
		}
	}
	// One shared part (the major version) is not enough to pick a line.
	if best < 0 || tie || bestLen < 2 {
		return Match{}, false
	}
	return cands[best].Match, true
}

func commonParts(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	n := 0
	for n < len(pa) && n < len(pb) && strings.TrimLeft(pa[n], "0") == strings.TrimLeft(pb[n], "0") {
		n++
	}
	return n
}
