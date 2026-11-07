//go:build windows

package remote

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	pEnumDisplayMonitors        = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfo             = user32.NewProc("GetMonitorInfoW")
	pGetDC                      = user32.NewProc("GetDC")
	pReleaseDC                  = user32.NewProc("ReleaseDC")
	pOpenInputDesktop           = user32.NewProc("OpenInputDesktop")
	pGetUserObjectInformation   = user32.NewProc("GetUserObjectInformationW")
	pSetThreadDesktop           = user32.NewProc("SetThreadDesktop")
	pCloseDesktop               = user32.NewProc("CloseDesktop")
	pGetCursorInfo              = user32.NewProc("GetCursorInfo")
	pGetIconInfo                = user32.NewProc("GetIconInfo")
	pDrawIconEx                 = user32.NewProc("DrawIconEx")
	pSendInput                  = user32.NewProc("SendInput")
	pGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	pOpenClipboard              = user32.NewProc("OpenClipboard")
	pCloseClipboard             = user32.NewProc("CloseClipboard")
	pEmptyClipboard             = user32.NewProc("EmptyClipboard")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pSetClipboardData           = user32.NewProc("SetClipboardData")
	pGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	pCreateWindowEx             = user32.NewProc("CreateWindowExW")
	pPeekMessage                = user32.NewProc("PeekMessageW")
	pTranslateMessage           = user32.NewProc("TranslateMessage")
	pDispatchMessage            = user32.NewProc("DispatchMessageW")
	pSetProcessDpiAwarenessCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware         = user32.NewProc("SetProcessDPIAware")
	pCreateCompatibleDC         = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection           = gdi32.NewProc("CreateDIBSection")
	pSelectObject               = gdi32.NewProc("SelectObject")
	pBitBlt                     = gdi32.NewProc("BitBlt")
	pDeleteObject               = gdi32.NewProc("DeleteObject")
	pDeleteDC                   = gdi32.NewProc("DeleteDC")
	pGdiFlush                   = gdi32.NewProc("GdiFlush")
	pGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                 = kernel32.NewProc("GlobalLock")
	pGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	pGlobalFree                 = kernel32.NewProc("GlobalFree")
	pWTSQuerySessionInformation = wtsapi32.NewProc("WTSQuerySessionInformationW")
	pWTSFreeMemory              = wtsapi32.NewProc("WTSFreeMemory")
	monitorCallback             = windows.NewCallback(enumMonitor)
	monitorMu                   sync.Mutex
	monitorList                 []Monitor
	errNoDesktop                = errors.New("the input desktop is not available")
)

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

