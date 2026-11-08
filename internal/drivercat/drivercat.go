// Package drivercat reads the BIOS catalogs that Dell, HP and Lenovo publish
// for their business PCs and decides which BIOS update a machine can take.
//
// The server fetches the catalogs (metadata only) for the models it has
// among its devices. A BIOS update found this way is offered like any other
// repository update: nothing is downloaded until an administrator adds it to
// the repository.
//
//   - Dell: CatalogIndexPC.cab lists one catalog per system ID (the SKU in
//     Win32_ComputerSystem.SystemSKUNumber). The BIOS is a Dell Update
//     Package, matched on the BIOS version.
//   - HP: the HP Image Assistant reference file per platform (the baseboard
//     product) lists SoftPaqs; the BIOS is matched on the ROM family ("S70")
//     and version.
//   - Lenovo: a catalog per machine type (the first four characters of the
//     model) points at package descriptors; the BIOS package says which BIOS
//     levels it updates and which levels mean it is already installed.
package drivercat

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Vendors.
const (
	Dell   = "dell"
	HP     = "hp"
	Lenovo = "lenovo"
)

// Catalog addresses.
const (
	DellIndexURL = "https://downloads.dell.com/catalog/CatalogIndexPC.cab"
	DellBase     = "https://downloads.dell.com/"
	HPRefURL     = "https://hpia.hpcloud.hp.com/ref/{p}/{p}_64_{os}.cab"
	LenovoURL    = "https://download.lenovo.com/catalog/{mt}_{os}.xml"
)

// Hosts are the hosts catalogs and files are fetched from, per vendor.
var Hosts = map[string][]string{
	Dell:   {"downloads.dell.com", "dl.dell.com"},
	HP:     {"hpia.hpcloud.hp.com", "ftp.hp.com", "ftp.ext.hp.com"},
	Lenovo: {"download.lenovo.com"},
}

// Platform is what a device reports about its hardware.
type Platform struct {
	Manufacturer string
	Model        string
	SKU          string // Win32_ComputerSystem.SystemSKUNumber
	Baseboard    string // Win32_BaseBoard.Product
	BIOSVersion  string // Win32_BIOS.SMBIOSBIOSVersion
	OSName       string // "Microsoft Windows 11 Pro"
	OSVersion    string // "24H2"
}

// VendorOf returns the catalog vendor for a manufacturer, or "".
func VendorOf(manufacturer string) string {
	m := strings.ToLower(manufacturer)
	switch {
	case strings.HasPrefix(m, "dell"):
		return Dell
	case strings.HasPrefix(m, "hp") || strings.HasPrefix(m, "hewlett"):
		return HP
	case strings.HasPrefix(m, "lenovo"):
		return Lenovo
	}
	return ""
}

var (
	hexKeyRE = regexp.MustCompile(`^[0-9A-F]{4}$`)
	lenovoRE = regexp.MustCompile(`^[0-9A-Z]{4}`)
)

// Key returns the vendor and the catalog key of a platform (Dell system ID,
// HP platform ID, Lenovo machine type), or "" when it has none.
func Key(p Platform) (vendor, key string) {
	vendor = VendorOf(p.Manufacturer)
	switch vendor {
	case Dell:
		key = strings.ToUpper(strings.TrimSpace(p.SKU))
	case HP:
		key = strings.ToUpper(strings.TrimSpace(p.Baseboard))
	case Lenovo:
		key = lenovoRE.FindString(strings.ToUpper(strings.TrimSpace(p.Model)))
		return vendor, key
	default:
		return "", ""
	}
	if !hexKeyRE.MatchString(key) {
		return vendor, ""
	}
	return vendor, key
}

