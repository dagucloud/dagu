// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Summary tells how far a backfill has come. It is small, so a list of
// every DAG reads the backfills it mentions without their records or
// progress.
type Summary struct {
	ID      string `json:"id"`
	DAGName string `json:"dag"`
	// Status is empty while the backfill is open, and failed once it ended
	// failed while a slot it failed has not succeeded since.
	Status Status `json:"status,omitempty"`
	Slots  int    `json:"slots"`
	// Done counts the slots that ended, and Failed those of them that
	// failed.
	Done   int `json:"done"`
	Failed int `json:"failed"`
	// CreatedAt is when the backfill was submitted, which orders the
	// backfills of one DAG.
	CreatedAt time.Time `json:"createdAt"`
}

const (
	// summaryCheckInterval is how often the dispatcher reads the summaries
	// of failed backfills.
	summaryCheckInterval = time.Minute
	// maxSummaryCheckInterval bounds how far apart the checks of one failed
	// backfill grow while its failures stay unresolved.
	maxSummaryCheckInterval = time.Hour
)

// summaryCheck is when the dispatcher next checks a failed backfill, and
// how long it waited since the last check.
type summaryCheck struct {
	next  time.Time
	every time.Duration
}

// clearResolved clears the summary of each failed backfill whose failed
// slots have all succeeded since, or whose DAG is gone. A check reads the
// backfill's record, progress, and slot history, so the checks of a
// backfill whose failures stay unresolved grow apart, from a minute to an
// hour.
func (d *Dispatcher) clearResolved(ctx context.Context) error {
	now := d.now()
	if !d.summariesCheckedAt.IsZero() && now.Sub(d.summariesCheckedAt) < summaryCheckInterval {
		return nil
	}
	d.summariesCheckedAt = now
	summaries, err := d.Store.Summaries(ctx)
	if err != nil {
		return err
	}
	if d.summaryChecks == nil {
		d.summaryChecks = make(map[string]summaryCheck)
	}
	failed := make(map[string]bool, len(summaries))
	var errs []error
	for _, summary := range summaries {
		if summary.Status != StatusFailed {
			continue
		}
		failed[summary.ID] = true
		check := d.summaryChecks[summary.ID]
		if now.Before(check.next) {
			continue
		}
		cleared, err := d.clearIfResolved(ctx, summary)
		if err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: %w", summary.ID, err))
		}
		if cleared {
			delete(d.summaryChecks, summary.ID)
			continue
		}
		check.every = min(max(2*check.every, summaryCheckInterval), maxSummaryCheckInterval)
		check.next = now.Add(check.every)
		d.summaryChecks[summary.ID] = check
	}
	for id := range d.summaryChecks {
		if !failed[id] {
			delete(d.summaryChecks, id)
		}
	}
	return errors.Join(errs...)
}

// clearIfResolved clears a failed backfill's summary under its DAG's lock,
// which a resume that opens it again also holds. It reports whether the
// backfill needs no further check.
func (d *Dispatcher) clearIfResolved(ctx context.Context, summary Summary) (bool, error) {
	unlock, err := d.Store.LockDAG(ctx, summary.DAGName)
	if err != nil {
		return false, err
	}
	defer unlock()
	record, err := d.Store.Get(ctx, summary.ID)
	if errors.Is(err, ErrNotFound) {
		return true, d.Store.ClearSummary(ctx, summary.ID)
	}
	if err != nil {
		return false, err
	}
	if record.Status != StatusFailed {
		return true, nil
	}
	if d.DAGExists != nil {
		exists, err := d.DAGExists(ctx, record)
		if err != nil {
			return false, err
		}
		if !exists {
			return true, d.Store.ClearSummary(ctx, summary.ID)
		}
	}
	target, err := d.Target(ctx, record)
	if err != nil {
		return false, err
	}
	report, err := Describe(ctx, d.Store, d.ReadRun, record)
	if err != nil {
		return false, err
	}
	unresolved, err := hasUnresolvedFailure(ctx, d.History, target.DAG, target.Schedule, &report)
	if err != nil || unresolved {
		return false, err
	}
	return true, d.Store.ClearSummary(ctx, summary.ID)
}
