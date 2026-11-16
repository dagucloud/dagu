// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// slotImportMargin is how far before the last import a read looks again
// for runs: a run created while that import ran could have had no status
// with a scheduled time yet.
const slotImportMargin = time.Hour

// slotImportBatch is how many runs an import adds to the slot index at a
// time, so a read that ends midway keeps the runs it added.
const slotImportBatch = 1000

// SlotEvents returns the slot events of a DAG whose slots fall in [from, to].
// It first adds the runs created since the last read that the index lacks,
// the whole history on the first read, so a run whose recording failed is
// added then.
func (r *DAGRunRepository) SlotEvents(ctx context.Context, dag *ir.DAG, from, to time.Time) ([]SlotEvent, error) {
	if r.slotIndex == nil {
		return nil, nil
	}
	if err := r.importSlots(ctx, dag); err != nil {
		return nil, fmt.Errorf("add the run history of %s to the slot index: %w", dag.Name, err)
	}
	return r.slotIndex.Events(ctx, dag.Name, from, to)
}

// RecordSkippedSlot records that the scheduler did not run a slot, and why.
func (r *DAGRunRepository) RecordSkippedSlot(ctx context.Context, dagName string, slot time.Time, schedule, reason string) error {
	if r.slotIndex == nil {
		return nil
	}
	return r.slotIndex.Record(ctx, SlotEvent{
		Kind:       SlotEventSkipped,
		DAGName:    dagName,
		Slot:       slot,
		Schedule:   schedule,
		SkipReason: reason,
	})
}

// PruneSlots removes the slot events of a DAG older than retentionDays.
func (r *DAGRunRepository) PruneSlots(ctx context.Context, dagName string, retentionDays int) error {
	if r.slotIndex == nil || retentionDays <= 0 {
		return nil
	}
	return r.slotIndex.Prune(ctx, dagName, r.slotCutoff(retentionDays))
}

func (r *DAGRunRepository) slotCutoff(retentionDays int) time.Time {
	return r.now().AddDate(0, 0, -retentionDays)
}

// slotCandidate is a run that an import adds to the slot index unless the
// index has it. It keeps what the import needs of the run's status, not the
// status itself.
type slotCandidate struct {
	event       SlotEvent
	profile     string
	openAttempt func(context.Context) (dagrun.Attempt, error)
}

// importSlots adds a run event for every run created since the last import
// that has a slot within the slot retention and no event yet.
func (r *DAGRunRepository) importSlots(ctx context.Context, dag *ir.DAG) error {
	through, err := r.slotIndex.ImportedThrough(ctx, dag.Name)
	if err != nil {
		return err
	}
	scanned := r.now()
	var from TimeInUTC
	if !through.IsZero() {
		from = NewUTC(through.Add(-slotImportMargin))
	}
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = r.slotCutoff(dag.SlotRetentionDays)
	}
	var candidates []slotCandidate
	var first, last time.Time
	err = r.store.WalkRuns(ctx, dag.Name, from, func(run ListedDAGRun) error {
		event, ok := slotRunEvent(run.Status)
		if !ok || event.Slot.Before(cutoff) {
			return nil
		}
		candidates = append(candidates, slotCandidate{event: event, profile: run.Status.ProfileName, openAttempt: run.OpenAttempt})
		if first.IsZero() || event.Slot.Before(first) {
			first = event.Slot
		}
		if event.Slot.After(last) {
			last = event.Slot
		}
		return nil
	})
	if err != nil {
		return err
	}
	var events []SlotEvent
	if len(candidates) > 0 {
		// Skips reading the snapshot of a run already recorded; Import
		// drops those again under the index's lock.
		recorded, err := r.slotIndex.Events(ctx, dag.Name, first, last)
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(recorded))
		for _, event := range recorded {
			known[event.RunID] = true
		}
		for _, candidate := range candidates {
			event := candidate.event
			if known[event.RunID] {
				continue
			}
			event.Schedule = firingSchedule(runDAG(ctx, candidate.openAttempt), r.location, r.calendars, candidate.profile, event.Slot)
			events = append(events, event)
			if len(events) == slotImportBatch {
				if err := r.importSlotEvents(ctx, dag.Name, events, time.Time{}); err != nil {
					return err
				}
				events = nil
			}
		}
	}
	return r.importSlotEvents(ctx, dag.Name, events, scanned)
}

// importSlotEvents adds events to the slot index unless ctx has ended. A
// snapshot read that the end of ctx cut short reads as no snapshot, which
// would record its run without its schedule.
func (r *DAGRunRepository) importSlotEvents(ctx context.Context, dagName string, events []SlotEvent, through time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.slotIndex.Import(ctx, dagName, events, through)
}

