// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
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
}

func (w *fakeWindows) get() []desktop.WindowID {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.list)
}

func (w *fakeWindows) set(list ...desktop.WindowID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = list
}

func never() time.Time { return time.Time{} }

// A read right after input waits until the screen has been left alone for a
// moment, so a window the input closes is under way before it is read.
func TestSteadyReadWaitsAfterInput(t *testing.T) {
	t.Parallel()

	sent := time.Now()
	els := newSteadyElements(newFakeElements(), (&fakeWindows{}).get, func() time.Time { return sent })
	_, err := els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(sent), quietAfterInput)
}

// A pattern counts as input: the read after it waits too.
func TestSteadyReadWaitsAfterAPattern(t *testing.T) {
	t.Parallel()

	fake := newFakeElements()
	els := newSteadyElements(fake, (&fakeWindows{}).get, never)
	save, err := els.Find(desktop.Selector{Role: desktop.RoleButton, ID: "saveButton"})
	require.NoError(t, err)
	require.Len(t, save, 1)
	require.NoError(t, els.Apply(save[0], desktop.PatternInvoke, ""))
	applied := time.Now()
	_, err = els.FrontWindow()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(applied), quietAfterInput-windowSettlePoll)
}

// A read after a window opened or closed waits until the windows have
// stayed as they are for a moment; with nothing changed, it reads at once.
func TestSteadyReadWaitsForTheWindowsToSettle(t *testing.T) {
	t.Parallel()

	windows := &fakeWindows{list: []desktop.WindowID{{Handle: 1}, {Handle: 2}}}
	els := newSteadyElements(newFakeElements(), windows.get, never)
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
