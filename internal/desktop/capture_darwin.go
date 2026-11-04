// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"image"
	"strconv"
)

// statusWindowLayer holds the status items an application shows in the
// menu bar, which a picture of its windows leaves out.
const statusWindowLayer = 25

// CaptureApps captures each window with the screencapture tool, which draws
// a window by its number whether or not other windows cover it. It is the
// tool the whole-screen capture runs, so a window capture needs no
// permission beyond Screen Recording and keeps working across releases
// that retire capture functions. The menu bar is captured as the screen
// shows it, since its background is the desktop's.
func (b *darwinBackend) CaptureApps(apps []string) (Capture, error) {
	if problem, ok := sessionProblem(); ok {
		return Capture{}, errors.New(problem.Message)
	}
	front := ""
	if pid := focusedApplication(); pid > 0 {
		front = processApp(pid)
	}
	menuBar := uint64(0)
	var windows []WindowID
	for _, w := range b.listWindows() {
		switch {
		case w.layer == mainMenuWindowLayer:
			// The menu bar shows the menus of the application in front.
			if appIn(front, apps) {
				menuBar = w.id.Handle
				windows = append(windows, w.id)
			}
		case w.layer >= normalWindowLayer && w.layer < overlayWindowLayer && w.layer != statusWindowLayer && appIn(w.id.App, apps):
			windows = append(windows, w.id)
		}
	}
	display := image.Rect(0, 0, b.display.pixelWidth, b.display.pixelHeight)
	return compose(display, windows, func(w WindowID) (*image.RGBA, error) {
		if w.Handle == menuBar {
			return screencapture("-R", b.pointRect(w.Bounds))
		}
		return captureWindow(w)
	})
}

// captureWindow draws a window by its number, whether or not other windows
// cover it. -o leaves out the window's shadow, so the picture is its frame.
func captureWindow(w WindowID) (*image.RGBA, error) {
	return screencapture("-o", "-l", strconv.FormatUint(w.Handle, 10))
}

// pointRect writes a rectangle of display pixels in the global points
// screencapture -R takes: x,y,width,height.
func (b *darwinBackend) pointRect(rect image.Rectangle) string {
	at := b.display.points(rect.Min)
	scale := b.display.scale
	return fmt.Sprintf("%g,%g,%g,%g", at.X, at.Y, float64(rect.Dx())/scale, float64(rect.Dy())/scale)
}