// runDAG returns the DAG snapshot a run was created with, or nil when it
// cannot be read. The snapshot names the schedule that fired the run, which
// the current definition may no longer have.
func runDAG(ctx context.Context, openAttempt func(context.Context) (dagrun.Attempt, error)) *ir.DAG {
	attempt, err := openAttempt(ctx)
	if err != nil {
		return nil
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil
	}
	return dag
}

// slotRunEvent returns the run event of a status that belongs to a slot: a
// top-level run with a scheduled time. A sub-DAG run takes its parent's slot
// and is not a run of its own slot. The event names no schedule yet.
func slotRunEvent(status *ir.DAGRunStatus) (SlotEvent, bool) {
	slot, ok := slotOfStatus(status)
	if !ok {
		return SlotEvent{}, false
	}
	return SlotEvent{
		Kind:        SlotEventRun,
		DAGName:     status.Name,
		Slot:        slot,
		RunID:       status.DAGRunID,
		TriggerType: status.TriggerType,
	}, true
}

// firingSchedule returns the expression of the recurring schedule of dag,
// active for profile, that fires at slot, or empty when none does.
// Calendars bind the business calendars of the DAG's schedules, which a
// snapshot read back from history lacks.
func firingSchedule(dag *ir.DAG, loc *time.Location, calendars *spec.CalendarSource, profile string, slot time.Time) string {
	var schedules []ir.Schedule
	if dag != nil {
		schedules = dag.Schedule
	}
	if calendars != nil && slices.ContainsFunc(schedules, ir.Schedule.HasCalendar) {
		schedules = slices.Clone(schedules)
		spec.BindScheduleCalendars(schedules, calendars)
	}
	return ir.FiringScheduleExpression(schedules, profile, loc, slot)
}

func slotOfStatus(status *ir.DAGRunStatus) (time.Time, bool) {
	if status == nil || status.ScheduleTime == "" || status.Ephemeral || status.TriggerType == ir.TriggerTypeSubDAG {
		return time.Time{}, false
	}
	if root := status.Root; !root.Zero() && (root.Name != status.Name || root.ID != status.DAGRunID) {
		return time.Time{}, false
	}
	slot, err := stringutil.ParseTime(status.ScheduleTime)
	if err != nil {
		return time.Time{}, false
	}
	return slot.UTC(), true
}

// slotRecorder records a new run in its slot. Location is the server zone
// that schedules without their own zone fire in, and calendars binds their
// business calendars.
type slotRecorder struct {
	index     SlotIndex
	location  *time.Location
	calendars *spec.CalendarSource
}

// recordSlotRemoval records, before a run is removed, what the removal means
// for its slot: retention keeps the run's final status, and an operator's
// delete drops the run. A run that never started is dropped either way, as
// it counts for no slot. The event is written first so a removal never loses
// it; a removal that then fails leaves the run, whose own status supersedes
// the event. An error means the run must not be removed.
func (r *DAGRunRepository) recordSlotRemoval(ctx context.Context, ref ir.DAGRunRef, drop, rejectActive bool) error {
	if r.slotIndex == nil {
		return nil
	}
	status, err := r.latestStatus(ctx, ref)
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read dag-run %s before removing it from its slot: %w", ref.ID, err)
	}
	slot, ok := slotOfStatus(status)
	// An active run that the removal will refuse keeps counting as it is.
	if !ok || (rejectActive && status.Status.IsActive()) {
		return nil
	}
	event := SlotEvent{
		Kind:        SlotEventDropped,
		DAGName:     status.Name,
		Slot:        slot,
		RunID:       status.DAGRunID,
		TriggerType: status.TriggerType,
	}
	if !drop && !status.NeverStarted() {
		event.Kind = SlotEventFinal
		event.Status = status.Status
	}
	if err := r.slotIndex.Record(ctx, event); err != nil {
		return fmt.Errorf("record dag-run %s in its slot before removing it: %w", ref.ID, err)
	}
	return nil
}

func (r *DAGRunRepository) latestStatus(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := r.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(ctx)
}

// recordSlotRun records the run event of a new top-level run on the first
// status write that carries a scheduled time. A failed record is tried
// again on the attempt's next write.
func (a *eventingAttempt) recordSlotRun(ctx context.Context, status *ir.DAGRunStatus) {
	if a.slots == nil || status.ScheduleTime == "" {
		return
	}
	event, ok := slotRunEvent(status)
	if !ok {
		a.slots = nil
		return
	}
	event.Schedule = firingSchedule(a.dag, a.slots.location, a.slots.calendars, status.ProfileName, event.Slot)
	if err := a.slots.index.Record(ctx, event); err != nil {
		logger.Warn(ctx, "Failed to record a run in its schedule slot",
			tag.RunID(status.DAGRunID), tag.Error(err))
		return
	}
	a.slots = nil
}
