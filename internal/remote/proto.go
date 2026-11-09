// Package remote is the remote desktop protocol between the operator's
// browser (web/src/pages/remote/protocol.ts) and the agent's desktop helper.
// The server relays these messages without looking at them.
//
// Every message is one binary WebSocket message whose first byte is its type.
// Integers are big-endian.
//
// Helper -> viewer:
//
//	MsgInfo      JSON Info: monitors, the shown area and the desktop
//	MsgFrame     u32 seq, u16 count, then count tiles:
//	             u16 x, u16 y, u16 w, u16 h, u8 encoding, u32 length, data
//	MsgClipboard UTF-8 text copied on the remote computer
//	MsgEvent     JSON Event
//	MsgRTC       JSON RTCSignal: the offer for the direct path
//
// Viewer -> helper:
//
//	MsgMouse     u8 action, u8 button, i16 wheel, u16 x, u16 y (in the shown area)
//	MsgKey       u8 down, u16 scan code (0xE0xx for extended keys)
//	MsgAck       u32 seq of a frame the viewer has drawn
//	MsgSetClip   UTF-8 text for the remote clipboard
//	MsgControl   JSON Control
//	MsgRTCAns    JSON RTCSignal: request, answer or failed
//
// Once the direct path (a WebRTC data channel) is open, the agent sends the
// screen over it and the viewer sends its input over it; messages are split
// in chunks (ChunkMore, ChunkLast). The relay stays open as the fallback.
//
// The helper keeps at most a few frames unacknowledged, so the frame rate
// follows what the connection and the browser can take.
package remote

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

// Message types.
const (
	MsgInfo      byte = 0x01
	MsgFrame     byte = 0x02
	MsgClipboard byte = 0x03
	MsgEvent     byte = 0x04
	MsgRTC       byte = 0x05 // agent -> viewer: JSON RTCSignal (the offer)

	MsgMouse   byte = 0x81
	MsgKey     byte = 0x82
	MsgAck     byte = 0x83
	MsgSetClip byte = 0x84
	MsgControl byte = 0x85
	MsgRTCAns  byte = 0x86 // viewer -> agent: JSON RTCSignal (request, answer, failed)
)

// RTCSignal sets up the direct path: the viewer asks for it ("request"),
// the agent sends an offer with all its candidates, and the viewer answers
// (or reports "failed"). It travels over the encrypted relay, so the DTLS
// fingerprints in the SDP are authenticated.
type RTCSignal struct {
	Type string `json:"type"` // request, offer, answer, failed
	SDP  string `json:"sdp,omitempty"`
}

// Paths a session's screen can travel, reported in an Event of type "path".
const (
	PathRelay  = "relay"
	PathDirect = "direct"
)

// Data channel framing: every data channel message starts with a byte that
// says whether it ends a session message (messages can be larger than a
// data channel message may be).
const (
	ChunkMore byte = 0
	ChunkLast byte = 1
	// ChunkSize is the payload per data channel message; every browser
	// takes 64 KiB.
	ChunkSize = 64<<10 - 1
)

// Tile encodings.
const (
	EncJPEG byte = 1
	EncPNG  byte = 2
)

// Mouse actions.
const (
	MouseMove  byte = 0
	MouseDown  byte = 1
	MouseUp    byte = 2
	MouseWheel byte = 3 // wheel > 0 scrolls up
	MouseHWhel byte = 4 // horizontal wheel, > 0 scrolls right
)

// Mouse buttons.
const (
	ButtonLeft   byte = 0
	ButtonMiddle byte = 1
	ButtonRight  byte = 2
	ButtonBack   byte = 3
	ButtonFwd    byte = 4
)

// MaxClipboard is the largest clipboard text sent either way.
const MaxClipboard = 1 << 20

// Monitor is one display. Coordinates are in physical pixels on the virtual
// screen.
type Monitor struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Primary bool   `json:"primary"`
}

// Rect is an area of the virtual screen.
type Rect struct{ X, Y, W, H int }

