// Package integrations connects FleetWarden to service desks and to any
// other system with an HTTP API. A service desk connector creates tickets
// (with the device report), adds private notes and reads the ticket's state
// back; a custom API integration calls a URL the administrator defines, with
// a body template filled in per device.
package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kind names a connector.
type Kind string

const (
	Custom       Kind = "custom"
	TOPdesk      Kind = "topdesk"
	Freshservice Kind = "freshservice"
	JSM          Kind = "jsm"
	ServiceNow   Kind = "servicenow"
	Zendesk      Kind = "zendesk"
	Halo         Kind = "halo"
	Zammad       Kind = "zammad"
	GLPI         Kind = "glpi"
)

// Field describes one setting of a connector, so the web interface can draw
// the form. Labels and hints are translated in the interface by Key.
type Field struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // text, url, email, secret, switch, select
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// KindInfo describes a connector.
type KindInfo struct {
	Kind        Kind    `json:"kind"`
	Name        string  `json:"name"`
	ServiceDesk bool    `json:"serviceDesk"`
	Fields      []Field `json:"fields"`
}

func prio(low, normal, high string) []Field {
	return []Field{
		{Key: "priorityLow", Type: "text", Default: low},
		{Key: "priorityNormal", Type: "text", Default: normal},
		{Key: "priorityHigh", Type: "text", Default: high},
	}
}

func join(parts ...[]Field) []Field {
	var out []Field
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// common are the settings every service desk has.
var common = []Field{
	{Key: "fallbackRequester", Type: "email"},
	{Key: "subject", Type: "text", Default: "[{{device.hostname}}] {{ticket.subject}}"},
	{Key: "syncStatus", Type: "switch", Default: "true"},
	{Key: "notes", Type: "switch", Default: "true"},
}

// Kinds lists the connectors in the order the interface shows them.
var Kinds = []KindInfo{
	{Kind: TOPdesk, Name: "TOPdesk", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.topdesk.net"},
		{Key: "username", Type: "text", Required: true},
		{Key: "password", Type: "secret", Required: true},
		{Key: "operatorGroup", Type: "text"},
		{Key: "category", Type: "text"},
		{Key: "callType", Type: "text"},
		{Key: "entryType", Type: "text"},
	}, prio("", "", ""), common)},
	{Kind: Freshservice, Name: "Freshservice", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.freshservice.com"},
		{Key: "apiKey", Type: "secret", Required: true},
		{Key: "groupId", Type: "text"},
		{Key: "workspaceId", Type: "text"},
		{Key: "category", Type: "text"},
	}, prio("1", "2", "3"), common)},
	{Kind: JSM, Name: "Jira Service Management", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.atlassian.net"},
		{Key: "email", Type: "email", Required: true},
		{Key: "apiToken", Type: "secret", Required: true},
		{Key: "serviceDeskId", Type: "text", Required: true},
		{Key: "requestTypeId", Type: "text", Required: true},
	}, prio("", "", ""), common)},
	{Kind: ServiceNow, Name: "ServiceNow", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.service-now.com"},
		{Key: "username", Type: "text", Required: true},
		{Key: "password", Type: "secret", Required: true},
		{Key: "assignmentGroup", Type: "text"},
		{Key: "category", Type: "text"},
	}, prio("3", "2", "1"), common)},
	{Kind: Zendesk, Name: "Zendesk", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.zendesk.com"},
		{Key: "email", Type: "email", Required: true},
		{Key: "apiToken", Type: "secret", Required: true},
		{Key: "groupId", Type: "text"},
	}, prio("low", "normal", "high"), common)},
	{Kind: Halo, Name: "HaloITSM", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://acme.haloitsm.com"},
		{Key: "authUrl", Type: "url"},
		{Key: "tenant", Type: "text"},
		{Key: "clientId", Type: "text", Required: true},
		{Key: "clientSecret", Type: "secret", Required: true},
		{Key: "ticketTypeId", Type: "text"},
		{Key: "team", Type: "text"},
	}, prio("4", "3", "2"), common)},
	{Kind: Zammad, Name: "Zammad", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://helpdesk.acme.nl"},
		{Key: "token", Type: "secret", Required: true},
		{Key: "group", Type: "text", Required: true, Default: "Users"},
	}, prio("1", "2", "3"), common)},
	{Kind: GLPI, Name: "GLPI", ServiceDesk: true, Fields: join([]Field{
		{Key: "url", Type: "url", Required: true, Placeholder: "https://glpi.acme.nl/apirest.php"},
		{Key: "appToken", Type: "secret"},
		{Key: "userToken", Type: "secret", Required: true},
		{Key: "categoryId", Type: "text"},
	}, prio("2", "3", "4"), common)},
	{Kind: Custom, Name: "Custom API", Fields: []Field{}},
}

// Info returns the description of a kind.
func Info(k Kind) (KindInfo, bool) {
	for _, i := range Kinds {
		if i.Kind == k {
			return i, true
		}
	}
	return KindInfo{}, false
}

// Config is a stored integration with its secrets opened.
type Config struct {
	Kind     Kind
	Settings map[string]string
	Secrets  map[string]string
	Custom   *CustomAPI
}

// Setting returns a setting, or the field's default when it is empty.
func (c Config) Setting(key string) string {
	if v := strings.TrimSpace(c.Settings[key]); v != "" {
		return v
	}
	if i, ok := Info(c.Kind); ok {
		for _, f := range i.Fields {
			if f.Key == key {
				return f.Default
			}
		}
	}
	return ""
}

