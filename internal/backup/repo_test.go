package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() { KDFParams.Memory, KDFParams.Time, KDFParams.Threads = 1024, 1, 1 }

func randomData(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x5eed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func chunksOf(t *testing.T, data []byte) [][]byte {
	t.Helper()
	c := newChunker(bytes.NewReader(data))
	var out [][]byte
	for {
		ch, err := c.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, append([]byte(nil), ch...))
	}
}

func TestChunkerBoundariesSurviveInsert(t *testing.T) {
	data := randomData(1, 20<<20)
	a := chunksOf(t, data)
	if !bytes.Equal(bytes.Join(a, nil), data) {
		t.Fatal("chunks do not reassemble the input")
	}
	for i, c := range a[:len(a)-1] {
		if len(c) < minChunk || len(c) > maxChunk {
			t.Fatalf("chunk %d has size %d", i, len(c))
		}
	}
	if len(a) < 8 || len(a) > 60 {
		t.Fatalf("unexpected chunk count %d for 20 MiB", len(a))
	}
	// Insert 100 bytes in the middle: most chunks must stay the same.
	mod := append(append(append([]byte(nil), data[:10<<20]...), randomData(2, 100)...), data[10<<20:]...)
	b := chunksOf(t, mod)
	seen := map[string]bool{}
	for _, c := range a {
		seen[string(c)] = true
	}
	same := 0
	for _, c := range b {
		if seen[string(c)] {
			same++
		}
	}
	if same < len(a)-3 {
		t.Fatalf("only %d of %d chunks unchanged after a small insert", same, len(a))
	}
}

