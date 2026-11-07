package agent

// Windows installer and uninstaller command lines. Pure functions, so the
// rules are tested on every platform; software_windows.go runs them.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// arpEntry is one program under the Uninstall registry keys ("Apps & features").
type arpEntry struct {
	Key                  string // sub key name: the product code for MSI packages
	DisplayName          string
	DisplayVersion       string
	Publisher            string
	UninstallString      string
	QuietUninstallString string
	WindowsInstaller     bool
	PerUser              bool
}

var guidRE = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

// msiexecGUIDRE finds "MsiExec.exe /I{GUID}" or "/X {GUID}" in an uninstall string.
var msiexecGUIDRE = regexp.MustCompile(`(?i)msiexec(?:\.exe)?"?\s+/[IX]\s*(\{[0-9A-F-]{36}\})`)

func quoteArg(s string) string { return `"` + strings.ReplaceAll(s, `"`, ``) + `"` }

// installCommand returns the program and the full command line for an
// installer. logFile receives the installer's log where the kind has one.
func installCommand(in agentproto.Installer, file, logFile, msiexec string) (string, string, error) {
	args := strings.TrimSpace(in.Args)
	switch in.Kind {
	case agentproto.KindMSI:
		// For MSI packages Args are extra msiexec options and properties; the
		// package always runs without UI and without restarting.
		cl := quoteArg(msiexec) + " /i " + quoteArg(file) + " " + agentproto.DefaultSilentArgs[agentproto.KindMSI]
		if args != "" {
			cl += " " + args
		}
		return msiexec, cl + " /l*v " + quoteArg(logFile), nil
	case agentproto.KindInno, agentproto.KindNullsoft, agentproto.KindBurn, agentproto.KindEXE:
		if args == "" {
			args = agentproto.DefaultSilentArgs[in.Kind]
		}
		if args == "" {
			return "", "", fmt.Errorf("this installer needs its unattended arguments (for example /S or /quiet)")
		}
		lower := strings.ToLower(args)
		switch {
		case in.Kind == agentproto.KindInno && !strings.Contains(lower, "/log"):
			args += " /LOG=" + quoteArg(logFile)
		case in.Kind == agentproto.KindBurn && !strings.Contains(lower, "/log"):
			args += " /log " + quoteArg(logFile)
		}
		return file, quoteArg(file) + " " + args, nil
	}
	return "", "", fmt.Errorf("installer kind %q is not supported on Windows", in.Kind)
}

// Installer families recognised in an uninstaller program.
const (
	familyUnknown  = ""
	familyInno     = "inno"
	familyNullsoft = "nullsoft"
)

var innoUninsRE = regexp.MustCompile(`(?i)^unins\d{3}\.exe$`)

// uninstallCommand picks how to remove a program unattended: msiexec /x for
// Windows Installer packages, the program's own quiet uninstall string, or
// its uninstaller with the silent switch of its installer family. args, when
// set, are added to the uninstaller's own command line (or to msiexec /x).
func uninstallCommand(e arpEntry, args, msiexec string, exists func(string) bool, family func(string) string) (string, string, error) {
	args = strings.TrimSpace(args)
	withArgs := func(cl string) string {
		if args != "" {
			return cl + " " + args
		}
		return cl
	}
	code := ""
	if e.WindowsInstaller && guidRE.MatchString(e.Key) {
		code = e.Key
	} else if m := msiexecGUIDRE.FindStringSubmatch(e.UninstallString); m != nil {
		code = m[1]
	}
	if code != "" {
		return msiexec, withArgs(quoteArg(msiexec) + " /x " + code + " /qn /norestart"), nil
	}
	if args == "" && e.QuietUninstallString != "" {
		exe, rest := splitCommand(e.QuietUninstallString, exists)
		if exe == "" {
			return "", "", fmt.Errorf("cannot read the quiet uninstall command %q", e.QuietUninstallString)
		}
		return exe, joinCommand(exe, rest), nil
	}
	exe, rest := splitCommand(e.UninstallString, exists)
	if exe == "" {
		return "", "", fmt.Errorf("%s has no uninstall command", e.DisplayName)
	}
	if args != "" {
		return exe, joinCommand(exe, rest+" "+args), nil
	}
	fam := family(exe)
	if fam == familyUnknown && innoUninsRE.MatchString(baseName(exe)) {
		fam = familyInno
	}
	switch fam {
	case familyInno:
		return exe, joinCommand(exe, rest+" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"), nil
	case familyNullsoft:
		return exe, joinCommand(exe, rest+" /S"), nil
	}
	return "", "", fmt.Errorf("no unattended uninstall is known for %s; give the uninstall arguments (its uninstaller is %s)", e.DisplayName, exe)
}

