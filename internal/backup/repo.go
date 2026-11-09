// Package backup implements FleetWarden's encrypted, deduplicating backups.
//
// A backup location holds a repository:
//
//	fleetwarden-backup.json                  format, KDF salt and the wrapped master key
//	chains/<instance>-<time>/data/<chunk id> encrypted, compressed chunks
//	chains/<instance>-<time>/snapshots/<time>-full.snap|-incr.snap
//
// A chain starts with a full backup, which uploads every chunk. Incremental
// backups in the same chain only upload chunks that are new since the
// earlier snapshots of the chain. Every snapshot manifest lists all chunks it
// needs, so restoring any snapshot only needs that manifest. Retention
// removes whole chains, so no chunk is ever shared between chains.
package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	configName   = "fleetwarden-backup.json"
	formatV1     = "fleetwarden-backup/1"
	timeLayout   = "20060102T150405Z"
	uploadLimit  = 4
	downloadLook = 4
)

// Snapshot kinds.
const (
	KindFull        = "full"
	KindIncremental = "incremental"
)

// ErrNoRepository is returned when a location holds no FleetWarden backups.
var ErrNoRepository = errors.New("no FleetWarden backups at this location")

type repoConfig struct {
	Format  string    `json:"format"`
	Created time.Time `json:"created"`
	KDF     struct {
		Algorithm string `json:"algorithm"`
		Salt      []byte `json:"salt"`
		Time      uint32 `json:"time"`
		Memory    uint32 `json:"memory"`
		Threads   uint8  `json:"threads"`
	} `json:"kdf"`
	Key []byte `json:"key"`
}

// Repo is an open repository.
type Repo struct {
	be     Backend
	cfg    repoConfig
	keys   *keys
	master []byte
}

// OpenRepo opens the repository at a location. With create, a missing
// repository is initialised with the passphrase.
func OpenRepo(ctx context.Context, be Backend, passphrase string, create bool) (*Repo, error) {
	if passphrase == "" {
		return nil, errors.New("no backup passphrase set")
	}
	raw, err := be.Get(ctx, configName)
	if errors.Is(err, ErrNotExist) {
		if !create {
			return nil, ErrNoRepository
		}
		return initRepo(ctx, be, passphrase)
	}
	if err != nil {
		return nil, err
	}
	var cfg repoConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Format != formatV1 || cfg.KDF.Algorithm != "argon2id" {
		return nil, fmt.Errorf("unsupported backup repository format")
	}
	kek := deriveKEK(passphrase, cfg.KDF.Salt, cfg.KDF.Time, cfg.KDF.Memory, cfg.KDF.Threads)
	master, err := unwrapKey(kek, cfg.Key)
	if err != nil {
		return nil, err
	}
	k, err := newKeys(master)
	if err != nil {
		return nil, err
	}
	return &Repo{be: be, cfg: cfg, keys: k, master: master}, nil
}

func newConfig(passphrase string, master []byte) (repoConfig, error) {
	var cfg repoConfig
	cfg.Format, cfg.Created = formatV1, time.Now().UTC()
	cfg.KDF.Algorithm = "argon2id"
	cfg.KDF.Salt = make([]byte, 16)
	if _, err := rand.Read(cfg.KDF.Salt); err != nil {
		return cfg, err
	}
	cfg.KDF.Time, cfg.KDF.Memory, cfg.KDF.Threads = KDFParams.Time, KDFParams.Memory, KDFParams.Threads
	var err error
	cfg.Key, err = wrapKey(deriveKEK(passphrase, cfg.KDF.Salt, cfg.KDF.Time, cfg.KDF.Memory, cfg.KDF.Threads), master)
	return cfg, err
}

