package vulndb

import (
	"regexp"
	"strings"
)

// Product is a Windows program FleetWarden recognises in the inventory and
// looks up in the NVD by its CPE vendor and product.
type Product struct {
	CPE      string // vendor:product
	Name     string // shown in the interface
	match    *regexp.Regexp
	exclude  *regexp.Regexp
	WingetID string // package that updates it
	Browser  string // chrome, edge, firefox: updated with the browser deployment
}

// ProductsVersion changes whenever Products gains or loses a CPE, so the
// server reads the NVD feeds again for the new products.
const ProductsVersion = 1

func p(cpe, name, re, winget string) Product {
	return Product{CPE: cpe, Name: name, match: regexp.MustCompile(`(?i)` + re), WingetID: winget}
}

// Products are the programs matched against the NVD. The names are the
// display names in Apps & features; versions there follow the vendor's own
// numbering, as the NVD does.
var Products = []Product{
	withBrowser(p("google:chrome", "Google Chrome", `^Google Chrome$`, "Google.Chrome"), "chrome"),
	withBrowser(p("microsoft:edge_chromium", "Microsoft Edge", `^Microsoft Edge$`, "Microsoft.Edge"), "edge"),
	withBrowser(p("mozilla:firefox", "Mozilla Firefox", `^Mozilla Firefox( \(.*\))?$`, "Mozilla.Firefox"), "firefox"),
	p("mozilla:firefox_esr", "Mozilla Firefox ESR", `^Mozilla Firefox ESR( \(.*\))?$`, "Mozilla.Firefox.ESR"),
	p("mozilla:thunderbird", "Mozilla Thunderbird", `^Mozilla Thunderbird( \(.*\))?$`, "Mozilla.Thunderbird"),
	p("7-zip:7-zip", "7-Zip", `^7-Zip [\d.]+( \(x64\)| \(x64 edition\))?$`, "7zip.7zip"),
	p("videolan:vlc_media_player", "VLC media player", `^VLC media player$`, "VideoLAN.VLC"),
	p("notepad-plus-plus:notepad++", "Notepad++", `^Notepad\+\+( \(.*\))?$`, "Notepad++.Notepad++"),
	p("adobe:acrobat_reader_dc", "Adobe Acrobat Reader", `^Adobe Acrobat Reader( DC)?( - \w+)?( \(64-bit\))?$`, "Adobe.Acrobat.Reader.64-bit"),
	p("adobe:acrobat_dc", "Adobe Acrobat", `^Adobe Acrobat( DC)?( \(64-bit\))?$`, ""),
	p("putty:putty", "PuTTY", `^PuTTY release [\d.]+( \(64-bit\))?$`, "PuTTY.PuTTY"),
	p("winscp:winscp", "WinSCP", `^WinSCP [\d.]+$`, "WinSCP.WinSCP"),
	p("wireshark:wireshark", "Wireshark", `^Wireshark [\d.]+( x64| x86| \(64-bit\))?$`, "WiresharkFoundation.Wireshark"),
	p("git-scm:git", "Git", `^Git$`, "Git.Git"),
	p("filezilla-project:filezilla_client", "FileZilla", `^FileZilla( Client)? [\d.]+$`, "TimKosse.FileZilla.Client"),
	p("keepass:keepass", "KeePass", `^KeePass Password Safe 2\.[\d.]+$`, "DominikReichl.KeePass"),
	p("teamviewer:teamviewer", "TeamViewer", `^TeamViewer( \d+)?$`, "TeamViewer.TeamViewer"),
	p("anydesk:anydesk", "AnyDesk", `^AnyDesk$`, "AnyDeskSoftwareGmbH.AnyDesk"),
	p("microsoft:visual_studio_code", "Visual Studio Code", `^Microsoft Visual Studio Code( \(User\))?$`, "Microsoft.VisualStudioCode"),
	p("libreoffice:libreoffice", "LibreOffice", `^LibreOffice [\d.]+$`, "TheDocumentFoundation.LibreOffice"),
	p("foxit:pdf_reader", "Foxit PDF Reader", `^Foxit (PDF )?Reader$`, "Foxit.FoxitReader"),
	p("nodejs:node.js", "Node.js", `^Node\.js$`, "OpenJS.NodeJS.LTS"),
	p("oracle:vm_virtualbox", "VirtualBox", `^Oracle (VM )?VirtualBox [\d.]+$`, "Oracle.VirtualBox"),
	p("vmware:workstation", "VMware Workstation", `^VMware Workstation$`, ""),
	p("rarlab:winrar", "WinRAR", `^WinRAR [\d.]+( \(64-bit\)| \(32-bit\))?$`, "RARLab.WinRAR"),
	p("slack:slack", "Slack", `^Slack( \(Machine( - MSI)?\))?$`, "SlackTechnologies.Slack"),
	p("docker:desktop", "Docker Desktop", `^Docker Desktop$`, "Docker.DockerDesktop"),
	p("microsoft:powershell", "PowerShell", `^PowerShell 7(\.\d+)?-x(64|86)$`, "Microsoft.PowerShell"),
	p("zoom:zoom", "Zoom", `^Zoom( Workplace)?( \(64-bit\))?$`, "Zoom.Zoom"),
	p("greenshot:greenshot", "Greenshot", `^Greenshot [\d.]+$`, "Greenshot.Greenshot"),
	p("python:python", "Python", `^Python 3\.\d+\.\d+ \((64|32)-bit\)$`, ""),
}

