// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A mark's label sits above its frame, below it at the top of the screen,
// and inside it around a window that fills the screen, and always on the
// screen.
func TestMarkLayout(t *testing.T) {
	t.Parallel()

	screen := image.Rect(0, 0, 800, 600)
	label := image.Pt(120, 20)
	for _, tc := range []struct {
		name          string
		target        image.Rectangle
		frame, box    image.Rectangle
		windowCovered image.Rectangle
	}{
		{"above", image.Rect(100, 100, 200, 130), image.Rect(97, 97, 203, 133), image.Rect(97, 77, 217, 97), image.Rect(97, 77, 217, 133)},
		{"below at the top", image.Rect(100, 5, 200, 30), image.Rect(97, 2, 203, 33), image.Rect(97, 33, 217, 53), image.Rect(97, 2, 217, 53)},
		{"inside a full screen", screen, screen, image.Rect(0, 3, 120, 23), screen},
		{"kept on the screen", image.Rect(750, 100, 790, 130), image.Rect(747, 97, 793, 133), image.Rect(680, 77, 800, 97), image.Rect(680, 77, 800, 133)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			window, frame, box := markLayout(tc.target, screen, label, 3)
			assert.Equal(t, tc.frame, frame)
			assert.Equal(t, tc.box, box)
			assert.Equal(t, tc.windowCovered, window)
		})
	}
	window, _, _ := markLayout(image.Rect(900, 100, 950, 130), screen, label, 3)
	assert.True(t, window.Empty(), "a target off the screen shows nothing")
}

// fakeSurface records what marks draw.
type fakeSurface struct {
	mu     sync.Mutex
	events []string
	err    error
}

func (s *fakeSurface) record(event string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return s.err
}

func (s *fakeSurface) draw(m Mark) error { return s.record("draw " + m.Label) }
func (s *fakeSurface) clear() error      { return s.record("clear") }
func (s *fakeSurface) close() error      { return s.record("close") }

func (s *fakeSurface) drawn() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// A mark goes once its linger passes, unless another showed meanwhile.
func TestMarksLinger(t *testing.T) {
	t.Parallel()

	s := &fakeSurface{}
	marks := newMarks(s)
	require.NoError(t, marks.Show(Mark{Label: "post · click"}))
	require.NoError(t, marks.Show(Mark{Label: "post · type"}))
	marks.lapse(1)
	assert.Equal(t, []string{"draw post · click", "draw post · type"}, s.drawn(), "the second mark is still in its linger")
	marks.lapse(2)
	assert.Equal(t, []string{"draw post · click", "draw post · type", "clear"}, s.drawn())

	quick := &fakeSurface{}
	marks = newMarks(quick)
	marks.linger = time.Millisecond
	require.NoError(t, marks.Show(Mark{Label: "post · click"}))
	assert.Eventually(t, func() bool { return len(quick.drawn()) == 2 }, 5*time.Second, time.Millisecond, "the mark never went")
}

// A surface that fails is not asked again, so a broken one cannot hold
// the actions up, and closing still closes it.
func TestMarksStopAfterAFailure(t *testing.T) {
	t.Parallel()

	s := &fakeSurface{err: errors.New("the helper exited")}
	marks := newMarks(s)
	require.Error(t, marks.Show(Mark{Label: "post · click"}))
	require.Error(t, marks.Show(Mark{Label: "post · type"}))
	s.err = nil
	require.NoError(t, marks.Close())
	assert.Equal(t, []string{"draw post · click", "close"}, s.drawn())
	assert.Error(t, marks.Show(Mark{Label: "post · scroll"}), "closed marks show nothing")
}
