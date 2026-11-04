// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// labelled is a white screen with a dark button and a label of text-sized
// letters on it: label picks which blocks of each letter are dark, so two
// labels have the same size and differ only in their letters.
func labelled(label int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	black := color.RGBA{A: 255}
	// Seven letters of 3 by 8 blocks of 2 pixels, each block dark or not.
	for letter := range 7 {
		for bx := range 3 {
			for by := range 8 {
				if (letter+1+bx*by*label+by*label*label)%3 != 0 {
					continue
				}
				for dy := range 2 {
					for dx := range 2 {
						img.Set(372+letter*8+bx*2+dx, 292+by*2+dy, black)
					}
				}
			}
		}
	}
	return img
}

// testKey stands for the installation key the tests record and replay with.
var testKey = newTargetKey("one")

// targetTurn records a click at the label on a screen.
func targetTurn(t *testing.T, seen *image.RGBA, at image.Point) (view, recordedTurn) {
	t.Helper()
	v := displayView(seen)
	rec := v.place(recordedAction{Action: clickAt(at.X, at.Y)}, seen, testKey)
	require.NotNil(t, rec.Target)
	require.NotEmpty(t, rec.TargetCells)
	return v, recordedTurn{Screen: v.fingerprint(seen), Actions: []recordedAction{rec}}
}

// A button relabelled with other text of the same size leaves the target's
// fingerprint within its limit, but not its pixels, so the replay is refused.
func TestMatchesRefusesATargetWithOtherText(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	seen, relabelled := labelled(1), labelled(2)
	v, turn := targetTurn(t, seen, at)

	assert.LessOrEqual(t, v.targetPrint(relabelled, at).Distance(*turn.Actions[0].Target), replayTargetDistance,
		"the fingerprint alone accepts the other label")
	assert.False(t, matches(relabelled, v, turn, testKey))
}

// A dark bar across the target, two squares wide and as tall as the area,
// moves the fingerprint past its limit. The squares alone would take it for
// a caret, so the fingerprint is what refuses it.
func TestMatchesKeepsTheFingerprintCheck(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	v, turn := targetTurn(t, labelled(1), at)
	barred := labelled(1)
	for y := 268; y < 332; y++ {
		for x := 384; x < 392; x++ {
			barred.Set(x, y, color.RGBA{A: 255})
		}
	}

	assert.Greater(t, v.targetPrint(barred, at).Distance(*turn.Actions[0].Target), replayTargetDistance,
		"the fingerprint refuses the bar")
	assert.False(t, matches(barred, v, turn, testKey))
}

// upscaled draws an image at an integer display scale, as a display with
// more pixels per point captures the same screen.
func upscaled(img *image.RGBA, scale int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()*scale, b.Dy()*scale))
	for y := range out.Bounds().Dy() {
		for x := range out.Bounds().Dx() {
			out.SetRGBA(x, y, img.RGBAAt(x/scale, y/scale))
		}
	}
	return out
}

// The same screen, and one where only a caret blinks in the target, replay
// at display scale 1 and 2. Each caret lies across the boundary of two
// squares; at scale 2 it is twice as wide and tall.
func TestMatchesAcceptsATargetThatOnlyBlinks(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scale, caretX int
	}{
		{scale: 1, caretX: 371},
		{scale: 2, caretX: 762},
	} {
		at := image.Pt(400*tc.scale, 300*tc.scale)
		v, turn := targetTurn(t, upscaled(labelled(1), tc.scale), at)
		assert.True(t, matches(upscaled(labelled(1), tc.scale), v, turn, testKey), "scale %d", tc.scale)

		caret := upscaled(labelled(1), tc.scale)
		for y := 290 * tc.scale; y < 310*tc.scale; y++ {
			for x := tc.caretX; x < tc.caretX+2*tc.scale; x++ {
				caret.Set(x, y, color.RGBA{A: 255})
			}
		}
		// The caret is inside the compared area, so the cells do change.
		assert.NotEqual(t, turn.Actions[0].TargetCells, v.targetCells(caret, at, testKey), "scale %d", tc.scale)
		assert.True(t, matches(caret, v, turn, testKey), "scale %d", tc.scale)
	}
}

// A recording made before the pixels were kept has only the fingerprint, and
// replays on it as it did.
func TestMatchesUsesTheFingerprintOfAnOlderRecording(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	seen := labelled(1)
	v, turn := targetTurn(t, seen, at)
	turn.Actions[0].TargetCells = nil

	assert.True(t, matches(labelled(2), v, turn, testKey))
	assert.False(t, matches(stripes(800, 600, 5), v, turn, testKey))
}

// Cells made under another installation key cannot be compared, nor can any
// when the key cannot be read, so the recording replays on its fingerprint
// as one made before the cells existed.
func TestMatchesUnderAnotherKeyUsesTheFingerprint(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	v, turn := targetTurn(t, labelled(1), at)

	for _, key := range []*targetKey{newTargetKey("other"), nil} {
		assert.True(t, matches(labelled(2), v, turn, key))
		assert.False(t, matches(stripes(800, 600, 5), v, turn, key))
	}
}

// The recording keeps keyed digests of the area around a click, not its
// picture: without the key, nothing in the recording tests a guess of what
// the area shows, such as text a person typed.
func TestTargetCellsKeepNoPixels(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	seen := labelled(1)
	_, turn := targetTurn(t, seen, at)
	cells := turn.Actions[0].TargetCells

	assert.NotContains(t, string(cells), "\x89PNG")
	assert.Equal(t, targetKeyID+targetDigest*16*16, len(cells))
	other := displayView(seen).targetCells(seen, at, newTargetKey("other"))
	for i := 0; i < len(cells); i += targetDigest {
		require.NotEqual(t, cells[i:i+targetDigest], other[i:i+targetDigest], "another key changes every digest, at byte %d", i)
	}
}

// A click near a window's edge replays in a window a pixel or two off in size,
// larger or smaller, which findWindow accepts: the last pixels of the window,
// which are the desktop's in one and the window's in the other, do not count.
func TestMatchesAtTheEdgeOfAWindowOffInSize(t *testing.T) {
	t.Parallel()

	paint := func(right, bottom int) *image.RGBA {
		img := labelled(1)
		for y := range 600 {
			for x := range 800 {
				if x >= right || y >= bottom {
					img.Set(x, y, color.RGBA{R: 90, G: 90, B: 90, A: 255})
				}
			}
		}
		return img
	}
	window := &recordedWindow{App: "app", Width: 400, Height: 300}
	recordedView := view{rect: image.Rect(100, 100, 500, 400), window: window}
	seen := paint(500, 400)
	at := image.Pt(495, 395)
	rec := recordedView.place(recordedAction{Action: clickAt(at.X, at.Y)}, seen, testKey)
	turn := recordedTurn{Screen: recordedView.fingerprint(seen), Window: window, Actions: []recordedAction{rec}}

	for _, grown := range []int{-2, -1, 0, 1, 2} {
		current := paint(500+grown, 400+grown)
		now := view{rect: image.Rect(100, 100, 500+grown, 400+grown), window: window}
		assert.True(t, matches(current, now, turn, testKey), "window %d pixels off in size", grown)
	}
	moved := paint(500, 400)
	moved.Set(480, 380, color.RGBA{A: 255})
	for y := 340; y < 400; y += 2 {
		for x := 440; x < 500; x += 2 {
			moved.Set(x, y, color.RGBA{A: 255})
		}
	}
	assert.False(t, matches(moved, recordedView, turn, testKey), "a change inside the window still refuses")
}
