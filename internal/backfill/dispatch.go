// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
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
	// DAGExists reports whether a backfill's DAG still has its definition.
	// A backfill whose DAG was renamed or deleted is cancelled. Nil assumes
	// it has.
	DAGExists func(ctx context.Context, record *Record) (bool, error)
	Now       func() time.Time
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
		if err := d.advanceLocked(ctx, record.ID, record.DAGName); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: %w", record.ID, err))
		}
	}
	return errors.Join(errs...)
}

// advanceLocked advances a backfill under its DAG's lock, which a submit,
// a cancel, and a resume also hold, and rereads the record under it.
func (d *Dispatcher) advanceLocked(ctx context.Context, id, dagName string) error {
	unlock, err := d.Store.LockDAG(ctx, dagName)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !record.Open() {
		return nil
	}
	return d.advance(ctx, record)
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
	observed := d.now()
	progress, err := d.Store.Progress(ctx, record.ID)
	if err != nil {
		return err
	}
	byslot := currentProgress(record, progress)

	var (
		pending []RecordSlot
		ended   []Progress
	)
	active, failed := 0, false
	for _, slot := range record.Slots {
		event := byslot[slot.At.UTC()]
		outcome, err := d.outcome(ctx, record, event, observed)
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
		if event.Kind == ProgressEnqueued && (outcome == slotSucceeded || outcome == slotFailed) {
			kind := ProgressSucceeded
			if outcome == slotFailed {
				kind = ProgressFailed
			}
			ended = append(ended, Progress{Slot: slot.At, Kind: kind, RunID: event.RunID, Generation: record.Generation, At: observed})
		}
	}
	// Recording how runs ended spares later reads from looking them up. A
	// retry can queue a slot again while this tick runs; the tick then
	// leaves the backfill to the next one, which reads the retry.
	if len(ended) > 0 {
		recorded, err := d.appendUnchanged(ctx, record, len(progress), ended, nil)
		if err != nil || !recorded {
			return err
		}
	}

	if d.DAGExists != nil && len(pending) > 0 {
		exists, err := d.DAGExists(ctx, record)
		if err != nil {
			return err
		}
		if !exists {
			return d.cancelRemaining(ctx, record, pending)
		}
	}
	seen := len(progress) + len(ended)
	if failed && record.Options.OnFailure == OnFailureStop {
		if active > 0 {
			return nil
		}
		return d.finish(ctx, record, seen, StatusFailed, pending)
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
		return d.finish(ctx, record, seen, status, nil)
	}
	return nil
}

// finish ends the backfill with status, after marking the slots it did not
// reach as not run, unless a retry queued a slot again since the tick read
// the progress.
func (d *Dispatcher) finish(ctx context.Context, record *Record, seen int, status Status, notRun []RecordSlot) error {
	now := d.now()
	events := make([]Progress, len(notRun))
	for i, slot := range notRun {
		events[i] = Progress{Slot: slot.At, Kind: ProgressNotRun, Generation: record.Generation, At: now}
	}
	_, err := d.appendUnchanged(ctx, record, seen, events, func() error {
		return d.Store.End(ctx, record.ID, status, now)
	})
	return err
}

// appendUnchanged appends events to the backfill's progress and, with end
// set, ends the backfill, provided the progress still holds the seen events
// only. It reports whether it did. The record lock orders it with a retry
// that queues a slot again: the retry's event follows what was appended
// here, or keeps anything from being appended.
func (d *Dispatcher) appendUnchanged(ctx context.Context, record *Record, seen int, events []Progress, end func() error) (bool, error) {
	unlock, err := d.Store.LockRecord(ctx, record.ID)
	if err != nil {
		return false, err
	}
	defer unlock()
	progress, err := d.Store.Progress(ctx, record.ID)
	if err != nil || len(progress) != seen {
		return false, err
	}
	if len(events) > 0 {
		if err := d.Store.Append(ctx, record.ID, events...); err != nil {
			return false, err
		}
	}
	if end != nil {
		if err := end(); err != nil {
			return false, err
		}
	}
	return true, nil
}

// retryStartGrace bounds how long a run that a retry queued again may stay
// ended before the retry makes it active. Past it, the retry is taken to
// have failed to start.
const retryStartGrace = time.Minute

