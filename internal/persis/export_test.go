// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// SlotImportBatch is how many runs an import adds to the slot index at a
// time.
const SlotImportBatch = slotImportBatch

// ImportSlotsByLookup adds a DAG's run history to the slot index by looking
// each run up by its ID to read its snapshot. It is the reference that the
// import reading the listing is compared against.
func (r *DAGRunRepository) ImportSlotsByLookup(ctx context.Context, dag *ir.DAG) error {
	through, err := r.slotIndex.ImportedThrough(ctx, dag.Name)
	if err != nil {
		return err
	}
	scanned := r.now()
	options := DAGRunListOptions{ExactName: dag.Name, AllHistory: true, Unbounded: true}
	if !through.IsZero() {
		options.From = NewUTC(through.Add(-slotImportMargin))
	}
	statuses, err := r.ListStatuses(ctx, options)
	if err != nil {
		return err
	}
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = r.slotCutoff(dag.SlotRetentionDays)
	}
	var candidates []*ir.DAGRunStatus
	var first, last time.Time
	for _, status := range statuses {
		slot, ok := slotOfStatus(status)
		if !ok || slot.Before(cutoff) {
			continue
		}
		candidates = append(candidates, status)
		if first.IsZero() || slot.Before(first) {
			first = slot
		}
		if slot.After(last) {
			last = slot
		}
	}
	var events []SlotEvent
	if len(candidates) > 0 {
		recorded, err := r.slotIndex.Events(ctx, dag.Name, first, last)
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(recorded))
		for _, event := range recorded {
			known[event.RunID] = true
		}
		for _, status := range candidates {
			if known[status.DAGRunID] {
				continue
			}
			event, _ := slotRunEvent(status)
			event.Schedule = firingSchedule(r.lookUpRunDAG(ctx, status), r.location, r.calendars, status.ProfileName, event.Slot)
			events = append(events, event)
		}
	}
	return r.slotIndex.Import(ctx, dag.Name, events, scanned)
}

func (r *DAGRunRepository) lookUpRunDAG(ctx context.Context, status *ir.DAGRunStatus) *ir.DAG {
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
