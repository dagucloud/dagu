// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyCode(t *testing.T) {
	t.Parallel()

	for key, want := range map[Key]struct {
		code  uint16
		shift bool
	}{
		KeyEnter: {0x24, false},
		KeyMeta:  {0x37, false},
		"f5":     {0x60, false},
		"a":      {0x00, false},
		"?":      {0x2C, true},
	} {
		code, shift, err := keyCode(key)
		require.NoError(t, err, key)
		assert.Equal(t, want.code, code, key)
		assert.Equal(t, want.shift, shift, key)
	}

	for _, key := range []Key{"f24", KeyPrintScreen} {
		_, _, err := keyCode(key)
		assert.Error(t, err, key)
	}
}

// A logged-in session reports whether it has the display, which checks the
// Core Foundation lookups behind the locked-screen check.
func TestDictionaryFlag(t *testing.T) {
	t.Parallel()
	require.NoError(t, load())
	session := cgSessionCopyCurrentDictionary()
	if session == 0 {
		t.Skip("no graphical login session")
	}
	t.Cleanup(func() { cfRelease(session) })

	onConsole, ok := dictionaryFlag(session, sessionOnConsoleKey)
	require.True(t, ok)
	assert.True(t, onConsole)
	_, ok = dictionaryFlag(session, "DaguNoSuchSessionKey")
	assert.False(t, ok)
}

// The trust check's prompt option is set to true, which is what makes macOS
// show the Accessibility prompt.
func TestAccessibilityPromptOptions(t *testing.T) {
	t.Parallel()
	require.NoError(t, load())
	options := accessibilityPromptOptions()
	require.NotZero(t, options)
	t.Cleanup(func() { cfRelease(options) })

	prompt, ok := dictionaryFlag(options, axTrustedCheckOptionPrompt)
	require.True(t, ok)
	assert.True(t, prompt)
}

// The window server reports when it last received input, which lies in the
// past.
func TestLastInput(t *testing.T) {
	t.Parallel()
	require.NoError(t, load())
	last := (&darwinBackend{}).LastInput()
	assert.False(t, last.IsZero())
	assert.False(t, last.After(time.Now()))
}

// While held, the power assertions are listed by pmset under the reason
// Dagu gives; releasing them removes them.
func TestKeepAwake(t *testing.T) {
	require.NoError(t, load())
	release, err := keepAwake()
	require.NoError(t, err)
	assert.Contains(t, powerAssertions(t), awakeReason)
	release()
	assert.NotContains(t, powerAssertions(t), awakeReason)
}

func powerAssertions(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("/usr/bin/pmset", "-g", "assertions").Output()
	require.NoError(t, err)
	return string(out)
}

// TestDarwinDesktop moves the pointer, so it runs only when asked to with
// DAGU_DESKTOP_E2E=1 on a desktop with the required permissions.
func TestDarwinDesktop(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	driver, err := Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Close() })

	diag := Check()
	shot, err := driver.Screenshot()
	require.NoError(t, err)
	assert.Equal(t, diag.Width, shot.Bounds().Dx())
	assert.Equal(t, diag.Height, shot.Bounds().Dy())

	backend, err := newDarwinBackend()
	require.NoError(t, err)
	require.NoError(t, backend.MoveTo(200, 300))
	x, y, err := backend.Position()
	require.NoError(t, err)
	assert.InDelta(t, 200, x, 2)
	assert.InDelta(t, 300, y, 2)
}
