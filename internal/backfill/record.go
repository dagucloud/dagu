// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// OnFailure is what a backfill does after a slot fails.
type OnFailure string

const (
	// OnFailureStop enqueues nothing more after a slot fails.
	OnFailureStop OnFailure = "stop"
	// OnFailureContinue goes on to the next slot.
	OnFailureContinue OnFailure = "continue"
)

// ParseOnFailure parses an --on-failure value.
func ParseOnFailure(value string) (OnFailure, error) {
	switch onFailure := OnFailure(value); onFailure {
	case OnFailureStop, OnFailureContinue:
		return onFailure, nil
	}
	return "", fmt.Errorf("invalid on-failure %q: use stop or continue", value)
}

// Options are what an operator chose when submitting a backfill.
type Options struct {
	// From and To are the range as given; To is zero when it defaulted to
	// now. Both are zero when Slots names the slots.
	From  time.Time   `json:"from,omitzero"`
	To    time.Time   `json:"to,omitzero"`
	Slots []time.Time `json:"slots,omitempty"`
	Rerun Rerun       `json:"rerun"`
	// MaxConcurrency bounds the runs of the backfill that are queued,
	// running, or waiting at once.
	MaxConcurrency int       `json:"maxConcurrency"`
	OnFailure      OnFailure `json:"onFailure"`
	Params         string    `json:"params,omitempty"`
	Profile        string    `json:"profile,omitempty"`
}

// Equal reports whether two submits asked for the same backfill.
func (o Options) Equal(other Options) bool {
	return o.From.Equal(other.From) && o.To.Equal(other.To) &&
		slices.EqualFunc(o.Slots, other.Slots, time.Time.Equal) &&
		o.Rerun == other.Rerun && o.MaxConcurrency == other.MaxConcurrency &&
		o.OnFailure == other.OnFailure && o.Params == other.Params && o.Profile == other.Profile
}

// Record is a submitted backfill.
type Record struct {
	ID      string `json:"id"`
	DAGName string `json:"dagName"`
	// DAGFile locates the DAG's definition.
	DAGFile string `json:"dagFile"`
	// Definition is the DAG's YAML pinned at submit; later slots run it
	// whatever edits follow.
	Definition []byte `json:"definition"`
	// Labels are the DAG's labels with its definition. They place the
	// backfill in the DAG's workspace, also once the DAG is gone.
	Labels    ir.Labels `json:"labels,omitempty"`
	Options   Options   `json:"options"`
	Actor     string    `json:"actor,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// PlannedAt is when the slots were last planned, at submit or resume. A
	// run recorded later can make a slot skip at its turn.
	PlannedAt time.Time `json:"plannedAt"`
	// Slots are the slots the backfill runs, oldest first, each with the
	// window it owns.
	Slots    []RecordSlot `json:"slots"`
	Revision string       `json:"revision"`
	// Generation counts the resumes of the backfill. Each gives its slots
	// new runs.
	Generation int `json:"generation,omitempty"`
	// Status is how the backfill ended; empty while it is open.
	Status Status `json:"status,omitempty"`
	// EndedAt is set when the backfill has ended.
	EndedAt time.Time `json:"endedAt,omitzero"`
}

// Status is how a backfill ended.
type Status string

const (
	// StatusSucceeded is a backfill whose every slot succeeded or was
	// skipped at its turn.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a backfill with a slot that did not succeed.
	StatusFailed Status = "failed"
	// StatusCancelled is a backfill an operator cancelled.
	StatusCancelled Status = "cancelled"
)

// ProgressKind is what happened to a slot of a backfill.
type ProgressKind string

const (
	// ProgressEnqueued records that the slot's run was queued.
	ProgressEnqueued ProgressKind = "enqueued"
	// ProgressSkipped records that the slot gained a run after planning
	// that --rerun skips, so the backfill ran nothing for it.
	ProgressSkipped ProgressKind = "skipped"
	// ProgressNotRun records that the backfill stopped before the slot.
	ProgressNotRun ProgressKind = "not_run"
	// ProgressCancelled records that the backfill was cancelled before the
	// slot's run started.
	ProgressCancelled ProgressKind = "cancelled"
)

// Progress is one event of a backfill's slots.
type Progress struct {
	Slot  time.Time    `json:"slot"`
	Kind  ProgressKind `json:"kind"`
	RunID string       `json:"runId,omitempty"`
	// Generation is the resume the event belongs to.
	Generation int       `json:"generation,omitempty"`
	At         time.Time `json:"at"`
}

// currentProgress returns the last event of each slot in the record's
// current generation.
func currentProgress(record *Record, events []Progress) map[time.Time]Progress {
	byslot := make(map[time.Time]Progress, len(events))
	for _, slot := range record.Slots {
		if slot.RunID != "" {
			byslot[slot.At.UTC()] = Progress{Slot: slot.At, Kind: ProgressEnqueued, RunID: slot.RunID, Generation: record.Generation}
		}
	}
	for _, event := range events {
		if event.Generation == record.Generation {
			byslot[event.Slot.UTC()] = event
		}
	}
	return byslot
}

// RecordSlot is one slot a backfill runs.
type RecordSlot struct {
	At     time.Time         `json:"slot"`
	Window ir.ScheduleWindow `json:"window"`
	// RunID is a run a resume found active for the slot, which the
	// resumed backfill follows instead of queueing a new one.
	RunID string `json:"runId,omitempty"`
}

// Open reports whether the backfill has not ended.
func (r *Record) Open() bool {
	return r.EndedAt.IsZero()
}

// Store persists backfill records.
type Store interface {
	// Create stores a new record. It fails with ErrExists when the ID is
	// taken.
	Create(ctx context.Context, record *Record) error
	// Get returns the record of a backfill, or ErrNotFound.
	Get(ctx context.Context, id string) (*Record, error)
	// List returns the records of a DAG, or of every DAG when dagName is
	// empty, oldest first.
	List(ctx context.Context, dagName string) ([]*Record, error)
	// Open returns the records of every open backfill, oldest first, without
	// reading those that ended.
	Open(ctx context.Context) ([]*Record, error)
	// LockDAG holds a lock that serializes submits for a DAG across
	// processes until unlock is called.
	LockDAG(ctx context.Context, dagName string) (unlock func(), err error)
	// Append adds progress events to a backfill.
	Append(ctx context.Context, id string, events ...Progress) error
	// Progress returns a backfill's progress events in the order appended.
	Progress(ctx context.Context, id string) ([]Progress, error)
	// End records how a backfill ended.
	End(ctx context.Context, id string, status Status, at time.Time) error
	// Update rewrites an existing record, as a resume does, and marks it
	// open while it has not ended.
	Update(ctx context.Context, record *Record) error
}

var (
	// ErrNotFound is returned for a backfill ID with no record.
	ErrNotFound = errors.New("backfill not found")
	// ErrExists is returned when a record's ID is taken.
	ErrExists = errors.New("backfill already exists")
)

// idAlphabet makes IDs easy to read and type: lowercase letters and digits
// without the ones that look alike.
const idAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// idLength keeps backfill run IDs within the 64-character limit.
const idLength = 8

// NewID returns a random backfill ID, each character equally likely.
func NewID() string {
	// Bytes at or above limit are dropped so every character is as likely.
	limit := byte(256 - 256%len(idAlphabet))
	id := make([]byte, 0, idLength)
	buf := make([]byte, idLength)
	for len(id) < idLength {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			if b < limit && len(id) < idLength {
				id = append(id, idAlphabet[int(b)%len(idAlphabet)])
			}
		}
	}
	return string(id)
}
