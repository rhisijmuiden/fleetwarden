package reports

import (
	"fmt"
	"strings"
	"time"
)

// Texts of reports, PDF and CSV in English and Dutch.
var texts = map[string]map[string]string{
	"en": {
		"pdf.generated":          "Generated on %s",
		"pdf.page":               "Page %d of %s",
		"pdf.noRows":             "Nothing to report.",
		"alert.offline":          "No contact for %s (last seen %s)",
		"alert.disk":             "%s has %d%% free (%s of %s)",
		"alert.back.offline":     "Back online",
		"alert.back.disk":        "%s has %d%% free again",
		"alert.threat":           "Active threat: %s",
		"alert.antivirus":        "Antivirus: %s",
		"alert.firewall":         "Firewall: %s",
		"alert.encryption":       "Disk encryption: %s",
		"alert.back.threat":      "No active threats any more",
		"alert.back.antivirus":   "Antivirus is on and up to date again",
		"alert.back.firewall":    "The firewall is on again",
		"alert.back.encryption":  "The system drive is encrypted again",
		"security.av.outdated":   "signatures out of date",
		"security.av.disabled":   "turned off",
		"security.av.none":       "none installed",
		"security.fw.off":        "turned off",
		"security.fw.partial":    "off for some network profiles",
		"security.enc.none":      "the system drive is not encrypted",
		"security.enc.partial":   "not every fixed drive is encrypted",
		"security.enc.suspended": "BitLocker protection is suspended",
		"dur.minutes":            "%d min",
		"dur.hours":              "%d h %d min",
		"dur.days":               "%d days %d h",
		"scope.all":              "All groups",
		"scope.groups":           "Groups: %s",

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
		"pdf.generated":          "Gemaakt op %s",
		"pdf.page":               "Pagina %d van %s",
		"pdf.noRows":             "Niets te melden.",
		"alert.offline":          "Geen contact gedurende %s (laatst gezien %s)",
		"alert.disk":             "%s heeft %d%% vrij (%s van %s)",
		"alert.back.offline":     "Weer online",
		"alert.back.disk":        "%s heeft weer %d%% vrij",
		"alert.threat":           "Actieve dreiging: %s",
		"alert.antivirus":        "Antivirus: %s",
		"alert.firewall":         "Firewall: %s",
		"alert.encryption":       "Schijfversleuteling: %s",
		"alert.back.threat":      "Geen actieve dreigingen meer",
		"alert.back.antivirus":   "Antivirus staat weer aan en is bijgewerkt",
		"alert.back.firewall":    "De firewall staat weer aan",
		"alert.back.encryption":  "De systeemschijf is weer versleuteld",
		"security.av.outdated":   "definities verouderd",
		"security.av.disabled":   "uitgeschakeld",
		"security.av.none":       "niet geïnstalleerd",
		"security.fw.off":        "uitgeschakeld",
		"security.fw.partial":    "uit voor sommige netwerkprofielen",
		"security.enc.none":      "de systeemschijf is niet versleuteld",
		"security.enc.partial":   "niet elke vaste schijf is versleuteld",
		"security.enc.suspended": "BitLocker-beveiliging is onderbroken",
		"dur.minutes":            "%d min",
		"dur.hours":              "%d uur %d min",
		"dur.days":               "%d dagen %d uur",
		"scope.all":              "Alle groepen",
		"scope.groups":           "Groepen: %s",

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

// Duration writes a period such as "2 h 15 min" or "3 days 4 h".
func Duration(lang string, d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m < 60:
		return T(lang, "dur.minutes", m)
	case m < 24*60:
		return T(lang, "dur.hours", m/60, m%60)
	default:
		return T(lang, "dur.days", m/(24*60), m%(24*60)/60)
	}
}

// Bytes writes a size in GB (or MB below 1 GB), with a decimal comma in Dutch.
func Bytes(lang string, b int64) string {
	var s string
	if b < 1<<30 {
		s = fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	} else if b < 100<<30 {
		s = fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	} else {
		s = fmt.Sprintf("%.0f GB", float64(b)/(1<<30))
	}
	if lang == "nl" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}
