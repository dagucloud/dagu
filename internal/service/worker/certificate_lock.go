// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// lockFileName is the file, in the credential directory, that the worker
// using the certificate keeps locked.
const lockFileName = "lock"

// lockTiming is how a worker waits for the lock on its certificate.
type lockTiming struct {
	// wait bounds how long a worker waits for a lock another worker holds,
	// such as one that is still stopping.
	wait time.Duration
	// retry is how often a waiting worker tries the lock again.
	retry time.Duration
}

var defaultLockTiming = lockTiming{wait: 30 * time.Second, retry: time.Second}

// errAnotherWorker is the failure of a worker whose certificate lock a live
// worker holds.
var errAnotherWorker = failure{
	cause: "Another worker uses this data directory",
	next:  "Give each worker its own data directory or volume.",
}

// lockCertificate locks the certificate in dir, the credential directory.
// The operating system holds the lock until release or until the process
// exits, also while it is paused. While another worker holds it, it waits
// at most timing.wait and then fails with errAnotherWorker. It fails with
// cannotLock when the lock cannot be taken at all, such as on a read-only
// filesystem.
func lockCertificate(ctx context.Context, dir string, timing lockTiming, cannotLock func(error) error) (*fileutil.FileLock, error) {
	path := filepath.Join(dir, lockFileName)
	try := func() (*fileutil.FileLock, error) {
		if err := mkdirPrivate(dir); err != nil {
			return nil, cannotLock(err)
		}
		lock, err := fileutil.TryLockFile(path, credentialFileMode)
		if err != nil && !errors.Is(err, fileutil.ErrLocked) {
			return nil, cannotLock(err)
		}
		return lock, err
	}
	lock, err := try()
	if !errors.Is(err, fileutil.ErrLocked) {
		return lock, err
	}
	logger.Info(ctx, "Another worker holds the certificate of this data directory; waiting for it", tag.Timeout(timing.wait))
	deadline := time.NewTimer(timing.wait)
	defer deadline.Stop()
	ticker := time.NewTicker(timing.retry)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errAnotherWorker
		case <-ticker.C:
		}
		lock, err := try()
		if !errors.Is(err, fileutil.ErrLocked) {
			return lock, err
		}
	}
}
