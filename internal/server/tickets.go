package server

// Tickets from the tray. A help request carries a report of the device (the
// parts the administrator chose under Self-service > Settings) and goes, as
// a ticket, to a service desk through an integration, to a mailbox through
// the mail server or through the user's own Outlook. Every request is also
// kept under Self-service > Help requests, with the ticket number, and the
// ticket's state in the service desk is read back for the tray.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/integrations"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// reportParts are the parts of the device report, in report order.
var reportParts = []string{"user", "time", "network", "os", "hardware", "disk", "uptime", "reboot", "updates", "software", "security", "alerts"}

// linkActions are the quick actions a ticket can link to.
var linkActions = []string{"remote", "terminal", "scan"}

const (
	maxCategories    = 30
	ticketSyncEvery  = 5 * time.Minute
	ticketSyncBatch  = 50
	maxReportUpdates = 15
)

// normalizeTickets checks and cleans the ticket part of the settings.
func (s *Server) normalizeTickets(ctx context.Context, set *selfServiceSettings) string {
	switch set.Destination {
	case "", agentproto.TicketFleetWarden:
		set.Destination = agentproto.TicketFleetWarden
	case agentproto.TicketOutlook, agentproto.TicketMail:
		set.TicketAddress = strings.TrimSpace(set.TicketAddress)
		if !mail.ValidAddress(set.TicketAddress) {
			return "invalid_email"
		}
	case agentproto.TicketIntegration:
		i, err := s.st.IntegrationByID(ctx, set.TicketIntegration)
		if err != nil {
			return "invalid_integration"
		}
		info, _ := integrations.Info(integrations.Kind(i.Kind))
		if !info.ServiceDesk && i.Kind != string(integrations.Custom) {
			return "invalid_integration"
		}
		set.TicketAddress = strings.TrimSpace(set.TicketAddress)
		if set.TicketAddress != "" && !mail.ValidAddress(set.TicketAddress) {
			return "invalid_email"
		}
	default:
		return "bad_request"
	}
	if set.Destination != agentproto.TicketIntegration {
		set.TicketIntegration = 0
		set.FallbackMail = false
	}
	if set.MinMessage < 0 || set.MinMessage > 500 {
		return "bad_request"
	}
	if !set.RequireMessage {
		set.MinMessage = 0
	}
	cats := []string{}
	for _, c := range set.Categories {
		c = cleanText(c, 60)
		if c != "" && !slices.Contains(cats, c) {
			cats = append(cats, c)
		}
	}
	if len(cats) > maxCategories {
		return "bad_request"
	}
	set.Categories = cats
	if set.AskCategory && len(cats) == 0 {
		set.AskCategory = false
	}
	parts := []string{}
	for _, p := range reportParts { // keep the report order
		if slices.Contains(set.Report, p) {
			parts = append(parts, p)
		}
	}
	for _, p := range set.Report {
		if !slices.Contains(reportParts, p) {
			return "bad_request"
		}
	}
	set.Report = parts
	acts := []string{}
	for _, a := range linkActions {
		if slices.Contains(set.LinkActions, a) {
			acts = append(acts, a)
		}
	}
	for _, a := range set.LinkActions {
		if !slices.Contains(linkActions, a) {
			return "bad_request"
		}
	}
	set.LinkActions = acts
	return ""
}

// ---- the report ----

type reportLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Tone marks a problem: warning or danger ("" for neutral, success for OK).
	Tone string `json:"tone,omitempty"`
}

type reportSection struct {
	Key   string       `json:"key"`
	Title string       `json:"title"`
	Lines []reportLine `json:"lines"`
}

type reportLink struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// ticketReport is the device report of a ticket.
type ticketReport struct {
	Hostname string          `json:"hostname"`
	Summary  []reportLine    `json:"summary"`
	Sections []reportSection `json:"sections"`
	Links    []reportLink    `json:"links"`
}

