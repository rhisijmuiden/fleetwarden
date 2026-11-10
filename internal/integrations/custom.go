package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CustomAPI is an integration the administrator defines: FleetWarden calls
// URL with Method when one of the integration's events happens.
type CustomAPI struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Auth is none, bearer, basic, apikey, oauth2 or hmac. The secret parts
	// (token, password, key, client secret, HMAC key) are in Config.Secrets
	// under "secret".
	Auth       string   `json:"auth"`
	Username   string   `json:"username"`   // basic
	HeaderName string   `json:"headerName"` // apikey: the header that holds the key
	TokenURL   string   `json:"tokenUrl"`   // oauth2
	ClientID   string   `json:"clientId"`   // oauth2
	Scope      string   `json:"scope"`      // oauth2
	Headers    []Header `json:"headers"`
	// ContentType is json, form, xml or text.
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
	// NumberPath is where the ticket number is in the JSON answer, like
	// $.ticket.number; NumberURL the same for a link to it.
	NumberPath string `json:"numberPath"`
	URLPath    string `json:"urlPath"`
	TimeoutSec int    `json:"timeoutSec"`
}

// Header is an extra request header; its value may hold variables.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var (
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	authKinds = []string{"none", "bearer", "basic", "apikey", "oauth2", "hmac"}
	ctypes    = map[string]string{"json": "application/json", "form": "application/x-www-form-urlencoded",
		"xml": "application/xml", "text": "text/plain; charset=utf-8"}
	headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)
	// reserved headers are set by FleetWarden.
	reserved = []string{"host", "content-length", "content-type", "authorization", "transfer-encoding", "connection"}
)

// Normalize fills the defaults.
func (a *CustomAPI) Normalize() {
	a.Method = strings.ToUpper(strings.TrimSpace(a.Method))
	if a.Method == "" {
		a.Method = "POST"
	}
	a.URL = strings.TrimSpace(a.URL)
	if a.Auth == "" {
		a.Auth = "none"
	}
	if a.ContentType == "" {
		a.ContentType = "json"
	}
	if a.TimeoutSec <= 0 {
		a.TimeoutSec = 15
	}
	if a.Headers == nil {
		a.Headers = []Header{}
	}
	if a.Auth == "apikey" && strings.TrimSpace(a.HeaderName) == "" {
		a.HeaderName = "X-API-Key"
	}
}

// Validate checks the definition.
func (a *CustomAPI) Validate() error {
	a.Normalize()
	if !slices.Contains(methods, a.Method) {
		return errors.New("method must be GET, POST, PUT, PATCH or DELETE")
	}
	// The URL may hold variables; check it with them filled in.
	if err := checkURL(Render(a.URL, nil, EscapeQuery)); err != nil {
		return fmt.Errorf("url: %w", err)
	}
	if !slices.Contains(authKinds, a.Auth) {
		return errors.New("unknown authentication")
	}
	if _, ok := ctypes[a.ContentType]; !ok {
		return errors.New("content type must be json, form, xml or text")
	}
	if a.TimeoutSec > 120 {
		return errors.New("timeout is at most 120 seconds")
	}
	if len(a.Body) > 64<<10 {
		return errors.New("body template is at most 64 KB")
	}
	if len(a.Headers) > 20 {
		return errors.New("at most 20 headers")
	}
	for _, h := range a.Headers {
		if !headerName.MatchString(h.Name) || slices.Contains(reserved, strings.ToLower(h.Name)) {
			return fmt.Errorf("header %q is not allowed", h.Name)
		}
		if strings.ContainsAny(h.Value, "\r\n") {
			return fmt.Errorf("header %q has a line break", h.Name)
		}
	}
	switch a.Auth {
	case "apikey":
		if !headerName.MatchString(a.HeaderName) {
			return errors.New("API key header name is not valid")
		}
	case "oauth2":
		if err := checkURL(a.TokenURL); err != nil {
			return fmt.Errorf("token url: %w", err)
		}
		if strings.TrimSpace(a.ClientID) == "" {
			return errors.New("client id is required")
		}
	}
	return nil
}

type custom struct {
	c   Config
	a   CustomAPI
	h   *client
	key string
}

func newCustom(c Config, h *client) (*custom, error) {
	a := *c.Custom
	a.Normalize()
	h.http = &http.Client{Timeout: time.Duration(a.TimeoutSec) * time.Second, Transport: h.http.Transport}
	secret := c.Secrets["secret"]
	switch a.Auth {
	case "bearer":
		h.header = map[string]string{"Authorization": "Bearer " + secret}
	case "basic":
		h.basic(a.Username, secret)
	case "apikey":
		h.header = map[string]string{a.HeaderName: secret}
	case "oauth2":
		h.token = &tokenSource{url: a.TokenURL, id: a.ClientID, secret: secret, scope: a.Scope, c: h}
	}
	return &custom{c: c, a: a, h: h, key: secret}, nil
}

