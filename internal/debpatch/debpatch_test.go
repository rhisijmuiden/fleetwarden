package debpatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddFiles(t *testing.T) {
	deb, err := BuildMinimal("fw-test", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddFiles(deb, []File{{Path: "/usr/share/fw/enroll.json", Mode: 0o600, Data: []byte(`{"token":"x"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(out, "/usr/share/fw/enroll.json")
	if err != nil || string(got) != `{"token":"x"}` {
		t.Fatalf("read back: %v %q", err, got)
	}
	if _, err := ReadFile(out, "/README"); err != nil {
		t.Errorf("original file lost: %v", err)
	}
	// Patching twice replaces the file instead of adding a duplicate.
	out2, err := AddFiles(out, []File{{Path: "/usr/share/fw/enroll.json", Mode: 0o600, Data: []byte(`{"token":"y"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadFile(out2, "/usr/share/fw/enroll.json"); string(got) != `{"token":"y"}` {
		t.Errorf("second patch: %q", got)
	}
	// dpkg-deb accepts the result when it is available.
	if _, err := exec.LookPath("dpkg-deb"); err == nil {
		p := filepath.Join(t.TempDir(), "x.deb")
		os.WriteFile(p, out2, 0o644)
		list, err := exec.Command("dpkg-deb", "-c", p).CombinedOutput()
		if err != nil || !strings.Contains(string(list), "./usr/share/fw/enroll.json") || !strings.Contains(string(list), "-rw-------") {
			t.Errorf("dpkg-deb -c: %v\n%s", err, list)
		}
		if info, err := exec.Command("dpkg-deb", "-f", p, "Package").Output(); err != nil || strings.TrimSpace(string(info)) != "fw-test" {
			t.Errorf("dpkg-deb -f: %v %s", err, info)
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := AddFiles([]byte("not a package"), nil); err == nil {
		t.Error("garbage accepted")
	}
}
