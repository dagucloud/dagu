// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"errors"
	"fmt"
	"image"
	"io"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procPostThreadMessageW       = user32.NewProc("PostThreadMessageW")

	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procTextOutW              = gdi32.NewProc("TextOutW")
	procGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	procGdiFlush              = gdi32.NewProc("GdiFlush")
)

const (
	markClassName = "DaguActionMark"
	markFontName  = "Segoe UI"
	// markFontPixels is the height of a label's text at baseDPI.
	markFontPixels = 15
	baseDPI        = 96

	// A mark's window is a layered pop-up that lets every click through,
	// is never activated, stays off the taskbar, and stays above other
	// windows.
	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExNoActivate  = 0x08000000
	wsExTopmost     = 0x00000008
	markExStyle     = wsExLayered | wsExTransparent | wsExNoActivate | wsExToolWindow | wsExTopmost

	// wdaExcludeFromCapture is WDA_EXCLUDEFROMCAPTURE, which Windows 10
	// version 2004 and later accept.
	wdaExcludeFromCapture = 0x00000011

	swHide        = 0
	swpNoMove     = 0x0002
	swpShowWindow = 0x0040
	// hwndTopmost is HWND_TOPMOST, (HWND)-1.
	hwndTopmost = ^uintptr(0)

	ulwAlpha   = 0x00000002
	acSrcOver  = 0
	acSrcAlpha = 1

	// wmMark asks a mark's thread for the command waiting for it, and
	// wmQuit ends its message loop.
	wmMark = 0x8000 + 1
	wmQuit = 0x0012

	// excludeFromCaptureBuild is Windows 10 version 2004. An older Windows
	// takes WDA_EXCLUDEFROMCAPTURE for WDA_MONITOR, which captures the
	// window as a black box rather than leaving it out.
	excludeFromCaptureBuild = 19041

	bkTransparent      = 1
	fwSemibold         = 600
	defaultCharset     = 1
	antialiasedQuality = 4
	whiteText          = 0x00FFFFFF

	errorClassAlreadyExists = 1410

	// windowReply bounds how long a mark's window may take to show or hide
	// a mark, so one that hangs cannot hold the actions up for long.
	windowReply = time.Second
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// winMsg is MSG.
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// winSize is SIZE.
type winSize struct {
	CX int32
	CY int32
}

// blendFunction is BLENDFUNCTION.
type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

var (
	markClassOnce sync.Once
	markClass     *uint16
	markInstance  windows.Handle
	errMarkClass  error
)

// OpenMarks opens marks drawn on the desktop of the current session, in a
// window Windows leaves out of every capture of the screen. It fails on a
// Windows that cannot leave a window out, one older than Windows 10 version
// 2004.
func OpenMarks() (Marks, error) {
	makeDPIAware()
	s, err := openWindowSurface(true)
	if err != nil {
		return nil, err
	}
	return newMarks(s), nil
}

// ServeMarks reports that Windows needs no helper process: a step draws
// its marks itself.
func ServeMarks(io.Reader, io.Writer) error {
	return errNoMarksHelper
}

// windowSurface draws marks in a window of its own, which the thread that
// made it serves until it closes, as Windows requires.
type windowSurface struct {
	hwnd     uintptr
	thread   uint32
	commands chan markCommand
}

// markCommand asks a mark's thread to show a mark, to hide the one shown
// when mark is nil, or to end.
type markCommand struct {
	mark *Mark
	quit bool
	done chan error
}

// openWindowSurface starts the thread of a mark's window. exclude leaves
// the window out of screen captures.
func openWindowSurface(exclude bool) (*windowSurface, error) {
	s := &windowSurface{commands: make(chan markCommand, 1)}
	ready := make(chan error, 1)
	go s.serve(exclude, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return s, nil
}

// serve makes the window and runs its message loop. The thread is never
// unlocked, so it ends with the loop, and the window's thread state with it.
func (s *windowSurface) serve(exclude bool, ready chan<- error) {
	runtime.LockOSThread()
	hwnd, err := createMarkWindow(exclude)
	if err != nil {
		ready <- err
		return
	}
	painter, err := newMarkPainter(hwnd)
	if err != nil {
		_, _, _ = procDestroyWindow.Call(hwnd)
		ready <- err
		return
	}
	defer painter.close()
	s.hwnd, s.thread = hwnd, windows.GetCurrentThreadId()
	ready <- nil
	var msg winMsg
	for {
		got, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0) //nolint:gosec // Win32 takes the MSG address as uintptr
		if int32(got) <= 0 {                                                      //nolint:gosec // GetMessage returns a BOOL that is -1 on failure
			return
		}
		if msg.Message != wmMark {
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg))) //nolint:gosec // Win32 takes the MSG address as uintptr
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg))) //nolint:gosec // Win32 takes the MSG address as uintptr
			continue
		}
		select {
		case command := <-s.commands:
			if command.quit {
				_, _, _ = procDestroyWindow.Call(hwnd)
				command.done <- nil
				return
			}
			command.done <- painter.paint(command.mark)
		default:
		}
	}
}

