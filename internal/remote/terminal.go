package remote

import "encoding/binary"

// Remote terminal messages, on the same encrypted session socket as the
// desktop (web/src/pages/remote/terminalProtocol.ts).
//
// Agent -> viewer:
//
//	MsgTermInfo    JSON TermInfo, once the shell runs
//	MsgTermOutput  terminal output bytes
//	MsgTermExit    JSON TermExit, when the shell exited
//
// Viewer -> agent:
//
//	MsgTermInput   bytes typed or pasted
//	MsgTermResize  u16 cols, u16 rows
const (
	MsgTermInfo   byte = 0x10
	MsgTermOutput byte = 0x11
	MsgTermExit   byte = 0x12

	MsgTermInput  byte = 0x91
	MsgTermResize byte = 0x92
)

// TermInfo describes the shell.
type TermInfo struct {
	Hostname string `json:"hostname"`
	Platform string `json:"platform"` // linux, windows
	Shell    string `json:"shell"`
	User     string `json:"user"`
}

// TermExit tells the viewer the shell exited.
type TermExit struct {
	Code int `json:"code"`
}

// EncodeTermResize builds a MsgTermResize message.
func EncodeTermResize(cols, rows int) []byte {
	b := binary.BigEndian.AppendUint16([]byte{MsgTermResize}, uint16(cols))
	return binary.BigEndian.AppendUint16(b, uint16(rows))
}

// DecodeTermResize parses a MsgTermResize message.
func DecodeTermResize(b []byte) (cols, rows int, ok bool) {
	if len(b) < 5 || b[0] != MsgTermResize {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint16(b[1:])), int(binary.BigEndian.Uint16(b[3:])), true
}
