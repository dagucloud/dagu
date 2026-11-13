// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ErrEnded is returned for an operation that needs an open backfill.
var ErrEnded = errors.New("the backfill has ended")

// RunControl reaches the runs of a backfill.
type RunControl struct {
	// ReadRun returns a run's latest status, or an error wrapping
	// dagrun.ErrDAGRunIDNotFound when the run is gone.
	ReadRun func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error)
	// Withdraw removes a queued run from its queue.
	Withdraw func(ctx context.Context, ref ir.DAGRunRef) error
	// Stop stops an active run.
	Stop func(ctx context.Context, ref ir.DAGRunRef) error
}

// Cancel ends an open backfill. Its queued runs are withdrawn, so their slots
// count as missing again, and the slots it had not reached are cancelled.
// Active runs finish unless stopRunning.
func (s *Service) Cancel(ctx context.Context, id string, control RunControl, stopRunning bool) (*Record, error) {
	record, unlock, err := s.lockRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if !record.Open() {
		return record, ErrEnded
	}
	if _, err := s.withdraw(ctx, record, control, stopRunning); err != nil {
		return nil, err
	}
	if err := s.Store.End(ctx, record.ID, StatusCancelled, s.now()); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, record.ID)
}

// ResumeRequest re-plans a backfill.
type ResumeRequest struct {
	ID string
	// DAG is the DAG's current definition, which the resumed slots run.
	DAG      *ir.DAG
	Schedule SlotSchedule
	// MaxConcurrency and OnFailure, when set, replace the backfill's.
	MaxConcurrency int
	OnFailure      OnFailure
	DryRun         bool
	Revision       string
	MaxSlots       int
}

// Resume runs again the slots of a backfill that have not succeeded, with
// the DAG's current definition. On an open backfill it first withdraws the
// queued runs and lets active ones finish. The resumed slots get new runs.
func (s *Service) Resume(ctx context.Context, req ResumeRequest, control RunControl) (SubmitResult, error) {
	record, unlock, err := s.lockRecord(ctx, req.ID)
	if err != nil {
		return SubmitResult{}, err
	}
	defer unlock()
	// A retry can queue a slot again, and open an ended backfill, while the
	// resume plans: the resume then writes only over what it read.
	read := *record
	progress, err := s.Store.Progress(ctx, record.ID)
	if err != nil {
		return SubmitResult{}, err
	}
	seen := len(progress)

	report, err := Describe(ctx, s.Store, control.ReadRun, record)
	if err != nil {
		return SubmitResult{}, err
	}
	var remaining []time.Time
	var carried []SlotReport
	withdrawn := make(map[string]bool)
	for _, slot := range report.Slots {
		switch slot.State {
		case SlotStateSucceeded, SlotStateSkipped:
		case SlotStateQueued:
			withdrawn[slot.RunID] = true
			remaining = append(remaining, slot.At)
		case SlotStateRunning, SlotStateWaiting:
			// The resumed backfill keeps following an active run, so it
			// counts against the concurrency and decides how the slot ends.
			carried = append(carried, slot)
			remaining = append(remaining, slot.At)
		case SlotStatePending, SlotStateFailed, SlotStateNotRun, SlotStateCancelled:
			remaining = append(remaining, slot.At)
		}
	}
	plan := PlanRequest{
		DAG:      req.DAG,
		Schedule: req.Schedule,
		Slots:    remaining,
		Rerun:    RerunFailed,
		Resume:   true,
		MaxSlots: req.MaxSlots,
		Now:      s.now(),
	}
	// A slot an active run of this backfill holds plans as active. Its
	// queued runs are left out, as they are withdrawn before the new runs
	// are queued.
	pending, err := s.pendingSlots(ctx, req.DAG.Name, record.ID)
	if err != nil {
		return SubmitResult{}, err
	}
	// A carried run holds its slot, also while its history still shows how
	// it ended before a retry that is about to start it.
	for _, slot := range carried {
		pending[slot.At.UTC()] = true
	}
	result := SubmitResult{}
	if len(remaining) > 0 {
		if result.Plan, err = s.plan(ctx, plan, pending, withdrawn); err != nil {
			return SubmitResult{}, err
		}
	}
	if req.DryRun {
		return result, nil
	}
	if req.Revision != "" && req.Revision != result.Plan.Revision {
		return result, ErrStalePlan
	}
	runs := result.Plan.Runs()
	if len(runs) == 0 {
		return result, nil
	}
	if record.Open() {
		withdrawn, err := s.withdraw(ctx, record, control, false)
		if err != nil {
			return SubmitResult{}, err
		}
		seen += withdrawn
	}

	windows := make(map[time.Time]ir.ScheduleWindow, len(record.Slots))
	for _, slot := range record.Slots {
		windows[slot.At.UTC()] = slot.Window
	}
	record.Generation++
	record.Definition, record.Labels = req.DAG.YamlData, req.DAG.Labels
	record.PlannedAt = plan.Now.UTC()
	record.Revision = result.Plan.Revision
	record.Status, record.EndedAt = "", time.Time{}
	record.Slots = record.Slots[:0]
	planned := make(map[time.Time]bool, len(runs))
	for _, slot := range runs {
		record.Slots = append(record.Slots, RecordSlot{At: slot.At.UTC(), Window: slot.Window})
		planned[slot.At.UTC()] = true
	}
	for _, slot := range carried {
		if planned[slot.At.UTC()] {
			continue
		}
		record.Slots = append(record.Slots, RecordSlot{At: slot.At.UTC(), Window: windows[slot.At.UTC()], RunID: slot.RunID})
	}
	slices.SortFunc(record.Slots, func(a, b RecordSlot) int { return a.At.Compare(b.At) })
	if req.MaxConcurrency > 0 {
		record.Options.MaxConcurrency = req.MaxConcurrency
	}
	if req.OnFailure != "" {
		record.Options.OnFailure = req.OnFailure
	}
	if err := s.updateUnchanged(ctx, &read, seen, record); err != nil {
		return result, err
	}
	result.Record = record
	return result, nil
}

