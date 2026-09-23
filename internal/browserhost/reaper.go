// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

const (
	closeTimeout = 10 * time.Second
	// ReapInterval is how often a long-lived process sweeps browser sessions.
	ReapInterval = time.Minute
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
	var errs []error
	if record.CDPURL != "" {
		errs = append(errs, CloseBrowser(closeCtx, record.CDPURL))
	}
	if record.ExtensionDir != "" {
		errs = append(errs, os.RemoveAll(record.ExtensionDir))
	}
	if record.OwnsUserDataDir && record.UserDataDir != "" {
		errs = append(errs, os.RemoveAll(record.UserDataDir))
	}
	errs = append(errs, store.Delete(record.ID))
	return errors.Join(errs...)
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
