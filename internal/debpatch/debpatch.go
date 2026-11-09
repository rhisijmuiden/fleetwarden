// Package debpatch adds files to an existing Debian package. The server uses
// it to bake the server address and an enrollment token into the agent .deb
// a user downloads from the Add client wizard.
package debpatch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// File is a file to add to the package's data archive.
type File struct {
	Path string // absolute path on the target system, e.g. /usr/share/x/y.json
	Mode int64
	Data []byte
}

const arMagic = "!<arch>\n"

type arEntry struct {
	name string
	data []byte
}

func readAr(r io.Reader) ([]arEntry, error) {
	magic := make([]byte, len(arMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != arMagic {
		return nil, errors.New("debpatch: not a Debian package")
	}
	var out []arEntry
	hdr := make([]byte, 60)
	for {
		if _, err := io.ReadFull(r, hdr); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("debpatch: %w", err)
		}
		if string(hdr[58:60]) != "`\n" {
			return nil, errors.New("debpatch: corrupt archive header")
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil || size < 0 || size > 1<<30 {
			return nil, errors.New("debpatch: corrupt archive size")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("debpatch: %w", err)
		}
		if size%2 == 1 {
			if _, err := io.ReadFull(r, make([]byte, 1)); err != nil && err != io.EOF {
				return nil, err
			}
		}
		out = append(out, arEntry{name: strings.TrimRight(strings.TrimSpace(string(hdr[0:16])), "/"), data: data})
	}
}

func writeAr(w io.Writer, entries []arEntry, mtime time.Time) error {
	if _, err := io.WriteString(w, arMagic); err != nil {
		return err
	}
	for _, e := range entries {
		h := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n", e.name, mtime.Unix(), 0, 0, "100644", len(e.data))
		if _, err := io.WriteString(w, h); err != nil {
			return err
		}
		if _, err := w.Write(e.data); err != nil {
			return err
		}
		if len(e.data)%2 == 1 {
			if _, err := w.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddFiles returns a copy of the package with extra files in data.tar.gz.
// Parent directories that the package does not contain yet are added too.
func AddFiles(deb []byte, files []File) ([]byte, error) {
	entries, err := readAr(bytes.NewReader(deb))
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, e := range entries {
		if strings.HasPrefix(e.name, "data.tar") {
			idx = i
		}
	}
	if idx < 0 {
		return nil, errors.New("debpatch: package has no data archive")
	}
	if entries[idx].name != "data.tar.gz" {
		return nil, fmt.Errorf("debpatch: %s is not supported (build the package with gzip compression)", entries[idx].name)
	}
	now := time.Now()
	data, err := addToTarGz(entries[idx].data, files, now)
	if err != nil {
		return nil, err
	}
	entries[idx].data = data
	var out bytes.Buffer
	if err := writeAr(&out, entries, now); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func addToTarGz(src []byte, files []File, now time.Time) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("debpatch: %w", err)
	}
	tr := tar.NewReader(zr)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	have := map[string]bool{}
	add := map[string]bool{}
	for _, f := range files {
		add["./"+strings.TrimPrefix(path.Clean(f.Path), "/")] = true
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("debpatch: %w", err)
		}
		name := "./" + strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		name = strings.TrimSuffix(name, "/")
		if add[name] {
			continue // replaced below
		}
		have[name] = true
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		p := "./" + strings.TrimPrefix(path.Clean(f.Path), "/")
		var dirs []string
		for d := path.Dir(p); d != "." && d != "/" && !have[d]; d = path.Dir(d) {
			dirs = append([]string{d}, dirs...)
		}
		for _, d := range dirs {
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d + "/", Mode: 0o755, ModTime: now, Uname: "root", Gname: "root"}); err != nil {
				return nil, err
			}
			have[d] = true
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: p, Mode: f.Mode, Size: int64(len(f.Data)), ModTime: now, Uname: "root", Gname: "root"}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReadFile returns a file from the package's data archive (tests and checks).
func ReadFile(deb []byte, name string) ([]byte, error) {
	entries, err := readAr(bytes.NewReader(deb))
	if err != nil {
		return nil, err
	}
	want := "./" + strings.TrimPrefix(path.Clean(name), "/")
	for _, e := range entries {
		if e.name != "data.tar.gz" {
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(e.data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err != nil {
				return nil, fmt.Errorf("debpatch: %s not found", name)
			}
			if "./"+strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/") == want {
				return io.ReadAll(tr)
			}
		}
	}
	return nil, errors.New("debpatch: package has no data.tar.gz")
}

// BuildMinimal creates a tiny but valid Debian package (tests).
func BuildMinimal(name, version string) ([]byte, error) {
	now := time.Now()
	mk := func(files map[string]string) ([]byte, error) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "./", Mode: 0o755, ModTime: now}); err != nil {
			return nil, err
		}
		for n, body := range files {
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: n, Mode: 0o644, Size: int64(len(body)), ModTime: now}); err != nil {
				return nil, err
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				return nil, err
			}
		}
		if err := tw.Close(); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	control, err := mk(map[string]string{"./control": "Package: " + name + "\nVersion: " + version +
		"\nArchitecture: all\nMaintainer: test <test@example.com>\nDescription: test package\n"})
	if err != nil {
		return nil, err
	}
	data, err := mk(map[string]string{"./README": "test\n"})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = writeAr(&out, []arEntry{{"debian-binary", []byte("2.0\n")}, {"control.tar.gz", control}, {"data.tar.gz", data}}, now)
	return out.Bytes(), err
}
