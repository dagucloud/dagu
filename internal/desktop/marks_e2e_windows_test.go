// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markArea is how far around its target a mark, label included, may
// reach.
const markArea = 48

// openTestMarks opens marks that stay until the test ends, drawn in a
// window left out of screen captures when exclude is set, and returns the
// window too.
func openTestMarks(t *testing.T, exclude bool) (Marks, uintptr) {
	t.Helper()
	s, err := openWindowSurface(exclude)
	require.NoError(t, err)
	marks := newMarks(s)
	marks.linger = time.Minute
	t.Cleanup(func() { _ = marks.Close() })
	return marks, s.hwnd
}

// A mark over a window takes neither the focus nor a click, and no capture
// of the screen holds it: a capture that holds the same mark drawn
// without the exclusion, shown after it, leaves it out.
func TestWindowsMarks(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	window := elementsWindow(t, driver, els)
	ctx := t.Context()

	outline, err := els.Outline(window, 0)
	require.NoError(t, err)
	byID := map[string]Element{}
	for _, e := range outline {
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	for _, id := range []string{"saveButton", "paymentSaveButton", "statusLabel"} {
		require.Contains(t, byID, id, "outline: %+v", outline)
	}
	save, other := byID["saveButton"].Bounds, byID["paymentSaveButton"].Bounds
	focused := driver.FocusedWindow()
	require.Equal(t, elementsWindowTitle, focused.Title)

	excluded, hwnd := openTestMarks(t, true)
	shown, _ := openTestMarks(t, false)
	require.NoError(t, excluded.Show(Mark{Target: save, Label: "excluded · click", Color: testMarkColor}))
	require.NoError(t, shown.Show(Mark{Target: other, Label: "shown · click", Color: testMarkColor}))
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	require.NotZero(t, visible, "the mark's window is not on the screen")

	assert.Equal(t, focused.Handle, driver.FocusedWindow().Handle, "a mark takes the focus")
	under := centre(save)
	assert.Equal(t, focused.Handle, driver.WindowAt(under).Handle, "a mark is the window under a point")
	at, err := els.At(under.X, under.Y)
	require.NoError(t, err)
	assert.Equal(t, "saveButton", at.ID, "a mark is the element under a point")

	// Once a capture holds the mark shown second, the desktop has drawn
	// the first one too.
	var shot *image.RGBA
	require.Eventually(t, func() bool {
		shot, err = driver.Screenshot()
		return err == nil && countMagenta(shot, other.Inset(-markArea)) > 0
	}, 10*time.Second, 100*time.Millisecond, "no capture holds the mark drawn without the exclusion: %v", err)
	assert.Zero(t, countMagenta(shot, save.Inset(-markArea)), "a capture of the screen holds the mark")

	// A click on the button under the mark reaches the button.
	require.NoError(t, driver.Click(ctx, &under, ButtonLeft, 1, nil))
	status := centre(byID["statusLabel"].Bounds)
	assert.Eventually(t, func() bool {
		e, err := els.At(status.X, status.Y)
		return err == nil && e.Name == "保存しました"
	}, 3*time.Second, 100*time.Millisecond, "the click did not reach the button under the mark")
}
