// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"image"
	"image/draw"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/imagefit"
)

// ErrNoWindows reports applications that show no window on the primary
// display.
var ErrNoWindows = errors.New("no window shows on the primary display")

// Capture is a picture of some windows of the primary display.
type Capture struct {
	// Image shows the display pixels of its bounds.
	Image *image.RGBA
	// Windows are the windows Image shows, the front of the Z-order first.
	Windows []WindowID
}

// appIn reports whether app is one of apps, compared ignoring case.
func appIn(app string, apps []string) bool {
	return app != "" && slices.ContainsFunc(apps, func(a string) bool { return strings.EqualFold(a, app) })
}

// windowCaptureWait bounds how long a capture waits for one window's
// picture, so a window whose application stops answering cannot hold the
// step. Windows reports such an application hung after about as long, and
// later captures leave its windows out.
const windowCaptureWait = 5 * time.Second

// errWindowTimeout reports a window whose picture did not come in time.
var errWindowTimeout = errors.New("the window did not draw in time")

// compose captures windows, listed the front of the Z-order first, and
// paints each at its place on display, front over back, over black. The
// picture covers the part of display the windows cover. A window that lies
// within one in front of it shows nothing and is not captured, and one that
// cannot be captured within wait, such as a menu that closed since it was
// listed, is left out.
func compose(display image.Rectangle, windows []WindowID, wait time.Duration, capture func(WindowID) (*image.RGBA, error)) (Capture, error) {
	var kept []WindowID
	for _, w := range windows {
		hidden := slices.ContainsFunc(kept, func(front WindowID) bool { return w.Bounds.In(front.Bounds) })
		if w.Bounds.Overlaps(display) && !hidden {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return Capture{}, ErrNoWindows
	}
	// The windows are captured in parallel, so captures that each take a
	// moment, such as those that start a process, do not add up. A capture
	// that misses the deadline finishes on its own, into a channel nobody
	// reads any more.
	type shot struct {
		i   int
		img *image.RGBA
		err error
	}
	results := make(chan shot, len(kept))
	for i, w := range kept {
		go func() {
			img, err := capture(w)
			results <- shot{i, img, err}
		}()
	}
	shots := make([]*image.RGBA, len(kept))
	errs := make([]error, len(kept))
	for i := range errs {
		errs[i] = errWindowTimeout
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
collect:
	for range kept {
		select {
		case r := <-results:
			shots[r.i], errs[r.i] = r.img, r.err
		case <-deadline.C:
			break collect
		}
	}

	var shown []WindowID
	var area image.Rectangle
	for i, w := range kept {
		if errs[i] == nil {
			shown = append(shown, w)
			area = area.Union(w.Bounds)
		}
	}
	if len(shown) == 0 {
		return Capture{}, errors.Join(errs...)
	}
	canvas := image.NewRGBA(area.Intersect(display))
	draw.Draw(canvas, canvas.Bounds(), image.Black, image.Point{}, draw.Src)
	for i, k := range slices.Backward(kept) {
		if errs[i] != nil {
			continue
		}
		img, bounds := shots[i], k.Bounds
		// A window on a display of another scale is drawn at another size.
		if img.Bounds().Size() != bounds.Size() {
			img = imagefit.Resize(img, bounds.Dx(), bounds.Dy())
		}
		draw.Draw(canvas, bounds, img, img.Bounds().Min, draw.Over)
	}
	return Capture{Image: canvas, Windows: shown}, nil
}
