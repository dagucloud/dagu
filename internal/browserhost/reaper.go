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

// SweepAll sweeps the browsers steps keep and the browser sessions under the
// browser data directory.
func SweepAll(ctx context.Context, browserDataDir string, now time.Time, resumable ResumableFunc) error {
	return errors.Join(
		Sweep(ctx, NewStore(browserDataDir), now, resumable),
		sweepSessions(ctx, NewInteractiveStore(browserDataDir), now),
	)
}

// ResumableFunc reports whether a detached session still belongs to a step
// that can resume it. A nil ResumableFunc treats every unexpired session as
// resumable.
type ResumableFunc func(context.Context, Record) bool

// Sweep closes browsers that no step can resume and removes their records:
// running sessions whose owner process is gone, detached sessions past their
// deadline, detached sessions whose step can no longer resume, and
// browsers whose write gate is gone.
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
	if err := closeOwned(closeCtx, record); err != nil {
		return err
	}
	return store.Delete(record.ID)
}

// Retire closes the browser of a browser session and deletes the files it
// owns, but keeps the record, as ended, until keepUntil, so the session's
// history can still be read. It returns the ended record.
func Retire(ctx context.Context, store *Store, record Record, keepUntil time.Time) (Record, error) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	if err := closeOwned(closeCtx, record); err != nil {
		return record, err
	}
	ended := Record{
		ID:          record.ID,
		State:       StateEnded,
		Deadline:    keepUntil,
		Profile:     record.Profile,
		Interactive: record.Interactive,
	}
	return ended, store.Save(ended)
}

// closeOwned closes the browser described by record and deletes the files
// it owns. A browser that did not close keeps its files, so a later sweep
// can try again.
func closeOwned(ctx context.Context, record Record) error {
	if record.CDPURL != "" {
		if err := closeRecordedBrowser(ctx, record); err != nil {
			return fmt.Errorf("close browser at %s: %w", record.CDPURL, err)
		}
	}
	// The certificate proxy and the write gate end on their own once their
	// browser is gone; ending them here frees their ports at once. The gate
	// ends only after the browser, which would send a request the gate
	// paused.
	endProcess(record.CertProxyPID, record.CertProxyStartedAt)
	endProcess(record.WriteGatePID, record.WriteGateStartedAt)
	var errs []error
	for _, dir := range []string{record.ExtensionDir, record.WorkDir} {
		if dir != "" {
			errs = append(errs, removeAll(ctx, dir))
		}
	}
	if record.OwnsUserDataDir && record.UserDataDir != "" {
		errs = append(errs, removeAll(ctx, record.UserDataDir))
	}
	return errors.Join(errs...)
}

// sweepSessions closes the browsers of browser sessions that went idle past
// their deadline or whose command process is gone, keeping their history,
// and removes ended sessions past their retention. A session a command is
// using is left alone.
func sweepSessions(ctx context.Context, store *Store, now time.Time) error {
	records, err := store.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, record := range records {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if record.State == StateEnded {
			if !record.Deadline.IsZero() && now.After(record.Deadline) {
				errs = append(errs, store.Purge(record.ID))
			}
			continue
		}
		if sessionAbandoned(record, now) {
			_, err := RetireAbandoned(ctx, store, record.ID, now)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RetireAbandoned retires the browser session id when nothing will use its
// browser again, unless one of its commands holds it. It reports whether
// the session was retired.
func RetireAbandoned(ctx context.Context, store *Store, id string, now time.Time) (bool, error) {
	// A session in use is left without taking its lock, which a command
	// starting meanwhile would find held.
	if record, err := store.Load(id); err != nil || !sessionAbandoned(record, now) {
		return false, nil
	}
	lock := store.SessionLock(id)
	if err := lock.TryLock(); err != nil {
		return false, nil
	}
	defer func() { _ = lock.Unlock() }()
	// A command may have renewed the session since it was last read.
	record, err := store.Load(id)
	if err != nil || !sessionAbandoned(record, now) {
		return false, nil
	}
	if _, err := Retire(ctx, store, record, now.Add(SessionRetention)); err != nil {
		return false, err
	}
	return true, nil
}

// sessionAbandoned reports whether nothing will use a browser session's
// browser again: it waited past its idle deadline, the command driving it
// is gone, or the gate deciding its writes is.
func sessionAbandoned(record Record, now time.Time) bool {
	if record.State != StateEnded && gateLost(record) {
		return true
	}
	switch record.State {
	case StateInteractive:
		return !record.Deadline.IsZero() && now.After(record.Deadline)
	case StateRunning:
		return !ownerAlive(record)
	case StateEnded:
		return false
	case StateDetached:
		// Only a step detaches its browser; a session never does.
		return true
	default:
		return true
	}
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

// RunReaper sweeps the browsers steps keep and the browser sessions under
// the browser data directory until ctx is cancelled.
func RunReaper(ctx context.Context, browserDataDir string, resumable ResumableFunc) {
	ticker := time.NewTicker(ReapInterval)
	defer ticker.Stop()
	for {
		_ = SweepAll(ctx, browserDataDir, time.Now(), resumable)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func abandoned(ctx context.Context, record Record, now time.Time, resumable ResumableFunc) bool {
	if gateLost(record) {
		return true
	}
	switch record.State {
	case StateRunning:
		return !ownerAlive(record)
	case StateDetached:
		if !record.Deadline.IsZero() && now.After(record.Deadline) {
			return true
		}
		return resumable != nil && !resumable(ctx, record)
	case StateInteractive, StateEnded:
		// Browser sessions live in their own store; a step's record never
		// holds these states.
		return true
	default:
		return true
	}
}

// gateLost reports whether the browser had a write gate that is gone.
func gateLost(record Record) bool {
	return record.WriteGatePID != 0 && !processAlive(record.WriteGatePID, record.WriteGateStartedAt)
}

func ownerAlive(record Record) bool {
	return processAlive(record.OwnerPID, record.OwnerStartedAt)
}

// processAlive reports whether pid still runs as the process that started
// at startedAt.
func processAlive(pid int, startedAt int64) bool {
	if !procutil.IsAlive(pid) {
		return false
	}
	matched, _, ok := procutil.MatchesStartTime(pid, startedAt)
	// Without a start time to compare, a live PID is trusted.
	return !ok || matched
}