var reportTexts = map[string]map[string]string{
	"en": {
		"s.device": "Device", "s.hardware": "Hardware", "s.status": "Updates and security", "s.software": "Catalog and third-party software",
		"s.alerts": "Open alerts", "computer": "Computer name", "user": "User", "email": "E-mail", "time": "Reported at",
		"localIp": "IP addresses", "publicIp": "Public IP", "os": "Operating system", "group": "FleetWarden group", "domain": "Domain",
		"model": "Model", "serial": "Serial number", "cpu": "CPU", "ram": "Memory", "disk": "Disk %s", "diskValue": "%s free of %s (%d%%)",
		"uptime": "Up since", "uptimeValue": "%s (%s)", "reboot": "Reboot pending", "yes": "Yes", "no": "No", "updates": "Updates waiting",
		"noUpdates": "None", "lastScan": "Last update scan", "never": "Never", "programs": "Programs installed", "upToDate": "up to date",
		"available": "%s available", "failed": "failed", "installing": "busy", "encryption": "Disk encryption", "antivirus": "Antivirus",
		"firewall": "Firewall", "threats": "Active threats", "noAlerts": "None", "noSoftware": "No catalog deployments or outdated programs",
		"link.device": "Open in FleetWarden", "link.remote": "Remote control", "link.terminal": "Terminal", "link.scan": "Run a scan",
		"days": "%d days %d h", "hours": "%d h", "summary.reboot": "Reboot pending", "summary.updates": "Updates waiting",
		"summary.software": "Software problems", "summary.security": "Security",
		"enc.encrypted": "Encrypted", "enc.partial": "Partly encrypted", "enc.encrypting": "Encrypting", "enc.suspended": "Suspended", "enc.none": "Not encrypted",
		"av.ok": "Active, up to date", "av.outdated": "Out of date", "av.disabled": "Disabled", "av.none": "None", "fw.on": "On", "fw.off": "Off",
		"fw.partial": "Partly on", "unknown": "Unknown", "ok": "OK", "problems": "%d problem(s)",
	},
	"nl": {
		"s.device": "Apparaat", "s.hardware": "Hardware", "s.status": "Updates en beveiliging", "s.software": "Catalogus- en third-party software",
		"s.alerts": "Open waarschuwingen", "computer": "Computernaam", "user": "Gebruiker", "email": "E-mail", "time": "Gemeld op",
		"localIp": "IP-adressen", "publicIp": "Publiek IP", "os": "Besturingssysteem", "group": "FleetWarden-groep", "domain": "Domein",
		"model": "Model", "serial": "Serienummer", "cpu": "CPU", "ram": "Geheugen", "disk": "Schijf %s", "diskValue": "%s vrij van %s (%d%%)",
		"uptime": "Aan sinds", "uptimeValue": "%s (%s)", "reboot": "Herstart nodig", "yes": "Ja", "no": "Nee", "updates": "Wachtende updates",
		"noUpdates": "Geen", "lastScan": "Laatste updatescan", "never": "Nooit", "programs": "Geïnstalleerde programma's", "upToDate": "actueel",
		"available": "%s beschikbaar", "failed": "mislukt", "installing": "bezig", "encryption": "Schijfversleuteling", "antivirus": "Antivirus",
		"firewall": "Firewall", "threats": "Actieve dreigingen", "noAlerts": "Geen", "noSoftware": "Geen uitrol uit de catalogus of verouderde programma's",
		"link.device": "Openen in FleetWarden", "link.remote": "Overnemen", "link.terminal": "Terminal", "link.scan": "Scan starten",
		"days": "%d dagen %d uur", "hours": "%d uur", "summary.reboot": "Herstart nodig", "summary.updates": "Wachtende updates",
		"summary.software": "Softwareproblemen", "summary.security": "Beveiliging",
		"enc.encrypted": "Versleuteld", "enc.partial": "Deels versleuteld", "enc.encrypting": "Bezig met versleutelen", "enc.suspended": "Onderbroken", "enc.none": "Niet versleuteld",
		"av.ok": "Actief, actueel", "av.outdated": "Verouderd", "av.disabled": "Uitgeschakeld", "av.none": "Geen", "fw.on": "Aan", "fw.off": "Uit",
		"fw.partial": "Deels aan", "unknown": "Onbekend", "ok": "OK", "problems": "%d probleem/problemen",
	},
}

func rt(lang, key string, args ...any) string {
	t, ok := reportTexts[lang][key]
	if !ok {
		t = reportTexts["en"][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(t, args...)
	}
	return t
}

func gib(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.0f GB", float64(b)/(1<<30))
	default:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	}
}

func fmtTime(t time.Time, lang string) string {
	if lang == "nl" {
		return t.Local().Format("02-01-2006 15:04")
	}
	return t.Local().Format("2006-01-02 15:04")
}

// ticketInput is what the user sent.
type ticketInput struct {
	User, Email, Subject, Message, Contact, Urgency, Category string
	At                                                        time.Time
}

