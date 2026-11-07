package catalog

// A stand-in for the winget CDN, for tests of the catalog and of the server.

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FakePackage is a package of a fake catalog. Manifest is the merged
// manifest YAML of its only version.
type FakePackage struct {
	ID, Name, Moniker, Version, Manifest string
	Tags                                 []string
}

// FakeCDN serves a catalog with these packages the way the winget CDN does.
func FakeCDN(dir string, pkgs []FakePackage) (http.Handler, error) {
	dbPath := filepath.Join(dir, "fake-index.db")
	os.Remove(dbPath)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		return nil, err
	}
	stmts := []string{
		`CREATE TABLE packages(rowid INTEGER PRIMARY KEY, id TEXT NOT NULL, name TEXT NOT NULL, moniker TEXT, latest_version TEXT NOT NULL,
			arp_min_version TEXT, arp_max_version TEXT, hash BLOB)`,
		`CREATE TABLE tags2(rowid INTEGER PRIMARY KEY, tag TEXT NOT NULL)`,
		`CREATE TABLE tags2_map(tag INT64 NOT NULL, package INT64 NOT NULL, PRIMARY KEY(tag, package)) WITHOUT ROWID`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, err
		}
	}
	files := map[string][]byte{}
	tagID := 0
	for i, p := range pkgs {
		man := []byte(p.Manifest)
		msum := sha256.Sum256(man)
		mpath := fmt.Sprintf("manifests/%s/%s/%s/0001", strings.ToLower(p.ID[:1]), strings.ReplaceAll(p.ID, ".", "/"), p.Version)
		files[mpath] = man
		vd := fmt.Sprintf("sV: 1.0\nvD:\n- v: %s\n  rP: %s\n  s256H: %s\n", p.Version, mpath, hex.EncodeToString(msum[:]))
		hash := sha256.Sum256([]byte(vd))
		enc, err := EncodeMSZIP([]byte(vd))
		if err != nil {
			db.Close()
			return nil, err
		}
		files["packages/"+p.ID+"/"+hex.EncodeToString(hash[:4])+"/versionData.mszyml"] = enc
		var moniker any
		if p.Moniker != "" {
			moniker = p.Moniker
		}
		if _, err := db.Exec("INSERT INTO packages (rowid, id, name, moniker, latest_version, hash) VALUES (?, ?, ?, ?, ?, ?)",
			i+1, p.ID, p.Name, moniker, p.Version, hash[:]); err != nil {
			db.Close()
			return nil, err
		}
		for _, t := range p.Tags {
			tagID++
			db.Exec("INSERT INTO tags2 (rowid, tag) VALUES (?, ?)", tagID, t)         //nolint:errcheck
			db.Exec("INSERT INTO tags2_map (tag, package) VALUES (?, ?)", tagID, i+1) //nolint:errcheck
		}
	}
	db.Close()
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, err
	}
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("Public/index.db")
	f.Write(raw) //nolint:errcheck
	zw.Close()
	files["source2.msix"] = zbuf.Bytes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b) //nolint:errcheck
	}), nil
}

// EncodeMSZIP writes data as one MSZIP block (data up to 32 KB).
func EncodeMSZIP(data []byte) ([]byte, error) {
	var blk bytes.Buffer
	blk.WriteString("CK")
	fw, err := flate.NewWriter(&blk, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	fw.Write(data) //nolint:errcheck
	fw.Close()
	out := []byte{0x0a, 0x51, 0xe5, 0xc0, 0x18, 0x00, 0xe0, 0x02}
	out = binary.LittleEndian.AppendUint64(out, uint64(len(data)))
	out = binary.LittleEndian.AppendUint64(out, uint64(len(data)))
	out = binary.LittleEndian.AppendUint32(out, uint32(blk.Len()))
	return append(out, blk.Bytes()...), nil
}
