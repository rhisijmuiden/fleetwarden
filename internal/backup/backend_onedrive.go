package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// oneDriveBackend stores objects in a user's OneDrive through Microsoft
// Graph with app-only authentication (client credentials). The app
// registration needs the Files.ReadWrite.All application permission.
type oneDriveBackend struct {
	l     Location
	base  string       // .../users/{user}/drive
	http  *http.Client // with the bearer token
	plain *http.Client // for pre-authenticated upload and download URLs
}

const smallUpload = 4 << 20

func openOneDrive(ctx context.Context, l Location, o Options) (Backend, error) {
	graph := strings.TrimRight(o.GraphURL, "/")
	if graph == "" {
		graph = "https://graph.microsoft.com/v1.0"
	}
	tokenURL := o.TokenURL
	if tokenURL == "" {
		tokenURL = "https://login.microsoftonline.com/" + url.PathEscape(l.TenantID) + "/oauth2/v2.0/token"
	}
	scope := "https://graph.microsoft.com/.default"
	cc := clientcredentials.Config{ClientID: l.ClientID, ClientSecret: l.ClientSecret, TokenURL: tokenURL, Scopes: []string{scope}}
	plain := &http.Client{Timeout: 10 * time.Minute}
	hc := cc.Client(context.WithValue(context.Background(), oauth2.HTTPClient, plain))
	hc.Timeout = 10 * time.Minute
	// Downloads redirect to a pre-authenticated URL that must not get the token.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := &oneDriveBackend{l: l, base: graph + "/users/" + url.PathEscape(l.DriveUser) + "/drive", http: hc, plain: plain}
	// Check the credentials and the drive right away for a clear error.
	if _, err := b.call(ctx, http.MethodGet, b.base+"?$select=id", nil, "", nil); err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, fmt.Errorf("OneDrive of %s not found (check the user and that it has a OneDrive license)", l.DriveUser)
		}
		return nil, fmt.Errorf("OneDrive: %w", err)
	}
	return b, nil
}

func (b *oneDriveBackend) itemURL(name, suffix string) string {
	p := join(b.l.Path, name)
	if p == "" {
		return b.base + "/root" + strings.TrimPrefix(suffix, ":")
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return b.base + "/root:/" + strings.Join(segs, "/") + suffix
}

type graphError struct {
	Status int
	Code   string
	Msg    string
}

func (e *graphError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("Microsoft Graph %d %s: %s", e.Status, e.Code, e.Msg)
	}
	return fmt.Sprintf("Microsoft Graph HTTP %d", e.Status)
}

// call performs a request with retries on throttling and server errors.
func (b *oneDriveBackend) call(ctx context.Context, method, u string, body []byte, contentType string, hdr http.Header) ([]byte, error) {
	client := b.http
	if hdr != nil && hdr.Get("X-FW-Plain") != "" {
		client = b.plain
		hdr = hdr.Clone()
		hdr.Del("X-FW-Plain")
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, err
		}
		for k, v := range hdr {
			req.Header[k] = v
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var re *oauth2.RetrieveError
			if errors.As(err, &re) {
				return nil, fmt.Errorf("sign-in to Microsoft Entra failed: %s %s", re.ErrorCode, re.ErrorDescription)
			}
			lastErr = err
			sleepCtx(ctx, time.Duration(attempt+1)*2*time.Second)
			continue
		}
		data, rerr := io.ReadAll(res.Body)
		res.Body.Close()
		if loc := res.Header.Get("Location"); res.StatusCode >= 300 && res.StatusCode < 400 && loc != "" && method == http.MethodGet && client != b.plain {
			u, client, hdr = loc, b.plain, nil
			attempt--
			continue
		}
		if res.StatusCode < 300 {
			return data, rerr
		}
		ge := &graphError{Status: res.StatusCode}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil {
			ge.Code, ge.Msg = env.Error.Code, env.Error.Message
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			lastErr = ge
			wait := time.Duration(attempt+1) * 2 * time.Second
			if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 && s < 300 {
				wait = time.Duration(s) * time.Second
			}
			sleepCtx(ctx, wait)
			continue
		}
		if res.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %v", ErrNotExist, ge)
		}
		return nil, ge
	}
	return nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (b *oneDriveBackend) Put(ctx context.Context, name string, data []byte) error {
	if len(data) <= smallUpload {
		_, err := b.call(ctx, http.MethodPut, b.itemURL(name, ":/content")+"?@microsoft.graph.conflictBehavior=replace", data, "application/octet-stream", nil)
		return err
	}
	req, _ := json.Marshal(map[string]any{"item": map[string]any{"@microsoft.graph.conflictBehavior": "replace"}})
	raw, err := b.call(ctx, http.MethodPost, b.itemURL(name, ":/createUploadSession"), req, "application/json", nil)
	if err != nil {
		return err
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.Unmarshal(raw, &sess); err != nil || sess.UploadURL == "" {
		return errors.New("OneDrive: no upload session")
	}
	// Fragments must be multiples of 320 KiB; objects are at most a few MiB.
	const frag = 320 << 10 * 16
	for off := 0; off < len(data); off += frag {
		end := min(off+frag, len(data))
		h := http.Header{}
		h.Set("X-FW-Plain", "1")
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end-1, len(data)))
		if _, err := b.call(ctx, http.MethodPut, sess.UploadURL, data[off:end], "application/octet-stream", h); err != nil {
			return err
		}
	}
	return nil
}

func (b *oneDriveBackend) Get(ctx context.Context, name string) ([]byte, error) {
	data, err := b.call(ctx, http.MethodGet, b.itemURL(name, ":/content"), nil, "", nil)
	if errors.Is(err, ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotExist)
	}
	return data, err
}

func (b *oneDriveBackend) List(ctx context.Context, dir string) ([]Entry, error) {
	var out []Entry
	next := b.itemURL(dir, ":/children") + "?$select=name,size,folder&$top=999"
	for next != "" {
		raw, err := b.call(ctx, http.MethodGet, next, nil, "", nil)
		if errors.Is(err, ErrNotExist) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		var page struct {
			Value []struct {
				Name   string          `json:"name"`
				Size   int64           `json:"size"`
				Folder json.RawMessage `json:"folder"`
			} `json:"value"`
			Next string `json:"@odata.nextLink"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		for _, v := range page.Value {
			out = append(out, Entry{Name: v.Name, Size: v.Size, Dir: len(v.Folder) > 0 && string(v.Folder) != "null"})
		}
		next = page.Next
	}
	return out, nil
}

func (b *oneDriveBackend) Delete(ctx context.Context, name string) error {
	_, err := b.call(ctx, http.MethodDelete, b.itemURL(name, ":"), nil, "", nil)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	return err
}

func (b *oneDriveBackend) RemoveAll(ctx context.Context, dir string) error {
	if dir == "" {
		return errors.New("refusing to remove the location root")
	}
	return b.Delete(ctx, dir)
}

func (b *oneDriveBackend) Close() error { return nil }
