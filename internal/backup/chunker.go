package backup

import (
	"errors"
	"io"
)

// Content-defined chunking (FastCDC-style gear hash with normalised chunk
// sizes). Cut points depend on the content, not on offsets, so an insert in a
// database dump only changes the chunks around it: an incremental backup then
// only uploads those.
const (
	minChunk = 256 << 10
	avgChunk = 1 << 20
	maxChunk = 4 << 20
)

// The gear hash shifts left, so the high bits depend on the most recent 64
// bytes. The masks test high bits: more bits before the average size (cut
// less likely), fewer after (cut more likely).
const (
	maskSmall uint64 = (1<<22 - 1) << (64 - 22)
	maskLarge uint64 = (1<<18 - 1) << (64 - 18)
)

// gear is a fixed pseudo-random table. It must never change: chunk
// boundaries, and therefore deduplication, depend on it.
var gear = func() (g [256]uint64) {
	x := uint64(0x4657434443484e4b) // "FWCDCHNK"
	for i := range g {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		g[i] = z ^ (z >> 31)
	}
	return g
}()

// chunker splits a stream into content-defined chunks.
type chunker struct {
	r          io.Reader
	buf        []byte
	start, end int
	eof        bool
}

func newChunker(r io.Reader) *chunker {
	return &chunker{r: r, buf: make([]byte, 2*maxChunk)}
}

func (c *chunker) fill() error {
	if c.eof || c.end-c.start >= maxChunk {
		return nil
	}
	n := copy(c.buf, c.buf[c.start:c.end])
	c.start, c.end = 0, n
	for c.end < len(c.buf) {
		m, err := c.r.Read(c.buf[c.end:])
		c.end += m
		if errors.Is(err, io.EOF) {
			c.eof = true
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Next returns the next chunk. The slice is only valid until the next call.
// It returns io.EOF after the last chunk.
func (c *chunker) Next() ([]byte, error) {
	if err := c.fill(); err != nil {
		return nil, err
	}
	data := c.buf[c.start:c.end]
	if len(data) == 0 {
		return nil, io.EOF
	}
	n := cutPoint(data)
	c.start += n
	return data[:n], nil
}

func cutPoint(data []byte) int {
	n := len(data)
	if n <= minChunk {
		return n
	}
	if n > maxChunk {
		n = maxChunk
	}
	var fp uint64
	i := minChunk
	for ; i < n && i < avgChunk; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskSmall == 0 {
			return i + 1
		}
	}
	for ; i < n; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskLarge == 0 {
			return i + 1
		}
	}
	return n
}