// On reports whether a switch setting is on.
func (c Config) On(key string) bool { return c.Setting(key) == "true" }

// Validate checks the required settings and the URL.
func (c Config) Validate() error {
	if c.Kind == Custom {
		if c.Custom == nil {
			return errors.New("custom API settings are missing")
		}
		return c.Custom.Validate()
	}
	info, ok := Info(c.Kind)
	if !ok {
		return fmt.Errorf("unknown kind %q", c.Kind)
	}
	for _, f := range info.Fields {
		v := c.Settings[f.Key]
		if f.Type == "secret" {
			v = c.Secrets[f.Key]
		}
		if f.Required && strings.TrimSpace(v) == "" && f.Default == "" {
			return fmt.Errorf("%s is required", f.Key)
		}
		if f.Type == "url" && v != "" {
			if err := checkURL(v); err != nil {
				return fmt.Errorf("%s: %w", f.Key, err)
			}
		}
	}
	return nil
}

func checkURL(v string) error {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("must be an http(s) URL")
	}
	if u.User != nil {
		return errors.New("must not contain a user name or password")
	}
	return nil
}

// Ticket is what FleetWarden sends to a service desk.
type Ticket struct {
	Subject        string
	Text           string // plain-text body
	HTML           string // HTML body; Text is used when empty
	RequesterEmail string
	RequesterName  string
	Urgency        string // low, normal, high
	Category       string
	Hostname       string
	DeviceURL      string
	// Vars fill the subject template and a custom API body.
	Vars map[string]string
}

func (t Ticket) body() string {
	if t.HTML != "" {
		return t.HTML
	}
	return "<pre>" + htmlEscape(t.Text) + "</pre>"
}

// Ref identifies a created ticket.
type Ref struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	URL    string `json:"url"`
}

// Status is the state of a ticket in the service desk.
type Status struct {
	State  string `json:"state"`
	Closed bool   `json:"closed"`
}

// Connector talks to one service desk or API.
type Connector interface {
	// Test checks the address and the credentials.
	Test(ctx context.Context) error
	// CreateTicket creates a ticket (or, for a custom API, makes the call).
	CreateTicket(ctx context.Context, t Ticket) (Ref, error)
	// AddNote adds a private note to a ticket.
	AddNote(ctx context.Context, ref Ref, text string) error
	// Status reads the state of a ticket.
	Status(ctx context.Context, ref Ref) (Status, error)
}

// ErrUnsupported is returned by a connector that cannot do an operation.
var ErrUnsupported = errors.New("not supported by this integration")

// Call is one HTTP request a connector made, for the call log.
type Call struct {
	Method   string
	URL      string
	Status   int
	Duration time.Duration
	Err      error
}

// Options are shared by all connectors.
type Options struct {
	HTTP *http.Client
	// Record is called after every request.
	Record func(Call)
}

// New returns the connector for a configuration.
func New(c Config, o Options) (Connector, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	h := &client{http: o.HTTP, record: o.Record, base: strings.TrimRight(c.Setting("url"), "/")}
	switch c.Kind {
	case TOPdesk:
		return &topdesk{c, h.basic(c.Setting("username"), c.Secrets["password"])}, nil
	case Freshservice:
		return &freshservice{c, h.basic(c.Secrets["apiKey"], "X")}, nil
	case JSM:
		return &jsm{c, h.basic(c.Setting("email"), c.Secrets["apiToken"])}, nil
	case ServiceNow:
		return &servicenow{c, h.basic(c.Setting("username"), c.Secrets["password"])}, nil
	case Zendesk:
		return &zendesk{c, h.basic(c.Setting("email")+"/token", c.Secrets["apiToken"])}, nil
	case Halo:
		authURL := c.Setting("authUrl")
		if authURL == "" {
			authURL = h.base + "/auth"
		}
		tokenURL := strings.TrimRight(authURL, "/") + "/token"
		if t := c.Setting("tenant"); t != "" {
			tokenURL += "?tenant=" + url.QueryEscape(t)
		}
		h.token = &tokenSource{url: tokenURL, id: c.Setting("clientId"), secret: c.Secrets["clientSecret"], scope: "all", c: h}
		return &halo{c, h}, nil
	case Zammad:
		h.header = map[string]string{"Authorization": "Token token=" + c.Secrets["token"]}
		return &zammad{c, h}, nil
	case GLPI:
		return &glpi{c: c, h: h}, nil
	case Custom:
		return newCustom(c, h)
	}
	return nil, fmt.Errorf("unknown kind %q", c.Kind)
}

// Priority maps an urgency to the service desk's value ("" when not set).
func (c Config) Priority(urgency string) string {
	switch urgency {
	case "low":
		return c.Setting("priorityLow")
	case "high":
		return c.Setting("priorityHigh")
	}
	return c.Setting("priorityNormal")
}

// Subject fills the subject template of the integration.
func (c Config) Subject(t Ticket) string {
	tpl := c.Setting("subject")
	if tpl == "" {
		return t.Subject
	}
	vars := map[string]string{"ticket.subject": t.Subject, "device.hostname": t.Hostname}
	for k, v := range t.Vars {
		vars[k] = v
	}
	s := strings.TrimSpace(Render(tpl, vars, EscapeNone))
	if s == "" {
		return t.Subject
	}
	return s
}
