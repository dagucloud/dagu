// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"unicode/utf16"

	"github.com/ebitengine/purego"
)

const (
	coreGraphicsPath        = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationPath      = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	applicationServicesPath = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"
	screencapturePath       = "/usr/sbin/screencapture"
)

// Quartz event constants.
const (
	hidEventTap = 0

	eventLeftMouseDown     = 1
	eventLeftMouseUp       = 2
	eventRightMouseDown    = 3
	eventRightMouseUp      = 4
	eventMouseMoved        = 5
	eventLeftMouseDragged  = 6
	eventRightMouseDragged = 7
	eventOtherMouseDown    = 25
	eventOtherMouseUp      = 26
	eventOtherMouseDragged = 27

	mouseButtonLeft   = 0
	mouseButtonRight  = 1
	mouseButtonCenter = 2

	mouseEventClickState = 1
	scrollUnitLine       = 1
	// maxUnicodeChunk is how many UTF-16 units one keyboard event carries.
	maxUnicodeChunk = 20
)

// Modifier flags carried by every posted event.
const (
	flagShift   uint64 = 0x00020000
	flagControl uint64 = 0x00040000
	flagOption  uint64 = 0x00080000
	flagCommand uint64 = 0x00100000
)

type cgPoint struct {
	X float64
	Y float64
}

type cgSize struct {
	Width  float64
	Height float64
}

type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

// Quartz functions, bound when the libraries load.
var (
	cgMainDisplayID                 func() uint32
	cgDisplayBounds                 func(display uint32) cgRect
	cgDisplayCopyDisplayMode        func(display uint32) uintptr
	cgDisplayModeGetPixelWidth      func(mode uintptr) uintptr
	cgDisplayModeRelease            func(mode uintptr)
	cgEventCreate                   func(source uintptr) uintptr
	cgEventGetLocation              func(event uintptr) cgPoint
	cgEventCreateMouseEvent         func(source uintptr, eventType uint32, at cgPoint, button uint32) uintptr
	cgEventCreateKeyboardEvent      func(source uintptr, keycode uint16, down bool) uintptr
	cgEventCreateScrollWheelEvent2  func(source uintptr, units uint32, count uint32, wheel1, wheel2, wheel3 int32) uintptr
	cgEventKeyboardSetUnicodeString func(event uintptr, length uintptr, text *uint16)
	cgEventSetIntegerValueField     func(event uintptr, field uint32, value int64)
	cgEventSetFlags                 func(event uintptr, flags uint64)
	cgEventPost                     func(tap uint32, event uintptr)
	cgPreflightScreenCaptureAccess  func() bool
	cgRequestScreenCaptureAccess    func() bool
	cgSessionCopyCurrentDictionary  func() uintptr
	cfRelease                       func(ref uintptr)
	axIsProcessTrusted              func() bool
)

var (
	loadOnce sync.Once
	errLoad  error
)

func load() error {
	loadOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			errLoad = fmt.Errorf("load CoreGraphics: %w", err)
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			errLoad = fmt.Errorf("load CoreFoundation: %w", err)
			return
		}
		as, err := purego.Dlopen(applicationServicesPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			errLoad = fmt.Errorf("load ApplicationServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgMainDisplayID, cg, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
		purego.RegisterLibFunc(&cgDisplayCopyDisplayMode, cg, "CGDisplayCopyDisplayMode")
		purego.RegisterLibFunc(&cgDisplayModeGetPixelWidth, cg, "CGDisplayModeGetPixelWidth")
		purego.RegisterLibFunc(&cgDisplayModeRelease, cg, "CGDisplayModeRelease")
		purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventGetLocation, cg, "CGEventGetLocation")
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, cg, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventCreateScrollWheelEvent2, cg, "CGEventCreateScrollWheelEvent2")
		purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, cg, "CGEventKeyboardSetUnicodeString")
		purego.RegisterLibFunc(&cgEventSetIntegerValueField, cg, "CGEventSetIntegerValueField")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, cg, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgRequestScreenCaptureAccess, cg, "CGRequestScreenCaptureAccess")
		purego.RegisterLibFunc(&cgSessionCopyCurrentDictionary, cg, "CGSessionCopyCurrentDictionary")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	})
	return errLoad
}

// Open returns a driver for the desktop of the current session.
func Open() (*Driver, error) {
	if err := Check().Err(); err != nil {
		return nil, err
	}
	backend, err := newDarwinBackend()
	if err != nil {
		return nil, err
	}
	return New(backend), nil
}

// Check reports whether the desktop of the current session can be automated.
func Check() Diagnostics {
	diag := Diagnostics{OS: runtime.GOOS}
	if err := load(); err != nil {
		diag.Problems = append(diag.Problems, err.Error())
		return diag
	}
	session := cgSessionCopyCurrentDictionary()
	if session == 0 {
		diag.Problems = append(diag.Problems, "no graphical login session; run the worker in a logged-in user session")
		return diag
	}
	cfRelease(session)
	if display, err := mainDisplay(); err == nil {
		diag.Width, diag.Height = display.pixelWidth, display.pixelHeight
	}
	if !cgPreflightScreenCaptureAccess() {
		diag.Problems = append(diag.Problems, "Screen Recording permission is missing; grant it in System Settings > Privacy & Security > Screen Recording")
	}
	if !axIsProcessTrusted() {
		diag.Problems = append(diag.Problems, "Accessibility permission is missing; grant it in System Settings > Privacy & Security > Accessibility")
	}
	return diag
}

