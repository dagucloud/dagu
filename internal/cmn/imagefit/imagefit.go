// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package imagefit scales images down to the size a model takes.
package imagefit

import (
	"image"
	"math"
)

// FitSize returns the largest size with the same aspect ratio that fits a
// long edge and a pixel count; a zero limit is ignored. The size never
// grows.
func FitSize(width, height, longEdge, maxPixels int) (int, int) {
	scale := 1.0
	if longEdge > 0 {
		scale = min(scale, float64(longEdge)/float64(max(width, height)))
	}
	if maxPixels > 0 {
		scale = min(scale, math.Sqrt(float64(maxPixels)/float64(width*height)))
	}
	if scale >= 1 {
		return width, height
	}
	return max(int(float64(width)*scale), 1), max(int(float64(height)*scale), 1)
}

// Resize scales an image down to a size by averaging the source pixels each
// target pixel covers.
func Resize(src *image.RGBA, width, height int) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw == 0 || sh == 0 {
		return dst
	}
	for y := range height {
		y0, y1 := y*sh/height, max((y+1)*sh/height, y*sh/height+1)
		for x := range width {
			x0, x1 := x*sw/width, max((x+1)*sw/width, x*sw/width+1)
			var r, g, b, a, n int
			for sy := y0; sy < y1; sy++ {
				row := src.PixOffset(bounds.Min.X+x0, bounds.Min.Y+sy)
				for i := row; i < row+(x1-x0)*4; i += 4 {
					r += int(src.Pix[i])
					g += int(src.Pix[i+1])
					b += int(src.Pix[i+2])
					a += int(src.Pix[i+3])
					n++
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = average(r, n)
			dst.Pix[i+1] = average(g, n)
			dst.Pix[i+2] = average(b, n)
			dst.Pix[i+3] = average(a, n)
		}
	}
	return dst
}

// average returns the mean of n byte values that add up to sum.
func average(sum, n int) uint8 {
	return uint8(sum / n) //nolint:gosec // the mean of bytes fits a byte
}
