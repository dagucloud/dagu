// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"cmp"
	"context"
	"errors"
	"slices"
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

// Failure is a group of a backfill's failed slots whose runs failed alike:
// at the same step, with the same error.
type Failure struct {
	Step  string `json:"step,omitempty"`
	Error string `json:"error,omitempty"`
	// Slots counts the slots of the group.
	Slots int `json:"slots"`
	// At and RunID are the group's earliest slot and its run.
	At    time.Time `json:"slot"`
	RunID string    `json:"runId"`
}

// Report is where a backfill stands.
type Report struct {
	Record *Record
	Slots  []SlotReport
	Counts map[SlotState]int
	// Estimate is about how long the slots left take, from the pace of the
	// runs that ended since the slots were last planned. It is zero until
	// one has, and once nothing is left.
	Estimate time.Duration
	// Failures groups the failed slots by how their runs failed, the
	// largest group first.
	Failures []Failure
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
			read, readErr := runState(ctx, readRun, record.DAGName, event, now)
			if readErr != nil {
				return Report{}, readErr
			}
			item.State = read.state
			if item.State == SlotStateQueued && report.queue == "" {
				report.queue = read.queue
			}
			// A run that failed since the last tick has no outcome recorded
			// yet: the status just read tells how it failed.
			event.Step, event.Error = read.step, read.err
		}
		report.Slots = append(report.Slots, item)
		report.Counts[item.State]++
		if item.State == SlotStateFailed {
			report.Failures = addFailure(report.Failures, item, event)
		}
	}
	report.Estimate = estimateLeft(record, progress, report.Counts)
	slices.SortStableFunc(report.Failures, func(a, b Failure) int { return cmp.Compare(b.Slots, a.Slots) })
	return report, nil
}

// estimateLeft is the time the slots left take at the pace of the runs that
// ended in the backfill's current generation.
func estimateLeft(record *Record, progress []Progress, counts map[SlotState]int) time.Duration {
	left := counts[SlotStatePending] + counts[SlotStateQueued] + counts[SlotStateRunning] + counts[SlotStateWaiting]
	if !record.Open() || left == 0 {
		return 0
	}
	ended := 0
	var last time.Time
	for _, event := range progress {
		if event.Generation == record.Generation && event.ended() {
			ended++
			if event.At.After(last) {
				last = event.At
			}
		}
	}
	elapsed := last.Sub(record.PlannedAt)
	if ended == 0 || elapsed <= 0 {
		return 0
	}
	return elapsed / time.Duration(ended) * time.Duration(left)
}

// runRead is what a read of a slot's run tells: its state, the queue it
// runs on, and, for a failed run, its failed step and error.
type runRead struct {
	state     SlotState
	queue     string
	step, err string
}

// runState reads the run that event queued.
func runState(ctx context.Context, readRun func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error), dagName string, event Progress, now time.Time) (runRead, error) {
	status, err := readRun(ctx, ir.NewDAGRunRef(dagName, event.RunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return runRead{state: SlotStateFailed}, nil
	}
	if err != nil {
		return runRead{}, err
	}
	read := runRead{queue: cmp.Or(status.ProcGroup, dagName)}
	switch {
	case status.Status == ir.Queued:
		read.state = SlotStateQueued
	case status.Status.IsWaiting():
		read.state = SlotStateWaiting
	case status.Status == ir.Running, awaitsRetry(event, status, now):
		read.state = SlotStateRunning
	case status.Status.IsSuccess():
		read.state = SlotStateSucceeded
	default:
		read.state = SlotStateFailed
		read.step, read.err = runFailure(status)
	}
	return read, nil
}

// addFailure counts a failed slot in the group of its step and error, which
// event recorded when the slot's run failed.
func addFailure(failures []Failure, slot SlotReport, event Progress) []Failure {
	for i := range failures {
		if failures[i].Step == event.Step && failures[i].Error == event.Error {
			failures[i].Slots++
			return failures
		}
	}
	return append(failures, Failure{Step: event.Step, Error: event.Error, Slots: 1, At: slot.At, RunID: slot.RunID})
}
