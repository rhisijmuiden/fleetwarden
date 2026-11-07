package secrets

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k", "secret.key")
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("hunter2")
	if err != nil || sealed == "hunter2" {
		t.Fatalf("seal: %q %v", sealed, err)
	}
	// A second load must use the same key.
	b2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b2.Open(sealed); err != nil || got != "hunter2" {
		t.Fatalf("open: %q %v", got, err)
	}
	if got, _ := b.Seal(""); got != "" {
		t.Errorf("empty secret sealed to %q", got)
	}
	other, _ := New(make([]byte, 32))
	if _, err := other.Open(sealed); err == nil {
		t.Error("opened with the wrong key")
	}
}
