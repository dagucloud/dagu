// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || (windows && (amd64 || arm64))

package desktop

import (
	"image"
	"image/color"
)

// countMagenta counts the pixels of r that show magenta, the color the
// end-to-end tests draw marks in, which neither a test window nor a
// desktop shows. A display's color profile shifts it a little on macOS.
func countMagenta(img *image.RGBA, r image.Rectangle) int {
	n := 0
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c := img.RGBAAt(x, y); c.R > 200 && c.G < 100 && c.B > 200 {
				n++
			}
		}
	}
	return n
}

// testMarkColor is the color the end-to-end tests draw marks in.
var testMarkColor = color.RGBA{R: 0xFF, B: 0xFF, A: 0xFF}
