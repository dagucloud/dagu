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
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	coreGraphicsPath        = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationPath      = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	applicationServicesPath = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"
	ioKitPath               = "/System/Library/Frameworks/IOKit.framework/IOKit"
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

	// cfStringEncodingUTF8 is kCFStringEncodingUTF8.
	cfStringEncodingUTF8 = 0x08000100
	// Keys of the login session dictionary. The lock key is present only
	// while the screen is locked.
	sessionOnConsoleKey    = "kCGSSessionOnConsoleKey"
	sessionScreenLockedKey = "CGSSessionScreenIsLocked"

	// Power assertions that keep the display and the system awake, at
	// kIOPMAssertionLevelOn.
	assertionDisplayAwake = "PreventUserIdleDisplaySleep"
	assertionSystemAwake  = "PreventUserIdleSystemSleep"
	assertionLevelOn      = 255

	// axTrustedCheckOptionPrompt is kAXTrustedCheckOptionPrompt.
	axTrustedCheckOptionPrompt = "AXTrustedCheckOptionPrompt"

	// Arguments that make CGEventSourceSecondsSinceLastEventType report
	// any input the window server received.
	eventSourceStateHIDSystem = 1
	anyInputEventType         = 0xFFFFFFFF

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
	cgEventSourceSecondsSinceLast   func(state int32, eventType uint32) float64
	cgPreflightScreenCaptureAccess  func() bool
	cgRequestScreenCaptureAccess    func() bool
	cgSessionCopyCurrentDictionary  func() uintptr
	cfRelease                       func(ref uintptr)
	cfStringCreateWithCString       func(alloc uintptr, text string, encoding uint32) uintptr
	cfDictionaryGetValue            func(dict, key uintptr) uintptr
	cfBooleanGetValue               func(boolean uintptr) bool
	cfDictionaryCreate              func(alloc uintptr, keys, values *uintptr, count int, keyCallBacks, valueCallBacks uintptr) uintptr
	axIsProcessTrustedWithOptions   func(options uintptr) bool
	axIsProcessTrusted              func() bool
	ioPMAssertionCreateWithName     func(kind uintptr, level uint32, name uintptr, id *uint32) int32
	ioPMAssertionRelease            func(id uint32) int32
)

// Core Foundation data symbols, resolved when the libraries load.
var (
	cfBooleanTrue                  uintptr
	cfTypeDictionaryKeyCallBacks   uintptr
	cfTypeDictionaryValueCallBacks uintptr
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
		purego.RegisterLibFunc(&cgEventSourceSecondsSinceLast, cg, "CGEventSourceSecondsSinceLastEventType")
		purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, cg, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgRequestScreenCaptureAccess, cg, "CGRequestScreenCaptureAccess")
		purego.RegisterLibFunc(&cgSessionCopyCurrentDictionary, cg, "CGSessionCopyCurrentDictionary")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cfBooleanGetValue, cf, "CFBooleanGetValue")
		purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")
		if errLoad = loadCFSymbols(cf); errLoad != nil {
			return
		}
		purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, as, "AXIsProcessTrustedWithOptions")
		iokit, err := purego.Dlopen(ioKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			errLoad = fmt.Errorf("load IOKit: %w", err)
			return
		}
		purego.RegisterLibFunc(&ioPMAssertionCreateWithName, iokit, "IOPMAssertionCreateWithName")
		purego.RegisterLibFunc(&ioPMAssertionRelease, iokit, "IOPMAssertionRelease")
	})
	return errLoad
}

// loadCFSymbols resolves the Core Foundation constants the permission
// prompt needs.
func loadCFSymbols(cf uintptr) error {
	booleanTrue, err := purego.Dlsym(cf, "kCFBooleanTrue")
	if err != nil {
		return fmt.Errorf("load kCFBooleanTrue: %w", err)
	}
	// The symbol is the address of the variable that holds the reference.
	cfBooleanTrue = *(*uintptr)(unsafe.Add(nil, booleanTrue)) //nolint:gosec // reads a C global whose address dlsym returned
	if cfTypeDictionaryKeyCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks"); err != nil {
		return fmt.Errorf("load kCFTypeDictionaryKeyCallBacks: %w", err)
	}
	if cfTypeDictionaryValueCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks"); err != nil {
		return fmt.Errorf("load kCFTypeDictionaryValueCallBacks: %w", err)
	}
	return nil
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
		diag.Problems = append(diag.Problems, Problem{Code: problemLoadFailed, Message: err.Error()})
		return diag
	}
	if problem, ok := sessionProblem(); ok {
		diag.Problems = append(diag.Problems, problem)
		return diag
	}
	if display, err := mainDisplay(); err != nil {
		diag.Problems = append(diag.Problems, Problem{Code: problemNoDisplay, Message: err.Error()})
	} else {
		diag.Width, diag.Height = display.pixelWidth, display.pixelHeight
	}
	if !cgPreflightScreenCaptureAccess() {
		diag.Problems = append(diag.Problems, Problem{Code: problemScreenRecording, Message: "Screen Recording permission is missing; grant it in System Settings > Privacy & Security > Screen Recording"})
	}
	if !axIsProcessTrusted() {
		diag.Problems = append(diag.Problems, Problem{Code: problemAccessibility, Message: "Accessibility permission is missing; grant it in System Settings > Privacy & Security > Accessibility"})
	}
	return diag
}

