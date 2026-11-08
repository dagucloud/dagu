// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const (
	maxLockHeartbeatInterval  = 7 * time.Second
	defaultLockStaleThreshold = 30 * time.Second

	// defaultFenceExitAfter bounds how long a scheduler that lost its lock may
	// take to stop before the process is ended.
	defaultFenceExitAfter = 10 * time.Second
)

// ErrLockLost is returned by Start when the scheduler stopped because it could
// no longer hold the scheduler lock. The process should exit with a failure so
// that a supervisor restarts it.
var ErrLockLost = errors.New("scheduler lock lost")

func exitOnLostLock(cause error) {
	fmt.Fprintf(os.Stderr, "scheduler did not stop in time after losing its lock: %v\n", cause)
	os.Exit(1)
}

// selfFence stops a scheduler that can no longer act as active. Stop can block
// on the same storage that caused the loss, so a watchdog ends the process if
// it does not finish in time.
func (s *Scheduler) selfFence(ctx context.Context, cause error) {
	lost := fmt.Errorf("%w: %v", ErrLockLost, cause)
	s.lockLost.Store(&lost)
	logger.Error(ctx, "Scheduler lock lost, self-fencing", tag.Error(cause))

	// Clear lockHeld BEFORE calling Stop. This makes releaseDirLock skip Unlock,
	// so a scheduler that just lost ownership cannot remove a replacement lock
	// acquired by another process.
	s.lockHeld.Store(false)

	s.armFenceWatchdog(lost)
	s.Stop(ctx)
}

// armFenceWatchdog ends the process if Start has not returned in time. Stop can
// return while Start still waits on a loop blocked on the same storage.
func (s *Scheduler) armFenceWatchdog(lost error) {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	if s.startReturned {
		return
	}
	s.fenceWatchdog = time.AfterFunc(s.fenceExitAfter, func() { s.fenceExit(lost) })
}

func (s *Scheduler) disarmFenceWatchdog() {
	s.fenceMu.Lock()
	defer s.fenceMu.Unlock()
	s.startReturned = true
	if s.fenceWatchdog != nil {
		s.fenceWatchdog.Stop()
	}
}

// lockLostError returns the reason Start must report a failure, if any.
func (s *Scheduler) lockLostError() error {
	if lost := s.lockLost.Load(); lost != nil {
		return *lost
	}
	return nil
}

// lockLease keeps the scheduler lock renewed and tracks how long the instance
// may keep acting as active.
//
// A standby can take the lock once a renewal is older than the stale threshold.
// The lease ends one heartbeat interval before that, so an instance that cannot
// reach its storage stops before another one can start.
type lockLease struct {
	lock     dirlock.DirLock
	interval time.Duration
	budget   time.Duration
	expiry   atomic.Pointer[time.Time]
}

func newLockLease(lock dirlock.DirLock, staleThreshold time.Duration) *lockLease {
	if staleThreshold <= 0 {
		staleThreshold = defaultLockStaleThreshold
	}
	interval := min(maxLockHeartbeatInterval, staleThreshold/4)
	lease := &lockLease{
		lock:     lock,
		interval: interval,
		budget:   staleThreshold - interval,
	}
	lease.renewedAt(time.Now())
	return lease
}

func (l *lockLease) renewedAt(start time.Time) {
	expiry := start.Add(l.budget)
	l.expiry.Store(&expiry)
}

// expired reports whether the instance can no longer trust that it is active.
func (l *lockLease) expired() bool {
	return !time.Now().Before(*l.expiry.Load())
}

// run renews the lease until done closes or ctx ends, and then returns nil. It
// returns an error once the lock is lost or the lease runs out. A renewal runs
// on its own goroutine, because a hard-mounted filesystem can block it forever.
func (l *lockLease) run(ctx context.Context, done <-chan any) error {
	type beat struct {
		start time.Time
		err   error
	}

	deadline := time.NewTimer(time.Until(*l.expiry.Load()))
	defer deadline.Stop()
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	results := make(chan beat, 1)
	inFlight := false

	for {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			return fmt.Errorf("scheduler lock was not renewed within %s", l.budget)
		case <-ticker.C:
			if inFlight {
				continue
			}
			inFlight = true
			start := time.Now()
			go func() {
				results <- beat{start: start, err: l.lock.Heartbeat(ctx)}
			}()
		case result := <-results:
			inFlight = false
			if result.err == nil {
				l.renewedAt(result.start)
				deadline.Reset(time.Until(*l.expiry.Load()))
				continue
			}
			if errors.Is(result.err, dirlock.ErrLockNotHeld) || errors.Is(result.err, dirlock.ErrNotLocked) {
				return result.err
			}
			logger.Warn(ctx, "Failed to renew scheduler lock, will retry", tag.Error(result.err))
		}
	}
}
