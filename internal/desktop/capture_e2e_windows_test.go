// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// colorWindow starts the one-color fixture window with a shell, titled
// title at rect, and waits for it to show. A topmost window stays above
// every window that is not.
func colorWindow(t *testing.T, driver *Driver, shell, title, colorName string, rect image.Rectangle, topMost bool) WindowID {
	t.Helper()
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join("testdata", "color.ps1"),
		"-Title", title, "-Color", colorName,
		"-X", strconv.Itoa(rect.Min.X), "-Y", strconv.Itoa(rect.Min.Y), "-Width", strconv.Itoa(rect.Dx()), "-Height", strconv.Itoa(rect.Dy())}
	if topMost {
		args = append(args, "-TopMost")
	}
	startApp(t, shell, args...)
	var found WindowID
	require.Eventually(t, func() bool {
		i := slices.IndexFunc(driver.Windows(), func(w WindowID) bool { return w.Title == title })
		if i < 0 {
			return false
		}
		found = driver.Windows()[i]
		return true
	}, 30*time.Second, 200*time.Millisecond, "the window %q never showed", title)
	return found
}

// isColor reports a pixel within a small distance of a color.
func isColor(c, want color.RGBA) bool {
	near := func(a, b uint8) bool { return max(a, b)-min(a, b) < 40 }
	return near(c.R, want.R) && near(c.G, want.G) && near(c.B, want.B)
}

// A window another application's window covers is captured as it draws
// itself: the screen shows the cover there, the capture the window.
func TestWindowsCaptureCoveredWindow(t *testing.T) {
	driver := e2eDesktop(t)
	magenta, lime := color.RGBA{R: 255, B: 255, A: 255}, color.RGBA{G: 255, A: 255}

	target := colorWindow(t, driver, "powershell.exe", "Dagu capture target", "Magenta", image.Rect(200, 200, 700, 500), false)
	// The cover runs in PowerShell 7, another application than the target's.
	cover := colorWindow(t, driver, "pwsh.exe", "Dagu capture cover", "Lime", image.Rect(150, 150, 750, 550), true)
	require.NotEqual(t, target.App, cover.App)
	at := centre(target.Bounds)
	require.Eventually(t, func() bool { return driver.WindowAt(at).Handle == cover.Handle }, 10*time.Second, 100*time.Millisecond,
		"the cover never lay over the target")
	screen, err := driver.Screenshot()
	require.NoError(t, err)
	require.True(t, isColor(screen.RGBAAt(at.X, at.Y), lime), "the screen shows the cover: %v", screen.RGBAAt(at.X, at.Y))

	capture, err := driver.CaptureApps([]string{target.App})
	require.NoError(t, err)
	handles := make([]uint64, 0, len(capture.Windows))
	for _, w := range capture.Windows {
		handles = append(handles, w.Handle)
	}
	assert.Contains(t, handles, target.Handle)
	assert.NotContains(t, handles, cover.Handle)
	assert.True(t, capture.Image.Bounds().In(image.Rect(0, 0, screen.Bounds().Dx(), screen.Bounds().Dy())), "%v", capture.Image.Bounds())
	got := capture.Image.RGBAAt(at.X, at.Y)
	assert.True(t, isColor(got, magenta), "the capture shows the target where the cover is: %v", got)
	for y := capture.Image.Bounds().Min.Y; y < capture.Image.Bounds().Max.Y; y += 10 {
		for x := capture.Image.Bounds().Min.X; x < capture.Image.Bounds().Max.X; x += 10 {
			require.False(t, isColor(capture.Image.RGBAAt(x, y), lime), "the cover shows at %d,%d", x, y)
		}
	}
}

// The list a combo box opens is a window of its own, and a capture of the
// application's windows shows it with the window it belongs to.
func TestWindowsCaptureShowsAComboBoxList(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	window := elementsWindow(t, driver, els)

	combo, err := els.Find(Selector{Role: RoleComboBox, ID: "methodBox"})
	require.NoError(t, err)
	require.Len(t, combo, 1)
	open, err := els.Find(Selector{Role: RoleButton, In: &Selector{Role: RoleComboBox, Name: "支払方法"}})
	require.NoError(t, err)
	require.Len(t, open, 1, "%+v", open)
	require.NoError(t, els.Apply(open[0], PatternInvoke, ""))
	t.Cleanup(func() { _ = driver.PressKeys(t.Context(), []Key{KeyEscape}, 1) })
	below := image.Pt(combo[0].Bounds.Min.X+20, combo[0].Bounds.Max.Y+12)
	require.Eventually(t, func() bool {
		item, err := els.At(below.X, below.Y)
		return err == nil && item.Role == RoleListItem
	}, 3*time.Second, 100*time.Millisecond, "the list never opened")

	capture, err := driver.CaptureApps([]string{window.App})
	require.NoError(t, err)
	var list *WindowID
	for i, w := range capture.Windows {
		if w.Title != elementsWindowTitle && below.In(w.Bounds) {
			list = &capture.Windows[i]
		}
	}
	require.NotNil(t, list, "the list is not among the captured windows: %s", describeWindows(capture.Windows))
	// A list item is drawn on a light background, which no part of the
	// black backdrop of the capture is.
	got := capture.Image.RGBAAt(below.X, below.Y)
	assert.False(t, isColor(got, color.RGBA{A: 255}), "the capture shows the list: %v", got)
}

func describeWindows(windows []WindowID) string {
	var out string
	for _, w := range windows {
		out += fmt.Sprintf("\n%d %q %s %v", w.Handle, w.Title, w.App, w.Bounds)
	}
	return out
}
