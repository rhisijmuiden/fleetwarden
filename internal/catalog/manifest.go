package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Manifest is the part of a merged winget manifest the server uses. Fields
// at the top level are defaults for every installer.
type Manifest struct {
	PackageIdentifier string `yaml:"PackageIdentifier" json:"id"`
	PackageVersion    string `yaml:"PackageVersion" json:"version"`
	PackageName       string `yaml:"PackageName" json:"name"`
	Publisher         string `yaml:"Publisher" json:"publisher"`
	ShortDescription  string `yaml:"ShortDescription" json:"description"`
	License           string `yaml:"License" json:"license"`
	PackageURL        string `yaml:"PackageUrl" json:"homepage"`
	ReleaseDate       string `yaml:"ReleaseDate" json:"releaseDate"`

	installerFields `yaml:",inline"`
	Installers      []Installer `yaml:"Installers" json:"installers"`
}

type installerFields struct {
	InstallerType          string       `yaml:"InstallerType" json:"type,omitempty"`
	NestedInstallerType    string       `yaml:"NestedInstallerType" json:"-"`
	Scope                  string       `yaml:"Scope" json:"scope,omitempty"`
	InstallerLocale        string       `yaml:"InstallerLocale" json:"locale,omitempty"`
	InstallerSwitches      *Switches    `yaml:"InstallerSwitches" json:"-"`
	InstallerSuccessCodes  []int        `yaml:"InstallerSuccessCodes" json:"-"`
	ExpectedReturnCodes    []ReturnCode `yaml:"ExpectedReturnCodes" json:"-"`
	ProductCode            string       `yaml:"ProductCode" json:"productCode,omitempty"`
	AppsAndFeaturesEntries []ARPEntry   `yaml:"AppsAndFeaturesEntries" json:"-"`
}

// Installer is one installer of a manifest.
type Installer struct {
	Architecture    string `yaml:"Architecture" json:"architecture"`
	InstallerURL    string `yaml:"InstallerUrl" json:"url"`
	InstallerSha256 string `yaml:"InstallerSha256" json:"sha256"`
	installerFields `yaml:",inline"`
}

// Switches are the installer's command line switches.
type Switches struct {
	Silent             string `yaml:"Silent"`
	SilentWithProgress string `yaml:"SilentWithProgress"`
	Custom             string `yaml:"Custom"`
}

// ReturnCode is an exit code with a known meaning.
type ReturnCode struct {
	Code     int    `yaml:"InstallerReturnCode"`
	Response string `yaml:"ReturnResponse"`
}

// ARPEntry is how the program shows up in "Apps & features".
type ARPEntry struct {
	DisplayName    string `yaml:"DisplayName"`
	Publisher      string `yaml:"Publisher"`
	DisplayVersion string `yaml:"DisplayVersion"`
	ProductCode    string `yaml:"ProductCode"`
	UpgradeCode    string `yaml:"UpgradeCode"`
	InstallerType  string `yaml:"InstallerType"`
}

// ParseManifest reads a merged manifest.
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("winget manifest: %w", err)
	}
	if m.PackageIdentifier == "" || len(m.Installers) == 0 {
		return nil, errors.New("winget manifest: no installers")
	}
	return &m, nil
}

// effective fills the installer's fields from the manifest defaults.
func (m *Manifest) effective(in Installer) Installer {
	f := &in.installerFields
	d := m.installerFields
	if f.InstallerType == "" {
		f.InstallerType = d.InstallerType
	}
	if f.NestedInstallerType == "" {
		f.NestedInstallerType = d.NestedInstallerType
	}
	if f.Scope == "" {
		f.Scope = d.Scope
	}
	if f.InstallerLocale == "" {
		f.InstallerLocale = d.InstallerLocale
	}
	if f.InstallerSwitches == nil {
		f.InstallerSwitches = d.InstallerSwitches
	} else if d.InstallerSwitches != nil {
		s := *f.InstallerSwitches
		if s.Silent == "" {
			s.Silent = d.InstallerSwitches.Silent
		}
		if s.SilentWithProgress == "" {
			s.SilentWithProgress = d.InstallerSwitches.SilentWithProgress
		}
		if s.Custom == "" {
			s.Custom = d.InstallerSwitches.Custom
		}
		f.InstallerSwitches = &s
	}
	if f.InstallerSuccessCodes == nil {
		f.InstallerSuccessCodes = d.InstallerSuccessCodes
	}
	if f.ExpectedReturnCodes == nil {
		f.ExpectedReturnCodes = d.ExpectedReturnCodes
	}
	if f.ProductCode == "" {
		f.ProductCode = d.ProductCode
	}
	if f.AppsAndFeaturesEntries == nil {
		f.AppsAndFeaturesEntries = d.AppsAndFeaturesEntries
	}
	return in
}

// kindOf maps a winget installer type to an agent installer kind ("" when
// the agent cannot run it unattended as a machine-wide install).
func kindOf(in Installer) string {
	switch strings.ToLower(in.InstallerType) {
	case "msi", "wix":
		return agentproto.KindMSI
	case "inno":
		return agentproto.KindInno
	case "nullsoft":
		return agentproto.KindNullsoft
	case "burn":
		return agentproto.KindBurn
	case "exe":
		if in.InstallerSwitches != nil && strings.TrimSpace(in.InstallerSwitches.Silent) != "" {
			return agentproto.KindEXE
		}
	}
	return ""
}

