// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/project"
)

const (
	// lifecycleLockDirName is the directory in paths.data_dir whose lock
	// serializes the creates and deletes of projects across processes.
	lifecycleLockDirName = "projects-lifecycle.lock"
	lifecycleLockStale   = 30 * time.Second
	// lifecycleLockBeat renews the lock well within lifecycleLockStale.
	lifecycleLockBeat = 5 * time.Second
)

// lifecycleLockWait bounds the wait for another create or delete.
var lifecycleLockWait = 10 * time.Second

// withLifecycleLock runs fn while holding the lock that one create or
// delete of a project holds at a time, in every process that shares
// paths.data_dir. It returns an error wrapping project.ErrLifecycleBusy
// when another one holds the lock longer than lifecycleLockWait. A lock
// left by a process that died is taken over once it goes stale.
func withLifecycleLock(ctx context.Context, cfg *config.Config, fn func() error) error {
	dir := filepath.Join(cfg.Paths.DataDir, lifecycleLockDirName)
	if err := fileutil.MkdirAll(dir, projectDirPermission); err != nil {
		return fmt.Errorf("create the project lifecycle lock %s: %w", dir, err)
	}
	// Staleness is judged by the lock's modification time: a contender
	// that watched the heartbeat instead would wait out a full
	// lifecycleLockStale, longer than lifecycleLockWait.
	lock := dirlock.New(dir, &dirlock.LockOptions{StaleThreshold: lifecycleLockStale})
	waitCtx, cancel := context.WithTimeout(ctx, lifecycleLockWait)
	err := lock.Lock(waitCtx)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return project.ErrLifecycleBusy
	}
	if err != nil {
		return fmt.Errorf("lock %s: %w", dir, err)
	}
	defer func() { _ = lock.Unlock() }()

	beatCtx, stop := context.WithCancel(ctx)
	beaten := make(chan struct{})
	go func() {
		defer close(beaten)
		ticker := time.NewTicker(lifecycleLockBeat)
		defer ticker.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-ticker.C:
				_ = lock.Heartbeat(beatCtx)
			}
		}
	}()
	defer func() {
		stop()
		<-beaten
	}()
	return fn()
}
