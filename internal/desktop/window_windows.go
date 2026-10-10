// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
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
)

const (
	// gaRoot asks GetAncestor for the top-level window.
	gaRoot = 2
	// swRestore un-minimizes a window; swShow shows it at its current size.
	swRestore = 9
	swShow    = 5
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

// windowID names a window by its handle, title, process, and application.
func windowID(hwnd windows.HWND) WindowID {
	if hwnd == 0 {
		return WindowID{}
	}
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(hwnd, &pid)
	title := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))) //nolint:gosec // Win32 takes the buffer address as uintptr
	return WindowID{Handle: uint64(hwnd), Title: windows.UTF16ToString(title[:n]), PID: pid, App: appName(hwnd)}
}
