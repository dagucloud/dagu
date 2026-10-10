// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procWindowFromPoint = user32.NewProc("WindowFromPoint")
	procGetAncestor     = user32.NewProc("GetAncestor")
)

// gaRoot asks GetAncestor for the top-level window.
const gaRoot = 2

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

// windowID names a window by its handle, title, and process.
func windowID(hwnd windows.HWND) WindowID {
	if hwnd == 0 {
		return WindowID{}
	}
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(hwnd, &pid)
	title := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))) //nolint:gosec // Win32 takes the buffer address as uintptr
	return WindowID{Handle: uint64(hwnd), Title: windows.UTF16ToString(title[:n]), PID: pid}
}
