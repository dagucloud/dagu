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
}

// summaryCheckInterval is how often the dispatcher checks whether the
// failures of a failed backfill were resolved since.
const summaryCheckInterval = time.Minute

// clearResolved clears the summary of each failed backfill whose failed
// slots have all succeeded since, or whose DAG is gone, at most once per
// summaryCheckInterval.
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
	var errs []error
	for _, summary := range summaries {
		if summary.Status != StatusFailed {
			continue
		}
		if err := d.clearIfResolved(ctx, summary); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: %w", summary.ID, err))
		}
	}
	return errors.Join(errs...)
}

// clearIfResolved clears a failed backfill's summary under its DAG's lock,
// which a resume that opens it again also holds.
func (d *Dispatcher) clearIfResolved(ctx context.Context, summary Summary) error {
	unlock, err := d.Store.LockDAG(ctx, summary.DAGName)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := d.Store.Get(ctx, summary.ID)
	if errors.Is(err, ErrNotFound) {
		return d.Store.ClearSummary(ctx, summary.ID)
	}
	if err != nil {
		return err
	}
	if record.Status != StatusFailed {
		return nil
	}
	if d.DAGExists != nil {
		exists, err := d.DAGExists(ctx, record)
		if err != nil {
			return err
		}
		if !exists {
			return d.Store.ClearSummary(ctx, summary.ID)
		}
	}
	target, err := d.Target(ctx, record)
	if err != nil {
		return err
	}
	report, err := Describe(ctx, d.Store, d.ReadRun, record)
	if err != nil {
		return err
	}
	unresolved, err := hasUnresolvedFailure(ctx, d.History, target.DAG, target.Schedule, &report)
	if err != nil || unresolved {
		return err
	}
	return d.Store.ClearSummary(ctx, summary.ID)
}
