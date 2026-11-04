// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

var (
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procSetParent        = user32.NewProc("SetParent")

	procSetThreadDpiHostingBehavior = user32.NewProc("SetThreadDpiHostingBehavior")
)

const (
	// testWindowClass is the class of the plain windows the tests show.
	testWindowClass = "DaguDesktopTest"
	// wsOverlappedWindow is WS_OVERLAPPEDWINDOW, a window with a title bar.
	wsOverlappedWindow = 0x00CF0000
	// swShowNoActivate shows a window without taking the foreground.
	swShowNoActivate = 4
	// errorClassAlreadyExists is what registering a class a second time,
	// as a repeated test run does, answers.
	errorClassAlreadyExists = windows.Errno(1410)
	// dpiHostingBehaviorMixed lets the windows a thread creates hold child
	// windows whose DPI awareness differs from theirs.
	dpiHostingBehaviorMixed = 1
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// showTestWindow shows a window of a class without taking the foreground.
// The window answers every message the default way, so it needs no message
// loop for calls from its own thread, which the caller keeps locked. The
// window is destroyed as the test ends.
func showTestWindow(t *testing.T, class, title string) windows.HWND {
	t.Helper()
	var instance windows.Handle
	require.NoError(t, windows.GetModuleHandleEx(0, nil, &instance))
	className, err := windows.UTF16PtrFromString(class)
	require.NoError(t, err)
	wc := wndClassEx{WndProc: procDefWindowProcW.Addr(), Instance: instance, ClassName: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 && !errors.Is(err, errorClassAlreadyExists) {
		require.NoError(t, err, "RegisterClassExW")
	}
	text, err := windows.UTF16PtrFromString(title)
	require.NoError(t, err)
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(text)), wsOverlappedWindow, 40, 40, 320, 200, 0, 0, uintptr(instance), 0)
	require.NotZero(t, hwnd, "CreateWindowExW: %v", err)
	t.Cleanup(func() { _, _, _ = procDestroyWindow.Call(hwnd) })
	_, _, _ = procShowWindow.Call(hwnd, swShowNoActivate)
	return windows.HWND(hwnd)
}

// listed returns the window of a handle among list.
func listed(list []WindowID, hwnd windows.HWND) (WindowID, bool) {
	i := slices.IndexFunc(list, func(w WindowID) bool { return w.Handle == uint64(hwnd) })
	if i < 0 {
		return WindowID{}, false
	}
	return list[i], true
}

// A window the window manager cloaks, as it does one on another virtual
// desktop or the frame of a Store app suspended after closing, stays
// visible to Win32 but is not on the screen, so neither list has it.
func TestWindowsDesktopLeavesOutCloakedWindows(t *testing.T) {
	driver := e2eDesktop(t)
	runtime.LockOSThread()
	hwnd := showTestWindow(t, testWindowClass, "Dagu cloaked window test")

	_, ok := listed(driver.Windows(), hwnd)
	require.True(t, ok, "a shown window is listed")
	_, ok = listed(driver.ReachableWindows(), hwnd)
	require.True(t, ok)

	cloak := int32(1)
	require.NoError(t, windows.DwmSetWindowAttribute(hwnd, windows.DWMWA_CLOAK, unsafe.Pointer(&cloak), uint32(unsafe.Sizeof(cloak))))
	assert.True(t, windows.IsWindowVisible(hwnd), "Win32 still calls a cloaked window visible")
	_, ok = listed(driver.Windows(), hwnd)
	assert.False(t, ok, "a cloaked window is not on the screen")
	_, ok = listed(driver.ReachableWindows(), hwnd)
	assert.False(t, ok, "no pointer action lands in a cloaked window")
}

// A Store app shows in a frame that ApplicationFrameHost owns, with the
// app's own window inside it, so the frame's process and application are
// the app's. Here a frame of the test's holds a window of a second process,
// as a frame holds the app's window.
func TestWindowsDesktopNamesTheAppInAFrame(t *testing.T) {
	driver := e2eDesktop(t)
	runtime.LockOSThread()
	const appTitle = "Dagu framed app"
	// The framed window is the PowerShell form's, whose DPI awareness is not
	// this process's; without mixed hosting, SetParent refuses it.
	_, _, _ = procSetThreadDpiHostingBehavior.Call(dpiHostingBehaviorMixed)
	// The class ApplicationFrameHost gives the frames it shows.
	frame := showTestWindow(t, "ApplicationFrameWindow", "Dagu frame test")

	app := exec.Command("powershell.exe", "-NoProfile", "-Command",
		"Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.Form; $f.Text = '"+appTitle+"'; [void]$f.ShowDialog()")
	require.NoError(t, app.Start())
	t.Cleanup(func() {
		_ = app.Process.Kill()
		_ = app.Wait()
	})
	pid := uint32(app.Process.Pid)
	// Listing reads the frame's title, which a window of this process gives
	// only on its own thread, so the wait polls here rather than on a
	// goroutine of its own.
	var shown WindowID
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		all := driver.Windows()
		if i := slices.IndexFunc(all, func(w WindowID) bool { return w.PID == pid && w.Title == appTitle }); i >= 0 {
			shown = all[i]
			break
		}
		require.True(t, time.Now().Before(deadline), "the app's window did not open")
	}
	before, ok := listed(driver.Windows(), frame)
	require.True(t, ok)
	require.NotEqual(t, pid, before.PID, "an empty frame is its owner's")

	previous, _, err := procSetParent.Call(uintptr(shown.Handle), uintptr(frame))
	require.NotZero(t, previous, "SetParent: %v", err)
	framed, ok := listed(driver.Windows(), frame)
	require.True(t, ok, "the frame is listed")
	assert.Equal(t, pid, framed.PID, "the frame is the window of the app inside it")
	assert.Equal(t, "powershell", framed.App)
}