// awaitsRetry reports whether a retry queued the slot's ended run again and
// has not made it active yet. A retry queues the slot just before it does.
func awaitsRetry(event Progress, status *ir.DAGRunStatus, now time.Time) bool {
	finished, err := stringutil.ParseTime(status.FinishedAt)
	return err == nil && !finished.IsZero() && finished.Before(event.At) && now.Sub(event.At) < retryStartGrace
}

// outcome reads where a slot stands from its last progress event and, while
// its run has not been seen to end, from its run.
func (d *Dispatcher) outcome(ctx context.Context, record *Record, event Progress, now time.Time) (slotOutcome, error) {
	switch event.Kind {
	case ProgressSkipped, ProgressCancelled:
		return slotSettled, nil
	case ProgressNotRun:
		// A slot that a stopped backfill did not reach did not succeed,
		// also once a retry opens the backfill again.
		return slotFailed, nil
	case ProgressSucceeded:
		return slotSucceeded, nil
	case ProgressFailed:
		return slotFailed, nil
	case ProgressEnqueued:
	default:
		return slotPending, nil
	}
	status, err := d.ReadRun(ctx, ir.NewDAGRunRef(record.DAGName, event.RunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return d.removedOutcome(ctx, record, event)
	}
	if err != nil {
		return slotPending, err
	}
	switch {
	case status.Status.IsActive(), awaitsRetry(event, status, now):
		return slotActive, nil
	case status.Status.IsSuccess():
		return slotSucceeded, nil
	default:
		return slotFailed, nil
	}
}

// removedOutcome reads how a run that is gone ended from the status history
// retention kept for its slot. A run removed otherwise did not load its slot.
func (d *Dispatcher) removedOutcome(ctx context.Context, record *Record, event Progress) (slotOutcome, error) {
	target, err := d.Target(ctx, record)
	if err != nil {
		return slotPending, err
	}
	events, err := d.History.SlotEvents(ctx, target.DAG, event.Slot, event.Slot)
	if err != nil {
		return slotPending, err
	}
	for _, slotEvent := range events {
		if slotEvent.RunID == event.RunID && slotEvent.Kind == persis.SlotEventFinal && slotEvent.Status.IsSuccess() {
			return slotSucceeded, nil
		}
	}
	return slotFailed, nil
}

// dispatch queues the run of a slot unless the recheck skips it, and
// reports whether a run is now active for the slot.
func (d *Dispatcher) dispatch(ctx context.Context, record *Record, target Target, slot RecordSlot) (bool, error) {
	skip, err := d.recheck(ctx, record, target, slot)
	if err != nil {
		return false, err
	}
	if skip {
		return false, d.Store.Append(ctx, record.ID, Progress{Slot: slot.At, Kind: ProgressSkipped, Generation: record.Generation, At: d.now()})
	}
	runID := RunID(record, slot.At)
	if err := d.Enqueue(ctx, record, target, slot, runID); err != nil && !errors.Is(err, dagrun.ErrDAGRunAlreadyExists) {
		return false, fmt.Errorf("queue the run of %s: %w", slot.At.Format(time.RFC3339), err)
	}
	return true, d.Store.Append(ctx, record.ID, Progress{Slot: slot.At, Kind: ProgressEnqueued, RunID: runID, Generation: record.Generation, At: d.now()})
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
	if !state.RecordedAt.After(record.PlannedAt) {
		return false, nil
	}
	rerun, named := record.Options.Rerun, len(record.Options.Slots) > 0
	if record.Generation > 0 {
		// A resume plans its slots as --rerun failed does.
		rerun, named = RerunFailed, false
	}
	return actionFor(state.Status, rerun, named) != ActionRun, nil
}

// cancelRemaining ends a backfill whose DAG is gone, with the slots it did
// not reach cancelled.
func (d *Dispatcher) cancelRemaining(ctx context.Context, record *Record, pending []RecordSlot) error {
	now := d.now()
	events := make([]Progress, len(pending))
	for i, slot := range pending {
		events[i] = Progress{Slot: slot.At, Kind: ProgressCancelled, Generation: record.Generation, At: now}
	}
	if err := d.Store.Append(ctx, record.ID, events...); err != nil {
		return err
	}
	return d.Store.End(ctx, record.ID, StatusCancelled, now)
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
