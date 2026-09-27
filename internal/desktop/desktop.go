// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package desktop captures the screen and sends pointer and keyboard input
// to the desktop of the current user session.
//
// A Driver builds clicks, drags, key combinations and typing from the few
// primitives a Backend implements for each operating system. Positions are
// pixels of the screenshots the driver captures, on the primary display.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"
)

// ErrUnsupported reports an operating system without a desktop backend.
var ErrUnsupported = errors.New("desktop automation is supported on macOS and Windows only")

// Button is a pointer button.
type Button string

// Pointer buttons.
const (
	ButtonLeft   Button = "left"
	ButtonRight  Button = "right"
	ButtonMiddle Button = "middle"
)

// ParseButton accepts a button name, defaulting to the left button.
func ParseButton(name string) (Button, error) {
	switch Button(strings.ToLower(name)) {
	case "", ButtonLeft:
		return ButtonLeft, nil
	case ButtonRight:
		return ButtonRight, nil
	case ButtonMiddle:
		return ButtonMiddle, nil
	default:
		return "", fmt.Errorf("unknown mouse button %q", name)
	}
}

// Backend is the operating system's side of a Driver.
type Backend interface {
	// Capture returns the primary display in physical pixels.
	Capture() (*image.RGBA, error)
	// MoveTo moves the pointer to a physical pixel position.
	MoveTo(x, y int) error
	// Position reports the pointer's physical pixel position.
	Position() (x, y int, err error)
	// Button presses or releases a button at the pointer. clicks counts
	// the clicks of a multi-click so far, starting at 1.
	Button(button Button, down bool, clicks int) error
	// Wheel scrolls by wheel notches; positive values scroll right and down.
	Wheel(dx, dy int) error
	// Key presses or releases a key.
	Key(key Key, down bool) error
	// Type types text at the keyboard focus.
	Type(text string) error
	// LastInput reports when the desktop last received pointer or keyboard
	// input from any source, the driver included, or the zero time when it
	// cannot tell.
	LastInput() time.Time
	// Close releases the backend.
	Close() error
}

// Diagnostics describes whether the current session can be automated.
type Diagnostics struct {
	OS     string
	Width  int
	Height int
	// Problems are the reasons automation cannot work, such as a missing
	// permission; empty when the desktop is usable.
	Problems []string
}

// Err summarizes the problems as one error, or returns nil.
func (d Diagnostics) Err() error {
	if len(d.Problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(d.Problems, "; "))
}

// Timing of composite input. Applications miss events that arrive
// faster than a person could produce them.
const (
	inputDelay     = 30 * time.Millisecond
	dragStepPixels = 20
	maxDragSteps   = 50
	// inputEcho is how long after the driver's own input the desktop may
	// still report that input as the latest one.
	inputEcho = time.Second
)

// Driver performs desktop actions on a Backend.
type Driver struct {
	backend Backend
	// lastInput is when the driver last sent input.
	lastInput time.Time
}

// New returns a driver for a backend.
func New(backend Backend) *Driver {
	d := &Driver{}
	d.backend = inputClock{Backend: backend, last: &d.lastInput}
	return d
}

// InputSentAt reports when the driver last sent input.
func (d *Driver) InputSentAt() time.Time {
	return d.lastInput
}

// AssumeInputSent records that input was sent at, such as by an earlier
// driver on the same desktop, so the desktop reporting it is not taken for a
// person's.
func (d *Driver) AssumeInputSent(at time.Time) {
	if at.After(d.lastInput) {
		d.lastInput = at
	}
}

// PersonInputSince reports whether input the driver did not send, such as a
// person's, reached the desktop after since.
func (d *Driver) PersonInputSince(since time.Time) bool {
	last := d.backend.LastInput()
	if !last.After(since) {
		return false
	}
	// The desktop may report the driver's own input a moment late, but
	// input the driver sent before since cannot arrive after it.
	return d.lastInput.Before(since) || last.After(d.lastInput.Add(inputEcho))
}