// Info describes what the viewer sees. Monitor -1 is all monitors.
type Info struct {
	Monitors []Monitor `json:"monitors"`
	Monitor  int       `json:"monitor"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Desktop  string    `json:"desktop"` // "Default", "Winlogon" (sign-in screen, UAC)
	Hostname string    `json:"hostname"`
	User     string    `json:"user,omitempty"`
	Quality  string    `json:"quality"`
	Features []string  `json:"features"` // "cad", "clipboard", "type"
}

// Event tells the viewer about something that happened.
type Event struct {
	Type    string `json:"type"` // "desktop", "error", "notice", "path"
	Desktop string `json:"desktop,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Control is an operator command.
type Control struct {
	Op      string `json:"op"` // monitor, quality, refresh, cad, type
	Monitor int    `json:"monitor,omitempty"`
	Quality string `json:"quality,omitempty"` // low, medium, high
	Text    string `json:"text,omitempty"`
}

// Tile is one encoded rectangle of a frame.
type Tile struct {
	X, Y, W, H int
	Enc        byte
	Data       []byte
}

// EncodeFrame builds a MsgFrame message.
func EncodeFrame(seq uint32, tiles []Tile) []byte {
	n := 7
	for _, t := range tiles {
		n += 13 + len(t.Data)
	}
	b := make([]byte, 0, n)
	b = append(b, MsgFrame)
	b = binary.BigEndian.AppendUint32(b, seq)
	b = binary.BigEndian.AppendUint16(b, uint16(len(tiles)))
	for _, t := range tiles {
		b = binary.BigEndian.AppendUint16(b, uint16(t.X))
		b = binary.BigEndian.AppendUint16(b, uint16(t.Y))
		b = binary.BigEndian.AppendUint16(b, uint16(t.W))
		b = binary.BigEndian.AppendUint16(b, uint16(t.H))
		b = append(b, t.Enc)
		b = binary.BigEndian.AppendUint32(b, uint32(len(t.Data)))
		b = append(b, t.Data...)
	}
	return b
}

var errShort = errors.New("remote: message too short")

// DecodeFrame parses a MsgFrame message (tests and tools).
func DecodeFrame(b []byte) (seq uint32, tiles []Tile, err error) {
	if len(b) < 7 || b[0] != MsgFrame {
		return 0, nil, errShort
	}
	seq = binary.BigEndian.Uint32(b[1:])
	n := int(binary.BigEndian.Uint16(b[5:]))
	b = b[7:]
	for range n {
		if len(b) < 13 {
			return 0, nil, errShort
		}
		t := Tile{X: int(binary.BigEndian.Uint16(b)), Y: int(binary.BigEndian.Uint16(b[2:])), W: int(binary.BigEndian.Uint16(b[4:])),
			H: int(binary.BigEndian.Uint16(b[6:])), Enc: b[8]}
		l := int(binary.BigEndian.Uint32(b[9:]))
		b = b[13:]
		if len(b) < l {
			return 0, nil, errShort
		}
		t.Data, b = b[:l], b[l:]
		tiles = append(tiles, t)
	}
	return seq, tiles, nil
}

// JSONMessage builds a message with a JSON body.
func JSONMessage(typ byte, v any) []byte {
	raw, _ := json.Marshal(v)
	return append([]byte{typ}, raw...)
}

// MouseInput is a decoded MsgMouse.
type MouseInput struct {
	Action, Button byte
	Wheel          int
	X, Y           int
}

// DecodeMouse parses a MsgMouse message.
func DecodeMouse(b []byte) (MouseInput, bool) {
	if len(b) < 9 || b[0] != MsgMouse {
		return MouseInput{}, false
	}
	return MouseInput{Action: b[1], Button: b[2], Wheel: int(int16(binary.BigEndian.Uint16(b[3:]))),
		X: int(binary.BigEndian.Uint16(b[5:])), Y: int(binary.BigEndian.Uint16(b[7:]))}, true
}

// EncodeMouse builds a MsgMouse message.
func EncodeMouse(m MouseInput) []byte {
	b := []byte{MsgMouse, m.Action, m.Button}
	b = binary.BigEndian.AppendUint16(b, uint16(int16(m.Wheel)))
	b = binary.BigEndian.AppendUint16(b, uint16(m.X))
	return binary.BigEndian.AppendUint16(b, uint16(m.Y))
}

// KeyInput is a decoded MsgKey: a PC/AT set 1 scan code, 0xE0xx when
// extended.
type KeyInput struct {
	Down bool
	Scan uint16
}

// DecodeKey parses a MsgKey message.
func DecodeKey(b []byte) (KeyInput, bool) {
	if len(b) < 4 || b[0] != MsgKey {
		return KeyInput{}, false
	}
	return KeyInput{Down: b[1] == 1, Scan: binary.BigEndian.Uint16(b[2:])}, true
}

// EncodeKey builds a MsgKey message.
func EncodeKey(k KeyInput) []byte {
	d := byte(0)
	if k.Down {
		d = 1
	}
	return binary.BigEndian.AppendUint16([]byte{MsgKey, d}, k.Scan)
}

// EncodeAck builds a MsgAck message.
func EncodeAck(seq uint32) []byte { return binary.BigEndian.AppendUint32([]byte{MsgAck}, seq) }

// DecodeControl parses a MsgControl message.
func DecodeControl(b []byte) (Control, bool) {
	var c Control
	if len(b) < 2 || b[0] != MsgControl || json.Unmarshal(b[1:], &c) != nil {
		return c, false
	}
	return c, true
}
