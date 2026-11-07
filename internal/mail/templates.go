package mail

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
)

// Brand is the white-label information used in e-mails.
type Brand struct {
	Product string
	Color   string
	URL     string
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var texts = map[string]map[string]string{
	"en": {
		"test.subject":           "{{product}} test e-mail",
		"test.body":              "This is a test e-mail from {{product}}. Your mail server settings work.",
		"update_failed.subject":  "{{product}} update failed",
		"update_failed.body":     "Updating {{product}} to version {{version}} failed and was rolled back: {{detail}}",
		"update_success.subject": "{{product}} updated to {{version}}",
		"update_success.body":    "{{product}} was updated to version {{version}}.",
		"footer":                 "You receive this e-mail because you are an administrator of {{product}}.",
		"open":                   "Open {{product}}",
	},
	"nl": {
		"test.subject":           "Testmail van {{product}}",
		"test.body":              "Dit is een testmail van {{product}}. Je mailserverinstellingen werken.",
		"update_failed.subject":  "Update van {{product}} mislukt",
		"update_failed.body":     "De update van {{product}} naar versie {{version}} is mislukt en teruggedraaid: {{detail}}",
		"update_success.subject": "{{product}} bijgewerkt naar {{version}}",
		"update_success.body":    "{{product}} is bijgewerkt naar versie {{version}}.",
		"footer":                 "Je ontvangt deze e-mail omdat je beheerder bent van {{product}}.",
		"open":                   "{{product}} openen",
	},
}

func tr(lang, key string, vars map[string]string) string {
	t, ok := texts[lang][key]
	if !ok {
		t = texts["en"][key]
	}
	for k, v := range vars {
		t = strings.ReplaceAll(t, "{{"+k+"}}", v)
	}
	return t
}

var layout = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="{{.Lang}}"><body style="margin:0;background:#f6f7f9;font-family:Inter,Segoe UI,Arial,sans-serif;color:#0f1729">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;background:#fff;border-radius:12px;border:1px solid #e3e6eb">
<tr><td style="padding:20px 28px;border-bottom:3px solid {{.Color}};font-size:18px;font-weight:600">{{.Product}}</td></tr>
<tr><td style="padding:28px;font-size:15px;line-height:1.6">{{.Body}}
{{if .URL}}<p style="margin:24px 0 0"><a href="{{.URL}}" style="display:inline-block;background:{{.Color}};color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;font-weight:600">{{.Open}}</a></p>{{end}}
</td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #e3e6eb;font-size:12px;color:#5b6578">{{.Footer}}</td></tr>
</table></td></tr></table></body></html>`))

// Render builds a branded message of the given kind in lang ("en" or "nl").
func Render(kind, lang, to string, brand Brand, vars map[string]string) Message {
	if brand.Product == "" {
		brand.Product = "FleetWarden"
	}
	if !hexColor.MatchString(brand.Color) {
		brand.Color = "#2f5bea"
	}
	v := map[string]string{"product": brand.Product}
	for k, val := range vars {
		v[k] = val
	}
	subject := tr(lang, kind+".subject", v)
	body := tr(lang, kind+".body", v)
	footer := tr(lang, "footer", v)
	var html bytes.Buffer
	_ = layout.Execute(&html, map[string]string{
		"Lang": lang, "Color": brand.Color, "Product": brand.Product, "Body": body,
		"URL": brand.URL, "Open": tr(lang, "open", v), "Footer": footer,
	})
	text := body + "\n\n"
	if brand.URL != "" {
		text += brand.URL + "\n\n"
	}
	text += "-- \n" + footer + "\n"
	return Message{To: to, Subject: subject, Text: text, HTML: html.String()}
}
