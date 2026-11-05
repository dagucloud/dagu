// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package desktop

import (
	"os"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// SendInput rejects events whose size differs from the platform's INPUT.
func TestInputMatchesWin32Layout(t *testing.T) {
	t.Parallel()

	want := uintptr(40)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 28
	}
	assert.Equal(t, want, unsafe.Sizeof(input{}))
	assert.LessOrEqual(t, unsafe.Sizeof(keybdInput{}), unsafe.Sizeof(mouseInput{}))
}

// A scroll sends one wheel message per notch, as a mouse wheel does. Wheel
// data is negative toward the user and to the left.
func TestWheelInputsSendANotchPerMessage(t *testing.T) {
	t.Parallel()

	oneNotchBack := uint32(0xFFFFFF88) // -120
	events := wheelInputs(-2, 3)
	require.Len(t, events, 5)
	for _, event := range events[:3] {
		assert.Equal(t, mouseInput{Flags: mouseWheel, MouseData: oneNotchBack}, event.Mouse)
	}
	for _, event := range events[3:] {
		assert.Equal(t, mouseInput{Flags: mouseHWheel, MouseData: oneNotchBack}, event.Mouse)
	}
	assert.Equal(t, uint32(120), wheelInputs(0, -1)[0].Mouse.MouseData)
}

// Only an active session draws; every other connect state is reported as a
// disconnected session, which is not a locked screen.
func TestSessionProblemForState(t *testing.T) {
	t.Parallel()

	_, ok := sessionProblemForState(windows.WTSActive)
	assert.False(t, ok)

	for name, state := range map[string]uint32{
		"connected":    windows.WTSConnected,
		"disconnected": windows.WTSDisconnected,
		"idle":         windows.WTSIdle,
	} {
		problem, ok := sessionProblemForState(state)
		require.True(t, ok, name)
		assert.Equal(t, "session_disconnected", problem.Code, name)
		assert.Contains(t, problem.Message, "disconnected", name)
	}
}

// The current session is always listed, in session 0 as well, so the lookup
// itself never fails; what it reports depends on where the test runs.
func TestSessionConnectState(t *testing.T) {
	t.Parallel()

	state, err := sessionConnectState()
	require.NoError(t, err)
	assert.LessOrEqual(t, state, uint32(windows.WTSInit))
}

// e2eDesktop skips unless the test runs on an interactive desktop, which
// CI enables with DAGU_DESKTOP_E2E=1.
func e2eDesktop(t *testing.T) *Driver {
	t.Helper()
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	driver, err := Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Close() })
	return driver
}

// Windows accepts the power requests that keep the display awake, which
// needs no interactive desktop.
func TestKeepAwake(t *testing.T) {
	release, err := keepAwake()
	require.NoError(t, err)
	release()
}

func TestWindowsDesktop(t *testing.T) {
	driver := e2eDesktop(t)

	diag := Check()
	shot, err := driver.Screenshot()
	require.NoError(t, err)
	assert.Equal(t, diag.Width, shot.Bounds().Dx())
	assert.Equal(t, diag.Height, shot.Bounds().Dy())

	backend := windowsBackend{}
	require.NoError(t, backend.MoveTo(20, 30))
	x, y, err := backend.Position()
	require.NoError(t, err)
	assert.Equal(t, [2]int{20, 30}, [2]int{x, y})

	require.NoError(t, backend.Key(KeyShift, true))
	require.NoError(t, backend.Key(KeyShift, false))
	require.NoError(t, backend.Key("?", true))
	require.NoError(t, backend.Key("?", false))
}

