// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/require"
)

const (
	testLeaseStale = 2 * time.Second
	// testLeaseRetry is the renewal interval a lease with testLeaseStale uses.
	testLeaseRetry = testLeaseStale / 4
)

type scriptedLock struct {
	dirlock.DirLock
	beats     atomic.Int32
	unlocks   atomic.Int32
	heartbeat func(beat int32) error
}

func (l *scriptedLock) Heartbeat(context.Context) error {
	return l.heartbeat(l.beats.Add(1))
}

func (l *scriptedLock) Unlock() error {
	l.unlocks.Add(1)
	return nil
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
			lease := newLockLease(&scriptedLock{}, tt.stale, time.Now())
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
	lease := newLockLease(lock, testLeaseStale, time.Now())
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
	lease := newLockLease(lock, testLeaseStale, time.Now())
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
	lease := newLockLease(lock, testLeaseStale, time.Now())
	started := time.Now()
	result, _ := runLease(t, lease)

	err := <-result
	require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	require.Less(t, time.Since(started), lease.budget, "losing the lock ends the lease at once")
}

// The lease starts when the renewal that confirms the lock starts. A renewal
// stuck on storage must fail startup before a standby can take over, without
// waiting for the renewal to return, and release the lock once it does.
func TestLeaseStartHang(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	lock := &scriptedLock{heartbeat: func(int32) error {
		<-release
		return nil
	}}
	started := time.Now()
	_, err := startLockLease(context.Background(), lock, testLeaseStale)
	require.Error(t, err)
	require.Less(t, time.Since(started), testLeaseStale, "startup must fail before the lock goes stale")

	close(release)
	require.Eventually(t, func() bool {
		return lock.unlocks.Load() == 1
	}, 5*time.Second, 10*time.Millisecond, "the lock must be released once the renewal returns")
}

// A startup renewal that fails for a moment must be retried rather than end
// startup, as a renewal after startup is.
func TestLeaseStartRetry(t *testing.T) {
	t.Parallel()

	lock := &scriptedLock{heartbeat: func(beat int32) error {
		if beat == 1 {
			return errors.New("input/output error")
		}
		return nil
	}}
	lease, err := startLockLease(context.Background(), lock, testLeaseStale)
	require.NoError(t, err)
	require.False(t, lease.expired())
	require.EqualValues(t, 2, lock.beats.Load())
	require.Zero(t, lock.unlocks.Load())
}

// A startup renewal that finds the lock taken over must fail at once.
func TestLeaseStartLost(t *testing.T) {
	t.Parallel()

	lock := &scriptedLock{heartbeat: func(int32) error {
		return dirlock.ErrLockNotHeld
	}}
	started := time.Now()
	_, err := startLockLease(context.Background(), lock, testLeaseStale)
	require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	require.EqualValues(t, 1, lock.beats.Load(), "a lost lock must not be renewed again")
	require.Less(t, time.Since(started), testLeaseRetry, "losing the lock must not wait for a retry")
}

// Stopping while a startup renewal waits to retry must end startup at once and
// release the lock.
func TestLeaseStartCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lock := &scriptedLock{heartbeat: func(int32) error {
		cancel()
		return errors.New("input/output error")
	}}
	started := time.Now()
	_, err := startLockLease(ctx, lock, testLeaseStale)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 1, lock.beats.Load())
	require.EqualValues(t, 1, lock.unlocks.Load())
	require.Less(t, time.Since(started), testLeaseRetry, "stopping must not wait for a retry")
}

// stallingLock stalls once acquiring has taken the lock, as a process paused
// at that moment does.
type stallingLock struct {
	dirlock.DirLock
	stall func()
}

func (l *stallingLock) Lock(ctx context.Context) error {
	if err := l.DirLock.Lock(ctx); err != nil {
		return err
	}
	l.stall()
	return nil
}

type statusRecorder struct {
	serviceregistry.ServiceRegistry
	mu       sync.Mutex
	statuses []serviceregistry.ServiceStatus
}

func (*statusRecorder) Register(context.Context, serviceregistry.ServiceName, serviceregistry.HostInfo) error {
	return nil
}

func (*statusRecorder) Unregister(context.Context) {}

func (r *statusRecorder) UpdateStatus(_ context.Context, _ serviceregistry.ServiceName, status serviceregistry.ServiceStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, status)
	return nil
}

func (r *statusRecorder) reported() []serviceregistry.ServiceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.statuses)
}

// A standby can take the lock over while acquiring it stalls. The scheduler
// that stalled must find that out before it reports itself active.
func TestLeaseTakenOverDuringAcquire(t *testing.T) {
	fixture := newHASchedulerFixture(t)
	sc := newHASchedulerForTest(t, fixture, TestHooks{})
	registry := &statusRecorder{}
	sc.serviceRegistry = registry

	lockDir := filepath.Join(persisfile.SchedulerProcessDir(fixture.cfg.Paths), "locks")
	standby := dirlock.New(lockDir, &dirlock.LockOptions{
		StaleThreshold:   50 * time.Millisecond,
		RetryInterval:    10 * time.Millisecond,
		ObserveHeartbeat: true,
	})
	sc.dirLock = &stallingLock{DirLock: sc.dirLock, stall: func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, standby.Lock(ctx))
	}}

	require.ErrorIs(t, sc.Start(context.Background()), ErrLockLost)
	require.NotContains(t, registry.reported(), serviceregistry.ServiceStatusActive)
	require.True(t, standby.IsHeldByMe(), "the stalled scheduler must leave the new holder's lock in place")
	require.NoError(t, standby.Unlock())
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
