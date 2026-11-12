// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// SlotIndex records, per DAG, what happened to each schedule slot. It
// outlives run history, so a slot keeps the status of a run that retention
// removed. It records events and never the current status of a live run.
// While a run exists, its own status supersedes every event about it.
type SlotIndex interface {
	// Record appends an event for a slot.
	Record(ctx context.Context, event SlotEvent) error
	// Events returns the events of a DAG whose slots fall in [from, to],
	// ordered by the UTC day of their slot and, within a day, by the order
	// they were recorded.
	Events(ctx context.Context, dagName string, from, to time.Time) ([]SlotEvent, error)
	// Prune removes the events of a DAG whose slots fall before cutoff.
	Prune(ctx context.Context, dagName string, cutoff time.Time) error
	// Imported reports whether a DAG's run history has been added to the
	// index.
	Imported(ctx context.Context, dagName string) (bool, error)
	// Import appends the events whose runs have no event yet and marks the
	// DAG's history added, atomically with respect to Record. A run removed
	// while its history was being read keeps its removal.
	Import(ctx context.Context, dagName string, events []SlotEvent) error
}

// SlotEventKind is what happened to a slot.
type SlotEventKind string

const (
	// SlotEventRun records that a run of the slot was created.
	SlotEventRun SlotEventKind = "run"
	// SlotEventFinal records the final status of a run, written before
	// retention removes it.
	SlotEventFinal SlotEventKind = "final"
	// SlotEventDropped records that a run no longer counts for its slot,
	// written before an operator's delete removes it.
	SlotEventDropped SlotEventKind = "dropped"
	// SlotEventSkipped records that the scheduler did not run the slot.
	SlotEventSkipped SlotEventKind = "skipped"
)

// SlotEvent is one recorded fact about a schedule slot.
type SlotEvent struct {
	Kind    SlotEventKind `json:"kind"`
	DAGName string        `json:"dagName"`
	// Slot is the slot's scheduled time, in UTC.
	Slot  time.Time `json:"slot"`
	RunID string    `json:"runId,omitempty"`
	// TriggerType is how the run was started.
	TriggerType ir.TriggerType `json:"triggerType,omitempty"`
	// Schedule is the expression of the start schedule that fires at the
	// slot, or empty when none does, as for a manual run off the schedule.
	Schedule string `json:"schedule,omitempty"`
	// Status is the run's final status, for a final event.
	Status ir.Status `json:"status,omitempty"`
	// SkipReason says why the scheduler skipped the slot.
	SkipReason string    `json:"skipReason,omitempty"`
	RecordedAt time.Time `json:"recordedAt"`
}
