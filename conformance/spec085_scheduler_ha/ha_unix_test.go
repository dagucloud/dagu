// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package spec085_scheduler_ha_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// An active scheduler that stalls past the stale threshold loses the lock. When
// it resumes it exits with a failure, and the slot that came due during the
// stall runs once, on the new active scheduler.
func TestStallActive(t *testing.T) {
	t.Parallel()
	c := newSlotCluster(t)
	active, standby := c.startPair()
	slot := nextSlot()

	sleepUntil(slot.Add(-2 * time.Second))
	require.NoError(t, syscall.Kill(active.process.PID(), syscall.SIGSTOP))
	standby.waitRole(t, roleActive, staleThreshold+2*retryInterval+startupAllowance)
	// Only the new active scheduler can run the slot while the old one is stopped.
	waitUntil(t, startupAllowance+harness.WaitTimeout(t), "the new active scheduler to run the slot", func() bool {
		return c.slotRuns(slot) > 0
	})

	require.NoError(t, syscall.Kill(active.process.PID(), syscall.SIGCONT))
	select {
	case <-active.process.Done():
	case <-time.After(harness.WaitTimeout(t)):
		require.FailNow(t, "the stalled scheduler did not exit after resuming")
	}
	require.NotZero(t, active.process.ExitCode())
	c.requireOneSlotRun(slot)
}