func initRepo(ctx context.Context, be Backend, passphrase string) (*Repo, error) {
	master := make([]byte, 64)
	if _, err := rand.Read(master); err != nil {
		return nil, err
	}
	cfg, err := newConfig(passphrase, master)
	if err != nil {
		return nil, err
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := be.Put(ctx, configName, raw); err != nil {
		return nil, err
	}
	k, err := newKeys(master)
	if err != nil {
		return nil, err
	}
	return &Repo{be: be, cfg: cfg, keys: k, master: master}, nil
}

// ChangePassphrase re-wraps the master key with a new passphrase. Existing
// backups stay readable, with the new passphrase only.
func (r *Repo) ChangePassphrase(ctx context.Context, passphrase string) error {
	if passphrase == "" {
		return errors.New("empty passphrase")
	}
	cfg, err := newConfig(passphrase, r.master)
	if err != nil {
		return err
	}
	cfg.Created = r.cfg.Created
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := r.be.Put(ctx, configName, raw); err != nil {
		return err
	}
	r.cfg = cfg
	return nil
}

// Snapshot identifies one backup at a location.
type Snapshot struct {
	Chain    string    `json:"chain"`
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Time     time.Time `json:"time"`
	Instance string    `json:"instance"`
}

// Manifest describes a snapshot. It is stored encrypted.
type Manifest struct {
	Snapshot
	Host          string     `json:"host"`
	Version       string     `json:"version"`
	Size          int64      `json:"size"`
	SHA256        string     `json:"sha256"`
	Chunks        []ChunkRef `json:"chunks"`
	NewChunks     int        `json:"newChunks"`
	UploadedBytes int64      `json:"uploadedBytes"`
}

// ChunkRef is one chunk of the snapshot stream, in order.
type ChunkRef struct {
	ID   string `json:"id"`
	Size int    `json:"size"`
}

var (
	chainRe    = regexp.MustCompile(`^([0-9a-f]{8,32})-(\d{8}T\d{6}Z)$`)
	snapshotRe = regexp.MustCompile(`^(\d{8}T\d{6}Z)-(full|incr)\.snap$`)
	chunkRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// NewChainID names a new chain for an instance.
func NewChainID(instance string, t time.Time) string {
	return instance + "-" + t.UTC().Format(timeLayout)
}

func snapshotFile(kind string, t time.Time) string {
	k := "full"
	if kind == KindIncremental {
		k = "incr"
	}
	return t.UTC().Format(timeLayout) + "-" + k + ".snap"
}

// Chain is a full backup with its incrementals.
type Chain struct {
	ID        string
	Instance  string
	Started   time.Time
	Snapshots []Snapshot // oldest first
}

// Chains lists the chains at the location, oldest first.
func (r *Repo) Chains(ctx context.Context) ([]Chain, error) {
	entries, err := r.be.List(ctx, "chains")
	if err != nil {
		return nil, err
	}
	var out []Chain
	for _, e := range entries {
		m := chainRe.FindStringSubmatch(e.Name)
		if !e.Dir || m == nil {
			continue
		}
		started, _ := time.Parse(timeLayout, m[2])
		c := Chain{ID: e.Name, Instance: m[1], Started: started}
		snaps, err := r.be.List(ctx, "chains/"+e.Name+"/snapshots")
		if err != nil {
			return nil, err
		}
		for _, s := range snaps {
			sm := snapshotRe.FindStringSubmatch(s.Name)
			if s.Dir || sm == nil {
				continue
			}
			t, _ := time.Parse(timeLayout, sm[1])
			kind := KindFull
			if sm[2] == "incr" {
				kind = KindIncremental
			}
			c.Snapshots = append(c.Snapshots, Snapshot{Chain: e.Name, ID: s.Name, Kind: kind, Time: t, Instance: m[1]})
		}
		sort.Slice(c.Snapshots, func(i, j int) bool { return c.Snapshots[i].Time.Before(c.Snapshots[j].Time) })
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out, nil
}

// Valid reports whether a chain starts with a full snapshot and can take incrementals.
func (c Chain) Valid() bool { return len(c.Snapshots) > 0 && c.Snapshots[0].Kind == KindFull }

// Snapshots lists all snapshots, newest first.
func (r *Repo) Snapshots(ctx context.Context) ([]Snapshot, error) {
	chains, err := r.Chains(ctx)
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, c := range chains {
		out = append(out, c.Snapshots...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func validRef(chain, id string) bool {
	return chainRe.MatchString(chain) && snapshotRe.MatchString(id)
}

// Manifest reads and decrypts a snapshot manifest.
func (r *Repo) Manifest(ctx context.Context, chain, id string) (*Manifest, error) {
	if !validRef(chain, id) {
		return nil, fmt.Errorf("invalid snapshot %s/%s", chain, id)
	}
	name := "chains/" + chain + "/snapshots/" + id
	raw, err := r.be.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	plain, err := r.keys.open("snapshot:"+chain+"/"+id, raw)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, ErrCorrupt
	}
	if m.Chain != chain || m.ID != id {
		return nil, ErrCorrupt
	}
	return &m, nil
}

// Progress receives the number of stream bytes processed so far.
type Progress func(done int64)

// Write stores a snapshot of src in chain. For an incremental snapshot only
// chunks that earlier snapshots of the chain do not have are uploaded.
func (r *Repo) Write(ctx context.Context, chain, kind string, at time.Time, src io.Reader, meta Manifest, progress Progress) (*Manifest, error) {
	if !chainRe.MatchString(chain) {
		return nil, fmt.Errorf("invalid chain %q", chain)
	}
	have := map[string]bool{}
	if kind == KindIncremental {
		chains, err := r.Chains(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, c := range chains {
			if c.ID != chain {
				continue
			}
			found = c.Valid()
			for _, s := range c.Snapshots {
				m, err := r.Manifest(ctx, chain, s.ID)
				if err != nil {
					return nil, fmt.Errorf("read snapshot %s: %w", s.ID, err)
				}
				for _, ch := range m.Chunks {
					have[ch.ID] = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("chain %s has no full backup", chain)
		}
	}
	m := meta
	m.Chain, m.Kind, m.Time = chain, kind, at.UTC().Truncate(time.Second)
	m.ID = snapshotFile(kind, m.Time)
	m.Instance = chainRe.FindStringSubmatch(chain)[1]

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(uploadLimit)
	var mu sync.Mutex
	hash := sha256.New()
	ch := newChunker(src)
	for {
		if gctx.Err() != nil {
			break
		}
		data, err := ch.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = g.Wait()
			return nil, err
		}
		hash.Write(data)
		m.Size += int64(len(data))
		id := r.keys.chunkID(data)
		m.Chunks = append(m.Chunks, ChunkRef{ID: id, Size: len(data)})
		if !have[id] {
			have[id] = true
			m.NewChunks++
			plain := append([]byte(nil), data...)
			g.Go(func() error {
				obj, err := r.keys.seal("chunk:"+id, plain)
				if err != nil {
					return err
				}
				if err := r.be.Put(gctx, "chains/"+chain+"/data/"+id, obj); err != nil {
					return err
				}
				mu.Lock()
				m.UploadedBytes += int64(len(obj))
				mu.Unlock()
				return nil
			})
		}
		if progress != nil {
			progress(m.Size)
		}
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.SHA256 = hex.EncodeToString(hash.Sum(nil))
	raw, _ := json.Marshal(m)
	obj, err := r.keys.seal("snapshot:"+chain+"/"+m.ID, raw)
	if err != nil {
		return nil, err
	}
	// The manifest goes last: a snapshot only exists once all its chunks do.
	if err := r.be.Put(ctx, "chains/"+chain+"/snapshots/"+m.ID, obj); err != nil {
		return nil, err
	}
	m.UploadedBytes += int64(len(obj))
	return &m, nil
}

// Read writes the stream of a snapshot to w, verifying every chunk and the
// total checksum.
func (r *Repo) Read(ctx context.Context, m *Manifest, w io.Writer, progress Progress) error {
	type result struct {
		data []byte
		err  error
	}
	// Download a few chunks ahead, write them in order.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slots := make(chan chan result, downloadLook)
	go func() {
		defer close(slots)
		for _, c := range m.Chunks {
			res := make(chan result, 1)
			select {
			case slots <- res:
			case <-ctx.Done():
				return
			}
			go func(c ChunkRef) {
				data, err := r.chunk(ctx, m.Chain, c)
				res <- result{data, err}
			}(c)
		}
	}()
	hash := sha256.New()
	var done int64
	for res := range slots {
		rs := <-res
		if rs.err != nil {
			return rs.err
		}
		hash.Write(rs.data)
		if _, err := w.Write(rs.data); err != nil {
			return err
		}
		done += int64(len(rs.data))
		if progress != nil {
			progress(done)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != m.SHA256 || done != m.Size {
		return ErrCorrupt
	}
	return nil
}

func (r *Repo) chunk(ctx context.Context, chain string, c ChunkRef) ([]byte, error) {
	if !chunkRe.MatchString(c.ID) {
		return nil, ErrCorrupt
	}
	raw, err := r.be.Get(ctx, "chains/"+chain+"/data/"+c.ID)
	if err != nil {
		return nil, fmt.Errorf("chunk %s: %w", c.ID[:12], err)
	}
	data, err := r.keys.open("chunk:"+c.ID, raw)
	if err != nil {
		return nil, err
	}
	if len(data) != c.Size || r.keys.chunkID(data) != c.ID {
		return nil, ErrCorrupt
	}
	return data, nil
}

// DeleteChain removes a chain with all its data.
func (r *Repo) DeleteChain(ctx context.Context, chain string) error {
	if !chainRe.MatchString(chain) {
		return fmt.Errorf("invalid chain %q", chain)
	}
	return r.be.RemoveAll(ctx, "chains/"+chain)
}

// Prune applies retention for one instance: it keeps the newest keep valid
// chains (plus current, the chain in use) and removes older ones, and removes
// chains without a full snapshot that are older than a day (interrupted
// full backups). It returns the removed chains.
func (r *Repo) Prune(ctx context.Context, instance, current string, keep int, now time.Time) ([]string, error) {
	chains, err := r.Chains(ctx)
	if err != nil {
		return nil, err
	}
	var removed []string
	kept := 0
	for i := len(chains) - 1; i >= 0; i-- {
		c := chains[i]
		if c.Instance != instance || c.ID == current {
			if c.ID == current {
				kept++
			}
			continue
		}
		drop := false
		if !c.Valid() {
			drop = now.Sub(c.Started) > 24*time.Hour
		} else if kept >= keep {
			drop = true
		} else {
			kept++
		}
		if drop {
			if err := r.DeleteChain(ctx, c.ID); err != nil {
				return removed, err
			}
			removed = append(removed, c.ID)
		}
	}
	return removed, nil
}

// String describes a snapshot for logs.
func (s Snapshot) String() string { return s.Chain + "/" + strings.TrimSuffix(s.ID, ".snap") }
