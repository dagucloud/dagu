// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// SlotState is where one slot of a backfill stands.
type SlotState string

const (
	SlotStatePending   SlotState = "pending"
	SlotStateQueued    SlotState = "queued"
	SlotStateRunning   SlotState = "running"
	SlotStateWaiting   SlotState = "waiting"
	SlotStateSucceeded SlotState = "succeeded"
	SlotStateFailed    SlotState = "failed"
	SlotStateSkipped   SlotState = "skipped"
	SlotStateNotRun    SlotState = "not run"
)

// SlotStateOrder lists every SlotState in the order a report shows them.
var SlotStateOrder = []SlotState{
	SlotStatePending, SlotStateQueued, SlotStateRunning, SlotStateWaiting,
	SlotStateSucceeded, SlotStateFailed, SlotStateSkipped, SlotStateNotRun,
}

// SlotReport is one slot of a backfill and its run.
type SlotReport struct {
	At    time.Time `json:"slot"`
	State SlotState `json:"state"`
	RunID string    `json:"runId,omitempty"`
}

// Report is where a backfill stands.
type Report struct {
	Record *Record
	Slots  []SlotReport
	Counts map[SlotState]int
}

// Describe reports each slot of a backfill from its progress and runs.
// readRun returns a run's latest status, or an error wrapping
// dagrun.ErrDAGRunIDNotFound when the run is gone.
func Describe(ctx context.Context, store Store, readRun func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error), record *Record) (Report, error) {
	progress, err := store.Progress(ctx, record.ID)
	if err != nil {
		return Report{}, err
	}
	byslot := make(map[time.Time]Progress, len(progress))
	for _, event := range progress {
		byslot[event.Slot.UTC()] = event
	}
	report := Report{Record: record, Counts: make(map[SlotState]int)}
	for _, slot := range record.Slots {
		event := byslot[slot.At.UTC()]
		item := SlotReport{At: slot.At, State: SlotStatePending, RunID: event.RunID}
		switch event.Kind {
		case ProgressSkipped:
			item.State = SlotStateSkipped
		case ProgressNotRun:
			item.State = SlotStateNotRun
		case ProgressEnqueued:
			if item.State, err = runState(ctx, readRun, record.DAGName, event.RunID); err != nil {
				return Report{}, err
			}
		}
		report.Slots = append(report.Slots, item)
		report.Counts[item.State]++
	}
	return report, nil
}

func runState(ctx context.Context, readRun func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error), dagName, runID string) (SlotState, error) {
	status, err := readRun(ctx, ir.NewDAGRunRef(dagName, runID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return SlotStateFailed, nil
	}
	if err != nil {
		return "", err
	}
	switch {
	case status.Status == ir.Queued:
		return SlotStateQueued, nil
	case status.Status.IsWaiting():
		return SlotStateWaiting, nil
	case status.Status == ir.Running:
		return SlotStateRunning, nil
	case status.Status.IsSuccess():
		return SlotStateSucceeded, nil
	default:
		return SlotStateFailed, nil
	}
}
