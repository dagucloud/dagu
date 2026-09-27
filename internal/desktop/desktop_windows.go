// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procSendInput                     = user32.NewProc("SendInput")
	procVkKeyScanW                    = user32.NewProc("VkKeyScanW")
	procMapVirtualKeyW                = user32.NewProc("MapVirtualKeyW")
	procOpenInputDesktop              = user32.NewProc("OpenInputDesktop")
	procCloseDesktop                  = user32.NewProc("CloseDesktop")
	procGetUserObjectInformationW     = user32.NewProc("GetUserObjectInformationW")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

// Win32 constants.
const (
	smCXScreen = 0
	smCYScreen = 1

	// dpiAwarenessPerMonitorV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
	dpiAwarenessPerMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4

	srcCopy       = 0x00CC0020
	captureBlt    = 0x40000000
	biRGB         = 0
	dibRGBColors  = 0
	uoiName       = 2
	desktopRead   = 0x0001
	defaultDesk   = "Default"
	mapVKToVSC    = 0
	vkScanNoShift = 0xFF

	inputMouse    = 0
	inputKeyboard = 1
	wheelDelta    = 120
)

// Input event flags.
const (
	mouseLeftDown   uint32 = 0x0002
	mouseLeftUp     uint32 = 0x0004
	mouseRightDown  uint32 = 0x0008
	mouseRightUp    uint32 = 0x0010
	mouseMiddleDown uint32 = 0x0020
	mouseMiddleUp   uint32 = 0x0040
	mouseWheel      uint32 = 0x0800
	mouseHWheel     uint32 = 0x1000

	keyExtended uint32 = 0x0001
	keyUp       uint32 = 0x0002
	keyUnicode  uint32 = 0x0004
)

// Virtual-key codes.
const (
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12
	vkLWin     = 0x5B
	vkReturn   = 0x0D
	vkTab      = 0x09
	vkEscape   = 0x1B
	vkBack     = 0x08
	vkDelete   = 0x2E
	vkSpace    = 0x20
	vkInsert   = 0x2D
	vkHome     = 0x24
	vkEnd      = 0x23
	vkPrior    = 0x21
	vkNext     = 0x22
	vkUp       = 0x26
	vkDown     = 0x28
	vkLeft     = 0x25
	vkRight    = 0x27
	vkCapital  = 0x14
	vkSnapshot = 0x2C
	vkF1       = 0x70
)

// namedKeys maps named keys to their virtual-key codes and whether they are
// extended keys, which Windows reports with a distinct scan code prefix.
var namedKeys = map[Key]struct {
	vk       uint16
	extended bool
}{
	KeyShift: {vkShift, false}, KeyCtrl: {vkControl, false}, KeyAlt: {vkMenu, false}, KeyMeta: {vkLWin, true},
	KeyEnter: {vkReturn, false}, KeyTab: {vkTab, false}, KeyEscape: {vkEscape, false},
	KeyBackspace: {vkBack, false}, KeyDelete: {vkDelete, true}, KeySpace: {vkSpace, false},
	KeyInsert: {vkInsert, true}, KeyHome: {vkHome, true}, KeyEnd: {vkEnd, true},
	KeyPageUp: {vkPrior, true}, KeyPageDown: {vkNext, true},
	KeyUp: {vkUp, true}, KeyDown: {vkDown, true}, KeyLeft: {vkLeft, true}, KeyRight: {vkRight, true},
	KeyCapsLock: {vkCapital, false}, KeyPrintScreen: {vkSnapshot, true},
}

// mouseInput is MOUSEINPUT, the largest member of the INPUT union.
type mouseInput struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// keybdInput is KEYBDINPUT, which overlays the start of the INPUT union.
type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// input is INPUT: 40 bytes on 64-bit Windows, 28 on 32-bit.
type input struct {
	Type  uint32
	Mouse mouseInput
}

func mouseEvent(flags, data uint32) input {
	return input{Type: inputMouse, Mouse: mouseInput{Flags: flags, MouseData: data}}
}

func keyboardEvent(vk, scan uint16, flags uint32) input {
	in := input{Type: inputKeyboard}
	*(*keybdInput)(unsafe.Pointer(&in.Mouse)) = keybdInput{Vk: vk, Scan: scan, Flags: flags} //nolint:gosec // KEYBDINPUT shares the INPUT union with MOUSEINPUT
	return in
}

