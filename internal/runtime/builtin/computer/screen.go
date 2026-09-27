// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

const pngMediaType = "image/png"

// errCapture marks a failure to read the screen, which says nothing about
// what the screen shows.
var errCapture = errors.New("capture the screen")

// settleTiming controls how long the step waits for the screen to stop
// changing after input.
type settleTiming struct {
	// delay lets applications start reacting before the first capture.
	delay time.Duration
	// poll spaces the captures compared for stability.
	poll time.Duration
	// timeout caps the wait for a screen that keeps changing, such as one
	// with an animation.
	timeout time.Duration
}

var defaultSettle = settleTiming{delay: 250 * time.Millisecond, poll: 150 * time.Millisecond, timeout: 3 * time.Second}

// stableDistance is the largest fingerprint change between two captures
// that still counts as a still screen.
const stableDistance = 1

// screen is a capture of the display and the scaled copy a model sees.
type screen struct {
	full   *image.RGBA
	scaled *image.RGBA
	png    []byte
	// capturedAt is when the display was captured.
	capturedAt time.Time
}

// newScreen scales a capture to fit a model's image limit.
func newScreen(full *image.RGBA, limit computeruse.ImageLimit) (screen, error) {
	bounds := full.Bounds()
	width, height := desktop.FitSize(bounds.Dx(), bounds.Dy(), limit.LongEdge, limit.MaxPixels)
	scaled := full
	if width != bounds.Dx() || height != bounds.Dy() {
		scaled = desktop.Resize(full, width, height)
	}
	data, err := desktop.EncodePNG(scaled)
	if err != nil {
		return screen{}, err
	}
	return screen{full: full, scaled: scaled, png: data}, nil
}

func (s screen) image() llmpkg.Image {
	return llmpkg.Image{MediaType: pngMediaType, Data: s.png}
}

func (s screen) forModel() computeruse.Screen {
	return computeruse.Screen{Image: s.image(), Width: s.scaled.Bounds().Dx(), Height: s.scaled.Bounds().Dy()}
}

// toFull maps a position the model gave on the scaled screenshot to display
// pixels.
func (s screen) toFull(p computeruse.Point) image.Point {
	return image.Pt(
		p.X*s.full.Bounds().Dx()/s.scaled.Bounds().Dx(),
		p.Y*s.full.Bounds().Dy()/s.scaled.Bounds().Dy(),
	)
}

// toModel maps display pixels to the scaled screenshot.
func (s screen) toModel(p image.Point) computeruse.Point {
	return computeruse.Point{
		X: p.X * s.scaled.Bounds().Dx() / s.full.Bounds().Dx(),
		Y: p.Y * s.scaled.Bounds().Dy() / s.full.Bounds().Dy(),
	}
}

// settle waits for the screen to stop changing and returns the last
// capture.
func (r *run) settle(ctx context.Context) (*image.RGBA, error) {
	timing := r.exec.settle
	if err := sleep(ctx, timing.delay); err != nil {
		return nil, err
	}
	previous, err := r.driver.Screenshot()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCapture, err)
	}
	deadline := time.Now().Add(timing.timeout)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, timing.poll); err != nil {
			return nil, err
		}
		current, err := r.driver.Screenshot()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errCapture, err)
		}
		if desktop.FingerprintOf(current).Distance(desktop.FingerprintOf(previous)) <= stableDistance {
			return current, nil
		}
		previous = current
	}
	return previous, nil
}

// observe waits for a still screen and scales it for a model.
func (r *run) observe(ctx context.Context, limit computeruse.ImageLimit) (screen, error) {
	full, err := r.settle(ctx)
	if err != nil {
		return screen{}, err
	}
	capturedAt := time.Now()
	shot, err := newScreen(full, limit)
	shot.capturedAt = capturedAt
	return shot, err
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