// buildTicketReport collects the device report for a help request. helpID is 0
// for a preview.
func (s *Server) buildTicketReport(ctx context.Context, dev store.Device, set selfServiceSettings, in ticketInput, helpID int64, lang string) ticketReport {
	rep := ticketReport{Hostname: dev.Hostname, Summary: []reportLine{}, Sections: []reportSection{}, Links: []reportLink{}}
	has := func(p string) bool { return slices.Contains(set.Report, p) }
	st, err := s.st.DeviceTicketState(ctx, dev.ID)
	if err != nil {
		s.log.Warn("ticket report state", "device", dev.ID, "err", err)
	}

	device := reportSection{Key: "device", Title: rt(lang, "s.device"), Lines: []reportLine{{Label: rt(lang, "computer"), Value: dev.Hostname}}}
	if has("user") {
		user := in.User
		if user == "" {
			user = dev.LoggedInUser
		}
		device.Lines = append(device.Lines, reportLine{Label: rt(lang, "user"), Value: orDash(user)})
		if in.Email != "" {
			device.Lines = append(device.Lines, reportLine{Label: rt(lang, "email"), Value: in.Email})
		}
	}
	if has("time") {
		device.Lines = append(device.Lines, reportLine{Label: rt(lang, "time"), Value: fmtTime(in.At, lang)})
	}
	if has("network") {
		device.Lines = append(device.Lines, reportLine{Label: rt(lang, "localIp"), Value: orDash(strings.Join(dev.LocalIPs, ", "))},
			reportLine{Label: rt(lang, "publicIp"), Value: orDash(dev.RemoteIP)})
	}
	if has("os") {
		osv := strings.TrimSpace(strings.Join([]string{dev.OSName, dev.OSVersion}, " "))
		if dev.OSBuild != "" {
			osv += " (" + dev.OSBuild + ")"
		}
		device.Lines = append(device.Lines, reportLine{Label: rt(lang, "os"), Value: orDash(osv)},
			reportLine{Label: rt(lang, "group"), Value: dev.GroupName})
		if dev.Domain != "" {
			device.Lines = append(device.Lines, reportLine{Label: rt(lang, "domain"), Value: dev.Domain})
		}
	}
	if has("uptime") && dev.BootTime != nil {
		up := in.At.Sub(*dev.BootTime)
		d, h := int(up.Hours())/24, int(up.Hours())%24
		dur := rt(lang, "hours", int(up.Hours()))
		if d > 0 {
			dur = rt(lang, "days", d, h)
		}
		device.Lines = append(device.Lines, reportLine{Label: rt(lang, "uptime"), Value: rt(lang, "uptimeValue", fmtTime(*dev.BootTime, lang), dur)})
	}
	rep.Sections = append(rep.Sections, device)

	if has("hardware") || has("disk") {
		hw := reportSection{Key: "hardware", Title: rt(lang, "s.hardware")}
		if has("hardware") {
			model := strings.TrimSpace(dev.Manufacturer + " " + dev.Model)
			cpu := dev.CPU
			if dev.CPUCores > 0 {
				cpu += fmt.Sprintf(" (%d cores)", dev.CPUCores)
			}
			hw.Lines = append(hw.Lines, reportLine{Label: rt(lang, "model"), Value: orDash(model)}, reportLine{Label: rt(lang, "serial"), Value: orDash(dev.Serial)},
				reportLine{Label: rt(lang, "cpu"), Value: orDash(cpu)})
			if dev.MemoryBytes > 0 {
				hw.Lines = append(hw.Lines, reportLine{Label: rt(lang, "ram"), Value: gib(dev.MemoryBytes)})
			}
		}
		if has("disk") {
			for _, v := range st.Volumes {
				if v.SizeBytes < minVolumeBytes {
					continue
				}
				p := freePercent(v)
				tone := ""
				if p < 10 {
					tone = "danger"
				} else if p < 20 {
					tone = "warning"
				}
				hw.Lines = append(hw.Lines, reportLine{Label: rt(lang, "disk", v.Mount), Value: rt(lang, "diskValue", gib(v.FreeBytes), gib(v.SizeBytes), p), Tone: tone})
			}
		}
		if len(hw.Lines) > 0 {
			rep.Sections = append(rep.Sections, hw)
		}
	}

	status := reportSection{Key: "status", Title: rt(lang, "s.status")}
	if has("reboot") {
		v, tone := rt(lang, "no"), "success"
		if st.RebootPending {
			v, tone = rt(lang, "yes"), "warning"
		}
		status.Lines = append(status.Lines, reportLine{Label: rt(lang, "reboot"), Value: v, Tone: tone})
		rep.Summary = append(rep.Summary, reportLine{Label: rt(lang, "summary.reboot"), Value: v, Tone: tone})
	}
	if has("updates") {
		rows, err := s.st.DevicePatches(ctx, dev.ID)
		if err != nil {
			s.log.Warn("ticket report updates", "device", dev.ID, "err", err)
		}
		var names []string
		for _, r := range rows {
			if r.Decision == "declined" {
				continue
			}
			n := r.KB
			if n == "" {
				n = r.Title
				if r.NewVersion != "" {
					n += " " + r.NewVersion
				}
			}
			names = append(names, n)
		}
		v, tone := rt(lang, "noUpdates"), "success"
		if len(names) > 0 {
			shown := names
			if len(shown) > maxReportUpdates {
				shown = shown[:maxReportUpdates]
			}
			v, tone = fmt.Sprintf("%d: %s", len(names), strings.Join(shown, ", ")), "warning"
			if len(names) > maxReportUpdates {
				v += ", …"
			}
		}
		status.Lines = append(status.Lines, reportLine{Label: rt(lang, "updates"), Value: v, Tone: tone})
		scan := rt(lang, "never")
		if st.PatchScanAt != nil {
			scan = fmtTime(*st.PatchScanAt, lang)
		}
		status.Lines = append(status.Lines, reportLine{Label: rt(lang, "lastScan"), Value: scan})
		rep.Summary = append(rep.Summary, reportLine{Label: rt(lang, "summary.updates"), Value: strconv.Itoa(len(names)), Tone: tone})
	}
	if has("security") {
		if sec, err := s.st.DeviceSecurity(ctx, dev.ID); err == nil {
			sm := sec.Summary
			bad := 0
			line := func(label, key, val string, good ...string) {
				tone := "warning"
				if slices.Contains(good, val) {
					tone = "success"
				} else if val == "unknown" || val == "" {
					tone = ""
				} else {
					bad++
				}
				text := rt(lang, key+"."+val)
				if text == "" || reportTexts["en"][key+"."+val] == "" {
					text = rt(lang, "unknown")
				}
				status.Lines = append(status.Lines, reportLine{Label: label, Value: text, Tone: tone})
			}
			line(rt(lang, "encryption"), "enc", sm.Encryption, "encrypted")
			line(rt(lang, "antivirus"), "av", sm.Antivirus, "ok")
			line(rt(lang, "firewall"), "fw", sm.Firewall, "on")
			if sm.Threats > 0 {
				bad++
				status.Lines = append(status.Lines, reportLine{Label: rt(lang, "threats"), Value: strconv.Itoa(sm.Threats), Tone: "danger"})
			}
			v, tone := rt(lang, "ok"), "success"
			if bad > 0 {
				v, tone = rt(lang, "problems", bad), "danger"
			}
			rep.Summary = append(rep.Summary, reportLine{Label: rt(lang, "summary.security"), Value: v, Tone: tone})
		}
	}
	if len(status.Lines) > 0 {
		rep.Sections = append(rep.Sections, status)
	}

	if has("software") {
		sw := reportSection{Key: "software", Title: rt(lang, "s.software")}
		problems := 0
		jobs, err := s.st.ListSoftwareJobs(ctx, store.JobQuery{Scope: store.Scope{All: true}, DeviceID: dev.ID, Limit: 200})
		if err != nil {
			s.log.Warn("ticket report software", "device", dev.ID, "err", err)
		}
		seen := map[string]bool{}
		for _, j := range jobs { // newest first: the last job per deployment counts
			if seen[j.DeploymentName] || j.Action == "uninstall" {
				continue
			}
			seen[j.DeploymentName] = true
			v, tone := j.Version, "success"
			switch j.State {
			case "succeeded":
				v = strings.TrimSpace(v + " " + rt(lang, "upToDate"))
			case "failed", "expired", "cancelled":
				v, tone = strings.TrimSpace(v+" "+rt(lang, "failed")), "danger"
				if j.Message != "" {
					v += ": " + cleanText(j.Message, 120)
				}
				problems++
			default:
				v, tone = strings.TrimSpace(v+" "+rt(lang, "installing")), ""
			}
			sw.Lines = append(sw.Lines, reportLine{Label: j.DeploymentName, Value: v, Tone: tone})
		}
		if dev.Platform == "windows" {
			out, _, err := s.outdatedSoftware(ctx, []store.ReportDevice{{ID: dev.ID, Platform: dev.Platform}}, nil)
			if err != nil {
				s.log.Warn("ticket report outdated", "device", dev.ID, "err", err)
			}
			for _, p := range out[dev.ID] {
				sw.Lines = append(sw.Lines, reportLine{Label: p.Name, Value: p.Version + ", " + rt(lang, "available", p.Latest), Tone: "warning"})
				problems++
			}
		}
		if rows, err := s.st.DeviceSoftware(ctx, dev.ID); err == nil && len(rows) > 0 {
			sw.Lines = append(sw.Lines, reportLine{Label: rt(lang, "programs"), Value: strconv.Itoa(len(rows))})
		}
		if len(sw.Lines) == 0 {
			sw.Lines = append(sw.Lines, reportLine{Label: rt(lang, "s.software"), Value: rt(lang, "noSoftware")})
		}
		tone := "success"
		if problems > 0 {
			tone = "danger"
		}
		rep.Summary = append(rep.Summary, reportLine{Label: rt(lang, "summary.software"), Value: strconv.Itoa(problems), Tone: tone})
		rep.Sections = append(rep.Sections, sw)
	}

	if has("alerts") {
		al := reportSection{Key: "alerts", Title: rt(lang, "s.alerts")}
		list, err := s.st.ListAlerts(ctx, store.Scope{All: true}, store.AlertFilter{Open: true, DeviceID: dev.ID, Limit: 20})
		if err != nil {
			s.log.Warn("ticket report alerts", "device", dev.ID, "err", err)
		}
		for _, a := range list {
			v := fmtTime(a.OpenedAt, lang)
			if a.Subject != "" {
				v = a.Subject + ", " + v
			}
			al.Lines = append(al.Lines, reportLine{Label: a.RuleName, Value: v, Tone: "warning"})
		}
		if len(al.Lines) == 0 {
			al.Lines = append(al.Lines, reportLine{Label: rt(lang, "s.alerts"), Value: rt(lang, "noAlerts"), Tone: "success"})
		}
		rep.Sections = append(rep.Sections, al)
	}

	if set.LinkDevice {
		if base := s.publicURL(ctx); base != "" {
			q := url.Values{}
			if helpID > 0 {
				q.Set("ticket", strconv.FormatInt(helpID, 10))
			}
			dl := base + "/devices/" + url.PathEscape(dev.ID)
			with := func(action string) string {
				v := url.Values{}
				for k, x := range q {
					v[k] = x
				}
				if action != "" {
					v.Set("action", action)
				}
				if len(v) == 0 {
					return dl
				}
				return dl + "?" + v.Encode()
			}
			rep.Links = append(rep.Links, reportLink{Action: "device", Label: rt(lang, "link.device"), URL: with("")})
			for _, a := range set.LinkActions {
				rep.Links = append(rep.Links, reportLink{Action: a, Label: rt(lang, "link."+a), URL: with(a)})
			}
		}
	}
	return rep
}

