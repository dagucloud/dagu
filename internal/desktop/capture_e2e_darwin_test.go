// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A window another window covers is captured as it draws itself. The
// fixtures take no focus and nothing clicks them, so the test runs only
// when asked to with DAGU_DESKTOP_E2E=1, with Screen Recording granted.
func TestDarwinCaptureCoveredWindow(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	require.NoError(t, load())
	require.True(t, cgPreflightScreenCaptureAccess(), "Screen Recording is not granted")
	backend, err := newDarwinBackend()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })

	target := findWindow(t, backend, startFixture(t, "testdata/color.js", nil, "dagu-capture-e2e", "magenta", "40", "60", "300", "200"), "dagu-capture-e2e")
	cover := findWindow(t, backend, startFixture(t, "testdata/color.js", nil, "dagu-cover-e2e", "green", "20", "40", "340", "240"), "dagu-cover-e2e")
	at := target.Bounds.Min.Add(target.Bounds.Size().Div(2))
	require.Eventually(t, func() bool { return backend.WindowAt(at.X, at.Y).Handle == cover.Handle }, 5*time.Second, 100*time.Millisecond,
		"the cover never lay over the target")

	shot, err := captureWindow(target)
	require.NoError(t, err)
	assert.Equal(t, target.Bounds.Size(), shot.Bounds().Size(), "the picture is the window's frame")
	got := shot.RGBAAt(shot.Bounds().Dx()/2, shot.Bounds().Dy()/2)
	assert.True(t, got.R > 180 && got.G < 120 && got.B > 180, "the capture shows the target's magenta, not the cover: %v", got)

	// Both fixtures are osascript's, so the application's capture shows
	// them in their order, the cover in front.
	capture, err := backend.CaptureApps([]string{target.App})
	require.NoError(t, err)
	front, ok := windowIn(capture.Windows, at)
	require.True(t, ok)
	assert.Equal(t, cover.Handle, front.Handle)
	assert.True(t, capture.Image.Bounds().Overlaps(target.Bounds))
}

// windowIn names the front window of a list at a position.
func windowIn(windows []WindowID, at image.Point) (WindowID, bool) {
	for _, w := range windows {
		if at.In(w.Bounds) {
			return w, true
		}
	}
	return WindowID{}, false
}
