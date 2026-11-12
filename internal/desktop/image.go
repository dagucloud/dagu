// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	"image"
	"image/png"
	"math/bits"

	"github.com/dagucloud/dagu/v2/internal/cmn/imagefit"
)

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

// ChangedPixels counts the pixels whose color differs between two images of
// the same size, matched by their offset from the top-left corner. It tells
// apart pictures too alike for a Fingerprint, such as a list before and
// after a short scroll.
func ChangedPixels(a, b *image.RGBA) int {
	size := a.Bounds().Size()
	changed := 0
	for y := range size.Y {
		rowA := a.Pix[a.PixOffset(a.Bounds().Min.X, a.Bounds().Min.Y+y):][:size.X*4]
		rowB := b.Pix[b.PixOffset(b.Bounds().Min.X, b.Bounds().Min.Y+y):][:size.X*4]
		if bytes.Equal(rowA, rowB) {
			continue
		}
		for x := 0; x < len(rowA); x += 4 {
			if !bytes.Equal(rowA[x:x+4], rowB[x:x+4]) {
				changed++
			}
		}
	}
	return changed
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
	small := imagefit.Resize(img, hashColumns, hashRows)
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