func newLocalRepo(t *testing.T, pass string) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	be, err := openLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenRepo(context.Background(), be, pass, true)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func readAll(t *testing.T, r *Repo, s Snapshot) []byte {
	t.Helper()
	m, err := r.Manifest(context.Background(), s.Chain, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Read(context.Background(), m, &buf, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFullAndIncremental(t *testing.T) {
	ctx := context.Background()
	r, dir := newLocalRepo(t, "correct horse battery staple")
	data := randomData(3, 12<<20)
	t0 := time.Date(2026, 11, 7, 2, 0, 0, 0, time.UTC)
	chain := NewChainID("abcdef01", t0)
	full, err := r.Write(ctx, chain, KindFull, t0, bytes.NewReader(data), Manifest{Version: "0.2.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.NewChunks != len(full.Chunks) {
		t.Fatalf("full backup uploaded %d of %d chunks", full.NewChunks, len(full.Chunks))
	}
	// Change a little: an incremental uploads only a few chunks.
	data2 := append(append([]byte(nil), data[:6<<20]...), append(randomData(4, 5000), data[6<<20:]...)...)
	inc, err := r.Write(ctx, chain, KindIncremental, t0.Add(time.Hour), bytes.NewReader(data2), Manifest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inc.NewChunks == 0 || inc.NewChunks > 3 || inc.UploadedBytes > full.UploadedBytes/2 {
		t.Fatalf("incremental uploaded %d chunks / %d bytes (full: %d bytes)", inc.NewChunks, inc.UploadedBytes, full.UploadedBytes)
	}
	snaps, err := r.Snapshots(ctx)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	if snaps[0].Kind != KindIncremental || snaps[1].Kind != KindFull {
		t.Fatalf("order: %+v", snaps)
	}
	if !bytes.Equal(readAll(t, r, snaps[0]), data2) || !bytes.Equal(readAll(t, r, snaps[1]), data) {
		t.Fatal("restored data differs")
	}
	// Stored data is encrypted: the plain bytes never appear.
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if info != nil && !info.IsDir() {
			raw, _ := os.ReadFile(p)
			if bytes.Contains(raw, data[1000:1064]) {
				t.Errorf("%s contains plain data", p)
			}
		}
		return nil
	})

	// Wrong passphrase.
	be, _ := openLocal(dir)
	if _, err := OpenRepo(ctx, be, "wrong", false); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// Change the passphrase and read with the new one.
	if err := r.ChangePassphrase(ctx, "new passphrase 123"); err != nil {
		t.Fatal(err)
	}
	r2, err := OpenRepo(ctx, be, "new passphrase 123", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, r2, snaps[0]), data2) {
		t.Fatal("restore after passphrase change differs")
	}
	if _, err := OpenRepo(ctx, be, "correct horse battery staple", false); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatal("old passphrase still works")
	}

	// Tampering is detected.
	m, _ := r2.Manifest(ctx, snaps[1].Chain, snaps[1].ID)
	p := filepath.Join(dir, "chains", chain, "data", m.Chunks[0].ID)
	raw, _ := os.ReadFile(p)
	raw[len(raw)-1] ^= 1
	os.WriteFile(p, raw, 0o600)
	if err := r2.Read(ctx, m, io.Discard, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered chunk: %v", err)
	}
}

func TestEmptyLocationAndIncrementalWithoutFull(t *testing.T) {
	ctx := context.Background()
	be, _ := openLocal(t.TempDir())
	if _, err := OpenRepo(ctx, be, "x", false); !errors.Is(err, ErrNoRepository) {
		t.Fatalf("got %v", err)
	}
	r, _ := newLocalRepo(t, "pass")
	if _, err := r.Write(ctx, NewChainID("abcdef01", time.Now()), KindIncremental, time.Now(), strings.NewReader("x"), Manifest{}, nil); err == nil {
		t.Fatal("incremental without a full backup succeeded")
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	r, _ := newLocalRepo(t, "pass")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var chains []string
	for i := range 4 {
		at := t0.Add(time.Duration(i) * 24 * time.Hour)
		c := NewChainID("aaaaaaaa", at)
		chains = append(chains, c)
		if _, err := r.Write(ctx, c, KindFull, at, strings.NewReader("data"), Manifest{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Another server's chain and an interrupted full backup.
	other := NewChainID("bbbbbbbb", t0)
	r.Write(ctx, other, KindFull, t0, strings.NewReader("x"), Manifest{}, nil)
	broken := NewChainID("aaaaaaaa", t0.Add(-48*time.Hour))
	r.be.Put(ctx, "chains/"+broken+"/data/"+strings.Repeat("0", 64), []byte("x"))

	removed, err := r.Prune(ctx, "aaaaaaaa", chains[3], 2, t0.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != chains[1]+","+chains[0]+","+broken {
		t.Fatalf("removed %v", removed)
	}
	left, _ := r.Chains(ctx)
	if len(left) != 3 {
		t.Fatalf("left %d chains", len(left))
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "secret.key"), []byte("k"), 0o600)
	os.MkdirAll(filepath.Join(src, "uploads", "a"), 0o750)
	os.WriteFile(filepath.Join(src, "uploads", "a", "x.msi"), []byte("msi"), 0o640)
	os.MkdirAll(filepath.Join(src, "backups"), 0o750)
	os.WriteFile(filepath.Join(src, "backups", "big"), []byte("no"), 0o640)
	dump := filepath.Join(t.TempDir(), "db.dump")
	os.WriteFile(dump, []byte("PGDMP"), 0o600)
	env := filepath.Join(t.TempDir(), "server.env")
	os.WriteFile(env, []byte("FW_LISTEN=x"), 0o640)
	spec := ArchiveSpec{Meta: ArchiveMeta{Version: "0.2.0", Host: "h", Instance: "aaaaaaaa", Created: time.Now()},
		DumpPath: dump, DataDir: src, Exclude: []string{"backups"}, EnvFile: env}
	var a, b bytes.Buffer
	if err := WriteArchive(&a, spec); err != nil {
		t.Fatal(err)
	}
	WriteArchive(&b, spec)
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("archive is not deterministic")
	}
	dst := t.TempDir()
	meta, err := ExtractArchive(&a, dst)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != "0.2.0" || meta.Instance != "aaaaaaaa" {
		t.Fatalf("meta %+v", meta)
	}
	for f, want := range map[string]string{"database.dump": "PGDMP", "data/secret.key": "k", "data/uploads/a/x.msi": "msi", "etc/server.env": "FW_LISTEN=x"} {
		got, err := os.ReadFile(filepath.Join(dst, f))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", f, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "data", "backups")); err == nil {
		t.Error("excluded folder was archived")
	}
}
