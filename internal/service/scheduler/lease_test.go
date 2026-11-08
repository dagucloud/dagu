// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/stretchr/testify/require"
)

const testLeaseStale = 2 * time.Second

type scriptedLock struct {
	dirlock.DirLock
	beats     atomic.Int32
	heartbeat func(beat int32) error
}

func (l *scriptedLock) Heartbeat(context.Context) error {
	return l.heartbeat(l.beats.Add(1))
}

func runLease(t *testing.T, lease *lockLease) (<-chan error, func()) {
	t.Helper()
	done := make(chan any)
	result := make(chan error, 1)
	go func() {
		result <- lease.run(context.Background(), done)
	}()
	return result, func() { close(done) }
}

func TestLeaseInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		stale        time.Duration
		wantInterval time.Duration
	}{
		{name: "default", stale: 30 * time.Second, wantInterval: 7 * time.Second},
		{name: "short", stale: 4 * time.Second, wantInterval: time.Second},
		{name: "unset", stale: 0, wantInterval: 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lease := newLockLease(&scriptedLock{}, tt.stale)
			require.Equal(t, tt.wantInterval, lease.interval)
		})
	}
}

// A renewal that fails for a moment must not end the lease while time remains.
func TestLeaseTransient(t *testing.T) {
	t.Parallel()

	lock := &scriptedLock{heartbeat: func(beat int32) error {
		if beat == 1 {
			return errors.New("input/output error")
		}
		return nil
	}}
	lease := newLockLease(lock, testLeaseStale)
	result, stop := runLease(t, lease)

	select {
	case err := <-result:
		t.Fatalf("lease ended on a transient error: %v", err)
	case <-time.After(2 * testLeaseStale):
	}
	require.False(t, lease.expired())

	stop()
	require.NoError(t, <-result)
}

// A renewal stuck on storage must end the lease before a standby can take over.
func TestLeaseHang(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)
	lock := &scriptedLock{heartbeat: func(int32) error {
		<-release
		return nil
	}}
	lease := newLockLease(lock, testLeaseStale)
	started := time.Now()
	result, _ := runLease(t, lease)

	select {
	case err := <-result:
		require.Error(t, err)
		require.Less(t, time.Since(started), testLeaseStale, "the lease must end before the lock goes stale")
	case <-time.After(5 * testLeaseStale):
		t.Fatal("lease did not end while the renewal hung")
	}
	require.True(t, lease.expired())
	require.EqualValues(t, 1, lock.beats.Load(), "a hung renewal must not start another")
}

func TestLeaseLost(t *testing.T) {
	t.Parallel()

	lock := &scriptedLock{heartbeat: func(int32) error {
		return dirlock.ErrLockNotHeld
	}}
	lease := newLockLease(lock, testLeaseStale)
	started := time.Now()
	result, _ := runLease(t, lease)

	err := <-result
	require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	require.Less(t, time.Since(started), lease.budget, "losing the lock ends the lease at once")
}

type stuckEntryReader struct {
	staticEntryReader
	release chan struct{}
}

func (r *stuckEntryReader) Stop() { <-r.release }

// A stop that hangs on storage must not leave a process running with no lock.
func TestFenceWatchdog(t *testing.T) {
	fixture := newHASchedulerFixture(t)
	sc := newHASchedulerForTest(t, fixture, TestHooks{})

	reader := &stuckEntryReader{release: make(chan struct{})}
	defer close(reader.release)
	sc.entryReader = reader
	sc.fenceExitAfter = 20 * time.Millisecond
	exited := make(chan error, 1)
	sc.fenceExit = func(cause error) { exited <- cause }

	go sc.selfFence(context.Background(), errors.New("lease ran out"))

	select {
	case cause := <-exited:
		require.ErrorIs(t, cause, ErrLockLost)
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not end the process")
	}
}

// Stop can return while Start still waits on a loop blocked on the same
// storage, so the watchdog must outlive Stop.
func TestFenceWatchdogOutlivesStop(t *testing.T) {
	fixture := newHASchedulerFixture(t)
	sc := newHASchedulerForTest(t, fixture, TestHooks{})

	release := make(chan struct{})
	close(release)
	sc.entryReader = &stuckEntryReader{release: release}
	sc.fenceExitAfter = 200 * time.Millisecond
	exited := make(chan error, 1)
	sc.fenceExit = func(cause error) { exited <- cause }

	sc.selfFence(context.Background(), errors.New("lease ran out"))

	select {
	case cause := <-exited:
		require.ErrorIs(t, cause, ErrLockLost)
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog stopped with Stop")
	}
}

// Once Start has returned, the process is free to carry on.
func TestFenceWatchdogDisarm(t *testing.T) {
	t.Parallel()

	var exits atomic.Int32
	sc := &Scheduler{fenceExitAfter: 10 * time.Millisecond, fenceExit: func(error) { exits.Add(1) }}

	sc.armFenceWatchdog(ErrLockLost)
	sc.disarmFenceWatchdog()
	sc.armFenceWatchdog(ErrLockLost)

	time.Sleep(100 * time.Millisecond)
	require.Zero(t, exits.Load())
}
