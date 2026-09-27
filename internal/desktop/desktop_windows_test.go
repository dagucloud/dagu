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
