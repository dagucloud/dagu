// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const defaultPollInterval = 50 * time.Millisecond

// Waiter centralizes integration-test timeout scaling and polling.
type Waiter struct {
	t *testing.T
}

// Timeout scales timeout for Windows and race builds.
func (w Waiter) Timeout(timeout time.Duration) time.Duration {
	return ScaleTimeout(timeout)
}

// ScaleTimeout applies the integration-test timeout multiplier for this platform.
func ScaleTimeout(timeout time.Duration) time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return timeout * 4
	case runtime.GOOS == "windows" || raceEnabled():
		return timeout * 2
	default:
		return timeout
	}
}

// Eventually waits until condition is satisfied.
func (w Waiter) Eventually(label string, timeout time.Duration, condition func() bool) {
	w.EventuallyEvery(label, timeout, defaultPollInterval, condition)
}

// EventuallyEvery waits until condition is satisfied using interval.
func (w Waiter) EventuallyEvery(label string, timeout, interval time.Duration, condition func() bool) {
	w.t.Helper()
	require.Eventually(w.t, condition, ScaleTimeout(timeout), interval, label)
}

// EventuallyEveryWithin waits until condition is satisfied using an already scaled timeout.
func (w Waiter) EventuallyEveryWithin(label string, timeout, interval time.Duration, condition func() bool) {
	w.t.Helper()
	require.Eventually(w.t, condition, timeout, interval, label)
}

// FileExists waits until path exists.
func (w Waiter) FileExists(path string, timeout time.Duration) {
	w.Eventually("expected file to exist: "+path, timeout, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

// FileMissing waits until path no longer exists.
func (w Waiter) FileMissing(path string, timeout time.Duration) {
	w.Eventually("expected file to be removed: "+path, timeout, func() bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err)
	})
}