// Call makes the call with vars and returns the status and the answer.
func (x *custom) Call(ctx context.Context, vars map[string]string) (int, []byte, error) {
	esc := EscapeNone
	switch x.a.ContentType {
	case "json":
		esc = EscapeJSON
	case "xml":
		esc = EscapeXML
	case "form":
		esc = EscapeQuery
	}
	var body []byte
	if x.a.Method != "GET" && x.a.Method != "DELETE" || strings.TrimSpace(x.a.Body) != "" {
		body = []byte(Render(x.a.Body, vars, esc))
		if x.a.ContentType == "json" && len(strings.TrimSpace(string(body))) > 0 && !json.Valid(body) {
			return 0, nil, errors.New("the body is not valid JSON after filling in the variables")
		}
	}
	h := map[string]string{}
	for _, hd := range x.a.Headers {
		h[hd.Name] = strings.NewReplacer("\r", "", "\n", "").Replace(Render(hd.Value, vars, EscapeNone))
	}
	if x.a.Auth == "hmac" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		m := hmac.New(sha256.New, []byte(x.key))
		m.Write([]byte(ts + "."))
		m.Write(body)
		h["X-FleetWarden-Timestamp"] = ts
		h["X-FleetWarden-Signature"] = "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	return x.h.do(ctx, request{method: x.a.Method, path: Render(x.a.URL, vars, EscapeQuery), body: body, ctype: ctypes[x.a.ContentType], header: h})
}

func (x *custom) Test(ctx context.Context) error {
	_, _, err := x.Call(ctx, SampleVars())
	return err
}

func (x *custom) CreateTicket(ctx context.Context, t Ticket) (Ref, error) {
	vars := map[string]string{"ticket.subject": t.Subject, "ticket.description": t.Text, "ticket.urgency": t.Urgency,
		"ticket.category": t.Category, "user.email": t.RequesterEmail, "user.displayName": t.RequesterName,
		"device.hostname": t.Hostname, "device.url": t.DeviceURL, "report.text": t.Text, "report.html": t.HTML}
	for k, v := range t.Vars {
		vars[k] = v
	}
	_, data, err := x.Call(ctx, vars)
	if err != nil {
		return Ref{}, err
	}
	var ref Ref
	if x.a.NumberPath != "" || x.a.URLPath != "" {
		var doc any
		if json.Unmarshal(data, &doc) == nil {
			ref.Number = str(JSONPath(doc, x.a.NumberPath))
			ref.URL = str(JSONPath(doc, x.a.URLPath))
		}
	}
	ref.ID = ref.Number
	return ref, nil
}

func (x *custom) AddNote(context.Context, Ref, string) error { return ErrUnsupported }

func (x *custom) Status(context.Context, Ref) (Status, error) { return Status{}, ErrUnsupported }

// CallCustom makes a custom API call outside a ticket (for an event).
func CallCustom(ctx context.Context, c Config, o Options, vars map[string]string) (int, []byte, error) {
	conn, err := New(c, o)
	if err != nil {
		return 0, nil, err
	}
	x, ok := conn.(*custom)
	if !ok {
		return 0, nil, ErrUnsupported
	}
	return x.Call(ctx, vars)
}

// JSONPath walks a decoded JSON document with a path like $.a.b[0].c or
// a.b.0.c. It returns nil when the path does not exist.
func JSONPath(doc any, path string) any {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	if path == "" {
		return nil
	}
	path = strings.NewReplacer("[", ".", "]", "").Replace(path)
	cur := doc
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			cur = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

// Escape says how a variable is written into a template.
type Escape int

const (
	EscapeNone  Escape = iota
	EscapeJSON         // inside a JSON string
	EscapeXML          // XML text
	EscapeQuery        // URL query or form value
)

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*(?:\|\s*([a-z]+)\s*)?\}\}`)

// Render fills {{name}} placeholders. The default escaping depends on where
// the template is used; the filters json (a quoted JSON string), raw (no
// escaping), url (query escaping) and html change it.
func Render(tpl string, vars map[string]string, esc Escape) string {
	return placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		p := placeholder.FindStringSubmatch(m)
		v := vars[p[1]]
		switch p[2] {
		case "json":
			b, _ := json.Marshal(v)
			return string(b)
		case "raw":
			return v
		case "url":
			return url.QueryEscape(v)
		case "html":
			return htmlEscape(v)
		}
		switch esc {
		case EscapeJSON:
			b, _ := json.Marshal(v)
			return string(b[1 : len(b)-1])
		case EscapeXML:
			return htmlEscape(v)
		case EscapeQuery:
			return url.QueryEscape(v)
		}
		return v
	})
}

// Variables lists the placeholders a template can use, for the interface.
var Variables = []string{
	"event", "time",
	"device.id", "device.hostname", "device.url", "device.ip", "device.os", "device.serial", "device.model", "device.group",
	"user.name", "user.email", "user.displayName",
	"ticket.subject", "ticket.description", "ticket.urgency", "ticket.category",
	"report.text", "report.html", "report.json",
	"alert.title", "alert.rule", "alert.kind", "alert.subject",
}

// SampleVars are the values used for a test call.
func SampleVars() map[string]string {
	return map[string]string{
		"event": "test", "time": time.Now().UTC().Format(time.RFC3339),
		"device.id": "00000000-0000-0000-0000-000000000000", "device.hostname": "TEST-PC", "device.url": "https://fleetwarden.example/devices/test",
		"device.ip": "192.0.2.10", "device.os": "Windows 11 Pro", "device.serial": "TEST123", "device.model": "Test model", "device.group": "Test",
		"user.name": "test", "user.email": "test@example.com", "user.displayName": "Test User",
		"ticket.subject": "FleetWarden test", "ticket.description": "This is a test from FleetWarden.", "ticket.urgency": "normal", "ticket.category": "",
		"report.text": "This is a test from FleetWarden.", "report.html": "<p>This is a test from FleetWarden.</p>", "report.json": "{}",
		"alert.title": "", "alert.rule": "", "alert.kind": "", "alert.subject": "",
	}
}
