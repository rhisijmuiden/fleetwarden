package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// localBackend stores objects in a folder on the server, for example a
// mounted network share.
type localBackend struct{ root string }

func openLocal(root string) (Backend, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create folder: %w", err)
	}
	return &localBackend{root: root}, nil
}

func (b *localBackend) p(name string) string { return filepath.Join(b.root, filepath.FromSlash(name)) }

func (b *localBackend) Put(_ context.Context, name string, data []byte) error {
	dst := b.p(name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := dst + ".tmp-" + hex.EncodeToString(rnd[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (b *localBackend) Get(_ context.Context, name string) ([]byte, error) {
	data, err := os.ReadFile(b.p(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotExist)
	}
	return data, err
}

func (b *localBackend) List(_ context.Context, dir string) ([]Entry, error) {
	des, err := os.ReadDir(b.p(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		e := Entry{Name: d.Name(), Dir: d.IsDir()}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	return out, nil
}

func (b *localBackend) Delete(_ context.Context, name string) error {
	err := os.Remove(b.p(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (b *localBackend) RemoveAll(_ context.Context, dir string) error {
	if dir == "" {
		return errors.New("refusing to remove the location root")
	}
	return os.RemoveAll(b.p(dir))
}

func (b *localBackend) Close() error { return nil }
