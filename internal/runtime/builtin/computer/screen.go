// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/imagefit"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
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

// Kinds of capture: the whole screen, or the windows of the applications a
// step works in. A recording notes which kind each screen it fingerprints
// was, so a replay compares a capture with one of its kind.
const (
	captureScreen  = "screen"
	captureWindows = "windows"
)

// captured is what a capture shows: the windows of apps, the front first,
// or the whole screen when apps is empty.
type captured struct {
	apps    []string
	windows []desktop.WindowID
}

func (c captured) kind() string {
	if len(c.apps) == 0 {
		return captureScreen
	}
	return captureWindows
}

// windowAt names the front window the capture shows at a display position.
func (c captured) windowAt(at image.Point) (desktop.WindowID, bool) {
	for _, w := range c.windows {
		if at.In(w.Bounds) {
			return w, true
		}
	}
	return desktop.WindowID{}, false
}

// shows reports an application whose windows the capture shows.
func (c captured) shows(app string) bool {
	return app != "" && slices.ContainsFunc(c.apps, func(a string) bool { return strings.EqualFold(a, app) })
}

// describeCapture names a kind of capture for the step log.
func describeCapture(kind string) string {
	if kind == captureWindows {
		return "the windows of the apps it works in"
	}
	return "the whole screen"
}

// screen is a capture and the scaled copy a model sees. full's bounds are
// the display pixels it shows.
type screen struct {
	full   *image.RGBA
	scaled *image.RGBA
	png    []byte
	// shown is what full shows.
	shown captured
	// capturedAt is when the display was captured.
	capturedAt time.Time
	// focus is the window that had the keyboard focus then.
	focus desktop.WindowID
}

// newScreen scales a capture to fit a model's image limit.
func newScreen(full *image.RGBA, limit computeruse.ImageLimit) (screen, error) {
	bounds := full.Bounds()
	width, height := imagefit.FitSize(bounds.Dx(), bounds.Dy(), limit.LongEdge, limit.MaxPixels)
	scaled := full
	if width != bounds.Dx() || height != bounds.Dy() {
		scaled = imagefit.Resize(full, width, height)
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
	full := s.full.Bounds()
	return image.Pt(
		full.Min.X+p.X*full.Dx()/s.scaled.Bounds().Dx(),
		full.Min.Y+p.Y*full.Dy()/s.scaled.Bounds().Dy(),
	)
}

// toModel maps display pixels to the scaled screenshot.
func (s screen) toModel(p image.Point) computeruse.Point {
	full := s.full.Bounds()
	return computeruse.Point{
		X: (p.X - full.Min.X) * s.scaled.Bounds().Dx() / full.Dx(),
		Y: (p.Y - full.Min.Y) * s.scaled.Bounds().Dy() / full.Dy(),
	}
}

// seenAt names the window the model saw at a display position on a
// screen: for a capture of windows, the front one it showed there, which
// another application's window may cover now, else the one there.
func (r *run) seenAt(s screen, at image.Point) desktop.WindowID {
	if s.shown.kind() == captureWindows {
		w, _ := s.shown.windowAt(at)
		return w
	}
	return r.driver.WindowAt(at)
}

// grab captures what a look shows: the windows of the applications the
// step works in (see lookApps), or the whole screen until it works in one
// or when their windows cannot be captured. The timeline says what the
// step captures whenever that changes, and r.shown keeps what this capture
// shows.
func (r *run) grab() (*image.RGBA, error) {
	apps := r.lookApps()
	note := "Capturing the whole screen until the step works in an app"
	if len(apps) > 0 {
		shot, err := r.driver.CaptureApps(apps)
		if err == nil {
			r.shown = captured{apps: apps, windows: shot.Windows}
			r.noteCapture("Capturing only the windows of " + strings.Join(apps, ", "))
			return shot.Image, nil
		}
		note = fmt.Sprintf("Capturing the whole screen, since the windows of %s cannot be captured: %s", strings.Join(apps, ", "), err)
		if errors.Is(err, desktop.ErrNoWindows) {
			note = fmt.Sprintf("Capturing the whole screen, since no window of %s shows on the main display", strings.Join(apps, ", "))
		}
	}
	full, err := r.driver.Screenshot()
	if err != nil {
		return nil, err
	}
	r.shown = captured{}
	r.noteCapture(note)
	return full, nil
}

// noteCapture records in the timeline what the step captures, when that
// changed since it last said.
func (r *run) noteCapture(note string) {
	if note != r.captureNote {
		r.captureNote = note
		r.timeline.Lifecycle(agentstep.StatusRunning, note)
	}
}

// A pointer action waits up to reachWait, looking every reachPoll, for the
// window it brought forward to be the one under it.
const (
	reachWait = time.Second
	reachPoll = 50 * time.Millisecond
)

// reach makes sure a pointer action at a display position lands on the
// window the model saw there. A capture of windows shows them as if
// nothing else were on the screen, so another application's window may lie
// over the spot: the window the capture shows there is brought forward
// first. A window of an application the capture shows, such as a menu an
// earlier action of the turn opened, is what the action is aimed at, and a
// spot outside every window the capture shows is refused, since the model
// saw nothing there. A capture of the whole screen showed what is there.
func (r *run) reach(ctx context.Context, index int, at image.Point) error {
	shown := r.shown
	if shown.kind() == captureScreen {
		return nil
	}
	seen, inside := shown.windowAt(at)
	deadline := time.Now().Add(reachWait)
	raised := false
	for {
		on := r.driver.WindowAt(at)
		if !on.Known() || (inside && on.Handle == seen.Handle) || shown.shows(on.App) {
			return nil
		}
		if !inside {
			return errors.New("that spot is outside the windows the screenshot shows; act only on what it shows")
		}
		if !raised {
			raised = true
			logAction(r.timeline, index, fmt.Sprintf("bringing %s forward from under %s", describeWindow(seen), on.App))
			if err := r.driver.Raise(seen); err != nil {
				return err
			}
		} else if !time.Now().Before(deadline) {
			return fmt.Errorf("a window of %s lies over that spot and stayed in front of %s", on.App, describeWindow(seen))
		}
		if err := sleep(ctx, reachPoll); err != nil {
			return err
		}
	}
}

// reachAction runs reach for where a pointer action in display pixels
// lands, and reports a failure as the action's result.
func (r *run) reachAction(ctx context.Context, index int, action computeruse.Action) computeruse.Result {
	result := computeruse.Result{CallID: action.CallID}
	if at, pointer := target(action); pointer {
		if err := r.reach(ctx, index, at); err != nil {
			result.Error = r.masker.MaskString(err.Error())
		}
	}
	return result
}

// settle waits for the screen to stop changing and returns the last
// capture, which r.shown describes.
func (r *run) settle(ctx context.Context) (*image.RGBA, error) {
	timing := r.exec.settle
	if err := sleep(ctx, timing.delay); err != nil {
		return nil, err
	}
	previous, err := r.grab()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCapture, err)
	}
	deadline := time.Now().Add(timing.timeout)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, timing.poll); err != nil {
			return nil, err
		}
		current, err := r.grab()
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
	capturedAt, focus := time.Now(), r.driver.FocusedWindow()
	shot, err := newScreen(full, limit)
	shot.shown, shot.capturedAt, shot.focus = r.shown, capturedAt, focus
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
