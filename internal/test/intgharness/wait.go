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
	return scaleTimeout(timeout)
}

func scaleTimeout(timeout time.Duration) time.Duration {
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
	require.Eventually(w.t, condition, scaleTimeout(timeout), interval, label)
}

// FileExists waits until path exists.
func (w Waiter) FileExists(path string, timeout time.Duration) {
	w.Eventually("expected file to exist: "+path, timeout, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}
