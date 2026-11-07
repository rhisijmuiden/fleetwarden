package remote

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"slices"
	"time"
	"unicode/utf8"
)

// Conn carries whole messages (a WebSocket, or a pipe with length prefixes).
type Conn interface {
	ReadMsg() ([]byte, error)
	WriteMsg([]byte) error
}

// Platform captures the screen and injects input. All calls come from one
// locked OS thread, as Windows needs for SetThreadDesktop.
type Platform interface {
	Monitors() []Monitor
	// Desktop switches to the desktop that receives input and returns its
	// name ("Default", "Winlogon").
	Desktop() string
	Capture(r Rect, dst *Screen) error
	// Mouse moves or clicks at x, y on the virtual screen.
	Mouse(m MouseInput)
	Key(k KeyInput)
	Type(text string)
	ClipboardSeq() uint32
	Clipboard() (string, error)
	SetClipboard(text string) error
	User() string
}

// MaxInflight is how many frames may wait for an acknowledgement.
const MaxInflight = 2

// Desktop runs a remote desktop session over a Conn.
type Desktop struct {
	P        Platform
	Features []string
	// AckTimeout counts a frame as drawn when the viewer does not
	// acknowledge it in time.
	AckTimeout time.Duration

	monitors []Monitor
	monitor  int // index in monitors, -1 all
	area     Rect
	quality  Quality
	enc      Encoder
	screen   *Screen
	desktop  string
	seq      uint32
	inflight []time.Time
	clipSeq  uint32
	clipSet  string
}

