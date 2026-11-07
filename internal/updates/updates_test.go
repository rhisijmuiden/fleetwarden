package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0-test.5", 1},
		{"0.1.0-test.10", "0.1.0-test.9", 1},
		{"0.2.0-test.1", "0.1.0", 1},
		{"1.0.0", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if ValidVersion("0.1.0-dev.3") || ValidVersion("1.0.0; rm -rf /") || !ValidVersion("0.1.0-test.2") {
		t.Error("ValidVersion")
	}
}

func TestLatest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deb := `"assets":[{"name":"fleetwarden-server_x_amd64.deb"}]`
		w.Write([]byte(`[
			{"tag_name":"v0.1.0-test.9","prerelease":true,` + deb + `},
			{"tag_name":"v0.1.0-test.10","prerelease":true,` + deb + `},
			{"tag_name":"v0.2.0-test.11","prerelease":true,"assets":[]},
			{"tag_name":"v0.1.0","prerelease":false,` + deb + `},
			{"tag_name":"v0.0.9","prerelease":false,` + deb + `},
			{"tag_name":"v9.0.0","draft":true,` + deb + `}
		]`))
	}))
	defer ts.Close()
	c := NewChecker("o/r")
	c.APIBase = ts.URL
	test, err := c.Latest(context.Background(), ChannelTest, false)
	if err != nil || test == nil || test.Version != "0.1.0-test.10" {
		t.Fatalf("test channel: %+v %v", test, err)
	}
	prod, _ := c.Latest(context.Background(), ChannelProduction, false)
	if prod == nil || prod.Version != "0.1.0" {
		t.Fatalf("production channel: %+v", prod)
	}
}

func TestRequest(t *testing.T) {
	dir := t.TempDir()
	r := Request{Version: "0.1.0-test.3", RequestedBy: "admin", RequestedAt: time.Now()}
	if err := RequestUpdate(dir, r, Status{}); err != nil {
		t.Fatal(err)
	}
	if !Pending(dir) || RequestUpdate(dir, r, Status{}) != ErrBusy {
		t.Error("second request not refused")
	}
	if RequestUpdate(t.TempDir(), r, Status{State: "installing", UpdatedAt: time.Now()}) != ErrBusy {
		t.Error("request accepted while running")
	}
	if RequestUpdate(t.TempDir(), Request{Version: "../../x"}, Status{}) == nil {
		t.Error("bad version accepted")
	}
}