// RequestPermissions asks macOS to show its Screen Recording prompt for the
// current process. Accessibility must be granted in System Settings.
func RequestPermissions() {
	if load() == nil && !cgPreflightScreenCaptureAccess() {
		cgRequestScreenCaptureAccess()
	}
}

// display describes the main display in points and pixels.
type display struct {
	bounds      cgRect
	pixelWidth  int
	pixelHeight int
	// scale is pixels per point; 2 on Retina displays.
	scale float64
}

func mainDisplay() (display, error) {
	id := cgMainDisplayID()
	bounds := cgDisplayBounds(id)
	if bounds.Size.Width <= 0 || bounds.Size.Height <= 0 {
		return display{}, errors.New("main display size is unavailable")
	}
	scale := 1.0
	if mode := cgDisplayCopyDisplayMode(id); mode != 0 {
		if pixels := cgDisplayModeGetPixelWidth(mode); pixels > 0 {
			scale = float64(pixels) / bounds.Size.Width
		}
		cgDisplayModeRelease(mode)
	}
	return display{
		bounds:      bounds,
		pixelWidth:  int(bounds.Size.Width * scale),
		pixelHeight: int(bounds.Size.Height * scale),
		scale:       scale,
	}, nil
}

// darwinBackend drives the desktop through Quartz events. Positions are
// screenshot pixels, which Quartz measures in points.
type darwinBackend struct {
	display display
	// buttons and flags track held buttons and modifiers, so moves become
	// drags and every event carries the held modifiers.
	buttons map[Button]bool
	flags   uint64
}

func newDarwinBackend() (*darwinBackend, error) {
	display, err := mainDisplay()
	if err != nil {
		return nil, err
	}
	return &darwinBackend{display: display, buttons: map[Button]bool{}}, nil
}