// send hands a command to the window's thread and waits for it, up to
// windowReply.
func (s *windowSurface) send(command markCommand) error {
	command.done = make(chan error, 1)
	select {
	case s.commands <- command:
	default:
		return errors.New("the mark's window is still busy with an earlier mark")
	}
	if ok, _, err := procPostMessageW.Call(s.hwnd, wmMark, 0, 0); ok == 0 {
		return fmt.Errorf("PostMessageW: %w", err)
	}
	select {
	case err := <-command.done:
		return err
	case <-time.After(windowReply):
		return fmt.Errorf("the mark's window did not answer within %s", windowReply)
	}
}

func (s *windowSurface) draw(m Mark) error { return s.send(markCommand{mark: &m}) }

func (s *windowSurface) clear() error { return s.send(markCommand{}) }

// close ends the window's thread. One that does not take the command, as
// when an earlier one is still waiting, is told to quit its message loop,
// which ends the thread and so its window.
func (s *windowSurface) close() error {
	if err := s.send(markCommand{quit: true}); err != nil {
		_, _, _ = procPostThreadMessageW.Call(uintptr(s.thread), wmQuit, 0, 0)
		return err
	}
	return nil
}

// createMarkWindow makes a mark's window, hidden until it shows a mark.
func createMarkWindow(exclude bool) (uintptr, error) {
	if build := windows.RtlGetVersion().BuildNumber; exclude && build < excludeFromCaptureBuild {
		return 0, fmt.Errorf("Windows build %d cannot leave marks out of screen captures; that takes build %d, Windows 10 version 2004", build, excludeFromCaptureBuild)
	}
	class, instance, err := registerMarkClass()
	if err != nil {
		return 0, err
	}
	hwnd, _, err := procCreateWindowExW.Call(markExStyle, uintptr(unsafe.Pointer(class)), 0, wsPopup, 0, 0, 0, 0, 0, 0, uintptr(instance), 0) //nolint:gosec // Win32 takes the class name address as uintptr
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", err)
	}
	if exclude {
		if ok, _, err := procSetWindowDisplayAffinity.Call(hwnd, wdaExcludeFromCapture); ok == 0 {
			_, _, _ = procDestroyWindow.Call(hwnd)
			return 0, fmt.Errorf("SetWindowDisplayAffinity: %w; this version of Windows cannot leave marks out of screen captures", err)
		}
	}
	return hwnd, nil
}

// registerMarkClass registers the window class of marks once. Its windows
// handle no message themselves.
func registerMarkClass() (*uint16, windows.Handle, error) {
	markClassOnce.Do(func() {
		name, err := windows.UTF16PtrFromString(markClassName)
		if err != nil {
			errMarkClass = err
			return
		}
		var instance windows.Handle
		if err := windows.GetModuleHandleEx(0, nil, &instance); err != nil {
			errMarkClass = fmt.Errorf("GetModuleHandleExW: %w", err)
			return
		}
		class := wndClassEx{WndProc: procDefWindowProcW.Addr(), Instance: instance, ClassName: name}
		class.Size = uint32(unsafe.Sizeof(class))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 && !errors.Is(err, windows.Errno(errorClassAlreadyExists)) { //nolint:gosec // Win32 takes the struct address as uintptr
			errMarkClass = fmt.Errorf("RegisterClassExW: %w", err)
			return
		}
		markClass, markInstance = name, instance
	})
	return markClass, markInstance, errMarkClass
}

// markPainter paints marks into a window, with a memory device context that
// holds the label's font.
type markPainter struct {
	hwnd uintptr
	dc   uintptr
	font uintptr
	dpi  int
}

func newMarkPainter(hwnd uintptr) (*markPainter, error) {
	dc, _, err := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	p := &markPainter{hwnd: hwnd, dc: dc, dpi: baseDPI}
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi > 0 {
			p.dpi = int(dpi)
		}
	}
	face, err := windows.UTF16PtrFromString(markFontName)
	if err != nil {
		p.close()
		return nil, err
	}
	height := -int32(p.scaled(markFontPixels))                                                                                                                         //nolint:gosec // a font height fits int32
	p.font, _, err = procCreateFontW.Call(signedArg(height), 0, 0, 0, fwSemibold, 0, 0, 0, defaultCharset, 0, 0, antialiasedQuality, 0, uintptr(unsafe.Pointer(face))) //nolint:gosec // Win32 takes the face name address as uintptr
	if p.font == 0 {
		p.close()
		return nil, fmt.Errorf("CreateFontW: %w", err)
	}
	_, _, _ = procSelectObject.Call(dc, p.font)
	_, _, _ = procSetBkMode.Call(dc, bkTransparent)
	_, _, _ = procSetTextColor.Call(dc, whiteText)
	return p, nil
}

