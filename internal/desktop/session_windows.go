// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package desktop

import "golang.org/x/sys/windows"

// OnConsoleSession reports whether this process runs in the session attached
// to the physical console, where a person at the keyboard could collide with
// a step's input. A remote desktop session is not the console: its
// connection feeds the desktop synthetic pointer and keyboard updates of its
// own, so "nobody has touched the desktop" is never true there. When the
// system cannot say, the console is assumed, which keeps the guard.
func OnConsoleSession() bool {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return true
	}
	console := windows.WTSGetActiveConsoleSessionId()
	// 0xFFFFFFFF means no session is attached to the console right now.
	if console == 0xFFFFFFFF {
		return true
	}
	return session == console
}