// archPreference lists the installer architectures a device can run, best first.
func archPreference(goarch string) []string {
	switch goarch {
	case "arm64":
		return []string{"arm64", "x64", "x86", "neutral"}
	case "386":
		return []string{"x86", "neutral"}
	}
	return []string{"x64", "x86", "neutral"}
}

// Choice is the installer picked for a device.
type Choice struct {
	Installer agentproto.Installer `json:"installer"`
	Detect    agentproto.Detect    `json:"detect"`
	// Type and Architecture as named in the catalog.
	Type         string `json:"type"`
	Architecture string `json:"architecture"`
	Scope        string `json:"scope"`
}

// ErrNoInstaller means the package has no installer the agent can run.
var ErrNoInstaller = errors.New("this package has no installer that can be installed unattended for all users on this kind of device")

// Resolve picks the installer for a device architecture (Go's GOARCH:
// amd64, arm64, 386) and builds the agent's installer and detection rule.
func (m *Manifest) Resolve(goarch string) (Choice, error) {
	var best *Installer
	bestScore := -1
	prefs := archPreference(goarch)
	for _, raw := range m.Installers {
		in := m.effective(raw)
		if kindOf(in) == "" || strings.EqualFold(in.Scope, "user") || !strings.HasPrefix(strings.ToLower(in.InstallerURL), "https://") {
			continue
		}
		rank := -1
		for i, a := range prefs {
			if strings.EqualFold(in.Architecture, a) {
				rank = i
			}
		}
		if rank < 0 {
			continue
		}
		score := (len(prefs)-rank)*100 + 10
		if strings.EqualFold(in.Scope, "machine") {
			score += 5
		}
		switch strings.ToLower(in.InstallerLocale) {
		case "", "en-us":
			score += 2
		case "en-gb", "en":
			score++
		}
		if score > bestScore {
			bestScore = score
			c := in
			best = &c
		}
	}
	if best == nil {
		return Choice{}, ErrNoInstaller
	}
	in := *best
	kind := kindOf(in)
	sw := Switches{}
	if in.InstallerSwitches != nil {
		sw = *in.InstallerSwitches
	}
	var args []string
	switch kind {
	case agentproto.KindMSI:
		// msiexec runs it with /qn /norestart; the manifest's own MSI UI
		// switches do not apply.
		if !strings.EqualFold(in.Scope, "user") {
			args = append(args, "ALLUSERS=1")
		}
	default:
		silent := strings.TrimSpace(sw.Silent)
		if silent == "" {
			silent = agentproto.DefaultSilentArgs[kind]
		}
		args = append(args, silent)
		if kind == agentproto.KindInno && !strings.Contains(strings.ToLower(silent), "/allusers") {
			args = append(args, "/ALLUSERS")
		}
	}
	if c := strings.TrimSpace(sw.Custom); c != "" {
		args = append(args, c)
	}
	out := agentproto.Installer{Kind: kind, URL: in.InstallerURL, SHA256: strings.ToLower(in.InstallerSha256),
		FileName: fileName(in.InstallerURL, kind), Args: strings.Join(args, " "), SuccessCodes: in.InstallerSuccessCodes}
	for _, rc := range in.ExpectedReturnCodes {
		switch rc.Response {
		case "rebootRequiredToFinish", "rebootRequiredForInstall", "rebootInitiated":
			out.RebootCodes = append(out.RebootCodes, rc.Code)
		case "alreadyInstalled", "noUpgrade":
			out.SuccessCodes = append(out.SuccessCodes, rc.Code)
		}
	}
	det := agentproto.Detect{Name: m.PackageName, ProductCode: in.ProductCode, Version: m.PackageVersion}
	if len(in.AppsAndFeaturesEntries) > 0 {
		e := in.AppsAndFeaturesEntries[0]
		// The display name often carries the version or language ("Mozilla
		// Firefox (x64 en-US)"); the package name is the stable start of it.
		if e.DisplayName != "" && !strings.HasPrefix(strings.ToLower(e.DisplayName), strings.ToLower(m.PackageName)) {
			det.Name = e.DisplayName
		}
		if e.ProductCode != "" && det.ProductCode == "" {
			det.ProductCode = e.ProductCode
		}
		if e.DisplayVersion != "" {
			det.Version = e.DisplayVersion
		}
	}
	if !guidRE.MatchString(det.ProductCode) {
		det.ProductCode = ""
	}
	return Choice{Installer: out, Detect: det, Type: in.InstallerType, Architecture: in.Architecture, Scope: in.Scope}, nil
}

var (
	guidRE     = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)
	safeNameRE = regexp.MustCompile(`[^A-Za-z0-9 ._()+-]`)
)

// fileName derives the local file name from the URL, keeping the extension
// the installer needs.
func fileName(raw, kind string) string {
	ext := ".exe"
	if kind == agentproto.KindMSI {
		ext = ".msi"
	}
	name := ""
	if u, err := url.Parse(raw); err == nil {
		name = safeNameRE.ReplaceAllString(path.Base(u.Path), "")
	}
	name = strings.TrimLeft(name, ".-_ ")
	if len(name) > 100 {
		name = name[len(name)-100:]
	}
	if name == "" || name == "/" {
		name = "setup"
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	name = strings.TrimLeft(name, ".-_ ")
	if c := name[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
		name = "setup-" + name
	}
	return name
}
