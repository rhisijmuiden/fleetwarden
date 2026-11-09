package remote

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// MaxMessage is the largest message on a stream (a full 4K frame at high
// quality stays well below it).
const MaxMessage = 32 << 20

// StreamConn frames messages on a byte stream (the pipe between the agent
// service and its desktop helper) with a 4-byte length.
type StreamConn struct {
	rw  io.ReadWriter
	wmu sync.Mutex
	hdr [4]byte
}

// NewStreamConn wraps a stream.
func NewStreamConn(rw io.ReadWriter) *StreamConn { return &StreamConn{rw: rw} }

// ReadMsg reads one message. Only one goroutine may read.
func (s *StreamConn) ReadMsg() ([]byte, error) {
	if _, err := io.ReadFull(s.rw, s.hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(s.hdr[:])
	if n > MaxMessage {
		return nil, errors.New("remote: message too large")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(s.rw, b); err != nil {
		return nil, err
	}
	return b, nil
}

// WriteMsg writes one message; it is safe for concurrent use.
func (s *StreamConn) WriteMsg(b []byte) error {
	if len(b) > MaxMessage {
		return errors.New("remote: message too large")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	buf := make([]byte, 4, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	_, err := s.rw.Write(append(buf, b...))
	return err
}
