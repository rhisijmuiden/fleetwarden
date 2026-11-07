package remote

import (
	"bytes"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
)

// Screen is a captured image: 32-bit BGRA (Windows DIB order), top-down.
type Screen struct {
	W, H   int
	Stride int
	Pix    []byte
}

// NewScreen allocates a screen buffer.
func NewScreen(w, h int) *Screen {
	return &Screen{W: w, H: h, Stride: w * 4, Pix: make([]byte, w*h*4)}
}

// TileSize is the edge of the squares compared between frames.
const TileSize = 64

// Quality presets: JPEG quality and the shortest time between frames.
type Quality struct {
	Name     string
	JPEG     int
	MinFrame int // milliseconds
}

// Qualities by name.
var Qualities = map[string]Quality{
	"low":    {"low", 35, 100},
	"medium": {"medium", 60, 66},
	"high":   {"high", 82, 40},
}

// Encoder turns screens into the tiles that changed since the previous one.
type Encoder struct {
	prev    *Screen
	Quality int
}

// Reset forgets the previous screen, so the next Encode sends everything.
func (e *Encoder) Reset() { e.prev = nil }

// Encode returns the changed areas of s as JPEG tiles. Changed tiles that
// touch in a row are merged into one rectangle. It keeps a copy of s.
func (e *Encoder) Encode(s *Screen) []Tile {
	cols := (s.W + TileSize - 1) / TileSize
	rows := (s.H + TileSize - 1) / TileSize
	full := e.prev == nil || e.prev.W != s.W || e.prev.H != s.H
	var rects []Rect
	for ty := range rows {
		y0 := ty * TileSize
		h := min(TileSize, s.H-y0)
		start := -1
		for tx := 0; tx <= cols; tx++ {
			changed := false
			if tx < cols {
				changed = full || e.tileChanged(s, tx*TileSize, y0, min(TileSize, s.W-tx*TileSize), h)
			}
			switch {
			case changed && start < 0:
				start = tx
			case !changed && start >= 0:
				x0 := start * TileSize
				rects = append(rects, Rect{X: x0, Y: y0, W: min(tx*TileSize, s.W) - x0, H: h})
				start = -1
			}
		}
	}
	if len(rects) == 0 {
		return nil
	}
	rects = mergeRows(rects)
	tiles := make([]Tile, len(rects))
	q := e.Quality
	if q <= 0 || q > 100 {
		q = 60
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(runtime.NumCPU(), 1))
	for i, r := range rects {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			tiles[i] = Tile{X: r.X, Y: r.Y, W: r.W, H: r.H, Enc: EncJPEG, Data: encodeJPEG(s, r, q)}
		}()
	}
	wg.Wait()
	if e.prev == nil || e.prev.W != s.W || e.prev.H != s.H {
		e.prev = NewScreen(s.W, s.H)
	}
	for y := range s.H {
		copy(e.prev.Pix[y*e.prev.Stride:y*e.prev.Stride+s.W*4], s.Pix[y*s.Stride:y*s.Stride+s.W*4])
	}
	return tiles
}

func (e *Encoder) tileChanged(s *Screen, x, y, w, h int) bool {
	p := e.prev
	for row := y; row < y+h; row++ {
		a := s.Pix[row*s.Stride+x*4 : row*s.Stride+(x+w)*4]
		b := p.Pix[row*p.Stride+x*4 : row*p.Stride+(x+w)*4]
		if !bytes.Equal(a, b) {
			return true
		}
	}
	return false
}

// mergeRows joins rectangles of consecutive tile rows that cover exactly the
// same columns (a window being scrolled, a full refresh).
func mergeRows(in []Rect) []Rect {
	out := in[:0:0]
	for _, r := range in {
		merged := false
		for i := len(out) - 1; i >= 0; i-- {
			o := &out[i]
			if o.Y+o.H < r.Y {
				break
			}
			if o.X == r.X && o.W == r.W && o.Y+o.H == r.Y && o.H+r.H <= 4*TileSize {
				o.H += r.H
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, r)
		}
	}
	return out
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func encodeJPEG(s *Screen, r Rect, q int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, r.W, r.H))
	for y := range r.H {
		src := s.Pix[(r.Y+y)*s.Stride+r.X*4 : (r.Y+y)*s.Stride+(r.X+r.W)*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+r.W*4]
		for i := 0; i < len(src); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 0xff
		}
	}
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)
	_ = jpeg.Encode(buf, img, &jpeg.Options{Quality: q})
	return bytes.Clone(buf.Bytes())
}
