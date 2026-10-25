// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	// The pointer is where it was moved once the move returns, so a press
	// made at the pointer lands there.
	require.NoError(t, backend.MoveTo(200, 300))
	x, y, err := backend.Position()
	require.NoError(t, err)
	assert.InDelta(t, 200, x, 2)
	assert.InDelta(t, 300, y, 2)
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

	fixture := startFixture(t, "testdata/windows.js", nil)
	find := func(title string) WindowID { return findWindow(t, backend, fixture, title) }
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

// A click on a window in the background reaches it, though the window takes
// no first click and would otherwise only come forward; and it does under a
// window that lets clicks through, as Notification Center's does while a
// banner shows, whether another application's or the window's own.
func TestDarwinClickReachesBackgroundWindow(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	require.NoError(t, Check().Err())
	backend, err := newDarwinBackend()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })

	// overlay is the window that lets clicks through: none, another
	// application's, or one of the probe's own.
	const none, another, own = "", "another", "own"
	for _, tc := range []struct {
		name    string
		overlay string
	}{
		{"behind another application", none},
		{"under another application's window that lets clicks through", another},
		{"under its own window that lets clicks through", own},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var presses pressLog
			var args []string
			if tc.overlay == own {
				args = append(args, "overlay")
			}
			probe := startFixture(t, "testdata/click.js", &presses, args...)
			target := findWindow(t, backend, probe, "dagu-click-e2e")
			// Another application takes the focus, its windows away from the
			// probe's.
			front := startFixture(t, "testdata/windows.js", nil)
			require.NoError(t, backend.Raise(findWindow(t, backend, front, "dagu-fixed-e2e")))
			require.Eventually(t, func() bool { return frontmost(front) && !frontmost(probe) }, 15*time.Second, 100*time.Millisecond,
				"the other application never took the focus")
			centre := image.Pt((target.Bounds.Min.X+target.Bounds.Max.X)/2, (target.Bounds.Min.Y+target.Bounds.Max.Y)/2)
			switch tc.overlay {
			case another:
				findWindow(t, backend, startFixture(t, "testdata/overlay.js", nil), "dagu-overlay-e2e")
			case own:
				findWindow(t, backend, probe, "dagu-click-overlay-e2e")
			}
			if tc.overlay != none {
				assert.Equal(t, target.Handle, backend.WindowAt(centre.X, centre.Y).Handle, "the probe's window is at its centre")
			}

			require.NoError(t, backend.MoveTo(centre.X, centre.Y))
			require.NoError(t, backend.Button(ButtonLeft, true, 1))
			require.NoError(t, backend.Button(ButtonLeft, false, 1))

			require.Eventually(t, func() bool { return presses.count() == 1 }, 5*time.Second, 50*time.Millisecond,
				"the click never reached the window in the background")
			assert.True(t, frontmost(probe), "the probe has the focus")
		})
	}
}

// The window at a position is the front one there, menus and the menu bar
// included; overlays, and a window above the applications' that lets the
// click through to an element in another window, are looked through. A
// click lands on the window at a position only when it is an application
// window.
func TestWindowAt(t *testing.T) {
	t.Parallel()

	window := func(handle uint64, pid uint32, layer int64, bounds image.Rectangle) windowInfo {
		return windowInfo{id: WindowID{Handle: handle, PID: pid, Bounds: bounds}, layer: layer}
	}
	const app, other = 1, 2
	front := window(1, app, normalWindowLayer, image.Rect(0, 30, 400, 300))
	behind := window(2, other, normalWindowLayer, image.Rect(300, 30, 900, 600))
	menuBar := window(4, 40, mainMenuWindowLayer, image.Rect(0, 0, 2000, 30))
	menu := window(5, app, 101, image.Rect(500, 100, 700, 300))
	// Notification Center's window covers the screen while a banner shows.
	banner := window(6, 60, 21, image.Rect(0, 0, 2000, 2000))
	// A floating window of the application's own, over part of its window.
	floating := window(7, app, 3, image.Rect(0, 30, 200, 300))
	windows := []windowInfo{
		window(3, 30, overlayWindowLayer, image.Rect(0, 0, 2000, 2000)),
		menuBar, menu, banner, floating, front, behind,
	}

	for _, tc := range []struct {
		name  string
		at    image.Point
		hit   uint64
		want  uint64
		click uint64
	}{
		{"where two windows overlap", image.Pt(350, 100), front.id.Handle, front.id.Handle, front.id.Handle},
		{"on a window behind another", image.Pt(800, 500), behind.id.Handle, behind.id.Handle, behind.id.Handle},
		{"on an open menu", image.Pt(600, 200), menu.id.Handle, menu.id.Handle, 0},
		{"on the menu bar", image.Pt(1500, 10), front.id.Handle, menuBar.id.Handle, 0},
		{"on a banner", image.Pt(1500, 100), banner.id.Handle, banner.id.Handle, 0},
		{"on the application's floating window", image.Pt(100, 100), floating.id.Handle, floating.id.Handle, 0},
		{"through the application's own window that lets clicks through", image.Pt(100, 100), front.id.Handle, front.id.Handle, front.id.Handle},
		{"where the hit test cannot tell", image.Pt(350, 100), 0, banner.id.Handle, 0},
		{"on the desktop", image.Pt(1500, 1500), 99, 0, 0},
	} {
		holds := func(_ image.Point, w windowInfo) (bool, bool) { return w.id.Handle == tc.hit, tc.hit != 0 }
		got, ok := windowAt(windows, tc.at, holds)
		assert.Equal(t, tc.want != 0, ok, tc.name)
		assert.Equal(t, tc.want, got.id.Handle, tc.name)
		clicked, ok := clickTarget(windows, tc.at, holds)
		assert.Equal(t, tc.click != 0, ok, tc.name)
		assert.Equal(t, tc.click, clicked.Handle, tc.name)
	}
}

// startFixture runs a JXA fixture with arguments until the test ends, its
// standard output going to stdout when that is set.
func startFixture(t *testing.T, script string, stdout io.Writer, args ...string) *exec.Cmd {
	t.Helper()
	fixture := exec.Command("/usr/bin/osascript", append([]string{"-l", "JavaScript", script}, args...)...) //nolint:gosec // fixed tool and test fixtures
	fixture.Stdout = stdout
	require.NoError(t, fixture.Start())
	t.Cleanup(func() {
		_ = fixture.Process.Kill()
		_ = fixture.Wait()
	})
	return fixture
}

// findWindow waits for a fixture's window with a title to show, at any
// layer.
func findWindow(t *testing.T, backend *darwinBackend, fixture *exec.Cmd, title string) WindowID {
	t.Helper()
	var found WindowID
	require.Eventually(t, func() bool {
		for _, w := range backend.listWindows() {
			if w.id.Title == title && w.id.PID == uint32(fixture.Process.Pid) { //nolint:gosec // process ids fit 32 bits
				found = w.id
				return true
			}
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "the window %q never appeared", title)
	return found
}

// frontmost reports whether a fixture's application is the active one, as
// the application itself tells it.
func frontmost(fixture *exec.Cmd) bool {
	app := axUIElementCreateApplication(int32(fixture.Process.Pid)) //nolint:gosec // process ids fit 32 bits
	if app == 0 {
		return false
	}
	defer cfRelease(app)
	active, _ := axBool(app, "AXFrontmost")
	return active
}

// pressLog collects what the click fixture prints, a line for each press.
type pressLog struct {
	mu  sync.Mutex
	out strings.Builder
}

func (l *pressLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out.Write(p)
}

func (l *pressLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.out.String(), "clicked\n")
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
