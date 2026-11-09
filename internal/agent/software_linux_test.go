//go:build linux

package agent

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type fakeApt struct {
	mu    sync.Mutex
	calls [][]string
	pkgs  map[string]string
}

func (f *fakeApt) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	last := args[len(args)-1]
	switch {
	case name == "dpkg-query":
		if v, ok := f.pkgs[last]; ok {
			return []byte("ii \t" + v), nil
		}
		return nil, exec.Command("false").Run()
	case name == "apt-get" && args[0] == "install":
		if last == "broken" {
			return []byte("E: Unable to locate package broken"), exec.Command("sh", "-c", "exit 100").Run()
		}
		f.pkgs[strings.TrimPrefix(last, "./")] = "1.0"
	case name == "apt-get" && (args[0] == "remove" || args[0] == "purge"):
		delete(f.pkgs, last)
	}
	return []byte("ok"), nil
}

func TestAptSoftware(t *testing.T) {
	f := &fakeApt{pkgs: map[string]string{"htop": "3.4.1-1"}}
	b := &aptSoftware{run: f.run}
	ctx := context.Background()
	if ok, v, err := b.Find(ctx, agentproto.Detect{Name: "htop"}); !ok || v != "3.4.1-1" || err != nil {
		t.Errorf("find: %v %s %v", ok, v, err)
	}
	if ok, _, _ := b.Find(ctx, agentproto.Detect{Name: "vim"}); ok {
		t.Error("vim found")
	}
	if _, err := b.Install(ctx, agentproto.Installer{Kind: "apt", Package: "vim", Args: "--no-install-recommends evil"}, ""); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	install := f.calls[len(f.calls)-1]
	f.mu.Unlock()
	if !slices.Contains(install, "--no-install-recommends") || slices.Contains(install, "evil") || install[len(install)-1] != "vim" {
		t.Errorf("install call: %v", install)
	}
	res, err := b.Install(ctx, agentproto.Installer{Kind: "apt", Package: "broken"}, "")
	if err == nil || res.ExitCode != 100 || !strings.Contains(res.Output, "Unable to locate") {
		t.Errorf("broken: %+v %v", res, err)
	}
	if _, err := b.Install(ctx, agentproto.Installer{Kind: "apt", Package: "fleetwarden-agent"}, ""); err == nil {
		t.Error("installing the agent through a job")
	}
	if _, found, err := b.Uninstall(ctx, agentproto.Uninstall{Name: "htop", Purge: true}); !found || err != nil {
		t.Errorf("purge: %v %v", found, err)
	}
	if _, found, _ := b.Uninstall(ctx, agentproto.Uninstall{Name: "htop"}); found {
		t.Error("htop still found")
	}
	if _, _, err := b.Uninstall(ctx, agentproto.Uninstall{Name: "-y; reboot"}); err == nil {
		t.Error("bad package name accepted")
	}
	if _, _, err := b.Uninstall(ctx, agentproto.Uninstall{Name: "fleetwarden-agent"}); err == nil {
		t.Error("removing the agent through a job")
	}
}
