package remote

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

var (
	pGetCursorPos = user32.NewProc("GetCursorPos")
	pGetKeyState  = user32.NewProc("GetKeyState")
)

// TestWindowsPlatform runs the real capture and input code in the test's own
// desktop session (the CI runner is signed in). FW_REMOTE_SHOT saves the
// captured screen as a PNG.
func TestWindowsPlatform(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	SetDPIAware()
	p := NewWindowsPlatform()

	mons := p.Monitors()
	if len(mons) == 0 {
		t.Skip("no display in this session")
	}
	t.Logf("monitors %+v, desktop %q, user %q", mons, p.Desktop(), p.User())
	m := mons[0]
	for _, mm := range mons {
		if mm.Primary {
			m = mm
		}
	}

	s := NewScreen(m.Width, m.Height)
	if err := p.Capture(Rect{X: m.X, Y: m.Y, W: m.Width, H: m.Height}, s); err != nil {
		t.Fatalf("capture: %v", err)
	}
	distinct := map[uint32]bool{}
	for i := 0; i+4 <= len(s.Pix) && len(distinct) < 8; i += 4 * 97 {
		distinct[uint32(s.Pix[i])|uint32(s.Pix[i+1])<<8|uint32(s.Pix[i+2])<<16] = true
	}
	if len(distinct) < 2 {
		t.Errorf("captured screen is one flat colour")
	}
	if dir := os.Getenv("FW_REMOTE_SHOT"); dir != "" {
		img := image.NewRGBA(image.Rect(0, 0, s.W, s.H))
		for y := range s.H {
			for x := range s.W {
				i, j := y*s.Stride+x*4, y*img.Stride+x*4
				img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = s.Pix[i+2], s.Pix[i+1], s.Pix[i], 255
			}
		}
		_ = os.MkdirAll(dir, 0o755)
		f, err := os.Create(filepath.Join(dir, "remote-capture.png"))
		if err == nil {
			_ = png.Encode(f, img)
			f.Close()
		}
	}

	// The mouse lands where the viewer points.
	tx, ty := m.X+m.Width/3, m.Y+m.Height/3
	p.Mouse(MouseInput{Action: MouseMove, X: tx, Y: ty})
	time.Sleep(100 * time.Millisecond)
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if abs(int(pt.X)-tx) > 2 || abs(int(pt.Y)-ty) > 2 {
		t.Errorf("cursor at %d,%d, want %d,%d", pt.X, pt.Y, tx, ty)
	}

	// A scan code press toggles Caps Lock; a second press restores it.
	caps := func() bool { r, _, _ := pGetKeyState.Call(0x14); return r&1 != 0 }
	before := caps()
	p.Key(KeyInput{Down: true, Scan: 0x3a})
	p.Key(KeyInput{Down: false, Scan: 0x3a})
	time.Sleep(100 * time.Millisecond)
	if caps() == before {
		t.Errorf("Caps Lock did not toggle")
	}
	p.Key(KeyInput{Down: true, Scan: 0x3a})
	p.Key(KeyInput{Down: false, Scan: 0x3a})

	// The clipboard round-trips Unicode text and moves its sequence number.
	seq := p.ClipboardSeq()
	const text = "Fleetwarden – klembord ✓"
	if err := p.SetClipboard(text); err != nil {
		t.Fatalf("set clipboard: %v", err)
	}
	got, err := p.Clipboard()
	if err != nil || got != text {
		t.Errorf("clipboard = %q, %v", got, err)
	}
	if p.ClipboardSeq() == seq {
		t.Errorf("clipboard sequence did not change")
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
