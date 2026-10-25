// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// powerAssertions lists the power assertions this process holds, so one
// that another Dagu process on the machine holds does not count.
func powerAssertions(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("/usr/bin/pmset", "-g", "assertions").Output()
	require.NoError(t, err)
	owner := fmt.Sprintf("pid %d(", os.Getpid())
	var own strings.Builder
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(strings.TrimSpace(line), owner) {
			own.WriteString(line)
		}
	}
	return own.String()
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
	// The window server applies a posted event a moment later.
	var x, y int
	assert.Eventually(t, func() bool {
		x, y, err = backend.Position()
		return err == nil && abs(x-200) <= 2 && abs(y-300) <= 2
	}, time.Second, 10*time.Millisecond, "the pointer is at %d,%d, not 200,300", x, y)
}

// TestDarwinWindows opens windows of its own and changes their size and
// order, so it runs only when asked to with DAGU_DESKTOP_E2E=1 on a desktop
// with the required permissions.
func TestDarwinWindows(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	require.NoError(t, Check().Err())
	backend, err := newDarwinBackend()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })

	fixture := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "testdata/windows.js")
	require.NoError(t, fixture.Start())
	t.Cleanup(func() {
		_ = fixture.Process.Kill()
		_ = fixture.Wait()
	})
	find := func(title string) WindowID {
		var found WindowID
		require.Eventually(t, func() bool {
			for _, w := range backend.Windows() {
				if w.Title == title && w.PID == uint32(fixture.Process.Pid) { //nolint:gosec // process ids fit 32 bits
					found = w
					return true
				}
			}
			return false
		}, 15*time.Second, 100*time.Millisecond, "the window %q never appeared", title)
		return found
	}
	window, fixed := find("dagu-window-e2e"), find("dagu-fixed-e2e")

	assert.Equal(t, "osascript", window.App)
	assert.InDelta(t, 400*backend.display.scale, window.Bounds.Dx(), 2)
	assert.Equal(t, window.Handle, backend.WindowAt(window.Bounds.Min.X+20, window.Bounds.Min.Y+20).Handle)

	require.NoError(t, backend.Raise(window))
	require.Eventually(t, func() bool { return backend.FocusedWindow().Handle == window.Handle }, 5*time.Second, 100*time.Millisecond)

	maximized, err := backend.Maximize(fixed)
	require.NoError(t, err)
	assert.False(t, maximized, "a window of fixed size keeps its size")

	maximized, err = backend.Maximize(window)
	require.NoError(t, err)
	assert.True(t, maximized)
	area, err := backend.workArea()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return nearRect(find("dagu-window-e2e").Bounds, backend.toPixelRect(area), 4)
	}, 5*time.Second, 100*time.Millisecond, "the window fills the work area")
	maximized, err = backend.Maximize(find("dagu-window-e2e"))
	require.NoError(t, err)
	assert.False(t, maximized, "a maximized window is left as it is")
}

func TestRoleOf(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		role, subrole, parent string
		want                  string
	}{
		{"AXButton", "", "AXGroup", RoleButton},
		{"AXTextField", "AXSecureTextField", "AXGroup", RoleTextField},
		{"AXTextArea", "", "AXScrollArea", RoleTextField},
		{"AXStaticText", "", "AXGroup", RoleText},
		{"AXCheckBox", "AXToggle", "AXGroup", RoleCheckbox},
		{"AXRadioButton", "", "AXRadioGroup", RoleRadio},
		{"AXRadioButton", "", "AXTabGroup", RoleTab},
		{"AXRadioButton", "AXTabButton", "AXGroup", RoleTab},
		{"AXPopUpButton", "", "AXGroup", RoleComboBox},
		{"AXRow", "AXTableRow", "AXTable", RoleListItem},
		{"AXCell", "", "AXRow", RoleCell},
		{"AXTable", "", "AXScrollArea", RoleList},
		{"AXGroup", "", "AXWindow", RoleGroup},
		{"AXWindow", "AXStandardWindow", "AXApplication", RoleWindow},
		{"AXScrollArea", "", "AXWindow", RoleOther},
	} {
		assert.Equal(t, tc.want, roleOf(tc.role, tc.subrole, tc.parent), "%s/%s in %s", tc.role, tc.subrole, tc.parent)
	}
}

// An element's value reads as text whether the application gives a string
// or a boolean, as a checkbox's state may come.
func TestValueText(t *testing.T) {
	t.Parallel()

	require.NoError(t, load())
	assert.Equal(t, "1", valueText(cfBooleanTrue))
	text := cfString("保存しました")
	require.NotZero(t, text)
	defer cfRelease(text)
	assert.Equal(t, "保存しました", valueText(text))
}
