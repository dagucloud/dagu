// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// lockTiming is how a worker takes and keeps the lock on its certificate.
type lockTiming struct {
	// staleAfter is how long a lock goes without a refresh before another
	// worker takes it over.
	staleAfter time.Duration
	// wait bounds how long a worker waits for a lock another worker holds.
	wait time.Duration
	// refresh is how often the holder refreshes the lock.
	refresh time.Duration
	// retry is how often a waiting worker checks the lock again.
	retry time.Duration
}

// defaultLockTiming refreshes well within the 30 seconds after which Spec
// 108 counts a lock as stale.
var defaultLockTiming = lockTiming{
	staleAfter: 30 * time.Second,
	wait:       30 * time.Second,
	refresh:    10 * time.Second,
	retry:      time.Second,
}

// errAnotherWorker is the failure of a worker whose certificate lock a live
// worker holds.
var errAnotherWorker = failure{
	cause: "Another worker uses this data directory",
	next:  "Give each worker its own data directory or volume.",
}

// certificateLock is a worker's hold on the certificate in its data
// directory, which it refreshes until released.
type certificateLock struct {
	lock dirlock.DirLock
	stop context.CancelFunc
	done chan struct{}
	// lost is closed when another worker took the lock over.
	lost chan struct{}
}

// lockCertificate takes the lock in dir. While another worker holds it, it
// waits at most timing.wait for the lock to be released or to go stale,
// and then fails with errAnotherWorker.
func lockCertificate(ctx context.Context, dir string, timing lockTiming) (*certificateLock, error) {
	lock := dirlock.New(dir, &dirlock.LockOptions{
		StaleThreshold: timing.staleAfter,
		RetryInterval:  timing.retry,
		OnWait: func() {
			logger.Info(ctx, "Another worker holds the certificate lock of this data directory; waiting for it",
				tag.Timeout(timing.wait))
		},
		OnStaleTakeover: func() {
			logger.Info(ctx, "Took over the certificate lock that a stopped worker left")
		},
	})
	waitCtx, cancel := context.WithTimeout(ctx, timing.wait)
	defer cancel()
	if err := lock.Lock(waitCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, errAnotherWorker
		}
		return nil, err
	}
	refreshCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	l := &certificateLock{lock: lock, stop: stop, done: make(chan struct{}), lost: make(chan struct{})}
	go l.refresh(refreshCtx, timing.refresh)
	return l, nil
}

// refresh renews the lock every interval until ctx ends or another worker
// takes the lock over.
func (l *certificateLock) refresh(ctx context.Context, interval time.Duration) {
	defer close(l.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := l.lock.Heartbeat(ctx)
		switch {
		case errors.Is(err, dirlock.ErrLockNotHeld):
			close(l.lost)
			return
		case err != nil:
			logger.Warn(ctx, "Failed to refresh the certificate lock", tag.Error(err))
		}
	}
}

// release stops refreshing the lock and releases it.
func (l *certificateLock) release() error {
	l.stop()
	<-l.done
	err := l.lock.Unlock()
	if errors.Is(err, dirlock.ErrLockNotHeld) {
		return nil
	}
	return err
}