// Item is a BIOS update in a vendor catalog.
type Item struct {
	ID          string    `json:"id"` // Dell release ID, HP SoftPaq, Lenovo package ID (lower case)
	Title       string    `json:"title"`
	Version     string    `json:"version"`
	Description string    `json:"description,omitempty"`
	Released    time.Time `json:"released,omitzero"`
	Critical    bool      `json:"critical,omitempty"`
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	// Command is the vendor's silent install command inside the package
	// (HP, Lenovo); ExitCodes the codes that mean success.
	Command   string `json:"command,omitempty"`
	ExitCodes []int  `json:"exitCodes,omitempty"`
	// Families are the HP ROM families the SoftPaq updates.
	Families []string `json:"families,omitempty"`
	// Requires are the Lenovo BIOS levels the package updates; Installed
	// the levels that mean it is already there.
	Requires  []string `json:"requires,omitempty"`
	Installed []string `json:"installed,omitempty"`
}

// Applies reports whether the item updates a BIOS: newer than the device
// has, for the right ROM family.
func (it Item) Applies(vendor, biosVersion string) bool {
	bios := strings.TrimSpace(biosVersion)
	if bios == "" {
		return false
	}
	switch vendor {
	case Dell:
		return CompareVersions(it.Version, bios) > 0
	case HP:
		family, ver := hpBIOS(bios)
		if family == "" || !slices.ContainsFunc(it.Families, func(f string) bool { return strings.EqualFold(f, family) }) {
			return false
		}
		return CompareVersions(it.Version, ver) > 0
	case Lenovo:
		level := lenovoLevel(bios)
		return level != "" && matchLevel(it.Requires, level) && !matchLevel(it.Installed, level)
	}
	return false
}

// Best returns the newest item that applies, if any.
func Best(vendor, biosVersion string, items []Item) (Item, bool) {
	var best Item
	found := false
	for _, it := range items {
		if !it.Applies(vendor, biosVersion) {
			continue
		}
		if !found || newer(it, best) {
			best, found = it, true
		}
	}
	return best, found
}

func newer(a, b Item) bool {
	if c := CompareVersions(a.Version, b.Version); c != 0 {
		return c > 0
	}
	return a.Released.After(b.Released)
}

var hpBIOSRE = regexp.MustCompile(`(?i)^([A-Z0-9]{2,4})\s+Ver\.?\s*([0-9][0-9.]*)`)

// hpBIOS splits "S70 Ver. 01.23.00" into family and version.
func hpBIOS(v string) (string, string) {
	m := hpBIOSRE.FindStringSubmatch(v)
	if m == nil {
		return "", ""
	}
	return strings.ToUpper(m[1]), m[2]
}

// lenovoLevel is the BIOS ID of "N3BET70W (1.50 )": N3BET70W.
func lenovoLevel(v string) string {
	f := strings.Fields(v)
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(f[0])
}

// matchLevel matches a BIOS level against Lenovo patterns ("N3BET6*").
func matchLevel(patterns []string, level string) bool {
	for _, p := range patterns {
		p = strings.ToUpper(strings.TrimSpace(p))
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if strings.HasPrefix(level, prefix) {
				return true
			}
		} else if p != "" && p == level {
			return true
		}
	}
	return false
}

var numRE = regexp.MustCompile(`\d+`)