// scaled converts pixels at baseDPI to the window's display.
func (p *markPainter) scaled(pixels int) int {
	return pixels * p.dpi / baseDPI
}

// paint shows a mark, or hides the window when mark is nil.
func (p *markPainter) paint(mark *Mark) error {
	if mark == nil {
		_, _, _ = procShowWindow.Call(p.hwnd, swHide)
		return nil
	}
	text := utf16.Encode([]rune(mark.Label))
	if len(text) == 0 {
		text = []uint16{' '}
	}
	var extent winSize
	if ok, _, err := procGetTextExtentPoint32W.Call(p.dc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), uintptr(unsafe.Pointer(&extent))); ok == 0 { //nolint:gosec // Win32 takes buffer addresses as uintptr
		return fmt.Errorf("GetTextExtentPoint32W: %w", err)
	}
	pad, border := p.scaled(markPadding), p.scaled(markBorder)
	width, height := screenSize()
	window, frame, box := markLayout(mark.Target, image.Rect(0, 0, width, height), image.Pt(int(extent.CX)+2*pad, int(extent.CY)+pad), border)
	if window.Empty() {
		_, _, _ = procShowWindow.Call(p.hwnd, swHide)
		return nil
	}

	info := bitmapInfo{
		Width:    int32(window.Dx()),  //nolint:gosec // screen sizes fit int32
		Height:   -int32(window.Dy()), //nolint:gosec // negative height requests top-down rows
		Planes:   1,
		BitCount: 32,
	}
	info.Size = uint32(unsafe.Sizeof(info) - unsafe.Sizeof(info.Colors))
	var bits unsafe.Pointer
	bitmap, _, err := procCreateDIBSection.Call(p.dc, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0) //nolint:gosec // Win32 takes buffer addresses as uintptr
	if bitmap == 0 || bits == nil {
		return fmt.Errorf("CreateDIBSection: %w", err)
	}
	defer func() { _, _, _ = procDeleteObject.Call(bitmap) }()
	previous, _, _ := procSelectObject.Call(p.dc, bitmap)
	defer func() { _, _, _ = procSelectObject.Call(p.dc, previous) }()

	// The bitmap holds premultiplied BGRA rows, transparent where nothing
	// is painted.
	pix := unsafe.Slice((*byte)(bits), window.Dx()*window.Dy()*4) //nolint:gosec // the section CreateDIBSection made holds these rows
	fill := func(r image.Rectangle) {
		eachPixel(window, r, func(i int) {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = mark.Color.B, mark.Color.G, mark.Color.R, 0xFF
		})
	}
	inner := frame.Inset(border)
	fill(image.Rect(frame.Min.X, frame.Min.Y, frame.Max.X, inner.Min.Y))
	fill(image.Rect(frame.Min.X, inner.Max.Y, frame.Max.X, frame.Max.Y))
	fill(image.Rect(frame.Min.X, inner.Min.Y, inner.Min.X, inner.Max.Y))
	fill(image.Rect(inner.Max.X, inner.Min.Y, frame.Max.X, inner.Max.Y))
	fill(box)
	origin := box.Min.Sub(window.Min).Add(image.Pt(pad, pad/2))
	_, _, _ = procTextOutW.Call(p.dc, uintptr(origin.X), uintptr(origin.Y), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text))) //nolint:gosec // Win32 takes the text address as uintptr
	// GDI may batch the text, and clears the alpha of the pixels it draws;
	// the label's box is opaque throughout.
	_, _, _ = procGdiFlush.Call()
	eachPixel(window, box, func(i int) { pix[i+3] = 0xFF })

	dst := point{X: int32(window.Min.X), Y: int32(window.Min.Y)}    //nolint:gosec // screen positions fit int32
	size := winSize{CX: int32(window.Dx()), CY: int32(window.Dy())} //nolint:gosec // screen sizes fit int32
	var src point
	blend := blendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 0xFF, AlphaFormat: acSrcAlpha}
	if ok, _, err := procUpdateLayeredWindow.Call(p.hwnd, 0, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)), p.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha); ok == 0 { //nolint:gosec // Win32 takes struct addresses as uintptr
		return fmt.Errorf("UpdateLayeredWindow: %w", err)
	}
	// Shown above every other window, a topmost one put there since too.
	if ok, _, err := procSetWindowPos.Call(p.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow); ok == 0 {
		return fmt.Errorf("SetWindowPos: %w", err)
	}
	return nil
}

// eachPixel calls fn with the offset in a BGRA bitmap of window of every
// pixel of r.
func eachPixel(window, r image.Rectangle, fn func(i int)) {
	r = r.Intersect(window)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			fn(((y-window.Min.Y)*window.Dx() + x - window.Min.X) * 4)
		}
	}
}

func (p *markPainter) close() {
	_, _, _ = procDeleteDC.Call(p.dc)
	if p.font != 0 {
		_, _, _ = procDeleteObject.Call(p.font)
	}
}
