// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"errors"
	"fmt"
	"image"
	"slices"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procWindowFromPoint     = user32.NewProc("WindowFromPoint")
	procGetAncestor         = user32.NewProc("GetAncestor")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procAttachThreadInput   = user32.NewProc("AttachThreadInput")
	procIsZoomed            = user32.NewProc("IsZoomed")
	procShowWindowAsync     = user32.NewProc("ShowWindowAsync")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGetWindow           = user32.NewProc("GetWindow")
	procIsWindowEnabled     = user32.NewProc("IsWindowEnabled")
	procPrintWindow         = user32.NewProc("PrintWindow")
	procIsHungAppWindow     = user32.NewProc("IsHungAppWindow")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")

	dwmapi                    = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")

	shcore               = windows.NewLazySystemDLL("shcore.dll")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

const (
	// gaRoot asks GetAncestor for the top-level window.
	gaRoot = 2
	// gwOwner asks GetWindow for the window that owns a window.
	gwOwner = 4
	// swRestore un-minimizes a window; swShow shows it at its current size;
	// swMaximize maximizes it.
	swRestore  = 9
	swShow     = 5
	swMaximize = 3

	// Window styles a maximizable window has, and the extended style of a
	// tool window, which is not maximized.
	gwlStyle        = -16
	gwlExStyle      = -20
	wsMaximizeBox   = 0x00010000
	wsChild         = 0x40000000
	wsExToolWindow  = 0x00000080
	wsExDlgModal    = 0x00000001
	monitorPrimary  = 1
	monitorNearest  = 2
	swpNoSize       = 0x0001
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	dwmExtendedRect = 9
	// dwmCloaked asks whether the window manager hides a window that is
	// visible, as it does one on another virtual desktop.
	dwmCloaked = 14
	// pwRenderFullContent makes PrintWindow render what the window manager
	// composes, GPU-drawn content included.
	pwRenderFullContent = 0x2
	// mdtEffectiveDPI asks GetDpiForMonitor for the scale the display
	// settings give a monitor.
	mdtEffectiveDPI = 0
)

// enumWindowsCallback is made once: Go never frees a callback and caps how
// many a process may make, so one per call would run out after enough
// replays. It fills enumOut, which enumMu reserves for one enumeration at a
// time, rather than a pointer smuggled through lParam, which vet rejects.
var (
	enumMu  sync.Mutex
	enumOut *[]WindowID

	enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1 // keep enumerating
		}
		*enumOut = append(*enumOut, windowID(windows.HWND(hwnd)))
		return 1
	})
)

func (windowsBackend) Windows() []WindowID {
	return slices.DeleteFunc(visibleWindows(), func(w WindowID) bool { return w.Title == "" })
}

func (windowsBackend) ReachableWindows() []WindowID {
	return visibleWindows()
}

// visibleWindows lists the visible top-level windows, titled or not.
func visibleWindows() []WindowID {
	// EnumWindows visits top-level windows front of the Z-order first, so
	// the slice keeps that order.
	enumMu.Lock()
	defer enumMu.Unlock()
	out := new([]WindowID)
	enumOut = out
	_, _, _ = procEnumWindows.Call(enumWindowsCallback, 0)
	enumOut = nil
	return *out
}

func (windowsBackend) Raise(w WindowID) error {
	if w.Handle == 0 {
		return nil
	}
	hwnd := uintptr(w.Handle) //nolint:gosec // a window handle round-trips through uintptr for Win32
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
	}
	// SetForegroundWindow is refused for a background process unless it
	// shares input state with the current foreground thread, so attach to
	// that thread for the call, the documented way to take the foreground.
	foreground := windows.GetForegroundWindow()
	current := windows.GetCurrentThreadId()
	var foregroundThread uint32
	if foreground != 0 {
		foregroundThread, _ = windows.GetWindowThreadProcessId(foreground, nil)
	}
	if foregroundThread != 0 && foregroundThread != current {
		_, _, _ = procAttachThreadInput.Call(uintptr(current), uintptr(foregroundThread), 1)
		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procShowWindow.Call(hwnd, swShow)
		_, _, _ = procAttachThreadInput.Call(uintptr(current), uintptr(foregroundThread), 0)
		return nil
	}
	_, _, _ = procBringWindowToTop.Call(hwnd)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	return nil
}

func (windowsBackend) FocusedWindow() WindowID {
	return windowID(windows.GetForegroundWindow())
}

func (windowsBackend) WindowAt(x, y int) WindowID {
	// WindowFromPoint takes a POINT by value, which 64-bit calls pass in
	// one register, as UI Automation's point calls do.
	hwnd, _, _ := procWindowFromPoint.Call(pointArg(x, y))
	if hwnd == 0 {
		return WindowID{}
	}
	if root, _, _ := procGetAncestor.Call(hwnd, gaRoot); root != 0 {
		hwnd = root
	}
	return windowID(windows.HWND(hwnd))
}

func (windowsBackend) Maximize(w WindowID) (bool, error) {
	if w.Handle == 0 {
		return false, nil
	}
	hwnd := uintptr(w.Handle) //nolint:gosec // a window handle round-trips through uintptr for Win32
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, signedArg(gwlStyle))
	exStyle, _, _ := procGetWindowLongPtrW.Call(hwnd, signedArg(gwlExStyle))
	// A dialog, a tool window, or a window without a maximize box keeps the
	// size its application gave it.
	if style&wsMaximizeBox == 0 || style&wsChild != 0 || exStyle&(wsExToolWindow|wsExDlgModal) != 0 {
		return false, nil
	}
	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
		return false, nil
	}
	// The screen is captured from the primary display, so a window on
	// another one moves there first and is maximized on it.
	primary, _, _ := procMonitorFromPoint.Call(pointArg(0, 0), monitorPrimary)
	if current, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest); current != primary {
		_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
	// ShowWindowAsync posts the change, so a window whose application does
	// not answer cannot hang the step.
	if ok, _, err := procShowWindowAsync.Call(hwnd, swMaximize); ok == 0 {
		return false, err
	}
	return true, nil
}

