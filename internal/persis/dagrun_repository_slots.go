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
// The first call for a DAG adds the runs already in its history.
func (r *DAGRunRepository) SlotEvents(ctx context.Context, dagName string, from, to time.Time) ([]SlotEvent, error) {
	if r.slotIndex == nil {
		return nil, nil
	}
	imported, err := r.slotIndex.Imported(ctx, dagName)
	if err != nil {
		return nil, err
	}
	if !imported {
		if err := r.importSlots(ctx, dagName); err != nil {
			return nil, fmt.Errorf("add the run history of %s to the slot index: %w", dagName, err)
		}
	}
	return r.slotIndex.Events(ctx, dagName, from, to)
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
	return r.slotIndex.Prune(ctx, dagName, r.now().AddDate(0, 0, -retentionDays))
}

// importSlots records a run event for every run in the DAG's history that
// has a slot and is not recorded yet. Two concurrent imports can record a run
// twice; readers count each run once.
func (r *DAGRunRepository) importSlots(ctx context.Context, dagName string) error {
	recorded, err := r.slotIndex.Events(ctx, dagName, time.Time{}, maxSlotTime)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(recorded))
	for _, event := range recorded {
		known[event.RunID] = true
	}
	statuses, err := r.ListStatuses(ctx, DAGRunListOptions{
		ExactName:  dagName,
		AllHistory: true,
		Unbounded:  true,
	})
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if _, ok := slotOfStatus(status); !ok || known[status.DAGRunID] {
			continue
		}
		event, _ := slotRunEvent(r.runDAG(ctx, status), r.location, status)
		if err := r.slotIndex.Record(ctx, event); err != nil {
			return err
		}
	}
	return r.slotIndex.MarkImported(ctx, dagName)
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
		Schedule:    ir.FiringScheduleExpression(dag.Schedule, loc, slot),
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

// slotRemoval records what removing a run means for its slot.
type slotRemoval struct {
	index SlotIndex
	// dropped is recorded once the run is removed.
	dropped *SlotEvent
}

// prepareSlotRemoval runs before a run is removed. Retention records the run's
// final status first, so the slot keeps it whether or not the removal
// completes. An operator's delete makes the run stop counting for its slot
// once the removal succeeds.
func (r *DAGRunRepository) prepareSlotRemoval(ctx context.Context, ref ir.DAGRunRef, drop bool) slotRemoval {
	if r.slotIndex == nil {
		return slotRemoval{}
	}
	status, err := r.latestStatus(ctx, ref)
	if err != nil {
		if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) && !errors.Is(err, dagrun.ErrNoStatusData) {
			logger.Warn(ctx, "Failed to read a run before removing it from its slot",
				tag.RunID(ref.ID), tag.Error(err))
		}
		return slotRemoval{}
	}
	slot, ok := slotOfStatus(status)
	if !ok {
		return slotRemoval{}
	}
	event := SlotEvent{DAGName: status.Name, Slot: slot, RunID: status.DAGRunID, TriggerType: status.TriggerType}
	if drop {
		event.Kind = SlotEventDropped
		return slotRemoval{index: r.slotIndex, dropped: &event}
	}
	event.Kind = SlotEventFinal
	event.Status = status.Status
	if err := r.slotIndex.Record(ctx, event); err != nil {
		logger.Warn(ctx, "Failed to record the final status of a run in its slot",
			tag.RunID(ref.ID), tag.Error(err))
	}
	return slotRemoval{}
}

// done records the drop of an operator's delete after the run is removed.
func (s slotRemoval) done(ctx context.Context) {
	if s.dropped == nil {
		return
	}
	if err := s.index.Record(ctx, *s.dropped); err != nil {
		logger.Warn(ctx, "Failed to drop a deleted run from its slot",
			tag.RunID(s.dropped.RunID), tag.Error(err))
	}
}

func (r *DAGRunRepository) latestStatus(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := r.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(ctx)
}

// recordSlotRun records the run event of a new top-level run on its first
// status write that carries a scheduled time.
func (a *eventingAttempt) recordSlotRun(ctx context.Context, status *ir.DAGRunStatus) {
	if a.slots == nil || status.ScheduleTime == "" {
		return
	}
	slots := a.slots
	a.slots = nil
	event, ok := slotRunEvent(a.dag, slots.location, status)
	if !ok {
		return
	}
	if err := slots.index.Record(ctx, event); err != nil {
		logger.Warn(ctx, "Failed to record a run in its schedule slot",
			tag.RunID(status.DAGRunID), tag.Error(err))
	}
}
