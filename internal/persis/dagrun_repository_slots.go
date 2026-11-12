// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// maxSlotTime bounds a read of every slot of a DAG.
var maxSlotTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// SlotEvents returns the slot events of a DAG whose slots fall in [from, to].
// The first call for a DAG adds the runs already in its history whose slots
// are within its slot retention.
func (r *DAGRunRepository) SlotEvents(ctx context.Context, dag *ir.DAG, from, to time.Time) ([]SlotEvent, error) {
	if r.slotIndex == nil {
		return nil, nil
	}
	imported, err := r.slotIndex.Imported(ctx, dag.Name)
	if err != nil {
		return nil, err
	}
	if !imported {
		if err := r.importSlots(ctx, dag); err != nil {
			return nil, fmt.Errorf("add the run history of %s to the slot index: %w", dag.Name, err)
		}
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

// importSlots adds a run event for every run in the DAG's history that has a
// slot within the slot retention and no event yet.
func (r *DAGRunRepository) importSlots(ctx context.Context, dag *ir.DAG) error {
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = r.slotCutoff(dag.SlotRetentionDays)
	}
	// Skips reading the snapshot of a run already recorded; Import drops
	// those again under the index's lock.
	recorded, err := r.slotIndex.Events(ctx, dag.Name, cutoff, maxSlotTime)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(recorded))
	for _, event := range recorded {
		known[event.RunID] = true
	}
	statuses, err := r.ListStatuses(ctx, DAGRunListOptions{
		ExactName:  dag.Name,
		AllHistory: true,
		Unbounded:  true,
	})
	if err != nil {
		return err
	}
	var events []SlotEvent
	for _, status := range statuses {
		slot, ok := slotOfStatus(status)
		if !ok || slot.Before(cutoff) || known[status.DAGRunID] {
			continue
		}
		event, _ := slotRunEvent(r.runDAG(ctx, status), r.location, status)
		events = append(events, event)
	}
	return r.slotIndex.Import(ctx, dag.Name, events)
}

// runDAG returns the DAG snapshot a run was created with, or nil when it
// cannot be read. The snapshot names the schedule that fired the run, which
// the current definition may no longer have.
func (r *DAGRunRepository) runDAG(ctx context.Context, status *ir.DAGRunStatus) *ir.DAG {
	attempt, err := r.FindAttempt(ctx, ir.NewDAGRunRef(status.Name, status.DAGRunID))
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
// and is not a run of its own slot.
func slotRunEvent(dag *ir.DAG, loc *time.Location, status *ir.DAGRunStatus) (SlotEvent, bool) {
	slot, ok := slotOfStatus(status)
	if !ok {
		return SlotEvent{}, false
	}
	if dag == nil {
		dag = &ir.DAG{}
	}
	return SlotEvent{
		Kind:        SlotEventRun,
		DAGName:     status.Name,
		Slot:        slot,
		RunID:       status.DAGRunID,
		TriggerType: status.TriggerType,
		Schedule:    ir.FiringScheduleExpression(dag.Schedule, status.ProfileName, loc, slot),
	}, true
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
// that schedules without their own zone fire in.
type slotRecorder struct {
	index    SlotIndex
	location *time.Location
}

// recordSlotRemoval records, before a run is removed, what the removal means
// for its slot: retention keeps the run's final status, and an operator's
// delete drops the run. The event is written first so a removal never loses
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
	if !drop {
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
	event, ok := slotRunEvent(a.dag, a.slots.location, status)
	if !ok {
		a.slots = nil
		return
	}
	if err := a.slots.index.Record(ctx, event); err != nil {
		logger.Warn(ctx, "Failed to record a run in its schedule slot",
			tag.RunID(status.DAGRunID), tag.Error(err))
		return
	}
	a.slots = nil
}