type point struct {
	X int32
	Y int32
}

type bitmapInfo struct {
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
	Colors        [1]uint32
}

var dpiOnce sync.Once

// Open returns a driver for the desktop of the current session.
func Open() (*Driver, error) {
	if err := Check().Err(); err != nil {
		return nil, err
	}
	return New(windowsBackend{}), nil
}

// Check reports whether the desktop of the current session can be automated.
func Check() Diagnostics {
	makeDPIAware()
	diag := Diagnostics{OS: runtime.GOOS}
	diag.Width, diag.Height = screenSize()

	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err == nil && session == 0 {
		diag.Problems = append(diag.Problems, "the process runs in session 0, which has no desktop; run the worker in a logged-in user session instead of as a service")
		return diag
	}
	if name, err := inputDesktopName(); err != nil {
		diag.Problems = append(diag.Problems, "the input desktop is not accessible; the screen may be locked")
	} else if name != defaultDesk {
		diag.Problems = append(diag.Problems, fmt.Sprintf("the %q desktop is active; the screen is locked or a secure prompt is shown", name))
	}
	return diag
}

// RequestPermissions does nothing on Windows, which needs no permission to
// capture the screen or send input.
func RequestPermissions() {}

// makeDPIAware makes positions physical pixels on scaled displays.
func makeDPIAware() {
	dpiOnce.Do(func() {
		if procSetProcessDpiAwarenessContext.Find() == nil {
			if ok, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2); ok != 0 {
				return
			}
		}
		_, _, _ = procSetProcessDPIAware.Call()
	})
}

func screenSize() (int, int) {
	width, _, _ := procGetSystemMetrics.Call(smCXScreen)
	height, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(width), int(height)
}

func inputDesktopName() (string, error) {
	desk, _, err := procOpenInputDesktop.Call(0, 0, desktopRead)
	if desk == 0 {
		return "", err
	}
	defer func() { _, _, _ = procCloseDesktop.Call(desk) }()
	var buf [256]uint16
	var needed uint32
	ok, _, err := procGetUserObjectInformationW.Call(desk, uoiName, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&needed))) //nolint:gosec // Win32 takes buffer addresses as uintptr
	if ok == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf[:]), nil
}

// windowsBackend drives the desktop through Win32 input and GDI capture.
type windowsBackend struct{}

func (windowsBackend) Capture() (*image.RGBA, error) {
	width, height := screenSize()
	if width <= 0 || height <= 0 {
		return nil, errors.New("screen size is unavailable")
	}
	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC: %w", err)
	}
	defer func() { _, _, _ = procReleaseDC.Call(0, screen) }()
	memory, _, err := procCreateCompatibleDC.Call(screen)
	if memory == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	defer func() { _, _, _ = procDeleteDC.Call(memory) }()
	bitmap, _, err := procCreateCompatibleBitmap.Call(screen, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap: %w", err)
	}
	defer func() { _, _, _ = procDeleteObject.Call(bitmap) }()

	previous, _, _ := procSelectObject.Call(memory, bitmap)
	ok, _, err := procBitBlt.Call(memory, 0, 0, uintptr(width), uintptr(height), screen, 0, 0, srcCopy|captureBlt)
	// GetDIBits needs the bitmap deselected.
	_, _, _ = procSelectObject.Call(memory, previous)
	if ok == 0 {
		return nil, fmt.Errorf("BitBlt: %w", err)
	}

	info := bitmapInfo{
		Width:    int32(width),   //nolint:gosec // screen sizes fit int32
		Height:   -int32(height), //nolint:gosec // negative height requests top-down rows
		Planes:   1,
		BitCount: 32,
	}
	info.Size = uint32(unsafe.Sizeof(info) - unsafe.Sizeof(info.Colors))
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	lines, _, err := procGetDIBits.Call(memory, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors) //nolint:gosec // Win32 takes buffer addresses as uintptr
	if lines == 0 {
		return nil, fmt.Errorf("GetDIBits: %w", err)
	}
	// GDI returns BGRX; swap to RGBA with an opaque alpha.
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2], img.Pix[i+3] = img.Pix[i+2], img.Pix[i], 0xFF
	}
	return img, nil
}

