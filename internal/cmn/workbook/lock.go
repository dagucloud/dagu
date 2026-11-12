// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LockOptions says how long to wait for a workbook another program holds.
type LockOptions struct {
	// WaitFor is how long to retry a locked workbook; zero fails at once.
	WaitFor time.Duration
	// Log receives one line per retry; nil discards them.
	Log func(string)

	// sleep and now are replaced in tests.
	sleep func(context.Context, time.Duration) error
	now   func() time.Time
}

const (
	lockRetryInitial = 2 * time.Second
	lockRetryMax     = time.Minute
)

// lockFilePath is the ~$name.xlsx file Excel writes beside an open workbook.
func lockFilePath(path string) string {
	return filepath.Join(filepath.Dir(path), "~$"+filepath.Base(path))
}

// checkLockFile looks for Excel's lock file beside the workbook, following
// a symbolic link so the lock of the real file is the one checked. Where
// the lock file can be probed, one that another process still holds open
// means the workbook is in use and the result is a LockedError, and one
// nobody holds is a leftover of a crash, so the result is a warning and the
// write goes ahead. Where it cannot be probed, the file is only a hint and
// the warning says so.
func checkLockFile(path string) (warning string, err error) {
	target := path
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		target = resolved
	}
	lock := lockFilePath(target)
	if _, statErr := os.Stat(lock); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", nil
		}
		return "", statErr
	}
	if lockFileHeld(lock) {
		return "", &LockedError{Path: path}
	}
	if lockProbeSupported {
		return fmt.Sprintf("%s exists but no program holds it; the workbook may have been closed without cleanup", filepath.Base(lock)), nil
	}
	return fmt.Sprintf("%s exists; the workbook may be open in another program, or the lock file may be a leftover", filepath.Base(lock)), nil
}

// classifyRenameError is classifyError for a rename over target, which
// Windows refuses with access denied rather than a sharing violation while
// another program holds the target open.
func classifyRenameError(path, target string, err error) error {
	if renameRefusedByHolder(target, err) {
		return &LockedError{Path: path}
	}
	return classifyError(path, err)
}

// classifyError turns a sharing violation into a LockedError and leaves
// every other error alone.
func classifyError(path string, err error) error {
	if err == nil {
		return nil
	}
	if isSharingViolation(err) {
		return &LockedError{Path: path}
	}
	return err
}

// withLockRetry runs attempt, retrying while it reports a LockedError and
// opts.WaitFor allows, with delays from two seconds doubling to one minute.
func withLockRetry(ctx context.Context, path string, opts LockOptions, attempt func() error) error {
	sleep := opts.sleep
	if sleep == nil {
		sleep = sleepContext
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(opts.WaitFor)
	delay := lockRetryInitial
	for {
		err := attempt()
		var locked *LockedError
		if err == nil || !errors.As(err, &locked) || opts.WaitFor <= 0 {
			return err
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return err
		}
		wait := min(delay, remaining)
		if opts.Log != nil {
			opts.Log(fmt.Sprintf("%s is open in another program; retrying in %s (%s left)",
				filepath.Base(path), wait.Round(time.Second), remaining.Round(time.Second)))
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
		delay = min(delay*2, lockRetryMax)
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
