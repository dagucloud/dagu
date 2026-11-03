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
// strokes on it: label picks which strokes, so two labels have the same size
// and differ only in their letters.
func labelled(label int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	black := color.RGBA{A: 255}
	for stroke := range 7 {
		x := 372 + stroke*8
		top, bottom := 292, 308
		if (stroke*label)%3 == 1 {
			top = 298
		}
		for y := top; y < bottom; y++ {
			for dx := range 3 {
				img.Set(x+dx, y, black)
			}
		}
	}
	return img
}

// targetTurn records a click at the label on a screen.
func targetTurn(t *testing.T, seen *image.RGBA, at image.Point) (view, recordedTurn) {
	t.Helper()
	v := displayView(seen)
	rec := v.place(recordedAction{Action: clickAt(at.X, at.Y)}, seen)
	require.NotNil(t, rec.Target)
	require.NotEmpty(t, rec.TargetPixels)
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
	assert.False(t, matches(relabelled, v, turn))
}

// The same screen, and one where only a caret blinks in the target, replay.
func TestMatchesAcceptsATargetThatOnlyBlinks(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	seen := labelled(1)
	v, turn := targetTurn(t, seen, at)
	assert.True(t, matches(labelled(1), v, turn))

	caret := labelled(1)
	for y := 290; y < 310; y++ {
		caret.Set(366, y, color.RGBA{A: 255})
		caret.Set(367, y, color.RGBA{A: 255})
	}
	assert.True(t, matches(caret, v, turn))
}

// A recording made before the pixels were kept has only the fingerprint, and
// replays on it as it did.
func TestMatchesUsesTheFingerprintOfAnOlderRecording(t *testing.T) {
	t.Parallel()

	at := image.Pt(400, 300)
	seen := labelled(1)
	v, turn := targetTurn(t, seen, at)
	turn.Actions[0].TargetPixels = nil

	assert.True(t, matches(labelled(2), v, turn))
	assert.False(t, matches(stripes(800, 600, 5), v, turn))
}