// Input to a window of a higher integrity level is dropped without an error,
// so the check fails before any event is sent. Equal and lower levels pass.
func TestCheckWindowIntegrity(t *testing.T) {
	const own, medium, high = 0x2000, 0x2000, 0x3000
	self := windows.GetCurrentProcessId()
	const target = uint32(4242)
	levels := map[uint32]uint32{self: own, target: high}
	defer func(orig func(uint32) (uint32, error)) { processIntegrity = orig }(processIntegrity)
	processIntegrity = func(pid uint32) (uint32, error) { return levels[pid], nil }

	err := checkWindow(WindowID{PID: target, App: "admintool"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admintool runs elevated")

	levels[target] = medium
	assert.NoError(t, checkWindow(WindowID{PID: target, App: "notepad"}))

	levels[target] = 0x1000
	assert.NoError(t, checkWindow(WindowID{PID: target, App: "browser"}))
	assert.NoError(t, checkWindow(WindowID{}))
}

func TestWindowLabelFallsBackToTitleThenPID(t *testing.T) {
	assert.Equal(t, "admintool", windowLabel(WindowID{PID: 4242, App: "admintool", Title: "Admin"}))
	assert.Equal(t, `window "Admin"`, windowLabel(WindowID{PID: 4242, Title: "Admin"}))
	assert.Equal(t, "process 4242", windowLabel(WindowID{PID: 4242}))
}

// A level that cannot be read fails closed: the window may be the elevated one.
func TestCheckWindowUnreadable(t *testing.T) {
	defer func(orig func(uint32) (uint32, error)) { processIntegrity = orig }(processIntegrity)
	self := windows.GetCurrentProcessId()
	for _, tc := range []struct {
		name    string
		window  WindowID
		readErr error
		want    string
		notWant string
	}{
		{name: "access denied names possible elevation", window: WindowID{PID: 4243, App: "locked"}, readErr: windows.ERROR_ACCESS_DENIED, want: "read the integrity level of locked", notWant: "locked runs elevated"},
		{name: "other errors retain generic context", window: WindowID{PID: 4244, App: "locked"}, readErr: windows.ERROR_INVALID_PARAMETER, want: "read the integrity level of locked", notWant: "may be elevated or protected"},
		{name: "title fallback", window: WindowID{PID: 4245, Title: "Secure Tool"}, readErr: windows.ERROR_ACCESS_DENIED, want: `window "Secure Tool"`},
		{name: "pid fallback", window: WindowID{PID: 4246}, readErr: windows.ERROR_ACCESS_DENIED, want: "process 4246"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processIntegrity = func(pid uint32) (uint32, error) {
				if pid == self {
					return 0x2000, nil
				}
				return 0, tc.readErr
			}
			err := checkWindow(tc.window)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.readErr)
			assert.Contains(t, err.Error(), tc.want)
			if tc.notWant != "" {
				assert.NotContains(t, err.Error(), tc.notWant)
			}
			if tc.readErr == windows.ERROR_ACCESS_DENIED {
				assert.Contains(t, err.Error(), "the target may be elevated or protected")
			}
		})
	}
}

func TestGetCursorPosErrorUsesInputDesktopProblemForAccessDenied(t *testing.T) {
	called := false
	err := getCursorPosError(windows.ERROR_ACCESS_DENIED, func() (Problem, bool) {
		called = true
		return Problem{Code: problemScreenLocked, Message: "the input desktop is not accessible; a secure prompt may be shown"}, true
	})
	require.ErrorIs(t, err, windows.ERROR_ACCESS_DENIED)
	assert.True(t, called)
	assert.Contains(t, err.Error(), "secure prompt")
	assert.NotContains(t, err.Error(), "GetCursorPos")
}

func TestGetCursorPosErrorKeepsGenericErrorWhenNoDesktopProblem(t *testing.T) {
	err := getCursorPosError(windows.ERROR_ACCESS_DENIED, func() (Problem, bool) {
		return Problem{}, false
	})
	require.ErrorIs(t, err, windows.ERROR_ACCESS_DENIED)
	assert.Contains(t, err.Error(), "GetCursorPos")

	other := getCursorPosError(windows.ERROR_INVALID_HANDLE, func() (Problem, bool) {
		t.Fatal("input desktop should only be checked for access denied")
		return Problem{}, false
	})
	require.ErrorIs(t, other, windows.ERROR_INVALID_HANDLE)
	assert.Contains(t, other.Error(), "GetCursorPos")
}

// The fake source above replaces readIntegrityLevel, so this reads the real
// token of the test process.
func TestReadIntegrityLevel(t *testing.T) {
	level, err := readIntegrityLevel(windows.GetCurrentProcessId())
	require.NoError(t, err)
	assert.Contains(t, []uint32{0x1000, 0x2000, 0x3000, 0x4000}, level)
}