func withBrowser(p Product, key string) Product {
	p.Browser = key
	return p
}

var productByCPE = func() map[string]*Product {
	m := map[string]*Product{}
	for i := range Products {
		m[Products[i].CPE] = &Products[i]
	}
	return m
}()

// ProductByCPE returns a product of the list.
func ProductByCPE(cpe string) (Product, bool) {
	p, ok := productByCPE[cpe]
	if !ok {
		return Product{}, false
	}
	return *p, true
}

// WantProduct selects the products of the list (for ParseNVD).
func WantProduct(cpe string) bool { _, ok := productByCPE[cpe]; return ok }

// MatchProgram finds the product of an installed Windows program.
func MatchProgram(name string) (Product, bool) {
	name = strings.TrimSpace(name)
	for _, p := range Products {
		if p.match.MatchString(name) && (p.exclude == nil || !p.exclude.MatchString(name)) {
			return p, true
		}
	}
	return Product{}, false
}

var leadingVersionRE = regexp.MustCompile(`\d+(?:\.\d+)*`)

// ProgramVersion is the comparable part of a displayed version
// ("ad 8.1.0" is 8.1.0, "3.12.4150.0" stays as it is).
func ProgramVersion(cpe, v string) string {
	m := leadingVersionRE.FindString(v)
	if cpe == "python:python" {
		// Python's installer shows 3.12.4150.0 for 3.12.4 (build 150).
		parts := strings.Split(m, ".")
		if len(parts) >= 3 && len(parts[2]) >= 4 {
			parts[2] = parts[2][:len(parts[2])-3]
			return strings.Join(parts[:3], ".")
		}
	}
	return m
}

// CompareProgram compares program versions part by part, numbers as
// numbers; missing parts count as zero (0.80 equals 0.80.0.0).
func CompareProgram(a, b string) int {
	pa, pb := strings.FieldsFunc(a, versionSep), strings.FieldsFunc(b, versionSep)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := "0", "0"
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		xn, yn := leadingNum(x), leadingNum(y)
		if c := compareNum(orZero(xn), orZero(yn)); c != 0 {
			return c
		}
		// 1.0.2zj: the letters after the number count too.
		if c := strings.Compare(x[len(xn):], y[len(yn):]); c != 0 {
			return c
		}
	}
	return 0
}

func versionSep(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' || r == ' ' }

func leadingNum(s string) string {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i]
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
