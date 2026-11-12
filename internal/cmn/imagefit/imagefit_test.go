// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package imagefit_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/imagefit"
	"github.com/stretchr/testify/assert"
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
		w, h := imagefit.FitSize(tc.width, tc.height, tc.longEdge, tc.maxPixels)
		assert.Equal(t, [2]int{tc.wantW, tc.wantH}, [2]int{w, h}, "%dx%d", tc.width, tc.height)
	}
}

func TestResizeAverages(t *testing.T) {
	t.Parallel()

	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{R: 200, A: 255})
	src.Set(1, 0, color.RGBA{R: 100, A: 255})

	dst := imagefit.Resize(src, 1, 1)
	assert.Equal(t, color.RGBA{R: 150, A: 255}, dst.RGBAAt(0, 0))
}
