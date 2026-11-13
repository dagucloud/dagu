// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// RunReader returns a run's latest status, or an error wrapping
// dagrun.ErrDAGRunIDNotFound when the run is gone.
type RunReader func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error)

// Latest reports the backfill of a DAG an operator should see: its newest
// open backfill, or else its newest backfill that ended failed while a slot
// it failed has not succeeded since. It returns nil when there is none. The
// slots of a failed backfill are read under the profile it ran with.
func Latest(ctx context.Context, store Store, readRun RunReader, history HistoryReader, dag *ir.DAG, sched SlotSchedule) (*Report, error) {
	records, err := store.List(ctx, dag.Name)
	if err != nil {
		return nil, err
	}
	for _, record := range slices.Backward(records) {
		if record.Open() {
			return describe(ctx, store, readRun, record)
		}
	}
	for _, record := range slices.Backward(records) {
		if record.Status != StatusFailed {
			continue
		}
		report, err := describe(ctx, store, readRun, record)
		if err != nil {
			return nil, err
		}
		recordSched := sched
		recordSched.Profile = record.Options.Profile
		unresolved, err := hasUnresolvedFailure(ctx, history, dag, recordSched, report)
		if err != nil {
			return nil, err
		}
		if unresolved {
			return report, nil
		}
	}
	return nil, nil
}

// hasUnresolvedFailure reports whether a slot a backfill failed has not
// succeeded since.
func hasUnresolvedFailure(ctx context.Context, history HistoryReader, dag *ir.DAG, sched SlotSchedule, report *Report) (bool, error) {
	var failed []time.Time
	for _, slot := range report.Slots {
		if slot.State == SlotStateFailed {
			failed = append(failed, slot.At)
		}
	}
	if len(failed) == 0 {
		return false, nil
	}
	past, err := LoadHistory(ctx, history, dag, sched, failed[0], failed[len(failed)-1])
	if err != nil {
		return false, err
	}
	for _, state := range SlotStates(failed, sched, past) {
		if state.Status != SlotSucceeded {
			return true, nil
		}
	}
	return false, nil
}

func describe(ctx context.Context, store Store, readRun RunReader, record *Record) (*Report, error) {
	report, err := Describe(ctx, store, readRun, record)
	if err != nil {
		return nil, err
	}
	return &report, nil
}
