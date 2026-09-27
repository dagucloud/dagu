// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
)

const (
	desktopLockName = "desktop.lock"
	desktopDirMode  = 0o700
	// leaseHeartbeatInterval keeps the lock well inside dirlock's staleness
	// threshold.
	leaseHeartbeatInterval = 10 * time.Second
)

// desktopLease holds exclusive use of the desktop. Two steps typing and
// clicking at once would interfere, so computer steps on one host take
// turns.
type desktopLease struct {
	lock dirlock.DirLock
	stop context.CancelFunc
}

func acquireDesktop(ctx context.Context, computerDir string, t *timeline) (*desktopLease, error) {
	lockDir := filepath.Join(computerDir, desktopLockName)
	if err := os.MkdirAll(lockDir, desktopDirMode); err != nil {
		return nil, fmt.Errorf("create desktop lock: %w", err)
	}
	lock := dirlock.New(lockDir, &dirlock.LockOptions{
		OnWait: func() { t.lifecycle(statusWaiting, "Waiting for another computer step to finish using the desktop") },
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock the desktop: %w", err)
	}
	heartbeatCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	go heartbeat(heartbeatCtx, lock)
	return &desktopLease{lock: lock, stop: stop}, nil
}

func (l *desktopLease) release() {
	if l == nil {
		return
	}
	l.stop()
	_ = l.lock.Unlock()
}

func heartbeat(ctx context.Context, lock dirlock.DirLock) {
	ticker := time.NewTicker(leaseHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = lock.Heartbeat(ctx)
		}
	}
}