// updateUnchanged rewrites a record, provided the backfill still has the
// status, end, and generation of read and seen progress events. Otherwise
// it fails with ErrStalePlan. The record lock orders it with a retry that
// queues a slot again or opens the backfill.
func (s *Service) updateUnchanged(ctx context.Context, read *Record, seen int, record *Record) error {
	unlock, err := s.Store.LockRecord(ctx, record.ID)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.Store.Get(ctx, record.ID)
	if err != nil {
		return err
	}
	progress, err := s.Store.Progress(ctx, record.ID)
	if err != nil {
		return err
	}
	if current.Status != read.Status || !current.EndedAt.Equal(read.EndedAt) ||
		current.Generation != read.Generation || len(progress) != seen {
		return ErrStalePlan
	}
	if err := s.Store.Update(ctx, record); err != nil {
		return fmt.Errorf("record the resumed backfill: %w", err)
	}
	return nil
}

// withdraw removes the queued runs of an open backfill and cancels the
// slots it has not reached; with stopRunning, it also stops active runs. It
// returns the number of progress events it appended.
func (s *Service) withdraw(ctx context.Context, record *Record, control RunControl, stopRunning bool) (int, error) {
	report, err := Describe(ctx, s.Store, control.ReadRun, record)
	if err != nil {
		return 0, err
	}
	var events []Progress
	now := s.now()
	// failed records the slots already withdrawn before it reports err, so
	// a retry finds them cancelled rather than failed.
	failed := func(err error) error {
		if len(events) == 0 {
			return err
		}
		return errors.Join(err, s.Store.Append(ctx, record.ID, events...))
	}
	for _, slot := range report.Slots {
		ref := ir.NewDAGRunRef(record.DAGName, slot.RunID)
		switch slot.State {
		case SlotStateQueued:
			if err := control.Withdraw(ctx, ref); err != nil {
				return 0, failed(fmt.Errorf("withdraw run %s: %w", slot.RunID, err))
			}
		case SlotStateRunning, SlotStateWaiting:
			if stopRunning {
				if err := control.Stop(ctx, ref); err != nil {
					return 0, failed(fmt.Errorf("stop run %s: %w", slot.RunID, err))
				}
			}
			continue
		case SlotStatePending:
		case SlotStateSucceeded, SlotStateFailed, SlotStateSkipped, SlotStateNotRun, SlotStateCancelled:
			continue
		}
		events = append(events, Progress{Slot: slot.At, Kind: ProgressCancelled, Generation: record.Generation, At: now})
	}
	if len(events) == 0 {
		return 0, nil
	}
	return len(events), s.Store.Append(ctx, record.ID, events...)
}

// lockRecord reads a backfill under its DAG's lock.
func (s *Service) lockRecord(ctx context.Context, id string) (*Record, func(), error) {
	record, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := s.Store.LockDAG(ctx, record.DAGName)
	if err != nil {
		return nil, nil, err
	}
	// The record may have changed while the lock was awaited.
	if record, err = s.Store.Get(ctx, id); err != nil {
		unlock()
		return nil, nil, err
	}
	return record, unlock, nil
}

// pendingSlots returns the slots that open backfills of a DAG, other than
// one, have yet to run.
func (s *Service) pendingSlots(ctx context.Context, dagName, except string) (map[time.Time]bool, error) {
	records, err := s.Store.Open(ctx)
	if err != nil {
		return nil, err
	}
	pending := make(map[time.Time]bool)
	for _, record := range records {
		if record.DAGName != dagName || record.ID == except {
			continue
		}
		for _, slot := range record.Slots {
			pending[slot.At.UTC()] = true
		}
	}
	return pending, nil
}
