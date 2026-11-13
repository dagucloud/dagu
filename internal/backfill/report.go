// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"cmp"
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
	SlotStateCancelled SlotState = "cancelled"
)

// SlotStateOrder lists every SlotState in the order a report shows them.
var SlotStateOrder = []SlotState{
	SlotStatePending, SlotStateQueued, SlotStateRunning, SlotStateWaiting,
	SlotStateSucceeded, SlotStateFailed, SlotStateSkipped, SlotStateNotRun, SlotStateCancelled,
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
	// queue is the queue of the backfill's oldest queued run.
	queue string
}

// Describe reports each slot of a backfill from its progress and runs.
// readRun returns a run's latest status, or an error wrapping
// dagrun.ErrDAGRunIDNotFound when the run is gone. A run that a retry has
// queued again but not yet started reads as running.
func Describe(ctx context.Context, store Store, readRun func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error), record *Record) (Report, error) {
	progress, err := store.Progress(ctx, record.ID)
	if err != nil {
		return Report{}, err
	}
	byslot := currentProgress(record, progress)
	now := time.Now()
	report := Report{Record: record, Counts: make(map[SlotState]int)}
	for _, slot := range record.Slots {
		event := byslot[slot.At.UTC()]
		item := SlotReport{At: slot.At, State: SlotStatePending, RunID: event.RunID}
		switch event.Kind {
		case ProgressSkipped:
			item.State = SlotStateSkipped
		case ProgressNotRun:
			item.State = SlotStateNotRun
		case ProgressCancelled:
			item.State = SlotStateCancelled
		case ProgressSucceeded:
			item.State = SlotStateSucceeded
		case ProgressFailed:
			item.State = SlotStateFailed
		case ProgressEnqueued:
			var queue string
			if item.State, queue, err = runState(ctx, readRun, record.DAGName, event, now); err != nil {
				return Report{}, err
			}
			if item.State == SlotStateQueued && report.queue == "" {
				report.queue = queue
			}
		}
		report.Slots = append(report.Slots, item)
		report.Counts[item.State]++
	}
	return report, nil
}

// runState returns the state of the run that event queued and the queue it
// runs on.
func runState(ctx context.Context, readRun func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error), dagName string, event Progress, now time.Time) (SlotState, string, error) {
	status, err := readRun(ctx, ir.NewDAGRunRef(dagName, event.RunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return SlotStateFailed, "", nil
	}
	if err != nil {
		return "", "", err
	}
	queue := cmp.Or(status.ProcGroup, dagName)
	switch {
	case status.Status == ir.Queued:
		return SlotStateQueued, queue, nil
	case status.Status.IsWaiting():
		return SlotStateWaiting, queue, nil
	case status.Status == ir.Running, awaitsRetry(event, status, now):
		return SlotStateRunning, queue, nil
	case status.Status.IsSuccess():
		return SlotStateSucceeded, queue, nil
	default:
		return SlotStateFailed, queue, nil
	}
}
