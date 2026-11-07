// Package catalog reads public software catalogs for deployment.
//
// Winget: the server downloads the community source index
// (cdn.winget.microsoft.com/cache/source2.msix, a few MB, refreshed daily)
// and searches it locally. For a chosen package it fetches the version list
// and the merged manifest, checks the manifest's SHA-256 against the
// version list and picks the installer for the device: the vendor's
// download URL with its SHA-256 and unattended switches. Endpoints do not
// need winget; the agent downloads the installer and checks the hash.
package catalog

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// DefaultWingetURL is the community source.
const DefaultWingetURL = "https://cdn.winget.microsoft.com/cache"

const (
	indexFile    = "winget-index.db"
	maxIndexSize = 256 << 20
	maxDocSize   = 8 << 20
	refreshEvery = 24 * time.Hour
	docCacheTTL  = time.Hour
)

// ErrNotLoaded means the index was not downloaded yet.
var ErrNotLoaded = errors.New("the winget catalog has not been downloaded yet")

// ErrNotFound means the package or version is not in the catalog.
var ErrNotFound = errors.New("not found in the winget catalog")

// Winget is the winget community catalog.
type Winget struct {
	Dir     string // where the index is kept
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time

	mu        sync.Mutex
	db        *sql.DB
	updatedAt time.Time
	checkedAt time.Time
	lastErr   string
	count     int
	docs      map[string]cachedDoc
	refreshMu sync.Mutex
}

type cachedDoc struct {
	raw []byte
	at  time.Time
}

// Package is one catalog entry.
type Package struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Moniker string `json:"moniker,omitempty"`
	Version string `json:"version"` // latest
	hash    []byte
}

// Status describes the local copy of the index.
type Status struct {
	Packages  int        `json:"packages"`
	UpdatedAt *time.Time `json:"updatedAt"`
	CheckedAt *time.Time `json:"checkedAt"`
	Error     string     `json:"error"`
}

func (w *Winget) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Winget) base() string {
	if w.BaseURL != "" {
		return strings.TrimRight(w.BaseURL, "/")
	}
	return DefaultWingetURL
}

func (w *Winget) client() *http.Client {
	if w.HTTP != nil {
		return w.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Open loads an index downloaded earlier, if any.
func (w *Winget) Open() error {
	path := filepath.Join(w.Dir, indexFile)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return w.load(path, st.ModTime())
}

func (w *Winget) load(path string, at time.Time) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM packages").Scan(&n); err != nil {
		db.Close()
		return fmt.Errorf("winget index: %w", err)
	}
	w.mu.Lock()
	old := w.db
	w.db, w.updatedAt, w.count = db, at, n
	w.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// Close releases the index.
func (w *Winget) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.db != nil {
		w.db.Close()
		w.db = nil
	}
}

// Status returns the state of the local index.
func (w *Winget) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := Status{Packages: w.count, Error: w.lastErr}
	if !w.updatedAt.IsZero() {
		t := w.updatedAt
		st.UpdatedAt = &t
	}
	if !w.checkedAt.IsZero() {
		t := w.checkedAt
		st.CheckedAt = &t
	}
	return st
}

// Due reports whether the index should be downloaded again.
func (w *Winget) Due() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.db == nil && w.now().Sub(w.checkedAt) > 15*time.Minute || w.now().Sub(w.updatedAt) > refreshEvery && w.now().Sub(w.checkedAt) > time.Hour
}

// Refresh downloads the index and swaps it in.
func (w *Winget) Refresh(ctx context.Context) error {
	if !w.refreshMu.TryLock() {
		return errors.New("the catalog is being downloaded already")
	}
	defer w.refreshMu.Unlock()
	err := w.refresh(ctx)
	w.mu.Lock()
	w.checkedAt = w.now()
	w.lastErr = ""
	if err != nil {
		w.lastErr = err.Error()
	}
	w.mu.Unlock()
	return err
}

