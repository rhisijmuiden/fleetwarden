package reports

import (
	"fmt"
	"strings"
	"time"
)

// Texts of reports, PDF and CSV in English and Dutch.
var texts = map[string]map[string]string{
	"en": {
		"pdf.generated": "Generated on %s",
		"pdf.page":      "Page %d of %s",
		"pdf.noRows":    "Nothing to report.",
		"scope.all":     "All groups",
		"scope.groups":  "Groups: %s",

		"compliance.title":        "Compliance report",
		"compliance.summary":      "Summary",
		"compliance.text":         "A device is compliant when it was seen in the last %d days, misses no security or critical updates older than %d days, has no outdated software and at least %d%% free space on every disk.",
		"compliance.score":        "Compliance",
		"compliance.devices":      "Devices",
		"compliance.compliant":    "Compliant",
		"compliance.stale":        "Not seen for %d days",
		"compliance.patches":      "Missing security updates",
		"compliance.software":     "Outdated software",
		"compliance.disk":         "Low disk space",
		"compliance.notScanned":   "No patch scan",
		"compliance.byGroup":      "Compliance per group",
		"compliance.nonCompliant": "Devices that are not compliant",
		"compliance.allCompliant": "Every device is compliant.",
		"compliance.ofDevices":    "%d of %d devices",
		"compliance.reason.stale": "not seen since %s",
		"compliance.reason.never": "never seen",
		"compliance.reason.patch": "%d security updates missing",
		"compliance.reason.scan":  "no patch scan",
		"compliance.reason.sw":    "%d outdated programs",
		"compliance.reason.disk":  "low disk space on %s (%d%% free)",
		"compliance.reasons":      "Reasons",
		"patches.title":           "Missing patches report",
		"patches.summary":         "Summary",
		"patches.devicesMissing":  "Devices missing updates",
		"patches.devicesSecurity": "Missing security updates",
		"patches.updates":         "Different updates",
		"patches.reboot":          "Waiting for a restart",
		"patches.upToDate":        "Up to date",
		"patches.byUpdate":        "Missing updates",
		"patches.byDevice":        "Devices",
		"patches.none":            "No device misses updates.",
		"software.title":          "Outdated software report",
		"software.summary":        "Summary",
		"software.devices":        "Devices with outdated software",
		"software.programs":       "Outdated programs",
		"software.installs":       "Outdated installations",
		"software.matched":        "Programs recognised",
		"software.byProgram":      "Outdated programs",
		"software.byDevice":       "Devices",
		"software.none":           "All recognised software is up to date.",
		"software.note":           "Windows programs are compared with the newest version in the winget catalog; programs the catalog does not know are not listed. Linux packages are in the missing patches report.",
		"software.noCatalog":      "The winget catalog has not been downloaded yet, so outdated software cannot be determined.",
		"col.device":              "Device",
		"col.group":               "Group",
		"col.platform":            "Platform",
		"col.lastSeen":            "Last seen",
		"col.score":               "Compliance",
		"col.devices":             "Devices",
		"col.compliant":           "Compliant",
		"col.stale":               "Not seen",
		"col.patches":             "Security updates",
		"col.software":            "Outdated software",
		"col.disk":                "Low disk",
		"col.update":              "Update",
		"col.kb":                  "KB",
		"col.category":            "Category",
		"col.severity":            "Severity",
		"col.released":            "Released",
		"col.firstSeen":           "First seen",
		"col.missing":             "Missing",
		"col.security":            "Security",
		"col.reboot":              "Restart",
		"col.lastScan":            "Last scan",
		"col.program":             "Program",
		"col.publisher":           "Publisher",
		"col.installed":           "Installed",
		"col.latest":              "Latest",
		"col.package":             "winget package",
		"col.reasons":             "Reasons",
		"col.compliantYesNo":      "Compliant",
		"col.freeSpace":           "Lowest free space",
		"yes":                     "yes",
		"no":                      "no",
		"never":                   "never",
		"category.security":       "Security",
		"category.critical":       "Critical",
		"category.cumulative":     "Cumulative",
		"category.definition":     "Definitions",
		"category.driver":         "Driver",
		"category.feature":        "Feature",
		"category.other":          "Other",
	},
	"nl": {
		"pdf.generated": "Gemaakt op %s",
		"pdf.page":      "Pagina %d van %s",
		"pdf.noRows":    "Niets te melden.",
		"scope.all":     "Alle groepen",
		"scope.groups":  "Groepen: %s",

		"compliance.title":        "Compliancerapport",
		"compliance.summary":      "Samenvatting",
		"compliance.text":         "Een apparaat is compliant als het de afgelopen %d dagen gezien is, geen beveiligings- of kritieke updates mist die ouder zijn dan %d dagen, geen verouderde software heeft en op elke schijf minstens %d%% vrije ruimte heeft.",
		"compliance.score":        "Compliance",
		"compliance.devices":      "Apparaten",
		"compliance.compliant":    "Compliant",
		"compliance.stale":        "%d dagen niet gezien",
		"compliance.patches":      "Missen beveiligingsupdates",
		"compliance.software":     "Verouderde software",
		"compliance.disk":         "Weinig schijfruimte",
		"compliance.notScanned":   "Geen patchscan",
		"compliance.byGroup":      "Compliance per groep",
		"compliance.nonCompliant": "Apparaten die niet compliant zijn",
		"compliance.allCompliant": "Alle apparaten zijn compliant.",
		"compliance.ofDevices":    "%d van %d apparaten",
		"compliance.reason.stale": "niet gezien sinds %s",
		"compliance.reason.never": "nooit gezien",
		"compliance.reason.patch": "%d beveiligingsupdates ontbreken",
		"compliance.reason.scan":  "geen patchscan",
		"compliance.reason.sw":    "%d verouderde programma's",
		"compliance.reason.disk":  "weinig ruimte op %s (%d%% vrij)",
		"compliance.reasons":      "Redenen",
		"patches.title":           "Rapport ontbrekende patches",
		"patches.summary":         "Samenvatting",
		"patches.devicesMissing":  "Apparaten die updates missen",
		"patches.devicesSecurity": "Missen beveiligingsupdates",
		"patches.updates":         "Verschillende updates",
		"patches.reboot":          "Wachten op herstart",
		"patches.upToDate":        "Bijgewerkt",
		"patches.byUpdate":        "Ontbrekende updates",
		"patches.byDevice":        "Apparaten",
		"patches.none":            "Geen enkel apparaat mist updates.",
		"software.title":          "Rapport verouderde software",
		"software.summary":        "Samenvatting",
		"software.devices":        "Apparaten met verouderde software",
		"software.programs":       "Verouderde programma's",
		"software.installs":       "Verouderde installaties",
		"software.matched":        "Herkende programma's",
		"software.byProgram":      "Verouderde programma's",
		"software.byDevice":       "Apparaten",
		"software.none":           "Alle herkende software is bijgewerkt.",
		"software.note":           "Windows-programma's worden vergeleken met de nieuwste versie in de winget-catalogus; programma's die de catalogus niet kent staan er niet in. Linux-pakketten staan in het rapport ontbrekende patches.",
		"software.noCatalog":      "De winget-catalogus is nog niet gedownload, dus verouderde software kan nog niet bepaald worden.",
		"col.device":              "Apparaat",
		"col.group":               "Groep",
		"col.platform":            "Platform",
		"col.lastSeen":            "Laatst gezien",
		"col.score":               "Compliance",
		"col.devices":             "Apparaten",
		"col.compliant":           "Compliant",
		"col.stale":               "Niet gezien",
		"col.patches":             "Beveiligingsupdates",
		"col.software":            "Verouderde software",
		"col.disk":                "Weinig ruimte",
		"col.update":              "Update",
		"col.kb":                  "KB",
		"col.category":            "Categorie",
		"col.severity":            "Ernst",
		"col.released":            "Uitgebracht",
		"col.firstSeen":           "Eerst gezien",
		"col.missing":             "Ontbreekt",
		"col.security":            "Beveiliging",
		"col.reboot":              "Herstart",
		"col.lastScan":            "Laatste scan",
		"col.program":             "Programma",
		"col.publisher":           "Uitgever",
		"col.installed":           "Geïnstalleerd",
		"col.latest":              "Nieuwste",
		"col.package":             "winget-pakket",
		"col.reasons":             "Redenen",
		"col.compliantYesNo":      "Compliant",
		"col.freeSpace":           "Minste vrije ruimte",
		"yes":                     "ja",
		"no":                      "nee",
		"never":                   "nooit",
		"category.security":       "Beveiliging",
		"category.critical":       "Kritiek",
		"category.cumulative":     "Cumulatief",
		"category.definition":     "Definities",
		"category.driver":         "Stuurprogramma",
		"category.feature":        "Functie",
		"category.other":          "Overig",
	},
}

func tr(lang, key string) string {
	if t, ok := texts[lang][key]; ok {
		return t
	}
	if t, ok := texts["en"][key]; ok {
		return t
	}
	return key
}

// T returns the text for key in lang, formatted with args when given.
func T(lang, key string, args ...any) string {
	if len(args) == 0 {
		return tr(lang, key)
	}
	return fmt.Sprintf(tr(lang, key), args...)
}

// Lang returns a supported language, English by default.
func Lang(l string) string {
	if strings.HasPrefix(l, "nl") {
		return "nl"
	}
	return "en"
}

var nlMonths = []string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"}

// FormatDate writes a date the way people in lang read it.
func FormatDate(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if lang == "nl" {
		return fmt.Sprintf("%d %s %d", t.Day(), nlMonths[t.Month()-1], t.Year())
	}
	return t.Format("2 January 2006")
}

func formatDateTime(lang string, t time.Time) string {
	return FormatDate(lang, t) + " " + t.Format("15:04")
}

// FormatDateTime writes a date and time.
func FormatDateTime(lang string, t time.Time) string { return formatDateTime(lang, t) }

// ShortDate writes a date for table cells and CSV: 2006-01-02.
func ShortDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