func enumMonitor(hmon, _ uintptr, _ *rect, _ uintptr) uintptr {
	mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if r, _, _ := pGetMonitorInfo.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return 1
	}
	name := windows.UTF16ToString(mi.Device[:])
	name = strings.TrimPrefix(name, `\\.\`)
	monitorList = append(monitorList, Monitor{Index: len(monitorList), Name: name, X: int(mi.Monitor.Left), Y: int(mi.Monitor.Top),
		Width: int(mi.Monitor.Right - mi.Monitor.Left), Height: int(mi.Monitor.Bottom - mi.Monitor.Top), Primary: mi.Flags&1 != 0})
	return 1
}

// SetDPIAware makes the process see physical pixels on every monitor; the
// helper calls it before anything else.
func SetDPIAware() {
	if pSetProcessDpiAwarenessCtx.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
		if r, _, _ := pSetProcessDpiAwarenessCtx.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	if pSetProcessDPIAware.Find() == nil {
		pSetProcessDPIAware.Call()
	}
}

// WindowsPlatform captures with GDI and injects input with SendInput. It
// follows the input desktop, so it also works on the sign-in screen and the
// UAC prompt when it runs as SYSTEM in the console session.
type WindowsPlatform struct {
	desk     uintptr
	deskName string

	memDC  uintptr
	bmp    uintptr
	oldBmp uintptr
	bits   unsafe.Pointer
	bw, bh int

	clip *clipboardThread
}

// NewWindowsPlatform creates the platform. Call it on the thread that will
// use it.
func NewWindowsPlatform() *WindowsPlatform {
	return &WindowsPlatform{clip: startClipboardThread()}
}

// Monitors lists the displays.
func (p *WindowsPlatform) Monitors() []Monitor {
	monitorMu.Lock()
	defer monitorMu.Unlock()
	monitorList = nil
	pEnumDisplayMonitors.Call(0, 0, monitorCallback, 0)
	// Primary first keeps "Monitor 1" the main screen, as in Windows.
	out := make([]Monitor, 0, len(monitorList))
	for _, m := range monitorList {
		if m.Primary {
			out = append(out, m)
		}
	}
	for _, m := range monitorList {
		if !m.Primary {
			out = append(out, m)
		}
	}
	for i := range out {
		out[i].Index = i
	}
	return out
}

const genericAll = 0x10000000

// Desktop follows the input desktop.
func (p *WindowsPlatform) Desktop() string {
	h, _, _ := pOpenInputDesktop.Call(0, 0, genericAll)
	if h == 0 {
		if p.deskName == "" {
			return "Default"
		}
		return p.deskName
	}
	var buf [128]uint16
	var need uint32
	pGetUserObjectInformation.Call(h, 2 /* UOI_NAME */, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&need)))
	name := windows.UTF16ToString(buf[:])
	if name == p.deskName && p.desk != 0 {
		pCloseDesktop.Call(h)
		return name
	}
	p.freeBitmap() // DCs belong to the old desktop
	if r, _, _ := pSetThreadDesktop.Call(h); r == 0 {
		pCloseDesktop.Call(h)
		if p.deskName == "" {
			p.deskName = name
		}
		return p.deskName
	}
	if p.desk != 0 {
		pCloseDesktop.Call(p.desk)
	}
	p.desk, p.deskName = h, name
	return name
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

func (p *WindowsPlatform) freeBitmap() {
	if p.memDC != 0 {
		if p.oldBmp != 0 {
			pSelectObject.Call(p.memDC, p.oldBmp)
		}
		pDeleteDC.Call(p.memDC)
	}
	if p.bmp != 0 {
		pDeleteObject.Call(p.bmp)
	}
	p.memDC, p.bmp, p.oldBmp, p.bits, p.bw, p.bh = 0, 0, 0, nil, 0, 0
}

func (p *WindowsPlatform) ensureBitmap(screenDC uintptr, w, h int) error {
	if p.memDC != 0 && p.bw == w && p.bh == h {
		return nil
	}
	p.freeBitmap()
	dc, _, _ := pCreateCompatibleDC.Call(screenDC)
	if dc == 0 {
		return errNoDesktop
	}
	bi := bitmapInfo{Header: bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		pDeleteDC.Call(dc)
		return errNoDesktop
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	p.memDC, p.bmp, p.oldBmp, p.bits, p.bw, p.bh = dc, bmp, old, bits, w, h
	return nil
}

type point struct{ X, Y int32 }

type cursorInfo struct {
	Size   uint32
	Flags  uint32
	Cursor uintptr
	Pos    point
}

type iconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

// Capture copies r of the virtual screen into dst, with the mouse pointer.
func (p *WindowsPlatform) Capture(r Rect, dst *Screen) error {
	screenDC, _, _ := pGetDC.Call(0)
	if screenDC == 0 {
		return errNoDesktop
	}
	defer pReleaseDC.Call(0, screenDC)
	if err := p.ensureBitmap(screenDC, r.W, r.H); err != nil {
		return err
	}
	const srcCopy = 0x00CC0020
	if ok, _, _ := pBitBlt.Call(p.memDC, 0, 0, uintptr(r.W), uintptr(r.H), screenDC, uintptr(int32(r.X)), uintptr(int32(r.Y)), srcCopy); ok == 0 {
		return errNoDesktop
	}
	ci := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if ok, _, _ := pGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); ok != 0 && ci.Flags&1 != 0 && ci.Cursor != 0 {
		var ii iconInfo
		hx, hy := int32(0), int32(0)
		if ok, _, _ := pGetIconInfo.Call(ci.Cursor, uintptr(unsafe.Pointer(&ii))); ok != 0 {
			hx, hy = int32(ii.XHotspot), int32(ii.YHotspot)
			if ii.Mask != 0 {
				pDeleteObject.Call(ii.Mask)
			}
			if ii.Color != 0 {
				pDeleteObject.Call(ii.Color)
			}
		}
		x, y := ci.Pos.X-int32(r.X)-hx, ci.Pos.Y-int32(r.Y)-hy
		pDrawIconEx.Call(p.memDC, uintptr(x), uintptr(y), ci.Cursor, 0, 0, 0, 0, 3 /* DI_NORMAL */)
	}
	pGdiFlush.Call()
	src := unsafe.Slice((*byte)(p.bits), r.W*r.H*4)
	for y := range r.H {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+r.W*4], src[y*r.W*4:(y+1)*r.W*4])
	}
	return nil
}

// INPUT for SendInput (40 bytes on 64-bit Windows).
type mouseInput struct {
	Type      uint32
	_         uint32
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyInput struct {
	Type      uint32
	_         uint32
	Vk, Scan  uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
	_         [8]byte
}

const (
	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseXDown       = 0x0080
	mouseXUp         = 0x0100
	mouseWheel       = 0x0800
	mouseHWheel      = 0x1000
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
	keyScancode = 0x0008
)

func metric(i uintptr) int32 {
	r, _, _ := pGetSystemMetrics.Call(i)
	return int32(r)
}

// Mouse moves, clicks or scrolls at an absolute position.
func (p *WindowsPlatform) Mouse(m MouseInput) {
	vx, vy, vw, vh := metric(76), metric(77), metric(78), metric(79)
	if vw < 2 || vh < 2 {
		return
	}
	in := mouseInput{Type: 0, Flags: mouseMove | mouseAbsolute | mouseVirtualDesk,
		Dx: int32((int64(int32(m.X)-vx)*65535 + int64(vw-1)/2) / int64(vw-1)),
		Dy: int32((int64(int32(m.Y)-vy)*65535 + int64(vh-1)/2) / int64(vh-1))}
	switch m.Action {
	case MouseDown, MouseUp:
		down := m.Action == MouseDown
		switch m.Button {
		case ButtonLeft:
			in.Flags |= pick(down, mouseLeftDown, mouseLeftUp)
		case ButtonRight:
			in.Flags |= pick(down, mouseRightDown, mouseRightUp)
		case ButtonMiddle:
			in.Flags |= pick(down, mouseMiddleDown, mouseMiddleUp)
		case ButtonBack, ButtonFwd:
			in.Flags |= pick(down, mouseXDown, mouseXUp)
			in.MouseData = uint32(m.Button - ButtonBack + 1)
		}
	case MouseWheel:
		in.Flags |= mouseWheel
		in.MouseData = uint32(int32(m.Wheel))
	case MouseHWhel:
		in.Flags |= mouseHWheel
		in.MouseData = uint32(int32(m.Wheel))
	}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func pick(c bool, a, b uint32) uint32 {
	if c {
		return a
	}
	return b
}

// Key presses or releases a key by scan code, so the remote keyboard layout
// applies.
func (p *WindowsPlatform) Key(k KeyInput) {
	in := keyInput{Type: 1, Scan: k.Scan & 0xff, Flags: keyScancode}
	if k.Scan&0xff00 == 0xe000 {
		in.Flags |= keyExtended
	}
	if !k.Down {
		in.Flags |= keyUp
	}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// Type enters text as Unicode characters.
func (p *WindowsPlatform) Type(text string) {
	var ins []keyInput
	scan := func(sc uint16) {
		ins = append(ins, keyInput{Type: 1, Scan: sc, Flags: keyScancode}, keyInput{Type: 1, Scan: sc, Flags: keyScancode | keyUp})
	}
	for _, u := range windows.StringToUTF16(text) {
		switch u {
		case 0, '\r':
			continue
		case '\n':
			scan(0x1c)
		case '\t':
			scan(0x0f)
		default:
			ins = append(ins, keyInput{Type: 1, Scan: u, Flags: keyUnicode}, keyInput{Type: 1, Scan: u, Flags: keyUnicode | keyUp})
		}
	}
	for len(ins) > 0 {
		n := min(len(ins), 64)
		pSendInput.Call(uintptr(n), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
		ins = ins[n:]
	}
}

// User is the user of this session.
func (p *WindowsPlatform) User() string {
	var buf *uint16
	var n uint32
	const currentSession = 0xFFFFFFFF
	if r, _, _ := pWTSQuerySessionInformation.Call(0, currentSession, 5 /* WTSUserName */, uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n))); r == 0 || buf == nil {
		return ""
	}
	defer pWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}

// ClipboardSeq changes whenever the clipboard does.
func (p *WindowsPlatform) ClipboardSeq() uint32 {
	r, _, _ := pGetClipboardSequenceNumber.Call()
	return uint32(r)
}

// Clipboard reads the clipboard text.
func (p *WindowsPlatform) Clipboard() (string, error) {
	var text string
	err := p.clip.do(func(hwnd uintptr) error {
		if !openClipboard(hwnd) {
			return errors.New("clipboard busy")
		}
		defer pCloseClipboard.Call()
		h, _, _ := pGetClipboardData.Call(13 /* CF_UNICODETEXT */)
		if h == 0 {
			return nil
		}
		ptr, _, _ := pGlobalLock.Call(h)
		if ptr == 0 {
			return nil
		}
		defer pGlobalUnlock.Call(h)
		text = windows.UTF16PtrToString((*uint16)(ptrOf(ptr)))
		return nil
	})
	return text, err
}

// SetClipboard puts text on the clipboard.
func (p *WindowsPlatform) SetClipboard(text string) error {
	return p.clip.do(func(hwnd uintptr) error {
		u := windows.StringToUTF16(text)
		size := uintptr(len(u) * 2)
		h, _, _ := pGlobalAlloc.Call(0x0002 /* GMEM_MOVEABLE */, size)
		if h == 0 {
			return errors.New("out of memory")
		}
		ptr, _, _ := pGlobalLock.Call(h)
		if ptr == 0 {
			pGlobalFree.Call(h)
			return errors.New("out of memory")
		}
		copy(unsafe.Slice((*uint16)(ptrOf(ptr)), len(u)), u)
		pGlobalUnlock.Call(h)
		if !openClipboard(hwnd) {
			pGlobalFree.Call(h)
			return errors.New("clipboard busy")
		}
		defer pCloseClipboard.Call()
		pEmptyClipboard.Call()
		if r, _, _ := pSetClipboardData.Call(13, h); r == 0 {
			pGlobalFree.Call(h)
			return errors.New("set clipboard failed")
		}
		return nil
	})
}

// ptrOf turns an address returned by a Windows API (global memory that the
// Go garbage collector does not manage) into a pointer.
func ptrOf(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func openClipboard(hwnd uintptr) bool {
	for range 10 {
		if r, _, _ := pOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		windows.SleepEx(20, false)
	}
	return false
}

// clipboardThread owns a message-only window: setting the clipboard needs
// an owner window, and a thread with windows cannot switch desktops, so it
// is not the capture thread. It keeps pumping messages so other programs
// that empty the clipboard do not wait for it.
type clipboardThread struct {
	req  chan func(hwnd uintptr)
	hwnd uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

func startClipboardThread() *clipboardThread {
	c := &clipboardThread{req: make(chan func(uintptr))}
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		cls, _ := windows.UTF16PtrFromString("STATIC")
		const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3)
		c.hwnd, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
		close(ready)
		var m msg
		for {
			for {
				if r, _, _ := pPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1 /* PM_REMOVE */); r == 0 {
					break
				}
				pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
			}
			select {
			case f := <-c.req:
				f(c.hwnd)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	<-ready
	return c
}

func (c *clipboardThread) do(f func(hwnd uintptr) error) error {
	done := make(chan error, 1)
	c.req <- func(h uintptr) { done <- f(h) }
	return <-done
}
