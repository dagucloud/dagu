// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

const (
	closeTimeout = 10 * time.Second
	// ReapInterval is how often a long-lived process sweeps browser sessions.
	ReapInterval = time.Minute
	// removeAttempts and removeRetryDelay give a closing browser time to
	// release its profile files.
	removeAttempts   = 20
	removeRetryDelay = 250 * time.Millisecond
)

// ResumableFunc reports whether a detached session still belongs to a step
// that can resume it. A nil ResumableFunc treats every unexpired session as
// resumable.
type ResumableFunc func(context.Context, Record) bool

// Sweep closes browsers that no step can resume and removes their records:
// running sessions whose owner process is gone, detached sessions past their
// deadline, and detached sessions whose step can no longer resume.
func Sweep(ctx context.Context, store *Store, now time.Time, resumable ResumableFunc) error {
	records, err := store.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, record := range records {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !abandoned(ctx, record, now, resumable) {
			continue
		}
		errs = append(errs, Release(ctx, store, record))
	}
	return errors.Join(errs...)
}

// Release closes the browser described by record, deletes the files it owns,
// and removes the record.
func Release(ctx context.Context, store *Store, record Record) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	// A browser that did not close keeps its record and files, so a later
	// sweep can try again.
	if record.CDPURL != "" {
		if err := closeRecordedBrowser(closeCtx, record); err != nil {
			return fmt.Errorf("close browser at %s: %w", record.CDPURL, err)
		}
	}
	var errs []error
	if record.ExtensionDir != "" {
		errs = append(errs, removeAll(closeCtx, record.ExtensionDir))
	}
	if record.OwnsUserDataDir && record.UserDataDir != "" {
		errs = append(errs, removeAll(closeCtx, record.UserDataDir))
	}
	errs = append(errs, store.Delete(record.ID))
	return errors.Join(errs...)
}

// closeRecordedBrowser closes the browser over DevTools. When the browser
// does not answer, it ends the recorded browser process instead, provided
// the process ID still belongs to that browser.
func closeRecordedBrowser(ctx context.Context, record Record) error {
	err := CloseBrowser(ctx, record.CDPURL)
	if err == nil {
		return nil
	}
	if endProcess(record.BrowserPID, record.BrowserStartedAt) && waitForExit(ctx, record.CDPURL) {
		return nil
	}
	return err
}

// endProcess ends pid when it is still the process that started at
// startedAt. Without a recorded start time it does nothing, because the
// process ID may have been reused.
func endProcess(pid int, startedAt int64) bool {
	matched, _, ok := procutil.MatchesStartTime(pid, startedAt)
	if !ok || !matched {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Kill() == nil
}

// removeAll deletes dir, retrying while an exiting browser still writes to
// it or holds its files open.
func removeAll(ctx context.Context, dir string) error {
	var err error
	for range removeAttempts {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(removeRetryDelay):
		}
	}
	return err
}

// RunReaper sweeps the store until ctx is cancelled.
func RunReaper(ctx context.Context, store *Store, resumable ResumableFunc) {
	ticker := time.NewTicker(ReapInterval)
	defer ticker.Stop()
	for {
		_ = Sweep(ctx, store, time.Now(), resumable)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func abandoned(ctx context.Context, record Record, now time.Time, resumable ResumableFunc) bool {
	switch record.State {
	case StateRunning:
		return !ownerAlive(record)
	case StateDetached:
		if !record.Deadline.IsZero() && now.After(record.Deadline) {
			return true
		}
		return resumable != nil && !resumable(ctx, record)
	default:
		return true
	}
}

func ownerAlive(record Record) bool {
	if !procutil.IsAlive(record.OwnerPID) {
		return false
	}
	matched, _, ok := procutil.MatchesStartTime(record.OwnerPID, record.OwnerStartedAt)
	// Without a start time to compare, a live PID is trusted.
	return !ok || matched
}
