//go:build windows

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

var testPipes atomic.Int32

func testPipe(t *testing.T) (*trayHub, string) {
	t.Helper()
	// A pipe per test: the previous test's listener may still be closing.
	name := fmt.Sprintf(`FleetWardenAgent.Tray.test-%d-%d`, os.Getpid(), testPipes.Add(1))
	l, err := listenTray(`\\.\pipe\` + name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := newTrayHub(nil)
	h.session = pipeClientSession
	go h.Serve(ctx, l)
	return h, name
}

// TestTrayNamedPipe runs the tray protocol over a real named pipe.
func TestTrayNamedPipe(t *testing.T) {
	h, name := testPipe(t)
	c, err := winio.DialPipe(`\\.\pipe\`+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, `{"type":"hello","lang":"en-US"}`)
	waitClients(t, h, 1)
	clients, _ := h.snapshot()
	t.Logf("tray session %d, active sessions %v", clients[0].session, activeSessions())

	got := make(chan Answer, 1)
	go func() {
		ans, err := h.Ask(context.Background(), Prompt{ID: "5-1", Kind: PromptInstall, Updates: 2, CanPostpone: true, PostponesLeft: 1, PostponeMinutes: 15, CountdownSeconds: 60})
		if err != nil {
			t.Error(err)
		}
		got <- ans
	}()
	r := bufio.NewScanner(c)
	if !r.Scan() {
		t.Fatal(r.Err())
	}
	var m trayMsg
	if err := json.Unmarshal(r.Bytes(), &m); err != nil || m.Type != "prompt" || m.Prompt.ID != "5-1" {
		t.Fatalf("prompt = %s (%v)", r.Bytes(), err)
	}
	fmt.Fprintln(c, `{"type":"answer","id":"5-1","postpone":true}`)
	if ans := <-got; !ans.Postpone {
		t.Fatal("expected postpone")
	}
}

// TestTrayApp starts the real tray app (FW_TRAY_EXE) against a test pipe.
// Nobody clicks, so the pop-up answers "now" when its countdown ends.
func TestTrayApp(t *testing.T) {
	exe := os.Getenv("FW_TRAY_EXE")
	if exe == "" {
		t.Skip("FW_TRAY_EXE not set")
	}
	h, name := testPipe(t)
	h.grace = time.Minute
	cmd := exec.Command(exe, "--pipe", name)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	start := time.Now()
	ans, err := h.Ask(context.Background(), Prompt{ID: "6-1", Kind: PromptReboot, CanPostpone: true, PostponesLeft: 1, PostponeMinutes: 30, CountdownSeconds: 5, ProductName: "FleetWarden CI", AccentColor: "#12A150"})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("tray answered %+v after %s", ans, took)
	if ans.Postpone || took > 40*time.Second {
		t.Fatalf("the tray app did not answer at the end of its countdown (answer %+v after %s)", ans, took)
	}
}