// signedArg passes a negative index the way Win32 takes it, sign-extended
// to the register width.
func signedArg(n int32) uintptr {
	return uintptr(int64(n)) //nolint:gosec // Win32 reads the bits as a signed index
}

// windowID names a window by its handle, title, process, application, and
// frame.
func windowID(hwnd windows.HWND) WindowID {
	if hwnd == 0 {
		return WindowID{}
	}
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(hwnd, &pid)
	title := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))) //nolint:gosec // Win32 takes the buffer address as uintptr
	return WindowID{Handle: uint64(hwnd), Title: windows.UTF16ToString(title[:n]), PID: pid, App: appName(hwnd), Bounds: windowBounds(hwnd)}
}

// windowBounds is a window's visible frame in display pixels. The window
// manager's extended frame leaves out the invisible resize border that
// GetWindowRect includes, so a maximized window starts at the display's
// corner rather than a few pixels outside it.
func windowBounds(hwnd windows.HWND) image.Rectangle {
	var rect windows.Rect
	if hr, _, _ := procDwmGetWindowAttribute.Call(uintptr(hwnd), dwmExtendedRect, uintptr(unsafe.Pointer(&rect)), unsafe.Sizeof(rect)); hr != 0 { //nolint:gosec // Win32 takes the buffer address as uintptr
		if ok, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect))); ok == 0 { //nolint:gosec // Win32 takes the buffer address as uintptr
			return image.Rectangle{}
		}
	}
	return image.Rect(int(rect.Left), int(rect.Top), int(rect.Right), int(rect.Bottom))
}

// holdsOwner reports whether a window keeps the window that owns it
// disabled, as a modal dialog or a message box does until it is answered.
func holdsOwner(hwnd windows.HWND) bool {
	owner, _, _ := procGetWindow.Call(uintptr(hwnd), gwOwner)
	if owner == 0 {
		return false
	}
	enabled, _, _ := procIsWindowEnabled.Call(owner)
	return enabled == 0
}

// CaptureApps draws each window with PrintWindow, which renders a window's
// own content whether or not other windows cover it. Windows.Graphics.Capture
// would need WinRT activation and a Direct3D device through COM vtables, and
// shows a border around each window it captures. A window whose application
// does not answer is left out, since PrintWindow may wait for it. A window
// the system stretches to the display's scale, as it does one of an
// application unaware of that scale, fails the capture: PrintWindow draws it
// at a size the screen does not show.
func (windowsBackend) CaptureApps(apps []string) (Capture, error) {
	if err := capturable(); err != nil {
		return Capture{}, err
	}
	var shown []WindowID
	for _, w := range visibleWindows() {
		hwnd := uintptr(w.Handle) //nolint:gosec // a window handle round-trips through uintptr for Win32
		if !appIn(w.App, apps) || flag(procIsIconic, hwnd) || flag(procIsHungAppWindow, hwnd) || cloaked(hwnd) {
			continue
		}
		if stretched(hwnd) {
			return Capture{}, fmt.Errorf("%s draws its windows at another scale than the display's", w.App)
		}
		shown = append(shown, w)
	}
	width, height := screenSize()
	return compose(image.Rect(0, 0, width, height), shown, printWindow)
}

// printWindow returns a window's picture of its Bounds.
func printWindow(w WindowID) (*image.RGBA, error) {
	hwnd := uintptr(w.Handle) //nolint:gosec // a window handle round-trips through uintptr for Win32
	var frame windows.Rect
	if ok, _, err := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&frame))); ok == 0 { //nolint:gosec // Win32 takes the buffer address as uintptr
		return nil, fmt.Errorf("GetWindowRect: %w", err)
	}
	width, height := int(frame.Right-frame.Left), int(frame.Bottom-frame.Top)
	if width <= 0 || height <= 0 {
		return nil, errors.New("the window has no size")
	}
	img, err := paintBitmap(width, height, func(memory, _ uintptr) error {
		if ok, _, err := procPrintWindow.Call(hwnd, memory, pwRenderFullContent); ok == 0 {
			return fmt.Errorf("PrintWindow: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The window's rectangle holds an invisible resize border around the
	// frame Bounds names.
	return Crop(img, w.Bounds.Sub(image.Pt(int(frame.Left), int(frame.Top)))), nil
}

// flag calls a Win32 function of a window that answers a BOOL.
func flag(proc *windows.LazyProc, hwnd uintptr) bool {
	yes, _, _ := proc.Call(hwnd)
	return yes != 0
}

// stretched reports a window drawn at another scale than its monitor's,
// which the system stretches to fit. Before Windows 10 1607, which has no
// scale per window, it reports none.
func stretched(hwnd uintptr) bool {
	if procGetDpiForWindow.Find() != nil || procGetDpiForMonitor.Find() != nil {
		return false
	}
	own, _, _ := procGetDpiForWindow.Call(hwnd)
	monitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	var dpiX, dpiY uint32
	if hr, _, _ := procGetDpiForMonitor.Call(monitor, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); hr != 0 { //nolint:gosec // Win32 takes the buffer addresses as uintptr
		return false
	}
	return own != 0 && uint32(own) != dpiX //nolint:gosec // a DPI fits 32 bits
}

// cloaked reports a visible window the window manager does not show.
func cloaked(hwnd uintptr) bool {
	var value uint32
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmCloaked, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value)) //nolint:gosec // Win32 takes the buffer address as uintptr
	return hr == 0 && value != 0
}
