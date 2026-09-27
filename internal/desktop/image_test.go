// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFitSize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		width, height, longEdge, maxPixels int
		wantW, wantH                       int
	}{
		{1280, 800, 1568, 1_150_000, 1280, 800},
		{2880, 1800, 1568, 1_150_000, 1356, 847},
		{2880, 1800, 2576, 0, 2576, 1610},
		{100, 50, 0, 0, 100, 50},
	} {
		w, h := desktop.FitSize(tc.width, tc.height, tc.longEdge, tc.maxPixels)
		assert.Equal(t, [2]int{tc.wantW, tc.wantH}, [2]int{w, h}, "%dx%d", tc.width, tc.height)
	}
}

func TestResizeAverages(t *testing.T) {
	t.Parallel()

	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{R: 200, A: 255})
	src.Set(1, 0, color.RGBA{R: 100, A: 255})

	dst := desktop.Resize(src, 1, 1)
	assert.Equal(t, color.RGBA{R: 150, A: 255}, dst.RGBAAt(0, 0))
}

func TestCropClipsToImage(t *testing.T) {
	t.Parallel()

	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	src.Set(9, 9, color.RGBA{G: 255, A: 255})

	dst := desktop.Crop(src, image.Rect(8, 8, 20, 20))
	assert.Equal(t, image.Rect(0, 0, 2, 2), dst.Bounds())
	assert.Equal(t, color.RGBA{G: 255, A: 255}, dst.RGBAAt(1, 1))
}

func TestEncodePNG(t *testing.T) {
	t.Parallel()

	data, err := desktop.EncodePNG(image.NewRGBA(image.Rect(0, 0, 3, 2)))
	require.NoError(t, err)
	decoded, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 3, 2), decoded.Bounds())
}

// gradient draws a horizontal brightness ramp, optionally with a dark box.
func gradient(box bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 90, 80))
	for y := range 80 {
		for x := range 90 {
			v := uint8(x * 255 / 89)
			if box && x >= 50 && x < 80 && y >= 20 && y < 60 {
				v = 0
			}
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	plain, boxed := gradient(false), gradient(true)
	assert.Equal(t, 0, desktop.FingerprintOf(plain).Distance(desktop.FingerprintOf(gradient(false))))
	assert.Greater(t, desktop.FingerprintOf(plain).Distance(desktop.FingerprintOf(boxed)), 4)

	// Near the box the screens differ; far from it they look the same.
	near, far := image.Pt(65, 40), image.Pt(15, 40)
	assert.Positive(t, desktop.FingerprintAround(plain, near, 16).Distance(desktop.FingerprintAround(boxed, near, 16)))
	assert.Zero(t, desktop.FingerprintAround(plain, far, 16).Distance(desktop.FingerprintAround(boxed, far, 16)))
}
