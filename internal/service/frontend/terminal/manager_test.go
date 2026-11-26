// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package terminal

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managerTiming(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 4
	}
	return base
}

func TestManager_ReleasePendingFreesReservation(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	_, err = manager.Acquire()
	require.ErrorIs(t, err, ErrMaxSessionsReached)

	lease.Release()

	_, err = manager.Acquire()
	require.NoError(t, err)
}

func TestManager_LeaseActivateRequiresActiveReservation(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)

	lease.Release()

	err = lease.Activate(&Connection{ID: "conn-1"})
	require.ErrorIs(t, err, ErrReservationInactive)
}

func TestManager_LeaseDoubleReleaseIsNoOp(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)

	lease.Release()
	lease.Release()

	_, err = manager.Acquire()
	require.NoError(t, err)
}

func TestManager_LeaseReleaseAfterActivationIsNoOp(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	lease.Release()
	lease.Release()

	_, err = manager.Acquire()
	require.NoError(t, err)
}

func TestManager_ReleaseSlotFreesCapacityBeforeCleanup(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	// Slot is occupied — second acquire must fail.
	_, err = manager.Acquire()
	require.ErrorIs(t, err, ErrMaxSessionsReached)

	// Release only the slot — connection still tracked for shutdown.
	lease.ReleaseSlot()

	// Slot is free — second acquire succeeds immediately.
	lease2, err := manager.Acquire()
	require.NoError(t, err)

	// Shutdown still waits for the first lease's full Release.
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
		close(done)
	}()

	// Shutdown should not complete yet — first lease not fully released.
	select {
	case <-done:
		t.Fatal("shutdown completed before lease.Release()")
	case <-time.After(50 * time.Millisecond):
	}

	// Complete cleanup.
	lease.Release()
	lease2.Release()
	<-done
}

func TestManager_ReleaseSlotThenReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	lease.ReleaseSlot()
	lease.ReleaseSlot() // double call is a no-op
	lease.Release()
	lease.Release() // double call is a no-op

	_, err = manager.Acquire()
	require.NoError(t, err)
}

func TestManager_ReleaseWithoutReleaseSlotAlsoFreesSlot(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	// Skip ReleaseSlot, go straight to Release — must free everything.
	lease.Release()

	_, err = manager.Acquire()
	require.NoError(t, err)
}

func TestManager_ActivateFailsWhenManagerIsShuttingDown(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Shutdown(ctx))

	err = lease.Activate(&Connection{ID: "conn-1"})
	require.ErrorIs(t, err, ErrManagerShuttingDown)

	_, err = manager.Acquire()
	require.ErrorIs(t, err, ErrManagerShuttingDown)
}

func TestManager_ShutdownWaitsForActiveSessionsOnly(t *testing.T) {
	t.Parallel()

	t.Run("ActiveSessions", func(t *testing.T) {
		manager := NewManager(context.Background(), 1)
		lease, err := manager.Acquire()
		require.NoError(t, err)
		require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

		go func() {
			// Intentional sleep: simulates a session that takes time before releasing.
			// The test verifies that Shutdown blocks until the lease is freed.
			time.Sleep(100 * time.Millisecond)
			lease.Release()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		start := time.Now()
		require.NoError(t, manager.Shutdown(ctx))
		assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	})

	t.Run("PendingReservations", func(t *testing.T) {
		manager := NewManager(context.Background(), 1)
		lease, err := manager.Acquire()
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		start := time.Now()
		require.NoError(t, manager.Shutdown(ctx))
		assert.Less(t, time.Since(start), 100*time.Millisecond)

		lease.Release()
	})
}

func TestManager_ShutdownObservesCleanupWithinRemainingBudget(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	// The session cleans up only once Shutdown has begun, so Shutdown must
	// wait for it, and it does so within the budget.
	var released atomic.Bool
	go func() {
		<-manager.ctx.Done()
		released.Store(true)
		lease.Release()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), managerTiming(100*time.Millisecond))
	defer cancel()

	require.NoError(t, manager.Shutdown(ctx))
	assert.True(t, released.Load(), "Shutdown returned before the session cleaned up")
}

func TestManager_ShutdownReturnsPromptlyWhenDeadlineExpires(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background(), 1)
	lease, err := manager.Acquire()
	require.NoError(t, err)
	require.NoError(t, lease.Activate(&Connection{ID: "conn-1"}))

	go func() {
		// Intentional sleep: simulates a lease released after the context
		// deadline so the test can verify Shutdown returns promptly on expiry.
		time.Sleep(managerTiming(50 * time.Millisecond))
		lease.Release()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), managerTiming(10*time.Millisecond))
	defer cancel()

	start := time.Now()
	err = manager.Shutdown(ctx)
	elapsed := time.Since(start)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, errTerminalShutdownTimeout)
	assert.Less(t, elapsed, managerTiming(75*time.Millisecond))
}

func TestForceKillDelay(t *testing.T) {
	t.Parallel()

	t.Run("NoDeadline", func(t *testing.T) {
		delay, ok := forceKillDelay(context.Background(), processShutdownGrace)
		assert.False(t, ok)
		assert.Zero(t, delay)
	})

	t.Run("ReservesCleanupWindow", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3200*time.Millisecond)
		defer cancel()

		delay, ok := forceKillDelay(ctx, processShutdownGrace)
		require.True(t, ok)
		assert.GreaterOrEqual(t, delay, 150*time.Millisecond)
		assert.Less(t, delay, 400*time.Millisecond)
	})
}

func TestWaitForForcedCleanup(t *testing.T) {
	t.Parallel()

	t.Run("DoneWinsImmediately", func(t *testing.T) {
		done := make(chan struct{})
		close(done)

		require.NoError(t, waitForForcedCleanup(done, context.Background()))
	})

	t.Run("ReturnsTimeoutWhenContextDone", func(t *testing.T) {
		done := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := waitForForcedCleanup(done, ctx)
		require.ErrorIs(t, err, errTerminalShutdownTimeout)
		require.ErrorIs(t, err, context.Canceled)
	})
}