// RequestPermissions asks macOS to show its Screen Recording and
// Accessibility prompts for the current process, for the permissions it
// lacks.
func RequestPermissions() {
	if load() != nil {
		return
	}
	if !cgPreflightScreenCaptureAccess() {
		cgRequestScreenCaptureAccess()
	}
	if !axIsProcessTrusted() {
		if options := accessibilityPromptOptions(); options != 0 {
			_ = axIsProcessTrustedWithOptions(options)
			cfRelease(options)
		}
	}
}

// accessibilityPromptOptions returns the options that make the trust check
// show the Accessibility prompt, or 0. The caller releases them.
func accessibilityPromptOptions() uintptr {
	key := cfString(axTrustedCheckOptionPrompt)
	if key == 0 {
		return 0
	}
	defer cfRelease(key)
	value := cfBooleanTrue
	return cfDictionaryCreate(0, &key, &value, 1, cfTypeDictionaryKeyCallBacks, cfTypeDictionaryValueCallBacks)
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

// sessionProblem reports why the login session cannot be operated: there
// is none, another user's session has the display, or the screen is locked.
// ok is false when the session can be operated.
func sessionProblem() (problem Problem, ok bool) {
	session := cgSessionCopyCurrentDictionary()
	if session == 0 {
		return Problem{Code: problemNoSession, Message: "no graphical login session; run the worker in a logged-in user session"}, true
	}
	defer cfRelease(session)
	if onConsole, found := dictionaryFlag(session, sessionOnConsoleKey); found && !onConsole {
		return Problem{Code: problemOtherSession, Message: "another user's session has the display; switch back to this user"}, true
	}
	if locked, found := dictionaryFlag(session, sessionScreenLockedKey); found && locked {
		return Problem{Code: problemScreenLocked, Message: "the screen is locked; unlock it and keep it unlocked while computer steps run"}, true
	}
	return Problem{}, false
}

// dictionaryFlag reads a boolean of a Core Foundation dictionary. ok is
// false when the dictionary does not hold the key.
func dictionaryFlag(dict uintptr, key string) (value, ok bool) {
	cfKey := cfString(key)
	if cfKey == 0 {
		return false, false
	}
	defer cfRelease(cfKey)
	ref := cfDictionaryGetValue(dict, cfKey)
	if ref == 0 {
		return false, false
	}
	return cfBooleanGetValue(ref), true
}

// cfString returns a Core Foundation string the caller releases, or 0.
func cfString(text string) uintptr {
	return cfStringCreateWithCString(0, text, cfStringEncodingUTF8)
}

// keepAwake keeps the display and the system awake until release is
// called. An error means the power settings apply as usual.
func keepAwake() (release func(), err error) {
	name := cfString(awakeReason)
	if name == 0 {
		return func() {}, errors.New("create the power assertion name")
	}
	defer cfRelease(name)
	var ids []uint32
	var errs []error
	for _, kind := range []string{assertionDisplayAwake, assertionSystemAwake} {
		kindRef := cfString(kind)
		if kindRef == 0 {
			errs = append(errs, fmt.Errorf("create the %s assertion type", kind))
			continue
		}
		var id uint32
		if code := ioPMAssertionCreateWithName(kindRef, assertionLevelOn, name, &id); code != 0 {
			errs = append(errs, fmt.Errorf("create %s assertion: IOReturn 0x%x", kind, uint32(code))) //nolint:gosec // IOReturn codes are reported as unsigned hex
		} else {
			ids = append(ids, id)
		}
		cfRelease(kindRef)
	}
	return func() {
		for _, id := range ids {
			_ = ioPMAssertionRelease(id)
		}
	}, errors.Join(errs...)
}

// darwinBackend drives the desktop through Quartz events. Positions are
// screenshot pixels, which Quartz measures in points.
type darwinBackend struct {
	display display
	// buttons and flags track held buttons and modifiers, so moves become
	// drags and every event carries the held modifiers.
	buttons map[Button]bool
	flags   uint64
	// wake releases the power assertions held while the backend is open.
	wake func()
}

func newDarwinBackend() (*darwinBackend, error) {
	display, err := mainDisplay()
	if err != nil {
		return nil, err
	}
	// A display that sleeps shows nothing to capture; staying awake is best
	// effort.
	wake, _ := keepAwake()
	return &darwinBackend{display: display, buttons: map[Button]bool{}, wake: wake}, nil
}

// Capture uses the screencapture tool, which keeps working across macOS
// releases that retire the Quartz capture functions.
func (b *darwinBackend) Capture() (*image.RGBA, error) {
	// A locked screen captures without error but shows the lock screen.
	if problem, ok := sessionProblem(); ok {
		return nil, errors.New(problem.Message)
	}
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

func (b *darwinBackend) LastInput() time.Time {
	seconds := cgEventSourceSecondsSinceLast(eventSourceStateHIDSystem, anyInputEventType)
	return time.Now().Add(-time.Duration(seconds * float64(time.Second)))
}

func (b *darwinBackend) Close() error {
	b.wake()
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
