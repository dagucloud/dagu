// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filled is a picture of one color the size of a rectangle.
func filled(rect image.Rectangle, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

var (
	red   = color.RGBA{R: 255, A: 255}
	blue  = color.RGBA{B: 255, A: 255}
	green = color.RGBA{G: 255, A: 255}
	black = color.RGBA{A: 255}
)

// shooter captures windows from pictures by handle and counts the
// windows it was asked for.
type shooter struct {
	mu     sync.Mutex
	shots  map[uint64]*image.RGBA
	failed map[uint64]bool
	asked  []uint64
}

func (s *shooter) capture(w WindowID) (*image.RGBA, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, w.Handle)
	if s.failed[w.Handle] {
		return nil, errors.New("the window closed")
	}
	return s.shots[w.Handle], nil
}

// The windows are painted at their places, the front one over the one
// behind, on black where neither is, over the part of the display they
// cover.
func TestComposePaintsWindowsInOrder(t *testing.T) {
	t.Parallel()

	display := image.Rect(0, 0, 1000, 800)
	front := WindowID{Handle: 1, Bounds: image.Rect(100, 100, 300, 300)}
	back := WindowID{Handle: 2, Bounds: image.Rect(200, 200, 500, 400)}
	s := &shooter{shots: map[uint64]*image.RGBA{1: filled(front.Bounds, red), 2: filled(back.Bounds, blue)}}

	got, err := compose(display, []WindowID{front, back}, s.capture)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(100, 100, 500, 400), got.Image.Bounds())
	assert.Equal(t, []WindowID{front, back}, got.Windows)
	assert.Equal(t, red, got.Image.RGBAAt(250, 250), "the front window covers the one behind")
	assert.Equal(t, blue, got.Image.RGBAAt(400, 350))
	assert.Equal(t, black, got.Image.RGBAAt(450, 150), "no window is there")
}

// A window inside one in front of it shows nothing and is not captured; a
// window off the display is left out, and one partly off it is cut to it.
func TestComposeLeavesOutHiddenWindows(t *testing.T) {
	t.Parallel()

	display := image.Rect(0, 0, 1000, 800)
	front := WindowID{Handle: 1, Bounds: image.Rect(800, 600, 1200, 900)}
	inside := WindowID{Handle: 2, Bounds: image.Rect(850, 650, 900, 700)}
	away := WindowID{Handle: 3, Bounds: image.Rect(1100, 0, 1300, 200)}
	s := &shooter{shots: map[uint64]*image.RGBA{1: filled(front.Bounds, red)}}

	got, err := compose(display, []WindowID{front, inside, away}, s.capture)
	require.NoError(t, err)
	assert.Equal(t, []uint64{1}, s.asked)
	assert.Equal(t, []WindowID{front}, got.Windows)
	assert.Equal(t, image.Rect(800, 600, 1000, 800), got.Image.Bounds())
}

// A window that cannot be captured is left out of the picture; when none
// can be, the capture fails, and with no window on the display it fails
// with ErrNoWindows.
func TestComposeLeavesOutFailedWindows(t *testing.T) {
	t.Parallel()

	display := image.Rect(0, 0, 1000, 800)
	menu := WindowID{Handle: 1, Bounds: image.Rect(0, 0, 100, 100)}
	window := WindowID{Handle: 2, Bounds: image.Rect(200, 200, 400, 400)}
	s := &shooter{shots: map[uint64]*image.RGBA{2: filled(window.Bounds, blue)}, failed: map[uint64]bool{1: true}}

	got, err := compose(display, []WindowID{menu, window}, s.capture)
	require.NoError(t, err)
	assert.Equal(t, []WindowID{window}, got.Windows)
	assert.Equal(t, window.Bounds, got.Image.Bounds())

	_, err = compose(display, []WindowID{menu}, s.capture)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoWindows)

	_, err = compose(display, nil, s.capture)
	assert.ErrorIs(t, err, ErrNoWindows)
}

// A window drawn at another scale, as on a display of another density, is
// fitted to its place.
func TestComposeFitsAWindowDrawnAtAnotherScale(t *testing.T) {
	t.Parallel()

	window := WindowID{Handle: 1, Bounds: image.Rect(10, 10, 60, 40)}
	s := &shooter{shots: map[uint64]*image.RGBA{1: filled(image.Rect(0, 0, 100, 60), green)}}

	got, err := compose(image.Rect(0, 0, 100, 100), []WindowID{window}, s.capture)
	require.NoError(t, err)
	assert.Equal(t, window.Bounds, got.Image.Bounds())
	assert.Equal(t, green, got.Image.RGBAAt(59, 39))
}