// text renders the report as plain text.
func (r ticketReport) text() string {
	var b strings.Builder
	for _, sec := range r.Sections {
		b.WriteString(sec.Title + "\n")
		for _, l := range sec.Lines {
			b.WriteString("  " + l.Label + ": " + l.Value + "\n")
		}
		b.WriteString("\n")
	}
	for _, l := range r.Links {
		b.WriteString(l.Label + ": " + l.URL + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

var toneColor = map[string]string{"success": "#12a150", "warning": "#b26b00", "danger": "#dc3545"}

// html renders the report for a ticket or an e-mail: simple tables with
// inline styles, which every service desk keeps.
func (r ticketReport) html() string {
	var b strings.Builder
	esc := html.EscapeString
	if len(r.Links) > 0 {
		b.WriteString(`<p>`)
		for i, l := range r.Links {
			if i > 0 {
				b.WriteString(" &nbsp;|&nbsp; ")
			}
			fmt.Fprintf(&b, `<a href="%s"><strong>%s</strong></a>`, esc(l.URL), esc(l.Label))
		}
		b.WriteString(`</p>`)
	}
	if len(r.Summary) > 0 {
		b.WriteString(`<p>`)
		for i, l := range r.Summary {
			if i > 0 {
				b.WriteString(" &nbsp;·&nbsp; ")
			}
			fmt.Fprintf(&b, `%s: <strong style="color:%s">%s</strong>`, esc(l.Label), toneColor[l.Tone], esc(l.Value))
		}
		b.WriteString(`</p>`)
	}
	for _, sec := range r.Sections {
		fmt.Fprintf(&b, `<h3 style="margin:16px 0 6px;font-size:14px">%s</h3><table style="border-collapse:collapse;font-size:13px">`, esc(sec.Title))
		for _, l := range sec.Lines {
			val := esc(l.Value)
			if c := toneColor[l.Tone]; c != "" && l.Tone != "success" {
				val = fmt.Sprintf(`<span style="color:%s">%s</span>`, c, val)
			}
			fmt.Fprintf(&b, `<tr><td style="padding:3px 16px 3px 0;color:#5b6578;vertical-align:top">%s</td><td style="padding:3px 0">%s</td></tr>`, esc(l.Label), val)
		}
		b.WriteString(`</table>`)
	}
	return b.String()
}

// ticketBody is the full ticket: the user's text, then the report.
func ticketBody(in ticketInput, rep ticketReport, lang string) (text, htm string) {
	var tb strings.Builder
	if in.Message != "" {
		tb.WriteString(in.Message + "\n\n")
	}
	if in.Contact != "" {
		tb.WriteString(ssText(lang, "mail.contact") + ": " + in.Contact + "\n\n")
	}
	tb.WriteString(rep.text())
	var hb strings.Builder
	if in.Message != "" {
		hb.WriteString("<p>" + htmlText(in.Message) + "</p>")
	}
	if in.Contact != "" {
		hb.WriteString("<p>" + html.EscapeString(ssText(lang, "mail.contact")+": "+in.Contact) + "</p>")
	}
	hb.WriteString(rep.html())
	return tb.String(), hb.String()
}

func reportVars(dev store.Device, in ticketInput, rep ticketReport, text, htm string) map[string]string {
	raw, _ := json.Marshal(rep)
	link := ""
	if len(rep.Links) > 0 {
		link = rep.Links[0].URL
	}
	ip := ""
	if len(dev.LocalIPs) > 0 {
		ip = dev.LocalIPs[0]
	}
	return map[string]string{"event": "ticket", "time": in.At.UTC().Format(time.RFC3339),
		"device.id": dev.ID, "device.hostname": dev.Hostname, "device.url": link, "device.ip": ip,
		"device.os": strings.TrimSpace(dev.OSName + " " + dev.OSVersion), "device.serial": dev.Serial,
		"device.model": strings.TrimSpace(dev.Manufacturer + " " + dev.Model), "device.group": dev.GroupName,
		"user.name": in.User, "user.email": in.Email, "user.displayName": in.User,
		"ticket.subject": in.Subject, "ticket.description": in.Message, "ticket.urgency": in.Urgency, "ticket.category": in.Category,
		"report.text": text, "report.html": htm, "report.json": string(raw)}
}

// ticketForm describes the form for the tray of a device.
func (s *Server) ticketForm(ctx context.Context, dev store.Device, set selfServiceSettings, user, lang string) *agentproto.TicketForm {
	f := &agentproto.TicketForm{RequireMessage: set.RequireMessage, MinMessage: set.MinMessage, AskUrgency: set.AskUrgency,
		AskCategory: set.AskCategory, Categories: set.Categories, AskContact: set.AskContact, Destination: set.Destination}
	switch set.Destination {
	case agentproto.TicketIntegration:
		if i, err := s.st.IntegrationByID(ctx, set.TicketIntegration); err == nil {
			if info, ok := integrations.Info(integrations.Kind(i.Kind)); ok && info.ServiceDesk {
				f.DestinationName = info.Name
			} else {
				f.DestinationName = i.Name
			}
		}
	case agentproto.TicketMail, agentproto.TicketOutlook:
		f.DestinationName = set.TicketAddress
	}
	if set.ShowSent {
		rep := s.buildTicketReport(ctx, dev, selfServiceSettings{Report: set.Report}, ticketInput{User: user, At: s.now()}, 0, lang)
		for _, sec := range rep.Sections {
			if sec.Key == "software" || sec.Key == "alerts" {
				continue // long lists; the summary below says enough
			}
			for _, l := range sec.Lines {
				f.Sent = append(f.Sent, agentproto.InfoLine{Label: l.Label, Value: l.Value})
			}
		}
		for _, l := range rep.Summary {
			if strings.HasPrefix(l.Label, rt(lang, "summary.software")) {
				f.Sent = append(f.Sent, agentproto.InfoLine{Label: l.Label, Value: l.Value})
			}
		}
	}
	return f
}

// validTicket checks the user's form against the settings; it returns the
// cleaned input or false.
func validTicket(set selfServiceSettings, h *agentproto.HelpRequest) (subject, message, contact, urgency, category, email string, ok bool) {
	subject = cleanText(h.Subject, 200)
	message = cleanOutput(h.Message, 4000)
	contact = cleanText(h.Contact, 200)
	if subject == "" {
		return
	}
	if set.RequireMessage && len([]rune(strings.TrimSpace(message))) < max(set.MinMessage, 1) {
		return
	}
	switch h.Urgency {
	case "", "low", "normal", "high":
		urgency = h.Urgency
	default:
		return
	}
	if urgency == "" {
		urgency = "normal"
	}
	category = cleanText(h.Category, 60)
	if category != "" && !slices.Contains(set.Categories, category) {
		return
	}
	if !set.AskContact {
		contact = ""
	}
	if e := cleanText(h.Email, 200); mail.ValidAddress(e) {
		email = e
	}
	return subject, message, contact, urgency, category, email, true
}

// sendTicket sends a stored help request to its destination. It returns an
// Outlook draft for the tray when the destination is Outlook.
func (s *Server) sendTicket(ctx context.Context, set selfServiceSettings, dev store.Device, id int64, in ticketInput, rep ticketReport) *agentproto.OutlookDraft {
	ml := set.Language
	text, htm := ticketBody(in, rep, ml)
	subject := "[" + dev.Hostname + "] " + in.Subject
	switch set.Destination {
	case agentproto.TicketOutlook:
		return &agentproto.OutlookDraft{To: []string{set.TicketAddress}, Subject: subject, Text: text, HTML: htm,
			AttachmentName: dev.Hostname + "-" + in.At.Format("20060102-1504") + ".html", Attachment: reportDocument(subject, htm)}
	case agentproto.TicketMail:
		s.mailTicket(ctx, set.TicketAddress, subject, text, htm)
	case agentproto.TicketIntegration:
		i, err := s.st.IntegrationByID(ctx, set.TicketIntegration)
		if err != nil || !i.Enabled {
			msg := "integration not found or disabled"
			_ = s.st.SetHelpTicket(ctx, id, "", "", "", msg)
			if set.FallbackMail {
				s.fallbackTicketMail(ctx, set, subject, text, htm)
			}
			return nil
		}
		t := integrations.Ticket{Subject: in.Subject, Text: text, HTML: htm, RequesterEmail: in.Email, RequesterName: in.User,
			Urgency: in.Urgency, Category: in.Category, Hostname: dev.Hostname, Vars: reportVars(dev, in, rep, text, htm)}
		if len(rep.Links) > 0 {
			t.DeviceURL = rep.Links[0].URL
		}
		s.runIntegrationDone(i, "ticket", func(ctx context.Context, c integrations.Connector) (integrations.Ref, error) {
			return c.CreateTicket(ctx, t)
		}, func(ref integrations.Ref, err error) {
			bg := context.WithoutCancel(s.bgCtx)
			if err != nil {
				_ = s.st.SetHelpTicket(bg, id, "", "", "", callErrText(err))
				if set.FallbackMail {
					s.fallbackTicketMail(bg, set, subject, text, htm)
				}
				return
			}
			if ref.Number == "" {
				ref.Number = ref.ID
			}
			if err := s.st.SetHelpTicket(bg, id, ref.ID, ref.Number, ref.URL, ""); err != nil {
				s.log.Warn("help ticket", "err", err)
			}
		})
	}
	return nil
}

func (s *Server) fallbackTicketMail(ctx context.Context, set selfServiceSettings, subject, text, htm string) {
	to := []string{}
	if set.TicketAddress != "" {
		to = append(to, set.TicketAddress)
	} else {
		to = append(to, set.Recipients...)
	}
	for _, a := range to {
		s.mailTicket(ctx, a, subject, text, htm)
	}
}

// mailTicket queues the ticket as an e-mail through the mail server.
func (s *Server) mailTicket(ctx context.Context, to, subject, text, htm string) {
	if _, err := s.st.EnqueueMail(ctx, store.Mail{Kind: "ticket", To: to, Subject: subject, BodyText: text,
		BodyHTML: reportDocument(subject, htm)}); err != nil {
		s.log.Warn("ticket mail", "err", err)
	}
}

// reportDocument wraps the report in a complete HTML document.
func reportDocument(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + html.EscapeString(title) +
		`</title></head><body style="font-family:Segoe UI,Arial,sans-serif;color:#0f1729">` + body + `</body></html>`
}

// ---- reading the ticket state back ----

// ticketSyncTick reads the state of open tickets in their service desks.
func (s *Server) ticketSyncTick(ctx context.Context) {
	list, err := s.st.HelpToSync(ctx, s.now().Add(-ticketSyncEvery), ticketSyncBatch)
	if err != nil {
		s.log.Warn("ticket sync", "err", err)
		return
	}
	if len(list) == 0 {
		return
	}
	cache := map[int64]*store.Integration{}
	for _, h := range list {
		iid := *h.IntegrationID
		i, ok := cache[iid]
		if !ok {
			got, err := s.st.IntegrationByID(ctx, iid)
			if err == nil {
				i = &got
			}
			cache[iid] = i
		}
		if i == nil || !i.Enabled || i.Settings["syncStatus"] == "false" || i.Kind == string(integrations.Custom) {
			_ = s.st.MarkHelpSynced(ctx, h.ID)
			continue
		}
		cfg, err := s.integrationConfig(*i)
		if err != nil {
			_ = s.st.MarkHelpSynced(ctx, h.ID)
			continue
		}
		var l callLog
		c, err := integrations.New(cfg, l.options())
		var stt integrations.Status
		if err == nil {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			stt, err = c.Status(cctx, integrations.Ref{ID: h.TicketID, Number: h.TicketNumber})
			cancel()
		}
		if err != nil {
			if !errors.Is(err, integrations.ErrUnsupported) {
				s.saveCalls(ctx, iid, "status", &l, integrations.Ref{}, err)
			}
			_ = s.st.MarkHelpSynced(ctx, h.ID)
			continue
		}
		if err := s.st.SetHelpTicketState(ctx, h.ID, cleanText(stt.State, 60), stt.Closed); err != nil {
			s.log.Warn("ticket state", "err", err)
		}
	}
}

// ticketNote adds a private note to the service desk ticket of a help
// request, when the integration logs notes.
func (s *Server) ticketNote(ctx context.Context, h store.HelpRequest, text string) {
	if h.IntegrationID == nil || h.TicketID == "" {
		return
	}
	i, err := s.st.IntegrationByID(ctx, *h.IntegrationID)
	if err != nil || !i.Enabled || i.Settings["notes"] == "false" || i.Kind == string(integrations.Custom) {
		return
	}
	ref := integrations.Ref{ID: h.TicketID, Number: h.TicketNumber}
	s.runIntegrationDone(i, "note", func(ctx context.Context, c integrations.Connector) (integrations.Ref, error) {
		return integrations.Ref{}, c.AddNote(ctx, ref, text)
	}, nil)
}

// ---- preview ----

type ticketPreviewReq struct {
	Settings selfServiceSettings `json:"settings"`
	DeviceID string              `json:"deviceId"`
}

type ticketPreview struct {
	Hostname    string       `json:"hostname"`
	Subject     string       `json:"subject"`
	Destination string       `json:"destination"`
	Target      string       `json:"target"`
	Report      ticketReport `json:"report"`
	HTML        string       `json:"html"`
	Text        string       `json:"text"`
	Message     string       `json:"message"`
	PublicURL   bool         `json:"publicUrl"`
}

// handleTicketPreview shows the ticket a device would send with the
// settings in the form (not yet saved).
func (s *Server) handleTicketPreview(w http.ResponseWriter, r *http.Request) {
	var req ticketPreviewReq
	if !decode(w, r, &req) {
		return
	}
	set := req.Settings
	if code := s.normalizeTickets(r.Context(), &set); code != "" && code != "invalid_email" && code != "invalid_integration" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	set.Language = langOf(set.Language)
	var dev store.Device
	var err error
	if req.DeviceID != "" {
		dev, err = s.st.DeviceByID(r.Context(), req.DeviceID)
		if err == nil && !s.scope(r).Allows(dev.GroupID) {
			err = store.ErrNotFound
		}
	} else {
		var list []store.Device
		list, _, err = s.st.ListDevices(r.Context(), store.DeviceQuery{Scope: s.scope(r), Limit: 1})
		if err == nil && len(list) == 0 {
			err = store.ErrNotFound
		}
		if err == nil {
			dev, err = s.st.DeviceByID(r.Context(), list[0].ID)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	lang := set.Language
	in := ticketInput{User: orDash(dev.LoggedInUser), Subject: rt(lang, "s.device"), At: s.now(),
		Message: ssText(lang, "previewMessage"), Urgency: "normal"}
	if set.AskCategory && len(set.Categories) > 0 {
		in.Category = set.Categories[0]
	}
	in.Subject = ssText(lang, "previewSubject")
	rep := s.buildTicketReport(r.Context(), dev, set, in, 0, lang)
	text, htm := ticketBody(in, rep, lang)
	out := ticketPreview{Hostname: dev.Hostname, Subject: "[" + dev.Hostname + "] " + in.Subject, Destination: set.Destination,
		Target: set.TicketAddress, Report: rep, HTML: htm, Text: text, Message: in.Message, PublicURL: s.publicURL(r.Context()) != ""}
	if set.Destination == agentproto.TicketIntegration {
		if i, err := s.st.IntegrationByID(r.Context(), set.TicketIntegration); err == nil {
			out.Target = i.Name
			if cfg, err := s.integrationConfig(i); err == nil && i.Kind != string(integrations.Custom) {
				out.Subject = cfg.Subject(integrations.Ticket{Subject: in.Subject, Hostname: dev.Hostname, Vars: reportVars(dev, in, rep, text, htm)})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// sortedKeys is used by tests.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
