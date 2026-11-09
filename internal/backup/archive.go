package backup

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The snapshot stream is a tar archive:
//
//	meta.json        ArchiveMeta
//	database.dump    pg_dump custom format, uncompressed (compressed per chunk)
//	data/...         the server data directory (secret key, uploads)
//	etc/server.env   the server configuration, for reference on restore
const archiveFormat = "fleetwarden-archive/1"

// ArchiveMeta describes the content of a snapshot.
type ArchiveMeta struct {
	Format   string    `json:"format"`
	Version  string    `json:"version"`
	Host     string    `json:"host"`
	Instance string    `json:"instance"`
	Created  time.Time `json:"created"`
}

// ArchiveSpec says what goes into a snapshot.
type ArchiveSpec struct {
	Meta     ArchiveMeta
	DumpPath string
	DataDir  string
	// Exclude are top-level names in DataDir that are not backed up.
	Exclude []string
	EnvFile string
}

type archiveFile struct {
	src, name string
	info      fs.FileInfo
}

func (s ArchiveSpec) files() ([]archiveFile, error) {
	var out []archiveFile
	info, err := os.Stat(s.DumpPath)
	if err != nil {
		return nil, err
	}
	out = append(out, archiveFile{s.DumpPath, "database.dump", info})
	excluded := map[string]bool{}
	for _, e := range s.Exclude {
		excluded[e] = true
	}
	if s.DataDir != "" {
		err := filepath.WalkDir(s.DataDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return nil
				}
				return err
			}
			rel, _ := filepath.Rel(s.DataDir, p)
			if rel == "." {
				return nil
			}
			top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
			if excluded[top] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // directories are implied, symlinks and sockets skipped
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			out = append(out, archiveFile{p, "data/" + filepath.ToSlash(rel), info})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if s.EnvFile != "" {
		if info, err := os.Stat(s.EnvFile); err == nil && info.Mode().IsRegular() {
			out = append(out, archiveFile{s.EnvFile, "etc/server.env", info})
		}
	}
	sort.SliceStable(out[1:], func(i, j int) bool { return out[1+i].name < out[1+j].name })
	return out, nil
}

// EstimateSize returns the approximate stream size, for progress.
func (s ArchiveSpec) EstimateSize() int64 {
	files, err := s.files()
	if err != nil {
		return 0
	}
	var n int64
	for _, f := range files {
		n += f.info.Size() + 512
	}
	return n
}

// WriteArchive writes the snapshot stream. The output is deterministic for
// unchanged input, so unchanged parts deduplicate between snapshots.
func WriteArchive(w io.Writer, s ArchiveSpec) error {
	files, err := s.files()
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	s.Meta.Format = archiveFormat
	meta, _ := json.MarshalIndent(s.Meta, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "meta.json", Mode: 0o600, Size: int64(len(meta)), ModTime: s.Meta.Created.Truncate(time.Second), Format: tar.FormatPAX}); err != nil {
		return err
	}
	if _, err := tw.Write(meta); err != nil {
		return err
	}
	for _, f := range files {
		if err := addFile(tw, f); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return tw.Close()
}

func addFile(tw *tar.Writer, f archiveFile) error {
	fh, err := os.Open(f.src)
	if err != nil {
		return err
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name: f.name, Mode: int64(info.Mode().Perm()), Size: info.Size(),
		ModTime: info.ModTime().Truncate(time.Second), Format: tar.FormatPAX, Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	// The file may change while it is read; tar needs exactly Size bytes.
	n, err := io.Copy(tw, io.LimitReader(fh, hdr.Size))
	if err != nil {
		return err
	}
	if n < hdr.Size {
		_, err = io.CopyN(tw, zeroReader{}, hdr.Size-n)
	}
	return err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// ExtractArchive unpacks a snapshot stream into dest, which must be empty.
// Only regular files below dest are written.
func ExtractArchive(r io.Reader, dest string) (ArchiveMeta, error) {
	var meta ArchiveMeta
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return meta, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return meta, fmt.Errorf("unsafe path %q in backup", hdr.Name)
		}
		if name == "meta.json" {
			raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return meta, err
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				return meta, ErrCorrupt
			}
		}
		dst := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return meta, err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return meta, err
		}
		if name == "meta.json" {
			raw, _ := json.MarshalIndent(meta, "", "  ")
			_, err = f.Write(raw)
		} else {
			_, err = io.Copy(f, tr)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return meta, err
		}
		_ = os.Chtimes(dst, hdr.ModTime, hdr.ModTime)
	}
	if meta.Format != archiveFormat {
		return meta, errors.New("the backup does not contain FleetWarden data")
	}
	if _, err := os.Stat(filepath.Join(dest, "database.dump")); err != nil {
		return meta, errors.New("the backup does not contain a database dump")
	}
	return meta, nil
}
