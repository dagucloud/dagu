// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"math/bits"
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

// Crop returns the part of an image inside a rectangle, clipped to the
// image.
func Crop(src *image.RGBA, rect image.Rectangle) *image.RGBA {
	rect = rect.Canon().Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := range rect.Dy() {
		from := src.PixOffset(rect.Min.X, rect.Min.Y+y)
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+rect.Dx()*4], src.Pix[from:from+rect.Dx()*4])
	}
	return dst
}

// EncodePNG encodes an image as PNG, favoring speed over size.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Fingerprint is a perceptual hash of an image: similar images have
// fingerprints a small Distance apart.
type Fingerprint uint64

// fingerprint grid: each row compares 9 samples, giving 8 bits.
const (
	hashColumns = 9
	hashRows    = 8
)

// FingerprintOf hashes an image by whether brightness rises between
// neighboring cells of a coarse grid.
func FingerprintOf(img *image.RGBA) Fingerprint {
	small := Resize(img, hashColumns, hashRows)
	var hash uint64
	for y := range hashRows {
		for x := range hashColumns - 1 {
			hash <<= 1
			if luma(small, x, y) < luma(small, x+1, y) {
				hash |= 1
			}
		}
	}
	return Fingerprint(hash)
}

// FingerprintAround hashes the square of an image centered on a point, which
// tells apart screens that differ only near where an action lands.
func FingerprintAround(img *image.RGBA, at image.Point, radius int) Fingerprint {
	return FingerprintOf(Crop(img, image.Rect(at.X-radius, at.Y-radius, at.X+radius, at.Y+radius)))
}

// Distance counts the bits two fingerprints differ in, from 0 to 64.
func (f Fingerprint) Distance(other Fingerprint) int {
	return bits.OnesCount64(uint64(f ^ other))
}

func luma(img *image.RGBA, x, y int) int {
	i := img.PixOffset(x, y)
	return 299*int(img.Pix[i]) + 587*int(img.Pix[i+1]) + 114*int(img.Pix[i+2])
}