func (windowsBackend) MoveTo(x, y int) error {
	if ok, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y)); ok == 0 {
		return fmt.Errorf("SetCursorPos: %w", err)
	}
	return nil
}

func (windowsBackend) Position() (int, int, error) {
	var p point
	if ok, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); ok == 0 { //nolint:gosec // Win32 takes the POINT address as uintptr
		return 0, 0, fmt.Errorf("GetCursorPos: %w", err)
	}
	return int(p.X), int(p.Y), nil
}

func (windowsBackend) Button(button Button, down bool, _ int) error {
	var flags uint32
	switch button {
	case ButtonLeft:
		flags = pick(down, mouseLeftDown, mouseLeftUp)
	case ButtonRight:
		flags = pick(down, mouseRightDown, mouseRightUp)
	case ButtonMiddle:
		flags = pick(down, mouseMiddleDown, mouseMiddleUp)
	default:
		return fmt.Errorf("unknown mouse button %q", button)
	}
	return sendInput(mouseEvent(flags, 0))
}

func (windowsBackend) Wheel(dx, dy int) error {
	var events []input
	if dy != 0 {
		// Positive wheel data scrolls up, away from the user.
		events = append(events, mouseEvent(mouseWheel, wheelData(-dy)))
	}
	if dx != 0 {
		events = append(events, mouseEvent(mouseHWheel, wheelData(dx)))
	}
	return sendInput(events...)
}

func wheelData(notches int) uint32 {
	return uint32(int32(notches * wheelDelta)) //nolint:gosec // two's complement is what Windows expects
}

func (windowsBackend) Key(key Key, down bool) error {
	flags := pick[uint32](down, 0, keyUp)
	if named, ok := namedKeys[key]; ok {
		if named.extended {
			flags |= keyExtended
		}
		return sendInput(keyboardEvent(named.vk, scanCode(named.vk), flags))
	}
	if n, ok := key.FunctionNumber(); ok {
		vk := uint16(vkF1 + n - 1) //nolint:gosec // function keys are F1 to F24
		return sendInput(keyboardEvent(vk, scanCode(vk), flags))
	}
	r, ok := key.Char()
	if !ok {
		return fmt.Errorf("unsupported key %q", key)
	}
	scan, _, _ := procVkKeyScanW.Call(uintptr(r)) //nolint:gosec // key characters are printable ASCII
	if scan&0xFFFF == 0xFFFF {
		return fmt.Errorf("key %q is not on the keyboard layout", key)
	}
	vk := uint16(scan & vkScanNoShift) //nolint:gosec // the low byte is the virtual-key code
	event := keyboardEvent(vk, scanCode(vk), flags)
	if scan&0x100 == 0 {
		return sendInput(event)
	}
	// The layout needs Shift for this character.
	shift := keyboardEvent(vkShift, scanCode(vkShift), pick[uint32](down, 0, keyUp))
	if down {
		return sendInput(shift, event)
	}
	return sendInput(event, shift)
}

func scanCode(vk uint16) uint16 {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), mapVKToVSC)
	return uint16(scan) //nolint:gosec // scan codes fit a WORD
}

func (windowsBackend) Type(text string) error {
	units := utf16.Encode([]rune(text))
	events := make([]input, 0, len(units)*2)
	for _, unit := range units {
		events = append(events, keyboardEvent(0, unit, keyUnicode), keyboardEvent(0, unit, keyUnicode|keyUp))
	}
	return sendInput(events...)
}

func (windowsBackend) Close() error {
	return nil
}

// sendInput injects events. Windows drops input silently to windows of a
// higher integrity level, so a partial count is reported as an error.
func sendInput(events ...input) error {
	if len(events) == 0 {
		return nil
	}
	sent, _, err := procSendInput.Call(uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0])) //nolint:gosec // Win32 takes the INPUT array address as uintptr
	if int(sent) != len(events) {
		return fmt.Errorf("SendInput delivered %d of %d events; the target may run elevated or the desktop is locked: %w", sent, len(events), err)
	}
	return nil
}