// Capture uses the screencapture tool, which keeps working across macOS
// releases that retire the Quartz capture functions.
func (b *darwinBackend) Capture() (*image.RGBA, error) {
	dir, err := os.MkdirTemp("", "dagu-screen-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "screen.png")
	if out, err := exec.Command(screencapturePath, "-x", "-m", "-t", "png", path).CombinedOutput(); err != nil { //nolint:gosec // fixed tool and a private temporary path
		return nil, fmt.Errorf("screencapture: %w: %s", err, out)
	}
	file, err := os.Open(path) //nolint:gosec // private temporary path
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	decoded, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	if rgba, ok := decoded.(*image.RGBA); ok {
		return rgba, nil
	}
	rgba := image.NewRGBA(decoded.Bounds())
	draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return rgba, nil
}

func (b *darwinBackend) toPoints(x, y int) cgPoint {
	return cgPoint{
		X: b.display.bounds.Origin.X + float64(x)/b.display.scale,
		Y: b.display.bounds.Origin.Y + float64(y)/b.display.scale,
	}
}

func (b *darwinBackend) location() cgPoint {
	event := cgEventCreate(0)
	defer cfRelease(event)
	return cgEventGetLocation(event)
}

func (b *darwinBackend) MoveTo(x, y int) error {
	eventType, button := uint32(eventMouseMoved), uint32(mouseButtonLeft)
	switch {
	case b.buttons[ButtonLeft]:
		eventType = eventLeftMouseDragged
	case b.buttons[ButtonRight]:
		eventType, button = eventRightMouseDragged, mouseButtonRight
	case b.buttons[ButtonMiddle]:
		eventType, button = eventOtherMouseDragged, mouseButtonCenter
	}
	return b.post(cgEventCreateMouseEvent(0, eventType, b.toPoints(x, y), button))
}

func (b *darwinBackend) Position() (int, int, error) {
	at := b.location()
	x := (at.X - b.display.bounds.Origin.X) * b.display.scale
	y := (at.Y - b.display.bounds.Origin.Y) * b.display.scale
	return int(x), int(y), nil
}

func (b *darwinBackend) Button(button Button, down bool, clicks int) error {
	var eventType, number uint32
	switch button {
	case ButtonLeft:
		eventType, number = pick[uint32](down, eventLeftMouseDown, eventLeftMouseUp), mouseButtonLeft
	case ButtonRight:
		eventType, number = pick[uint32](down, eventRightMouseDown, eventRightMouseUp), mouseButtonRight
	case ButtonMiddle:
		eventType, number = pick[uint32](down, eventOtherMouseDown, eventOtherMouseUp), mouseButtonCenter
	default:
		return fmt.Errorf("unknown mouse button %q", button)
	}
	event := cgEventCreateMouseEvent(0, eventType, b.location(), number)
	if event == 0 {
		return errors.New("create mouse event")
	}
	// macOS recognizes double and triple clicks by the click state.
	cgEventSetIntegerValueField(event, mouseEventClickState, int64(max(clicks, 1)))
	b.buttons[button] = down
	return b.post(event)
}

func (b *darwinBackend) Wheel(dx, dy int) error {
	// Positive wheel values scroll up and left.
	return b.post(cgEventCreateScrollWheelEvent2(0, scrollUnitLine, 2, int32(-dy), int32(-dx), 0)) //nolint:gosec // notch counts are small
}

func (b *darwinBackend) Key(key Key, down bool) error {
	if flag, ok := modifierFlags[key]; ok {
		if down {
			b.flags |= flag
		} else {
			b.flags &^= flag
		}
	}
	code, shift, err := keyCode(key)
	if err != nil {
		return err
	}
	if !shift {
		return b.post(cgEventCreateKeyboardEvent(0, code, down))
	}
	// The character needs Shift on a US layout.
	if down {
		b.flags |= flagShift
		return b.post(cgEventCreateKeyboardEvent(0, code, true))
	}
	err = b.post(cgEventCreateKeyboardEvent(0, code, false))
	b.flags &^= flagShift
	return err
}

func (b *darwinBackend) Type(text string) error {
	units := utf16.Encode([]rune(text))
	for start := 0; start < len(units); start += maxUnicodeChunk {
		chunk := units[start:min(start+maxUnicodeChunk, len(units))]
		for _, down := range []bool{true, false} {
			event := cgEventCreateKeyboardEvent(0, 0, down)
			if event == 0 {
				return errors.New("create keyboard event")
			}
			cgEventKeyboardSetUnicodeString(event, uintptr(len(chunk)), &chunk[0])
			if err := b.post(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *darwinBackend) Close() error {
	return nil
}

// post sends an event with the held modifiers and releases it.
func (b *darwinBackend) post(event uintptr) error {
	if event == 0 {
		return errors.New("create input event")
	}
	defer cfRelease(event)
	cgEventSetFlags(event, b.flags)
	cgEventPost(hidEventTap, event)
	return nil
}

var modifierFlags = map[Key]uint64{
	KeyShift: flagShift, KeyCtrl: flagControl, KeyAlt: flagOption, KeyMeta: flagCommand,
}

// namedKeyCodes are macOS virtual key codes of named keys.
var namedKeyCodes = map[Key]uint16{
	KeyShift: 0x38, KeyCtrl: 0x3B, KeyAlt: 0x3A, KeyMeta: 0x37,
	KeyEnter: 0x24, KeyTab: 0x30, KeyEscape: 0x35, KeyBackspace: 0x33, KeyDelete: 0x75,
	KeySpace: 0x31, KeyInsert: 0x72, KeyHome: 0x73, KeyEnd: 0x77, KeyPageUp: 0x74, KeyPageDown: 0x79,
	KeyUp: 0x7E, KeyDown: 0x7D, KeyLeft: 0x7B, KeyRight: 0x7C, KeyCapsLock: 0x39,
}

// functionKeyCodes are the key codes of F1 to F20.
var functionKeyCodes = []uint16{
	0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D,
	0x67, 0x6F, 0x69, 0x6B, 0x71, 0x6A, 0x40, 0x4F, 0x50, 0x5A,
}

// usKeyCodes are the key codes of characters on a US ANSI keyboard.
var usKeyCodes = map[rune]uint16{
	'a': 0x00, 's': 0x01, 'd': 0x02, 'f': 0x03, 'h': 0x04, 'g': 0x05, 'z': 0x06, 'x': 0x07,
	'c': 0x08, 'v': 0x09, 'b': 0x0B, 'q': 0x0C, 'w': 0x0D, 'e': 0x0E, 'r': 0x0F, 'y': 0x10,
	't': 0x11, '1': 0x12, '2': 0x13, '3': 0x14, '4': 0x15, '6': 0x16, '5': 0x17, '=': 0x18,
	'9': 0x19, '7': 0x1A, '-': 0x1B, '8': 0x1C, '0': 0x1D, ']': 0x1E, 'o': 0x1F, 'u': 0x20,
	'[': 0x21, 'i': 0x22, 'p': 0x23, 'l': 0x25, 'j': 0x26, '\'': 0x27, 'k': 0x28, ';': 0x29,
	'\\': 0x2A, ',': 0x2B, '/': 0x2C, 'n': 0x2D, 'm': 0x2E, '.': 0x2F, '`': 0x32,
}

// keyCode returns the virtual key code of a key and whether the character
// needs Shift.
func keyCode(key Key) (code uint16, shift bool, err error) {
	if code, ok := namedKeyCodes[key]; ok {
		return code, false, nil
	}
	if n, ok := key.FunctionNumber(); ok {
		if n > len(functionKeyCodes) {
			return 0, false, fmt.Errorf("macOS has no key %q", key)
		}
		return functionKeyCodes[n-1], false, nil
	}
	if base, shift, ok := key.USKey(); ok {
		if code, ok := usKeyCodes[base]; ok {
			return code, shift, nil
		}
	}
	return 0, false, fmt.Errorf("unsupported key %q", key)
}
