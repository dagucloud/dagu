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
