// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"image"
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

	dwmapi                    = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
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
		if w := windowID(windows.HWND(hwnd)); w.Title != "" {
			*enumOut = append(*enumOut, w)
		}
		return 1
	})
)

func (windowsBackend) Windows() []WindowID {
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
