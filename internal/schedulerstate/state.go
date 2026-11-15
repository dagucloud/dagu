// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package schedulerstate defines durable scheduler state and its persistence contract.
package schedulerstate

import (
	"context"
	"maps"
	"time"
)

// State holds persistent scheduler watermark state.
type State struct {
	LastTick time.Time
	DAGs     map[string]DAGWatermark
}

// DAGWatermark tracks scheduler state for a single DAG.
type DAGWatermark struct {
	LastScheduledTime        time.Time                      `json:"lastScheduledTime"`
	StartScheduleFingerprint string                         `json:"startScheduleFingerprint,omitempty"`
	SkipSuccessResetAt       time.Time                      `json:"skipSuccessResetAt"`
	OneOffs                  map[string]OneOffScheduleState `json:"oneOffs,omitempty"`
	NextRun                  *time.Time                     `json:"nextRun,omitempty"`
	// LastSlot is the latest start-schedule slot that came due, and what the
	// scheduler did with it; nil before any slot came due.
	LastSlot *ScheduleSlot `json:"lastSlot,omitempty"`
}

// ScheduleSlot is a start-schedule slot that came due, and what came of it.
type ScheduleSlot struct {
	At      time.Time   `json:"at"`
	Outcome SlotOutcome `json:"outcome"`
}

// SlotOutcome is what the scheduler did with a start-schedule slot.
type SlotOutcome string

const (
	// SlotDispatched: a run was started or enqueued for the slot.
	SlotDispatched SlotOutcome = "dispatched"
	// SlotRunning: skipped because a run of the DAG was in progress.
	SlotRunning SlotOutcome = "running"
	// SlotQueued: skipped because a run of the DAG was queued.
	SlotQueued SlotOutcome = "queued"
	// SlotDone: skipped because a run already started at or after the slot.
	SlotDone SlotOutcome = "done"
	// SlotSucceeded: skipped because the slot's previous run succeeded and
	// the DAG skips after success.
	SlotSucceeded SlotOutcome = "succeeded"
	// SlotOverlap: a catch-up slot dropped by the DAG's overlap policy.
	SlotOverlap SlotOutcome = "overlap"
	// SlotFailed: checking or dispatching the run failed.
	SlotFailed SlotOutcome = "failed"
	// SlotPlanned: chosen to run, with its dispatch not yet settled.
	SlotPlanned SlotOutcome = "planned"
	// SlotMissed: came due while no scheduler ran, and was not caught up.
	SlotMissed SlotOutcome = "missed"
)

// Settled reports whether the slot ran, or was skipped on purpose.
func (o SlotOutcome) Settled() bool {
	switch o {
	case SlotFailed, SlotPlanned, SlotMissed:
		return false
	case SlotDispatched, SlotRunning, SlotQueued, SlotDone, SlotSucceeded, SlotOverlap:
		return true
	default:
		return true
	}
}

// OneOffScheduleStatus is the persisted state of a one-off schedule.
type OneOffScheduleStatus string

const (
	OneOffStatusPending  OneOffScheduleStatus = "pending"
	OneOffStatusConsumed OneOffScheduleStatus = "consumed"
)

// OneOffScheduleState tracks a single one-off schedule instance.
type OneOffScheduleState struct {
	ScheduledTime time.Time            `json:"scheduledTime"`
	Status        OneOffScheduleStatus `json:"status"`
}

// Store persists scheduler state. Implementations must be safe for concurrent
// use. Load returns an independent snapshot that callers may mutate, and Save
// must not retain or mutate the provided state.
type Store interface {
	Load(ctx context.Context) (*State, error)
	Save(ctx context.Context, state *State) error
}

// Clone returns a deep copy of state.
func Clone(state *State) *State {
	if state == nil {
		return nil
	}
	cloned := &State{
		LastTick: state.LastTick,
		DAGs:     make(map[string]DAGWatermark, len(state.DAGs)),
	}
	for dagName, dagState := range state.DAGs {
		cloned.DAGs[dagName] = CloneDAGWatermark(dagState)
	}
	return cloned
}

// CloneDAGWatermark returns a deep copy of a DAG watermark.
func CloneDAGWatermark(watermark DAGWatermark) DAGWatermark {
	cloned := DAGWatermark{
		LastScheduledTime:        watermark.LastScheduledTime,
		StartScheduleFingerprint: watermark.StartScheduleFingerprint,
		SkipSuccessResetAt:       watermark.SkipSuccessResetAt,
	}
	if watermark.NextRun != nil {
		nextRun := *watermark.NextRun
		cloned.NextRun = &nextRun
	}
	if watermark.LastSlot != nil {
		slot := *watermark.LastSlot
		cloned.LastSlot = &slot
	}
	if len(watermark.OneOffs) > 0 {
		cloned.OneOffs = make(map[string]OneOffScheduleState, len(watermark.OneOffs))
		maps.Copy(cloned.OneOffs, watermark.OneOffs)
	}
	return cloned
}
