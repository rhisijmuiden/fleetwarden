package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"net"
	"testing"
	"time"
)

func TestEncoderSendsOnlyChanges(t *testing.T) {
	s := NewScreen(200, 130)
	var e Encoder
	tiles := e.Encode(s)
	area := 0
	for _, tl := range tiles {
		area += tl.W * tl.H
		img, err := jpeg.Decode(bytes.NewReader(tl.Data))
		if err != nil || img.Bounds().Dx() != tl.W || img.Bounds().Dy() != tl.H {
			t.Fatalf("tile %+v: %v", tl, err)
		}
	}
	if area != 200*130 {
		t.Fatalf("first frame covers %d pixels, want %d", area, 200*130)
	}
	if tiles := e.Encode(s); len(tiles) != 0 {
		t.Fatalf("unchanged screen gave %d tiles", len(tiles))
	}
	// One pixel in the second tile column of the last row.
	s.Pix[(129)*s.Stride+70*4] = 0xff
	tiles = e.Encode(s)
	if len(tiles) != 1 || tiles[0].X != 64 || tiles[0].Y != 128 || tiles[0].W != 64 || tiles[0].H != 2 {
		t.Fatalf("tiles after a small change: %+v", tiles)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	in := []Tile{{X: 1, Y: 2, W: 3, H: 4, Enc: EncJPEG, Data: []byte("abc")}, {X: 5, Y: 6, W: 7, H: 8, Enc: EncPNG, Data: nil}}
	seq, out, err := DecodeFrame(EncodeFrame(9, in))
	if err != nil || seq != 9 || len(out) != 2 || out[0].X != 1 || string(out[0].Data) != "abc" || out[1].H != 8 {
		t.Fatalf("%d %+v %v", seq, out, err)
	}
	m, ok := DecodeMouse(EncodeMouse(MouseInput{Action: MouseWheel, Wheel: -120, X: 10, Y: 20}))
	if !ok || m.Wheel != -120 || m.X != 10 || m.Y != 20 {
		t.Fatalf("mouse %+v", m)
	}
	k, ok := DecodeKey(EncodeKey(KeyInput{Down: true, Scan: 0xe05b}))
	if !ok || !k.Down || k.Scan != 0xe05b {
		t.Fatalf("key %+v", k)
	}
}

type viewer struct {
	t *testing.T
	c *StreamConn
}

func (v viewer) next(typ byte) []byte {
	v.t.Helper()
	for {
		m, err := v.c.ReadMsg()
		if err != nil {
			v.t.Fatal(err)
		}
		if m[0] == typ {
			return m
		}
	}
}

func (v viewer) info() Info {
	v.t.Helper()
	var i Info
	if err := json.Unmarshal(v.next(MsgInfo)[1:], &i); err != nil {
		v.t.Fatal(err)
	}
	return i
}

func TestDesktopSession(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	p := NewFakePlatform()
	d := &Desktop{P: p, Features: []string{"clipboard"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, NewStreamConn(b)) }()
	v := viewer{t, NewStreamConn(a)}

	info := v.info()
	if info.Width != 320 || info.Height != 200 || len(info.Monitors) != 2 || info.Monitor != 0 || info.Quality != "medium" {
		t.Fatalf("info %+v", info)
	}
	seq, tiles, err := DecodeFrame(v.next(MsgFrame))
	if err != nil || seq != 1 || len(tiles) == 0 {
		t.Fatalf("first frame: %d %d %v", seq, len(tiles), err)
	}
	// Without an acknowledgement a second frame may come, a third not.
	p.Change()
	v.next(MsgFrame)
	p.Change()
	got := make(chan []byte, 1)
	go func() {
		m, _ := v.c.ReadMsg()
		got <- m
	}()
	select {
	case m := <-got:
		t.Fatalf("frame without acknowledgement: % x", m[:min(len(m), 8)])
	case <-time.After(300 * time.Millisecond):
	}
	if err := v.c.WriteMsg(EncodeAck(2)); err != nil {
		t.Fatal(err)
	}
	if m := <-got; m[0] != MsgFrame {
		t.Fatalf("after ack: % x", m[:1])
	}

	// The second monitor: input is moved to its position.
	v.c.WriteMsg(JSONMessage(MsgControl, Control{Op: "monitor", Monitor: 1}))
	if info := v.info(); info.Width != 160 || info.Monitor != 1 {
		t.Fatalf("monitor 2: %+v", info)
	}
	v.c.WriteMsg(EncodeMouse(MouseInput{Action: MouseDown, Button: ButtonRight, X: 10, Y: 500}))
	v.c.WriteMsg(EncodeKey(KeyInput{Down: true, Scan: 0x1e}))
	v.c.WriteMsg(JSONMessage(MsgControl, Control{Op: "type", Text: "hallo"}))
	v.c.WriteMsg(append([]byte{MsgSetClip}, "van de beheerder"...))
	time.Sleep(200 * time.Millisecond)
	p.CopyOnRemote("van de gebruiker")
	if clip := string(v.next(MsgClipboard)[1:]); clip != "van de gebruiker" {
		t.Fatalf("clipboard %q", clip)
	}
	var mice []MouseInput
	var keys []KeyInput
	var typed []string
	for range 50 {
		if mice, keys, typed, _ = p.Snapshot(); len(typed) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mice) != 1 || mice[0].X != 330 || mice[0].Y != 99 || mice[0].Button != ButtonRight {
		t.Errorf("mouse %+v", mice)
	}
	if len(keys) != 1 || keys[0].Scan != 0x1e || len(typed) != 1 || typed[0] != "hallo" {
		t.Errorf("keys %+v typed %q", keys, typed)
	}
	a.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end when the connection closed")
	}
}
