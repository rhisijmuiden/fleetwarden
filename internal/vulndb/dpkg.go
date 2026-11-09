package vulndb

import "strings"

// CompareDebian compares two Debian package versions ([epoch:]upstream[-revision])
// like dpkg --compare-versions.
func CompareDebian(a, b string) int {
	ea, ua, ra := splitDebian(a)
	eb, ub, rb := splitDebian(b)
	if c := compareNum(ea, eb); c != 0 {
		return c
	}
	if c := compareDebPart(ua, ub); c != 0 {
		return c
	}
	return compareDebPart(ra, rb)
}

func splitDebian(v string) (string, string, string) {
	v = strings.TrimSpace(v)
	epoch := "0"
	if i := strings.IndexByte(v, ':'); i >= 0 {
		epoch, v = v[:i], v[i+1:]
	}
	rev := ""
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		v, rev = v[:i], v[i+1:]
	}
	return epoch, v, rev
}

func compareNum(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// order is dpkg's character order: ~ before everything, letters before
// other characters.
func order(c byte) int {
	switch {
	case c == '~':
		return -1
	case isDigit(c):
		return 0
	case c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z':
		return int(c)
	case c == 0:
		return 0
	}
	return int(c) + 256
}

func compareDebPart(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// Non-digit prefix.
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			var ca, cb byte
			if i < len(a) && !isDigit(a[i]) {
				ca = a[i]
			}
			if j < len(b) && !isDigit(b[j]) {
				cb = b[j]
			}
			if oa, ob := order(ca), order(cb); oa != ob {
				if oa < ob {
					return -1
				}
				return 1
			}
			if ca != 0 {
				i++
			}
			if cb != 0 {
				j++
			}
			if ca == 0 && cb == 0 {
				break
			}
		}
		si := i
		for i < len(a) && isDigit(a[i]) {
			i++
		}
		sj := j
		for j < len(b) && isDigit(b[j]) {
			j++
		}
		na, nb := a[si:i], b[sj:j]
		if na == "" {
			na = "0"
		}
		if nb == "" {
			nb = "0"
		}
		if c := compareNum(na, nb); c != 0 {
			return c
		}
	}
	return 0
}

// SourceVersion is the source version of a binary package version: a
// binary-only rebuild ("+b1") has the version of its source.
func SourceVersion(v string) string {
	if i := strings.LastIndex(v, "+b"); i > 0 {
		rest := v[i+2:]
		ok := rest != ""
		for k := 0; k < len(rest); k++ {
			ok = ok && isDigit(rest[k])
		}
		if ok {
			return v[:i]
		}
	}
	return v
}