func (w *Winget) refresh(ctx context.Context) error {
	if err := os.MkdirAll(w.Dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(w.Dir, "source-*.msix")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base()+"/source2.msix", nil)
	if err != nil {
		return err
	}
	res, err := w.client().Do(req)
	if err != nil {
		return fmt.Errorf("download the winget catalog: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download the winget catalog: %s", res.Status)
	}
	n, err := io.Copy(tmp, io.LimitReader(res.Body, maxIndexSize+1))
	if err != nil {
		return fmt.Errorf("download the winget catalog: %w", err)
	}
	if n > maxIndexSize {
		return errors.New("the winget catalog is unexpectedly large")
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return fmt.Errorf("winget catalog: %w", err)
	}
	var idx *zip.File
	for _, f := range zr.File {
		if f.Name == "Public/index.db" {
			idx = f
		}
	}
	if idx == nil {
		return errors.New("winget catalog: Public/index.db is missing")
	}
	rc, err := idx.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dst := filepath.Join(w.Dir, indexFile)
	next := dst + ".new"
	out, err := os.OpenFile(next, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, maxIndexSize)); err != nil {
		out.Close()
		os.Remove(next)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Check the new index before it replaces the old one.
	probe, err := sql.Open("sqlite", "file:"+filepath.ToSlash(next)+"?mode=ro&immutable=1")
	if err == nil {
		var c int
		err = probe.QueryRow("SELECT count(*) FROM packages").Scan(&c)
		probe.Close()
		if err == nil && c == 0 {
			err = errors.New("no packages")
		}
	}
	if err != nil {
		os.Remove(next)
		return fmt.Errorf("winget catalog: %w", err)
	}
	w.mu.Lock()
	if w.db != nil {
		w.db.Close()
		w.db = nil
	}
	w.mu.Unlock()
	if err := os.Rename(next, dst); err != nil {
		return err
	}
	w.mu.Lock()
	w.docs = nil
	w.mu.Unlock()
	return w.load(dst, w.now())
}

