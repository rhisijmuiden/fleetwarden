package remote

import (
	"sync"
)

// FakePlatform is a Platform for tests: a small screen that changes when
// told to, and a record of the input it got.
type FakePlatform struct {
	mu      sync.Mutex
	W, H    int
	Frame   int // changes the picture
	Mice    []MouseInput
	Keys    []KeyInput
	Typed   []string
	Clip    string
	ClipSeq uint32
	Name    string
}

// NewFakePlatform creates a fake 320x200 screen.
func NewFakePlatform() *FakePlatform { return &FakePlatform{W: 320, H: 200, Name: "Default"} }

func (f *FakePlatform) Monitors() []Monitor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []Monitor{{Index: 0, Name: "DISPLAY1", Width: f.W, Height: f.H, Primary: true}, {Index: 1, Name: "DISPLAY2", X: f.W, Width: 160, Height: 100}}
}

func (f *FakePlatform) Desktop() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Name
}

func (f *FakePlatform) Capture(r Rect, dst *Screen) error {
	f.mu.Lock()
	n := f.Frame
	f.mu.Unlock()
	for y := range r.H {
		for x := range r.W {
			i := y*dst.Stride + x*4
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = byte(x+r.X), byte(y), byte(n*40), 0xff
		}
	}
	return nil
}

// Change alters the picture, so the next capture has changed tiles.
func (f *FakePlatform) Change() {
	f.mu.Lock()
	f.Frame++
	f.mu.Unlock()
}

func (f *FakePlatform) Mouse(m MouseInput) {
	f.mu.Lock()
	f.Mice = append(f.Mice, m)
	f.mu.Unlock()
}

func (f *FakePlatform) Key(k KeyInput) {
	f.mu.Lock()
	f.Keys = append(f.Keys, k)
	f.mu.Unlock()
}

func (f *FakePlatform) Type(text string) {
	f.mu.Lock()
	f.Typed = append(f.Typed, text)
	f.mu.Unlock()
}

func (f *FakePlatform) ClipboardSeq() uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ClipSeq
}

func (f *FakePlatform) Clipboard() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Clip, nil
}

func (f *FakePlatform) SetClipboard(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Clip = text
	f.ClipSeq++
	return nil
}

// CopyOnRemote simulates the user copying text.
func (f *FakePlatform) CopyOnRemote(text string) {
	f.mu.Lock()
	f.Clip = text
	f.ClipSeq++
	f.mu.Unlock()
}

func (f *FakePlatform) User() string { return `TEST\user` }

// Snapshot returns copies of the recorded input.
func (f *FakePlatform) Snapshot() (mice []MouseInput, keys []KeyInput, typed []string, clip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MouseInput(nil), f.Mice...), append([]KeyInput(nil), f.Keys...), append([]string(nil), f.Typed...), f.Clip
}