// CompareVersions compares the numbers in two version strings: 1.21.0 <
// 1.23.0, 01.09.00 < 01.10.00, A09 < A10.
func CompareVersions(a, b string) int {
	x, y := numRE.FindAllString(a, -1), numRE.FindAllString(b, -1)
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q int
		if i < len(x) {
			p, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			q, _ = strconv.Atoi(y[i])
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---- XML ----

// utf8XML turns a UTF-16 document (Dell) into UTF-8.
func utf8XML(raw []byte) []byte {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if len(raw) < 2 || !(raw[0] == 0xff && raw[1] == 0xfe) {
		return raw
	}
	raw = raw[2:]
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return []byte(string(utf16.Decode(u)))
}

func decodeXML(raw []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(utf8XML(raw)))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec.Decode(v)
}

type dellHash struct {
	Algorithm string `xml:"algorithm,attr"`
	Value     string `xml:",chardata"`
}

type dellModel struct {
	SystemID string `xml:"systemID,attr"`
}

func sha256Of(hashes []dellHash) string {
	for _, h := range hashes {
		if strings.EqualFold(h.Algorithm, "SHA256") {
			return strings.ToLower(strings.TrimSpace(h.Value))
		}
	}
	return ""
}

// DellModelCatalog is the per-model catalog a Dell system ID points at.
type DellModelCatalog struct {
	Path   string
	SHA256 string
}

// ParseDellIndex reads CatalogIndexPC.xml: system ID -> model catalog.
func ParseDellIndex(raw []byte) (map[string]DellModelCatalog, error) {
	var doc struct {
		Groups []struct {
			Models []dellModel `xml:"SupportedSystems>Brand>Model"`
			Info   struct {
				Path   string     `xml:"path,attr"`
				Hashes []dellHash `xml:"Cryptography>Hash"`
			} `xml:"ManifestInformation"`
		} `xml:"GroupManifest"`
	}
	if err := decodeXML(raw, &doc); err != nil {
		return nil, err
	}
	out := map[string]DellModelCatalog{}
	for _, g := range doc.Groups {
		if g.Info.Path == "" {
			continue
		}
		for _, m := range g.Models {
			out[strings.ToUpper(m.SystemID)] = DellModelCatalog{Path: g.Info.Path, SHA256: sha256Of(g.Info.Hashes)}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the Dell catalog index lists no models")
	}
	return out, nil
}

// ParseDellModel reads a model catalog and returns the BIOS updates for the
// system ID.
func ParseDellModel(raw []byte, systemID string) ([]Item, error) {
	var doc struct {
		Components []struct {
			ReleaseID     string `xml:"releaseID,attr"`
			VendorVersion string `xml:"vendorVersion,attr"`
			Path          string `xml:"path,attr"`
			Size          int64  `xml:"size,attr"`
			DateTime      string `xml:"dateTime,attr"`
			Name          string `xml:"Name>Display"`
			Description   string `xml:"Description>Display"`
			Type          struct {
				Value string `xml:"value,attr"`
			} `xml:"ComponentType"`
			Criticality struct {
				Value string `xml:"value,attr"`
			} `xml:"Criticality"`
			Models []dellModel `xml:"SupportedSystems>Brand>Model"`
			Hashes []dellHash  `xml:"Cryptography>Hash"`
		} `xml:"SoftwareComponent"`
	}
	if err := decodeXML(raw, &doc); err != nil {
		return nil, err
	}
	var out []Item
	for _, c := range doc.Components {
		if c.Type.Value != "BIOS" || c.Path == "" || !slices.ContainsFunc(c.Models, func(m dellModel) bool {
			return strings.EqualFold(m.SystemID, systemID)
		}) {
			continue
		}
		it := Item{
			ID: strings.ToLower(c.ReleaseID), Title: strings.TrimSpace(c.Name), Version: c.VendorVersion,
			Description: strings.TrimSpace(c.Description), URL: DellBase + strings.TrimPrefix(c.Path, "/"),
			Name: lastPart(c.Path), Size: c.Size, SHA256: sha256Of(c.Hashes),
			// 1 = Recommended; 2 = Urgent (Dell's "critical").
			Critical: c.Criticality.Value == "2",
		}
		if t, err := time.Parse(time.RFC3339, c.DateTime); err == nil {
			it.Released = t
		}
		if it.ID == "" || it.SHA256 == "" {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// HPRef returns the HP Image Assistant reference file for a platform
// and Windows release ("11.0.24h2").
func HPRef(platform, os string) string {
	return strings.NewReplacer("{p}", strings.ToLower(platform), "{os}", os).Replace(HPRefURL)
}

// HPOSVersions are the reference files to try for a Windows release, the
// best match first. HP publishes one file per Windows release.
func HPOSVersions(p Platform) []string {
	ver := strings.ToLower(strings.TrimSpace(p.OSVersion))
	win := "11.0"
	if strings.Contains(p.OSName, "Windows 10") {
		win = "10.0"
	}
	var out []string
	if regexp.MustCompile(`^\d\dh\d$`).MatchString(ver) {
		out = append(out, win+"."+ver)
	}
	for _, v := range []string{"11.0.25h2", "11.0.24h2", "11.0.23h2", "10.0.22h2"} {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// ParseHPRef reads an HP Image Assistant reference file and returns the
// BIOS SoftPaqs.
func ParseHPRef(raw []byte) ([]Item, error) {
	var doc struct {
		Updates []struct {
			ID          string `xml:"Id"`
			Name        string `xml:"Name"`
			Category    string `xml:"Category"`
			Version     string `xml:"Version"`
			ReleaseType string `xml:"ReleaseType"`
			Silent      string `xml:"SilentInstall"`
			URL         string `xml:"Url"`
			SHA256      string `xml:"SHA256"`
			Size        int64  `xml:"Size"`
			Date        string `xml:"DateReleased"`
			Description string `xml:"Description"`
			BIOS        []struct {
				Family string `xml:"BiosFamily"`
			} `xml:"DetailFileInformation>BiosDetail"`
		} `xml:"Solutions>UpdateInfo"`
	}
	if err := decodeXML(raw, &doc); err != nil {
		return nil, err
	}
	var out []Item
	for _, u := range doc.Updates {
		if !strings.HasPrefix(u.Category, "BIOS") || u.URL == "" || u.Silent == "" {
			continue
		}
		link := strings.TrimSpace(u.URL)
		link = strings.TrimPrefix(strings.TrimPrefix(link, "http://"), "https://")
		it := Item{
			ID: strings.ToLower(u.ID), Title: strings.TrimSpace(u.Name), Version: strings.TrimSpace(u.Version),
			Description: strings.TrimSpace(u.Description), URL: "https://" + link, Name: lastPart(link), Size: u.Size,
			SHA256: strings.ToLower(strings.TrimSpace(u.SHA256)), Command: hpCommand(u.Silent), ExitCodes: []int{0, 3010, 1641},
			Critical: strings.EqualFold(u.ReleaseType, "Critical"),
		}
		if t, err := time.Parse("2006-01-02", strings.TrimSpace(u.Date)); err == nil {
			it.Released = t
		}
		for _, b := range u.BIOS {
			if f := strings.ToUpper(strings.TrimSpace(b.Family)); f != "" && !slices.Contains(it.Families, f) {
				it.Families = append(it.Families, f)
			}
		}
		if it.ID == "" || it.SHA256 == "" || len(it.Families) == 0 {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// hpCommand drops the switch that makes HP's flash tool restart the PC by
// itself (-r): the patch job restarts in the maintenance window.
func hpCommand(cmd string) string {
	f := splitCommand(cmd)
	out := f[:0]
	for _, a := range f {
		if !strings.EqualFold(a, "-r") && !strings.EqualFold(a, "/r") {
			out = append(out, a)
		}
	}
	return joinCommand(out)
}

// LenovoCatalog returns the catalog address for a machine type.
func LenovoCatalog(mt string, p Platform) []string {
	oses := []string{"Win11", "Win10"}
	if strings.Contains(p.OSName, "Windows 10") {
		oses = []string{"Win10", "Win11"}
	}
	var out []string
	for _, os := range oses {
		out = append(out, strings.NewReplacer("{mt}", strings.ToUpper(mt), "{os}", os).Replace(LenovoURL))
	}
	return out
}

// LenovoRef is a package in a Lenovo catalog.
type LenovoRef struct {
	Location string
	SHA256   string
}

// ParseLenovoCatalog returns the BIOS packages of a machine type catalog.
func ParseLenovoCatalog(raw []byte) ([]LenovoRef, error) {
	var doc struct {
		Packages []struct {
			Location string `xml:"location"`
			Category string `xml:"category"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
		} `xml:"package"`
	}
	if err := decodeXML(raw, &doc); err != nil {
		return nil, err
	}
	var out []LenovoRef
	for _, p := range doc.Packages {
		if !strings.EqualFold(strings.TrimSpace(p.Category), "BIOS UEFI") || p.Location == "" {
			continue
		}
		ref := LenovoRef{Location: strings.TrimSpace(p.Location)}
		if strings.EqualFold(p.Checksum.Type, "sha256") {
			ref.SHA256 = strings.ToLower(strings.TrimSpace(p.Checksum.Value))
		}
		out = append(out, ref)
	}
	return out, nil
}

type lenovoBios struct {
	Levels []string `xml:"Level"`
}

// lenovoCond is a DetectInstall or Dependencies tree; only the BIOS levels
// in it are used.
type lenovoCond struct {
	Bios []lenovoBios `xml:"_Bios"`
	And  []lenovoCond `xml:"And"`
	Or   []lenovoCond `xml:"Or"`
}

func (c lenovoCond) levels() []string {
	var out []string
	for _, b := range c.Bios {
		out = append(out, b.Levels...)
	}
	for _, s := range append(slices.Clone(c.And), c.Or...) {
		out = append(out, s.levels()...)
	}
	return out
}

// ParseLenovoPackage reads a package descriptor; location is where it was
// fetched from (the installer sits next to it).
func ParseLenovoPackage(raw []byte, location string) (Item, error) {
	var doc struct {
		ID       string     `xml:"id,attr"`
		Version  string     `xml:"version,attr"`
		Title    string     `xml:"Title>Desc"`
		Severity lenovoAttr `xml:"Severity"`
		Extract  string     `xml:"ExtractCommand"`
		Released string     `xml:"ReleaseDate"`
		Install  struct {
			RC      string `xml:"rc,attr"`
			Cmdline string `xml:"Cmdline"`
		} `xml:"Install"`
		Detect lenovoCond `xml:"DetectInstall"`
		Deps   lenovoCond `xml:"Dependencies"`
		File   struct {
			Name string `xml:"Name"`
			CRC  string `xml:"CRC"`
			Size int64  `xml:"Size"`
		} `xml:"Files>Installer>File"`
	}
	if err := decodeXML(raw, &doc); err != nil {
		return Item{}, err
	}
	base := location[:strings.LastIndex(location, "/")+1]
	it := Item{
		ID: strings.ToLower(doc.ID), Title: strings.TrimSpace(doc.Title), Version: strings.TrimSpace(doc.Version),
		URL: base + doc.File.Name, Name: doc.File.Name, Size: doc.File.Size, SHA256: strings.ToLower(strings.TrimSpace(doc.File.CRC)),
		Command: strings.TrimSpace(doc.Install.Cmdline), Requires: doc.Deps.levels(), Installed: doc.Detect.levels(),
		Critical: doc.Severity.Type == "1",
	}
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(doc.Released)); err == nil {
		it.Released = t
	}
	for _, v := range strings.Split(doc.Install.RC, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			it.ExitCodes = append(it.ExitCodes, n)
		}
	}
	switch {
	case it.ID == "" || doc.File.Name == "" || len(it.SHA256) != 64:
		return Item{}, errors.New("the package has no installer with a checksum")
	case !strings.Contains(strings.ToUpper(doc.Extract), "/EXTRACT") || it.Command == "":
		return Item{}, errors.New("the package cannot be installed silently")
	case len(it.Requires) == 0:
		return Item{}, errors.New("the package does not say which BIOS it updates")
	}
	return it, nil
}

type lenovoAttr struct {
	Type string `xml:"type,attr"`
}

func lastPart(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func splitCommand(cmd string) []string { return agentproto.SplitCommand(cmd) }

func joinCommand(args []string) string { return agentproto.JoinCommand(args) }
