// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWindows is a list of windows a test changes, as a window opening or
// closing would.
type fakeWindows struct {
	mu   sync.Mutex
	list []desktop.WindowID
	// changing has every read find another window, as windows that keep
	// changing do however the reads are timed.
	changing bool
}

func (w *fakeWindows) get() []desktop.WindowID {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.changing {
		w.list = []desktop.WindowID{{Handle: w.list[0].Handle + 1}}
	}
	return slices.Clone(w.list)
}

func (w *fakeWindows) keepChanging(changing bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.changing = changing
}

func (w *fakeWindows) set(list ...desktop.WindowID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = list
}

// steadyOver wraps elements with a reader whose step sent its last input at
// sent, within an operation that never ends.
func steadyOver(els desktop.Elements, windows *fakeWindows, sent time.Time) *steadyElements {
	return newSteadyElements(els, windows.get, func() time.Time { return sent }, context.Background)
}

// A read right after input waits until the screen has been left alone for a
// moment, so a window the input closes is under way before it is read.
func TestSteadyReadWaitsAfterInput(t *testing.T) {
	t.Parallel()

	sent := time.Now()
	els := steadyOver(newFakeElements(), &fakeWindows{}, sent)
	_, err := els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(sent), quietAfterInput)
}

// A pattern or a focus counts as input: the read after it waits too.
func TestSteadyReadWaitsAfterAPatternOrAFocus(t *testing.T) {
	t.Parallel()

	els := steadyOver(newFakeElements(), &fakeWindows{}, time.Time{})
	save, err := els.Find(desktop.Selector{Role: desktop.RoleButton, ID: "saveButton"})
	require.NoError(t, err)
	require.Len(t, save, 1)

	require.NoError(t, els.Apply(save[0], desktop.PatternInvoke, ""))
	applied := time.Now()
	_, err = els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(applied), quietAfterInput-windowSettlePoll, "after a pattern")

	require.NoError(t, els.Focus(save[0]))
	focused := time.Now()
	_, err = els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(focused), quietAfterInput-windowSettlePoll, "after a focus")
}

// A read after a window opened or closed waits until the windows have
// stayed as they are for a moment; with nothing changed, it reads at once.
func TestSteadyReadWaitsForTheWindowsToSettle(t *testing.T) {
	t.Parallel()

	windows := &fakeWindows{list: []desktop.WindowID{{Handle: 1}, {Handle: 2}}}
	els := steadyOver(newFakeElements(), windows, time.Time{})
	_, err := els.FrontWindow()
	require.NoError(t, err)

	began := time.Now()
	_, err = els.FrontWindow()
	require.NoError(t, err)
	assert.Less(t, time.Since(began), windowSettle, "the windows did not change")

	windows.set(desktop.WindowID{Handle: 1})
	began = time.Now()
	_, err = els.Find(desktop.Selector{Role: desktop.RoleButton, ID: "saveButton"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(began), windowSettle, "a window closed")
}

// Windows still changing when a wait reaches its limit leave the next read
// waiting again, even when they look the same as at the limit.
func TestSteadyReadWaitsAgainAfterUnsettledWindows(t *testing.T) {
	t.Parallel()

	windows := &fakeWindows{list: []desktop.WindowID{{Handle: 1}}}
	els := steadyOver(newFakeElements(), windows, time.Time{})
	els.settleFor, els.settleLimit = 100*time.Millisecond, 300*time.Millisecond
	_, err := els.FrontWindow()
	require.NoError(t, err)

	windows.set(desktop.WindowID{Handle: 2})
	windows.keepChanging(true)
	_, err = els.FrontWindow()
	require.NoError(t, err)
	windows.keepChanging(false)
	require.True(t, els.unsettled, "the windows were still changing at the limit")

	began := time.Now()
	_, err = els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(began), els.settleFor, "the next read waits again")
	assert.False(t, els.unsettled, "the windows settled")
}

// A read whose operation ends while it waits gives up with the operation's
// error, reading nothing.
func TestSteadyReadEndsWithItsOperation(t *testing.T) {
	t.Parallel()

	fake := newFakeElements()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	sent := time.Now()
	els := newSteadyElements(fake, (&fakeWindows{}).get, func() time.Time { return sent }, func() context.Context { return ctx })
	_, err := els.FrontWindow()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(sent), quietAfterInput, "the wait ended with the operation")
	assert.Zero(t, fake.reads, "nothing was read")
}