func (w *Winget) handle() (*sql.DB, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.db == nil {
		return nil, ErrNotLoaded
	}
	return w.db, nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Search finds packages by identifier, name, moniker or tag.
func (w *Winget) Search(ctx context.Context, q string, limit int) ([]Package, error) {
	db, err := w.handle()
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Package{}
	if q == "" {
		return out, nil
	}
	lq := strings.ToLower(q)
	rows, err := db.QueryContext(ctx, `SELECT id, name, COALESCE(moniker, ''), latest_version FROM packages
		WHERE id LIKE ?1 ESCAPE '\' OR name LIKE ?1 ESCAPE '\' OR lower(COALESCE(moniker, '')) = ?2
			OR rowid IN (SELECT m.package FROM tags2_map m JOIN tags2 t ON t.rowid = m.tag WHERE lower(t.tag) = ?2)
		ORDER BY (lower(id) = ?2 OR lower(name) = ?2) DESC, lower(COALESCE(moniker, '')) = ?2 DESC,
			(name LIKE ?3 ESCAPE '\' OR id LIKE ?3 ESCAPE '\') DESC,
			length(id) - length(replace(id, '.', '')), length(name), name LIMIT ?4`,
		"%"+likeEscape(q)+"%", lq, likeEscape(q)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Package
		if err := rows.Scan(&p.ID, &p.Name, &p.Moniker, &p.Version); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one package by its identifier.
func (w *Winget) Get(ctx context.Context, id string) (Package, error) {
	db, err := w.handle()
	if err != nil {
		return Package{}, err
	}
	var p Package
	err = db.QueryRowContext(ctx, "SELECT id, name, COALESCE(moniker, ''), latest_version, hash FROM packages WHERE id = ?1 COLLATE NOCASE", id).
		Scan(&p.ID, &p.Name, &p.Moniker, &p.Version, &p.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Version is one published version of a package.
type Version struct {
	Version string `yaml:"v" json:"version"`
	Path    string `yaml:"rP" json:"-"`
	SHA256  string `yaml:"s256H" json:"-"`
}

// Versions lists the versions of a package, newest first.
func (w *Winget) Versions(ctx context.Context, id string) ([]Version, error) {
	p, err := w.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(p.hash) < 4 {
		return nil, errors.New("winget catalog: package without version data")
	}
	raw, err := w.fetch(ctx, "packages/"+p.ID+"/"+hex.EncodeToString(p.hash[:4])+"/versionData.mszyml", "")
	if err != nil {
		return nil, err
	}
	data, err := DecodeMSZIP(raw)
	if err != nil {
		return nil, fmt.Errorf("winget version data: %w", err)
	}
	var vd struct {
		Versions []Version `yaml:"vD"`
	}
	if err := yaml.Unmarshal(data, &vd); err != nil {
		return nil, fmt.Errorf("winget version data: %w", err)
	}
	sort.SliceStable(vd.Versions, func(i, j int) bool { return CompareVersions(vd.Versions[i].Version, vd.Versions[j].Version) > 0 })
	return vd.Versions, nil
}

// Manifest returns the merged manifest of a version ("" = the newest).
func (w *Winget) Manifest(ctx context.Context, id, version string) (*Manifest, error) {
	vs, err := w.Versions(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNotFound
	}
	v := vs[0]
	if version != "" {
		found := false
		for _, x := range vs {
			if x.Version == version {
				v, found = x, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("version %s of %s: %w", version, id, ErrNotFound)
		}
	}
	if !strings.HasPrefix(v.Path, "manifests/") || strings.Contains(v.Path, "..") {
		return nil, errors.New("winget catalog: unexpected manifest path")
	}
	raw, err := w.fetch(ctx, v.Path, v.SHA256)
	if err != nil {
		return nil, err
	}
	return ParseManifest(raw)
}

// fetch downloads a catalog document (cached for an hour) and checks its
// hash when one is given.
func (w *Winget) fetch(ctx context.Context, rel, sum string) ([]byte, error) {
	key := rel
	w.mu.Lock()
	if d, ok := w.docs[key]; ok && w.now().Sub(d.at) < docCacheTTL {
		w.mu.Unlock()
		return d.raw, nil
	}
	w.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base()+"/"+rel, nil)
	if err != nil {
		return nil, err
	}
	res, err := w.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("winget catalog: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("winget catalog: %s", res.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxDocSize))
	if err != nil {
		return nil, err
	}
	if sum != "" {
		h := sha256.Sum256(raw)
		if !strings.EqualFold(hex.EncodeToString(h[:]), sum) {
			return nil, errors.New("winget catalog: the manifest does not match its hash")
		}
	}
	w.mu.Lock()
	if w.docs == nil || len(w.docs) > 2000 {
		w.docs = map[string]cachedDoc{}
	}
	w.docs[key] = cachedDoc{raw: raw, at: w.now()}
	w.mu.Unlock()
	return raw, nil
}

// DecodeMSZIP decodes a Windows Compression API MSZIP buffer: a 24-byte
// header, then blocks of [uint32 size]["CK" + deflate], each using the
// previous block's output as its dictionary.
func DecodeMSZIP(b []byte) ([]byte, error) {
	if len(b) < 24 || !bytes.Equal(b[:4], []byte{0x0a, 0x51, 0xe5, 0xc0}) {
		return b, nil // not compressed
	}
	total := binary.LittleEndian.Uint64(b[8:16])
	if total > maxDocSize*4 {
		return nil, errors.New("too large")
	}
	out := make([]byte, 0, total)
	var dict []byte
	pos := 24
	for pos < len(b) && uint64(len(out)) < total {
		if pos+4 > len(b) {
			return nil, errors.New("truncated block header")
		}
		n := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
		pos += 4
		if n < 2 || pos+n > len(b) || b[pos] != 'C' || b[pos+1] != 'K' {
			return nil, errors.New("bad MSZIP block")
		}
		r := flate.NewReaderDict(bytes.NewReader(b[pos+2:pos+n]), dict)
		blk, err := io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		if err != nil {
			return nil, err
		}
		pos += n
		out = append(out, blk...)
		dict = blk
		if len(dict) > 32<<10 {
			dict = dict[len(dict)-32<<10:]
		}
	}
	if uint64(len(out)) != total {
		return nil, fmt.Errorf("decoded %d bytes, expected %d", len(out), total)
	}
	return out, nil
}

// CompareVersions compares dotted versions part by part, numbers as numbers.
func CompareVersions(a, b string) int {
	split := func(v string) []string {
		return strings.FieldsFunc(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "v")), func(r rune) bool {
			return r == '.' || r == '-' || r == '_' || r == '+' || r == ' '
		})
	}
	num := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		}
		return s != ""
	}
	pa, pb := split(a), split(b)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := "0", "0"
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		nx, ny := num(x), num(y)
		switch {
		case nx && ny:
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				if len(x) < len(y) {
					return -1
				}
				return 1
			}
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		case nx:
			return 1
		case ny:
			return -1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return 0
}
