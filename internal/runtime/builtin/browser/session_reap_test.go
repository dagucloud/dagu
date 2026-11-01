// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reap runs a session's watchdog until it returns, and returns its error.
func (ts *testSessions) reap(id string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- ts.sessions.Reap(ts.context(), id) }()
	return done
}

func waitReap(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the watchdog did not return")
	}
}

// steppedWait runs a watchdog one wait at a time: each wait reports how
// long it would last and returns once the test releases it, so the test
// moves the clock while the watchdog is not looking.
type steppedWait struct {
	asked   chan time.Duration
	release chan struct{}
}

func newSteppedWait() *steppedWait {
	return &steppedWait{asked: make(chan time.Duration), release: make(chan struct{})}
}

func (w *steppedWait) wait(ctx context.Context, d time.Duration) error {
	select {
	case w.asked <- d:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-w.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next waits until the watchdog waits, and returns for how long.
func (w *steppedWait) next(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-w.asked:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("the watchdog did not wait")
		return 0
	}
}

// The watchdog closes a session's browser once the session is idle past
// its timeout, keeping its history, and waits out a deadline a command
// renewed.
func TestSessionWatchdogClosesAnIdleSession(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	waits := newSteppedWait()
	ts.sessions.wait = waits.wait
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", IdleTimeout: 10 * time.Minute})
	first := ts.record(opened.ID).Deadline
	done := ts.reap(opened.ID)

	waits.next(t)
	ts.clock = ts.clock.Add(5 * time.Minute)
	_, err := ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	require.NoError(t, err)
	renewed := ts.record(opened.ID).Deadline
	require.True(t, renewed.After(first), "the command renews the deadline")

	ts.clock = first.Add(time.Second)
	waits.release <- struct{}{}
	waits.next(t)
	assert.Equal(t, browserhost.StateInteractive, ts.record(opened.ID).State, "the command renewed the session past the first deadline")

	ts.clock = renewed.Add(time.Second)
	waits.release <- struct{}{}
	waitReap(t, done)
	record := ts.record(opened.ID)
	assert.Equal(t, browserhost.StateEnded, record.State)
	assert.Len(t, ts.state(opened.ID).Ops, 1, "the history is kept")
}

// The watchdog of a session closed by hand has nothing left to do.
func TestSessionWatchdogEndsWithItsSession(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", IdleTimeout: 10 * time.Minute})
	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID}))
	waitReap(t, ts.reap(opened.ID))
}