// WaitForIdle returns once nobody but the driver has used the desktop for
// idle, checking every poll. onWait runs once if the driver has to wait.
func (d *Driver) WaitForIdle(ctx context.Context, idle, poll time.Duration, onWait func()) error {
	for waited := false; d.PersonInputSince(time.Now().Add(-idle)); waited = true {
		if !waited {
			onWait()
		}
		if err := pause(ctx, poll); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the driver.
func (d *Driver) Close() error {
	return d.backend.Close()
}

// Screenshot captures the primary display.
func (d *Driver) Screenshot() (*image.RGBA, error) {
	return d.backend.Capture()
}

// CursorPosition reports the pointer position.
func (d *Driver) CursorPosition() (image.Point, error) {
	x, y, err := d.backend.Position()
	return image.Pt(x, y), err
}

// Move moves the pointer.
func (d *Driver) Move(ctx context.Context, at image.Point) error {
	if err := d.backend.MoveTo(at.X, at.Y); err != nil {
		return err
	}
	return pause(ctx, inputDelay)
}

// MoveHolding moves the pointer while holding modifiers, as when hovering
// with a key pressed.
func (d *Driver) MoveHolding(ctx context.Context, at image.Point, modifiers []Key) error {
	return d.withKeys(ctx, modifiers, func() error {
		return d.Move(ctx, at)
	})
}

// Click clicks a button count times at a position, or at the pointer when
// at is nil, while holding modifiers.
func (d *Driver) Click(ctx context.Context, at *image.Point, button Button, count int, modifiers []Key) error {
	if at != nil {
		if err := d.Move(ctx, *at); err != nil {
			return err
		}
	}
	return d.withKeys(ctx, modifiers, func() error {
		for i := 1; i <= max(count, 1); i++ {
			if err := d.backend.Button(button, true, i); err != nil {
				return err
			}
			if err := d.backend.Button(button, false, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// PressButton presses a button at the pointer.
func (d *Driver) PressButton(button Button) error {
	return d.backend.Button(button, true, 1)
}

// ReleaseButton releases a button at the pointer.
func (d *Driver) ReleaseButton(button Button) error {
	return d.backend.Button(button, false, 1)
}

// Drag holds the left button from the first point of a path to the last,
// moving in small steps so applications see the drag.
func (d *Driver) Drag(ctx context.Context, path []image.Point, modifiers []Key) error {
	if len(path) < 2 {
		return errors.New("a drag needs at least two points")
	}
	if err := d.Move(ctx, path[0]); err != nil {
		return err
	}
	return d.withKeys(ctx, modifiers, func() error {
		if err := d.backend.Button(ButtonLeft, true, 1); err != nil {
			return err
		}
		// The button is released even when moving fails.
		err := d.glide(ctx, path)
		return errors.Join(err, d.backend.Button(ButtonLeft, false, 1))
	})
}

func (d *Driver) glide(ctx context.Context, path []image.Point) error {
	for i := 1; i < len(path); i++ {
		from, to := path[i-1], path[i]
		steps := min(max(abs(to.X-from.X), abs(to.Y-from.Y))/dragStepPixels, maxDragSteps)
		steps = max(steps, 1)
		for step := 1; step <= steps; step++ {
			x := from.X + (to.X-from.X)*step/steps
			y := from.Y + (to.Y-from.Y)*step/steps
			if err := d.Move(ctx, image.Pt(x, y)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Scroll scrolls wheel notches at a position, or at the pointer when at is
// nil, while holding modifiers.
func (d *Driver) Scroll(ctx context.Context, at *image.Point, dx, dy int, modifiers []Key) error {
	if at != nil {
		if err := d.Move(ctx, *at); err != nil {
			return err
		}
	}
	return d.withKeys(ctx, modifiers, func() error {
		return d.backend.Wheel(dx, dy)
	})
}

// PressKeys presses keys together, then releases them, repeat times.
func (d *Driver) PressKeys(ctx context.Context, keys []Key, repeat int) error {
	if len(keys) == 0 {
		return errors.New("no keys to press")
	}
	for range max(repeat, 1) {
		if err := d.withKeys(ctx, keys[:len(keys)-1], func() error {
			last := keys[len(keys)-1]
			if err := d.backend.Key(last, true); err != nil {
				return err
			}
			return d.backend.Key(last, false)
		}); err != nil {
			return err
		}
		if err := pause(ctx, inputDelay); err != nil {
			return err
		}
	}
	return nil
}

// HoldKeys holds keys down for a duration.
func (d *Driver) HoldKeys(ctx context.Context, keys []Key, duration time.Duration) error {
	return d.withKeys(ctx, keys, func() error {
		return pause(ctx, duration)
	})
}

// Type types text. Line breaks are sent as the Enter key, which
// applications treat more consistently than a typed newline.
func (d *Driver) Type(ctx context.Context, text string) error {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			if err := d.PressKeys(ctx, []Key{KeyEnter}, 1); err != nil {
				return err
			}
		}
		if line == "" {
			continue
		}
		if err := d.backend.Type(line); err != nil {
			return err
		}
	}
	return pause(ctx, inputDelay)
}

// withKeys holds keys down around fn and releases them in reverse order,
// also when fn fails.
func (d *Driver) withKeys(ctx context.Context, keys []Key, fn func() error) (err error) {
	var held []Key
	defer func() {
		for _, key := range slices.Backward(held) {
			err = errors.Join(err, d.backend.Key(key, false))
		}
	}()
	for _, key := range keys {
		if err := d.backend.Key(key, true); err != nil {
			return err
		}
		held = append(held, key)
	}
	if len(held) > 0 {
		if err := pause(ctx, inputDelay); err != nil {
			return err
		}
	}
	return fn()
}

// inputClock notes when input was last sent through a backend.
type inputClock struct {
	Backend
	last *time.Time
}

func (c inputClock) MoveTo(x, y int) error {
	defer c.sent()
	return c.Backend.MoveTo(x, y)
}

func (c inputClock) Button(button Button, down bool, clicks int) error {
	defer c.sent()
	return c.Backend.Button(button, down, clicks)
}

func (c inputClock) Wheel(dx, dy int) error {
	defer c.sent()
	return c.Backend.Wheel(dx, dy)
}

func (c inputClock) Key(key Key, down bool) error {
	defer c.sent()
	return c.Backend.Key(key, down)
}

func (c inputClock) Type(text string) error {
	defer c.sent()
	return c.Backend.Type(text)
}

func (c inputClock) sent() {
	*c.last = time.Now()
}

func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
