// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"fmt"
	"image"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain draws marks when a test starts this binary as the marks
// helper, as OpenMarks starts the dagu binary.
func TestMain(m *testing.M) {
	if slices.Equal(os.Args[1:], marksHelperArgs) {
		if err := ServeMarks(os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	RegisterMarksHelper()
	os.Exit(m.Run())
}

// A mark is a window above the others that takes neither the focus nor a
// click meant for the window under it, and no capture of the screen holds
// it. Run it with DAGU_DESKTOP_E2E=1 on a desktop with the Screen
// Recording permission.
func TestMacMarks(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	require.NoError(t, load())
	backend, err := newDarwinBackend()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })
	d := backend.display
	middle := image.Pt(d.pixelWidth/2, d.pixelHeight/2)
	target := image.Rectangle{Min: middle.Sub(image.Pt(150, 40)), Max: middle.Add(image.Pt(150, 40))}
	area := target.Inset(-int(48 * d.scale))
	before, err := backend.Capture()
	require.NoError(t, err)
	require.Zero(t, countMagenta(before, area), "the screen already shows the marks' color where the test draws one")
	focused := backend.FocusedWindow()

	opened, err := OpenMarks()
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.Close() })
	// The mark stays until the test ends.
	marks := opened.(*lingeringMarks)
	marks.linger = time.Minute
	require.NoError(t, marks.Show(Mark{Target: target, Label: "test · click", Color: testMarkColor}))

	// The window server lists the window once it has drawn it, a frame
	// or two after it was shown.
	helper := uint32(marks.surface.(*helperSurface).cmd.Process.Pid) //nolint:gosec // process ids fit 32 bits
	var mark windowInfo
	require.Eventually(t, func() bool {
		windows := backend.listWindows()
		i := slices.IndexFunc(windows, func(w windowInfo) bool { return w.id.PID == helper })
		if i >= 0 {
			mark = windows[i]
		}
		return i >= 0
	}, 5*time.Second, 10*time.Millisecond, "the mark's window is not on the screen")
	assert.GreaterOrEqual(t, mark.layer, int64(overlayWindowLayer))
	assert.True(t, target.In(mark.id.Bounds), "the mark's window %v does not cover %v", mark.id.Bounds, target)
	assert.Equal(t, focused.Handle, backend.FocusedWindow().Handle, "a mark takes the focus")
	assert.NotEqual(t, helper, backend.WindowAt(middle.X, middle.Y).PID, "a mark is the window under a point")
	system := axUIElementCreateSystemWide()
	require.NotZero(t, system)
	defer cfRelease(system)
	point := d.points(middle)
	var element uintptr
	require.Equal(t, int32(axErrorSuccess), axUIElementCopyElementAtPosition(system, float32(point.X), float32(point.Y), &element))
	defer cfRelease(element)
	var pid int32
	require.Equal(t, int32(axErrorSuccess), axUIElementGetPid(element, &pid))
	assert.NotEqual(t, int32(helper), pid, "a mark is the element under a point") //nolint:gosec // process ids fit 32 bits

	shot, err := backend.Capture()
	require.NoError(t, err)
	assert.Zero(t, countMagenta(shot, area), "a capture of the screen holds the mark")
}