func joinCommand(exe, rest string) string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return quoteArg(exe)
	}
	return quoteArg(exe) + " " + rest
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// splitCommand splits a command line from the registry into the program
// and its arguments. Paths with spaces are often not quoted, so the longest
// prefix that exists wins.
func splitCommand(s string, exists func(string) bool) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if s[0] == '"' {
		if end := strings.IndexByte(s[1:], '"'); end >= 0 {
			return s[1 : end+1], strings.TrimSpace(s[end+2:])
		}
		return strings.Trim(s, `"`), ""
	}
	best := -1
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if exists(s[:i]) {
				best = i
			}
		}
	}
	if best < 0 {
		if i := strings.IndexByte(s, ' '); i >= 0 {
			return s[:i], strings.TrimSpace(s[i+1:])
		}
		return s, ""
	}
	return s[:best], strings.TrimSpace(s[best:])
}

// Windows Installer exit codes with a meaning worth reporting.
var msiExitText = map[int]string{
	1602: "the installation was cancelled",
	1603: "fatal error during installation",
	1605: "the product is not installed",
	1618: "another installation is in progress",
	1619: "the installation package could not be opened",
	1620: "the installation package could not be opened",
	1625: "the installation is forbidden by system policy",
	1633: "this installation package is not supported on this processor type",
	1638: "another version of this product is already installed",
	1639: "invalid command line argument",
}

// ExitInstallBusy means Windows Installer is busy with another installation.
const ExitInstallBusy = 1618

// classifyExit decides whether an exit code means success and whether a
// restart is needed.
func classifyExit(code int, in agentproto.Installer) (ok, reboot bool, msg string) {
	switch code {
	case 0:
		return true, false, ""
	case 3010, 1641:
		return true, true, ""
	}
	for _, c := range in.RebootCodes {
		if c == code {
			return true, true, ""
		}
	}
	for _, c := range in.SuccessCodes {
		if c == code {
			return true, false, ""
		}
	}
	if t, ok := msiExitText[code]; ok {
		return false, false, fmt.Sprintf("exit code %d: %s", code, t)
	}
	return false, false, fmt.Sprintf("exit code %d", code)
}

// matchEntries returns the programs that match d, the best match first:
// the product code, then the exact name, then names that start with it.
func matchEntries(entries []arpEntry, d agentproto.Detect) []arpEntry {
	var exact, prefix []arpEntry
	name := strings.ToLower(strings.TrimSpace(d.Name))
	for _, e := range entries {
		if d.ProductCode != "" && strings.EqualFold(e.Key, d.ProductCode) {
			exact = append([]arpEntry{e}, exact...)
			continue
		}
		if name == "" {
			continue
		}
		n := strings.ToLower(e.DisplayName)
		switch {
		case n == name:
			exact = append(exact, e)
		case strings.HasPrefix(n, name):
			prefix = append(prefix, e)
		}
	}
	return append(exact, prefix...)
}

// findInstalled reports the highest installed version of the matches.
func findInstalled(entries []arpEntry, d agentproto.Detect) (bool, string) {
	m := matchEntries(entries, d)
	if len(m) == 0 {
		return false, ""
	}
	best := m[0].DisplayVersion
	for _, e := range m[1:] {
		if compareVersions(e.DisplayVersion, best) > 0 {
			best = e.DisplayVersion
		}
	}
	return true, best
}

// uninstallTargets returns the programs with exactly this name (and version).
func uninstallTargets(entries []arpEntry, u agentproto.Uninstall) []arpEntry {
	var out []arpEntry
	for _, e := range entries {
		if strings.EqualFold(strings.TrimSpace(e.DisplayName), strings.TrimSpace(u.Name)) && (u.Version == "" || e.DisplayVersion == u.Version) {
			out = append(out, e)
		}
	}
	return out
}

// decodeLog turns an installer log (UTF-16 with a byte order mark, or
// ANSI/UTF-8) into text.
func decodeLog(b []byte) string {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	return string(b)
}
