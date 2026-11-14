// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package desktop

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// problemSessionDisconnected is a Remote Desktop or child session whose
// client has gone: the session exists but nothing draws it. It is a Windows
// condition, so it lives with the code that detects it.
const problemSessionDisconnected = "session_disconnected"

var (
	wtsapi32                               = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW        = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory                      = wtsapi32.NewProc("WTSFreeMemory")
	wtsCurrentSession               uint32 = 0xFFFFFFFF // WTS_CURRENT_SESSION
	wtsConnectStateClass            uint32 = 8          // WTSConnectState in WTS_INFO_CLASS
)

// sessionConnectState returns the WTS connect state of the session this
// process runs in: windows.WTSActive while a client or the console draws it,
// windows.WTSDisconnected after the Remote Desktop client has gone. It asks
// about the current session only, which needs no permission on other
// sessions, so a restricted account gets an answer rather than a failure.
func sessionConnectState() (uint32, error) {
	var buffer *uint32
	var size uint32
	// Handle 0 is WTS_CURRENT_SERVER_HANDLE.
	r1, _, callErr := procWTSQuerySessionInformationW.Call(0, uintptr(wtsCurrentSession), uintptr(wtsConnectStateClass), uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&size))) //nolint:gosec // out-parameters of a Win32 call
	if r1 == 0 {
		if callErr != nil {
			return 0, callErr
		}
		return 0, errors.New("WTSQuerySessionInformation failed")
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buffer))) //nolint:errcheck,gosec // frees the buffer the system handed out; nothing to report
	if buffer == nil || size < 4 {
		return 0, errors.New("WTSQuerySessionInformation returned no connect state")
	}
	return *buffer, nil
}

// sessionProblemForState names a session that is not drawing. Only an active
// session has a display that renders and takes input; a connected,
// disconnected, or idle one keeps its last picture and its size, so the
// locked-screen and display-size checks say nothing is wrong.
func sessionProblemForState(state uint32) (Problem, bool) {
	if state == windows.WTSActive {
		return Problem{}, false
	}
	return Problem{
		Code:    problemSessionDisconnected,
		Message: "the remote desktop session is disconnected, so its display is not drawing; reconnect the session (or re-attach the child session) and keep it connected while computer steps run",
	}, true
}

// sessionProblem reports a session that is not drawing. When the system
// cannot say, the session is assumed to be drawing, like OnConsoleSession
// assumes the console: a lookup failure must not stop every step.
func sessionProblem() (Problem, bool) {
	state, err := sessionConnectState()
	if err != nil {
		return Problem{}, false
	}
	return sessionProblemForState(state)
}

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
