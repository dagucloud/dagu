// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// runIDSlotLayout writes a slot into a run ID.
const runIDSlotLayout = "20060102T150405Z"

// RunID returns the ID of the run a backfill queues for a slot. It is the
// same on every tick, so a dispatcher that crashed after queueing a run
// finds it again.
func RunID(record *Record, slot time.Time) string {
	return fmt.Sprintf("%s%d-%s", runIDPrefix(record), record.Generation, slot.UTC().Format(runIDSlotLayout))
}

func runIDPrefix(record *Record) string {
	return "backfill-" + record.ID + "-"
}

// ownRun reports whether a run belongs to the backfill.
func ownRun(record *Record, runID string) bool {
	return strings.HasPrefix(runID, runIDPrefix(record))
}

// Target is what a dispatcher needs of a backfill's DAG: its pinned
// definition and how its slots fire.
type Target struct {
	DAG      *ir.DAG
	Schedule SlotSchedule
}

// Dispatcher queues the runs of open backfills, oldest slot first.
type Dispatcher struct {
	Store   Store
	History HistoryReader
	// ReadRun returns a run's latest status, or an error wrapping
	// dagrun.ErrDAGRunIDNotFound when the run is gone.
	ReadRun func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error)
	// Target loads a backfill's DAG from its pinned definition.
	Target func(ctx context.Context, record *Record) (Target, error)
	// Enqueue queues the run of a slot. It returns an error wrapping
	// dagrun.ErrDAGRunAlreadyExists when the run is already queued.
	Enqueue func(ctx context.Context, record *Record, target Target, slot RecordSlot, runID string) error
	Now     func() time.Time
}

// Tick advances every open backfill: it queues the next slots while fewer
// runs than the backfill's concurrency are active, and ends the backfill
// once every slot has settled.
func (d *Dispatcher) Tick(ctx context.Context) error {
	records, err := d.Store.Open(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, record := range records {
		if err := d.advance(ctx, record); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: %w", record.ID, err))
		}
	}
	return errors.Join(errs...)
}

// slotOutcome is where a slot of a backfill stands.
type slotOutcome int

const (
	slotPending slotOutcome = iota
	slotActive
	slotSucceeded
	slotFailed
	slotSettled
)

func (d *Dispatcher) advance(ctx context.Context, record *Record) error {
	progress, err := d.Store.Progress(ctx, record.ID)
	if err != nil {
		return err
	}
	byslot := make(map[time.Time]Progress, len(progress))
	for _, event := range progress {
		byslot[event.Slot.UTC()] = event
	}

	var pending []RecordSlot
	active, failed := 0, false
	for _, slot := range record.Slots {
		outcome, err := d.outcome(ctx, record, byslot[slot.At.UTC()])
		if err != nil {
			return err
		}
		switch outcome {
		case slotPending:
			pending = append(pending, slot)
		case slotActive:
			active++
		case slotFailed:
			failed = true
		case slotSucceeded, slotSettled:
		}
	}

	if failed && record.Options.OnFailure == OnFailureStop {
		if active > 0 {
			return nil
		}
		return d.stop(ctx, record, pending)
	}
	if len(pending) > 0 && active < record.Options.MaxConcurrency {
		target, err := d.Target(ctx, record)
		if err != nil {
			return err
		}
		for len(pending) > 0 && active < record.Options.MaxConcurrency {
			slot := pending[0]
			pending = pending[1:]
			queued, err := d.dispatch(ctx, record, target, slot)
			if err != nil {
				return err
			}
			if queued {
				active++
			}
		}
	}
	if len(pending) == 0 && active == 0 {
		status := StatusSucceeded
		if failed {
			status = StatusFailed
		}
		return d.Store.End(ctx, record.ID, status, d.now())
	}
	return nil
}

// outcome reads where a slot stands from its last progress event and, once
// queued, from its run.
func (d *Dispatcher) outcome(ctx context.Context, record *Record, event Progress) (slotOutcome, error) {
	switch event.Kind {
	case ProgressSkipped, ProgressNotRun:
		return slotSettled, nil
	case ProgressEnqueued:
	default:
		return slotPending, nil
	}
	status, err := d.ReadRun(ctx, ir.NewDAGRunRef(record.DAGName, event.RunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		// A run removed before it succeeded did not load its slot.
		return slotFailed, nil
	}
	if err != nil {
		return slotPending, err
	}
	switch {
	case status.Status.IsActive():
		return slotActive, nil
	case status.Status.IsSuccess():
		return slotSucceeded, nil
	default:
		return slotFailed, nil
	}
}

// dispatch queues the run of a slot unless the recheck skips it, and
// reports whether a run is now active for the slot.
func (d *Dispatcher) dispatch(ctx context.Context, record *Record, target Target, slot RecordSlot) (bool, error) {
	skip, err := d.recheck(ctx, record, target, slot)
	if err != nil {
		return false, err
	}
	if skip {
		return false, d.Store.Append(ctx, record.ID, Progress{Slot: slot.At, Kind: ProgressSkipped, At: d.now()})
	}
	runID := RunID(record, slot.At)
	if err := d.Enqueue(ctx, record, target, slot, runID); err != nil && !errors.Is(err, dagrun.ErrDAGRunAlreadyExists) {
		return false, fmt.Errorf("queue the run of %s: %w", slot.At.Format(time.RFC3339), err)
	}
	return true, d.Store.Append(ctx, record.ID, Progress{Slot: slot.At, Kind: ProgressEnqueued, RunID: runID, At: d.now()})
}

// recheck reports whether a slot gained a run since the backfill was
// planned that its --rerun now skips, or has another run active.
func (d *Dispatcher) recheck(ctx context.Context, record *Record, target Target, slot RecordSlot) (bool, error) {
	history, err := LoadHistory(ctx, d.History, target.DAG, target.Schedule, slot.At, slot.At)
	if err != nil {
		return false, err
	}
	state := SlotStates([]time.Time{slot.At}, target.Schedule, history)[0]
	if state.RunID == "" || ownRun(record, state.RunID) {
		return false, nil
	}
	if state.Status == SlotActive {
		return true, nil
	}
	if !state.RecordedAt.After(record.CreatedAt) {
		return false, nil
	}
	return actionFor(state.Status, record.Options.Rerun, len(record.Options.Slots) > 0) != ActionRun, nil
}

// stop marks the slots a stopped backfill did not reach and ends it failed.
func (d *Dispatcher) stop(ctx context.Context, record *Record, pending []RecordSlot) error {
	if len(pending) > 0 {
		now := d.now()
		events := make([]Progress, len(pending))
		for i, slot := range pending {
			events[i] = Progress{Slot: slot.At, Kind: ProgressNotRun, At: now}
		}
		if err := d.Store.Append(ctx, record.ID, events...); err != nil {
			return err
		}
	}
	return d.Store.End(ctx, record.ID, StatusFailed, d.now())
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
