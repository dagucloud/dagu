// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingBackend records input events as readable strings.
type recordingBackend struct {
	events  []string
	failKey desktop.Key
}

func (b *recordingBackend) Capture() (*image.RGBA, error) {
	return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
}

func (b *recordingBackend) MoveTo(x, y int) error {
	b.events = append(b.events, fmt.Sprintf("move %d,%d", x, y))
	return nil
}

func (b *recordingBackend) Position() (int, int, error) { return 7, 8, nil }

func (b *recordingBackend) Button(button desktop.Button, down bool, clicks int) error {
	b.events = append(b.events, fmt.Sprintf("%s %s #%d", button, upDown(down), clicks))
	return nil
}

func (b *recordingBackend) Wheel(dx, dy int) error {
	b.events = append(b.events, fmt.Sprintf("wheel %d,%d", dx, dy))
	return nil
}

func (b *recordingBackend) Key(key desktop.Key, down bool) error {
	if key == b.failKey && down {
		return errors.New("key failed")
	}
	b.events = append(b.events, fmt.Sprintf("key %s %s", key, upDown(down)))
	return nil
}

func (b *recordingBackend) Type(text string) error {
	b.events = append(b.events, "type "+text)
	return nil
}

func (b *recordingBackend) Close() error { return nil }

func upDown(down bool) string {
	if down {
		return "down"
	}
	return "up"
}

func TestDriverClickHoldsModifiers(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	at := image.Pt(10, 20)
	require.NoError(t, driver.Click(context.Background(), &at, desktop.ButtonLeft, 2, []desktop.Key{desktop.KeyShift}))

	assert.Equal(t, []string{
		"move 10,20",
		"key shift down",
		"left down #1", "left up #1",
		"left down #2", "left up #2",
		"key shift up",
	}, backend.events)
}

func TestDriverDragMovesInSteps(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	require.NoError(t, driver.Drag(context.Background(), []image.Point{{X: 0, Y: 0}, {X: 40, Y: 0}}, nil))

	assert.Equal(t, []string{"move 0,0", "left down #1", "move 20,0", "move 40,0", "left up #1"}, backend.events)
}

func TestDriverPressKeysReleasesInReverse(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	require.NoError(t, driver.PressKeys(context.Background(), []desktop.Key{desktop.KeyCtrl, desktop.KeyShift, "t"}, 2))

	press := []string{"key ctrl down", "key shift down", "key t down", "key t up", "key shift up", "key ctrl up"}
	assert.Equal(t, append(press, press...), backend.events)
}

// A failing key must not leave earlier keys held down.
func TestDriverReleasesHeldKeysOnFailure(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{failKey: desktop.KeyShift}
	driver := desktop.New(backend)
	err := driver.PressKeys(context.Background(), []desktop.Key{desktop.KeyCtrl, desktop.KeyShift, "t"}, 1)

	require.Error(t, err)
	assert.Equal(t, []string{"key ctrl down", "key ctrl up"}, backend.events)
}

func TestDriverTypeSendsLineBreaksAsEnter(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	require.NoError(t, driver.Type(context.Background(), "a\r\n\nb"))

	assert.Equal(t, []string{"type a", "key enter down", "key enter up", "key enter down", "key enter up", "type b"}, backend.events)
}

func TestDriverScrollAtPoint(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	at := image.Pt(5, 6)
	require.NoError(t, driver.Scroll(context.Background(), &at, 0, -3, nil))

	assert.Equal(t, []string{"move 5,6", "wheel 0,-3"}, backend.events)
}

func TestParseButton(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]desktop.Button{"": desktop.ButtonLeft, "Right": desktop.ButtonRight, "middle": desktop.ButtonMiddle} {
		got, err := desktop.ParseButton(name)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := desktop.ParseButton("back")
	require.Error(t, err)
}

func TestDriverMoveHoldingModifiers(t *testing.T) {
	t.Parallel()

	backend := &recordingBackend{}
	driver := desktop.New(backend)
	require.NoError(t, driver.MoveHolding(context.Background(), image.Pt(3, 4), []desktop.Key{desktop.KeyAlt}))

	assert.Equal(t, []string{"key alt down", "move 3,4", "key alt up"}, backend.events)
}
