package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxResponse bounds what is read from a service desk's answer.
const maxResponse = 4 << 20

// HTTPError is a response outside 2xx.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	b := strings.TrimSpace(e.Body)
	if len(b) > 300 {
		b = b[:300] + "…"
	}
	if b == "" {
		return fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("HTTP %d %s: %s", e.Status, http.StatusText(e.Status), b)
}

// client is the HTTP helper the connectors share.
type client struct {
	http   *http.Client
	record func(Call)
	base   string
	header map[string]string
	token  *tokenSource
}

func (c *client) basic(user, pass string) *client {
	c.header = map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))}
	return c
}

// request is one call.
type request struct {
	method  string
	path    string // relative to base, or an absolute URL
	body    []byte
	ctype   string
	header  map[string]string
	noToken bool
}

// do sends a request and returns the status and body; a status outside 2xx
// is an *HTTPError.
func (c *client) do(ctx context.Context, r request) (int, []byte, error) {
	u := r.path
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = c.base + r.path
	}
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "FleetWarden")
	if r.body != nil {
		ct := r.ctype
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if !r.noToken {
		for k, v := range c.header {
			req.Header.Set(k, v)
		}
	}
	if c.token != nil && !r.noToken {
		tok, err := c.token.get(ctx)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	call := Call{Method: r.method, URL: redact(u), Duration: time.Since(start)}
	if err != nil {
		call.Err = err
		c.rec(call)
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	call.Status, call.Duration = resp.StatusCode, time.Since(start)
	if err == nil && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		err = &HTTPError{Status: resp.StatusCode, Body: string(data)}
	}
	call.Err = err
	c.rec(call)
	return resp.StatusCode, data, err
}

func (c *client) rec(call Call) {
	if c.record != nil {
		c.record(call)
	}
}

// json sends v as JSON and decodes the answer into out (when not nil).
func (c *client) json(ctx context.Context, method, path string, v, out any) error {
	var body []byte
	if v != nil {
		var err error
		if body, err = json.Marshal(v); err != nil {
			return err
		}
	}
	_, data, err := c.do(ctx, request{method: method, path: path, body: body})
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("unexpected answer: %w", err)
		}
	}
	return nil
}

// redact drops the query string, which may hold a key.
func redact(u string) string {
	if p, err := url.Parse(u); err == nil {
		p.RawQuery, p.User = "", nil
		return p.String()
	}
	return u
}

// tokenSource gets and caches an OAuth 2.0 client-credentials token.
type tokenSource struct {
	url, id, secret, scope string
	c                      *client
	// header adds the client id and secret as Basic authentication instead
	// of in the form.
	basicAuth bool

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (t *tokenSource) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expires) {
		return t.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	if t.scope != "" {
		form.Set("scope", t.scope)
	}
	h := map[string]string{}
	if t.basicAuth {
		h["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(t.id)+":"+url.QueryEscape(t.secret)))
	} else {
		form.Set("client_id", t.id)
		form.Set("client_secret", t.secret)
	}
	// noToken: the token request never carries the integration's own headers.
	_, data, err := t.c.do(ctx, request{method: http.MethodPost, path: t.url, body: []byte(form.Encode()),
		ctype: "application/x-www-form-urlencoded", header: h, noToken: true})
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("get token: no access_token in the answer")
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 300
	}
	t.token = out.AccessToken
	t.expires = time.Now().Add(time.Duration(out.ExpiresIn)*time.Second - 30*time.Second)
	return t.token, nil
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// str turns a JSON value (number or string) into a string.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// num returns v as a JSON number when it is one, else as a string.
func num(v string) any {
	var n json.Number
	if err := json.Unmarshal([]byte(v), &n); err == nil && v != "" {
		return n
	}
	return v
}
