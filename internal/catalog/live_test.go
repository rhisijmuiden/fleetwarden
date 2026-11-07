package catalog

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveWinget downloads the real catalog (FW_TEST_WINGET=1).
func TestLiveWinget(t *testing.T) {
	if os.Getenv("FW_TEST_WINGET") == "" {
		t.Skip("FW_TEST_WINGET not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	w := &Winget{Dir: t.TempDir()}
	defer w.Close()
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if st := w.Status(); st.Packages < 1000 {
		t.Fatalf("status: %+v", st)
	}
	found, err := w.Search(ctx, "7zip", 10)
	if err != nil || len(found) == 0 {
		t.Fatalf("search: %v %+v", err, found)
	}
	t.Logf("search 7zip: %+v", found)
	for _, id := range []string{"7zip.7zip", "Google.Chrome", "Mozilla.Firefox", "Microsoft.Edge", "Notepad++.Notepad++", "VideoLAN.VLC", "Microsoft.VisualStudioCode", "Python.Python.3.12"} {
		vs, err := w.Versions(ctx, id)
		if err != nil || len(vs) == 0 {
			t.Errorf("%s versions: %v", id, err)
			continue
		}
		m, err := w.Manifest(ctx, id, "")
		if err != nil {
			t.Errorf("%s manifest: %v", id, err)
			continue
		}
		c, err := m.Resolve("amd64")
		t.Logf("%s %s (%d versions): %+v err=%v", id, m.PackageVersion, len(vs), c, err)
		if err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}