// Run sends the screen and applies input until the connection ends or ctx
// is cancelled.
func (d *Desktop) Run(ctx context.Context, c Conn) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if d.AckTimeout == 0 {
		d.AckTimeout = 5 * time.Second
	}
	in := make(chan []byte, 256)
	rerr := make(chan error, 1)
	go func() {
		for {
			m, err := c.ReadMsg()
			if err != nil {
				rerr <- err
				return
			}
			select {
			case in <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	d.quality = Qualities["medium"]
	d.desktop = d.P.Desktop()
	d.monitors = d.P.Monitors()
	d.monitor = -2 // the primary monitor
	d.selectMonitor()
	d.clipSeq = d.P.ClipboardSeq()
	if err := d.sendInfo(c); err != nil {
		return err
	}

	next := time.NewTimer(0)
	defer next.Stop()
	clip := time.NewTicker(500 * time.Millisecond)
	defer clip.Stop()
	monCheck := time.NewTicker(2 * time.Second)
	defer monCheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-rerr:
			return err
		case m := <-in:
			if err := d.handle(c, m); err != nil {
				return err
			}
			if len(m) > 0 && m[0] == MsgAck {
				resetTimer(next, 0)
			}
		case <-clip.C:
			if err := d.pollClipboard(c); err != nil {
				return err
			}
		case <-monCheck.C:
			if mons := d.P.Monitors(); !slices.Equal(mons, d.monitors) {
				d.monitors = mons
				d.selectMonitor()
				d.enc.Reset()
				if err := d.sendInfo(c); err != nil {
					return err
				}
			}
		case <-next.C:
			wait, err := d.frame(c)
			if err != nil {
				return err
			}
			resetTimer(next, wait)
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	t.Stop()
	select {
	case <-t.C:
	default:
	}
	t.Reset(d)
}

// frame captures and sends one frame when the viewer has room, and returns
// when to try again.
func (d *Desktop) frame(c Conn) (time.Duration, error) {
	now := time.Now()
	for len(d.inflight) > 0 && now.Sub(d.inflight[0]) > d.AckTimeout {
		d.inflight = d.inflight[1:]
	}
	if len(d.inflight) >= MaxInflight {
		return d.AckTimeout, nil // an acknowledgement resets the timer
	}
	if name := d.P.Desktop(); name != d.desktop {
		d.desktop = name
		d.monitors = d.P.Monitors()
		d.selectMonitor()
		d.enc.Reset()
		if err := c.WriteMsg(JSONMessage(MsgEvent, Event{Type: "desktop", Desktop: name})); err != nil {
			return 0, err
		}
		if err := d.sendInfo(c); err != nil {
			return 0, err
		}
	}
	if d.screen == nil || d.screen.W != d.area.W || d.screen.H != d.area.H {
		d.screen = NewScreen(d.area.W, d.area.H)
		d.enc.Reset()
	}
	if err := d.P.Capture(d.area, d.screen); err != nil {
		// The desktop is switching (sign-in, UAC); try again shortly.
		return 250 * time.Millisecond, nil
	}
	d.enc.Quality = d.quality.JPEG
	tiles := d.enc.Encode(d.screen)
	took := time.Since(now)
	wait := max(time.Duration(d.quality.MinFrame)*time.Millisecond-took, 5*time.Millisecond)
	if len(tiles) == 0 {
		return wait, nil
	}
	d.seq++
	if err := c.WriteMsg(EncodeFrame(d.seq, tiles)); err != nil {
		return 0, err
	}
	d.inflight = append(d.inflight, time.Now())
	return wait, nil
}

// selectMonitor sets the shown area for d.monitor; -2 picks the primary
// monitor, -1 shows all.
func (d *Desktop) selectMonitor() {
	if len(d.monitors) == 0 {
		d.monitors = []Monitor{{Index: 0, Name: "Screen", Width: 1024, Height: 768, Primary: true}}
	}
	if d.monitor >= len(d.monitors) || d.monitor < -2 {
		d.monitor = -2
	}
	if d.monitor == -2 {
		d.monitor = 0
		for i, m := range d.monitors {
			if m.Primary {
				d.monitor = i
			}
		}
	}
	if d.monitor >= 0 {
		m := d.monitors[d.monitor]
		d.area = Rect{X: m.X, Y: m.Y, W: m.Width, H: m.Height}
		return
	}
	x0, y0, x1, y1 := d.monitors[0].X, d.monitors[0].Y, d.monitors[0].X+d.monitors[0].Width, d.monitors[0].Y+d.monitors[0].Height
	for _, m := range d.monitors[1:] {
		x0, y0 = min(x0, m.X), min(y0, m.Y)
		x1, y1 = max(x1, m.X+m.Width), max(y1, m.Y+m.Height)
	}
	d.area = Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func (d *Desktop) sendInfo(c Conn) error {
	host, _ := os.Hostname()
	return c.WriteMsg(JSONMessage(MsgInfo, Info{Monitors: d.monitors, Monitor: d.monitor, Width: d.area.W, Height: d.area.H,
		Desktop: d.desktop, Hostname: host, User: d.P.User(), Quality: d.quality.Name, Features: d.Features}))
}

func (d *Desktop) pollClipboard(c Conn) error {
	seq := d.P.ClipboardSeq()
	if seq == d.clipSeq {
		return nil
	}
	d.clipSeq = seq
	text, err := d.P.Clipboard()
	if err != nil || text == "" || text == d.clipSet || len(text) > MaxClipboard {
		return nil
	}
	d.clipSet = text
	return c.WriteMsg(append([]byte{MsgClipboard}, text...))
}

func (d *Desktop) handle(c Conn, m []byte) error {
	if len(m) == 0 {
		return nil
	}
	switch m[0] {
	case MsgMouse:
		if in, ok := DecodeMouse(m); ok {
			in.X = d.area.X + min(in.X, max(d.area.W-1, 0))
			in.Y = d.area.Y + min(in.Y, max(d.area.H-1, 0))
			d.P.Desktop()
			d.P.Mouse(in)
		}
	case MsgKey:
		if k, ok := DecodeKey(m); ok {
			d.P.Desktop()
			d.P.Key(k)
		}
	case MsgAck:
		if len(m) >= 5 {
			seq := binary.BigEndian.Uint32(m[1:])
			// Acknowledgements arrive in order; drop the frames up to seq.
			drop := len(d.inflight) - int(d.seq-seq)
			if drop > 0 {
				d.inflight = d.inflight[min(drop, len(d.inflight)):]
			}
		}
	case MsgSetClip:
		text := string(m[1:])
		if len(text) <= MaxClipboard && utf8.ValidString(text) {
			d.clipSet = text
			if err := d.P.SetClipboard(text); err == nil {
				d.clipSeq = d.P.ClipboardSeq()
			}
		}
	case MsgControl:
		ctl, ok := DecodeControl(m)
		if !ok {
			return nil
		}
		switch ctl.Op {
		case "monitor":
			d.monitor = ctl.Monitor
			if d.monitor < -1 {
				d.monitor = -2
			}
			d.selectMonitor()
			d.enc.Reset()
			return d.sendInfo(c)
		case "quality":
			if q, ok := Qualities[ctl.Quality]; ok {
				d.quality = q
				d.enc.Reset()
				return d.sendInfo(c)
			}
		case "refresh":
			d.enc.Reset()
		case "type":
			if len(ctl.Text) <= 64<<10 {
				d.P.Desktop()
				d.P.Type(ctl.Text)
			}
		}
	}
	return nil
}

// ErrUnsupported means this platform has no remote desktop.
var ErrUnsupported = errors.New("remote desktop is not supported on this platform")
