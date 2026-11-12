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
	Definition []byte    `json:"definition"`
	Options    Options   `json:"options"`
	Actor      string    `json:"actor,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	// Slots are the slots the backfill runs, oldest first, each with the
	// window it owns.
	Slots    []RecordSlot `json:"slots"`
	Revision string       `json:"revision"`
	// EndedAt is set when the backfill has ended.
	EndedAt time.Time `json:"endedAt,omitzero"`
}

// RecordSlot is one slot a backfill runs.
type RecordSlot struct {
	At     time.Time         `json:"slot"`
	Window ir.ScheduleWindow `json:"window"`
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
	// LockDAG holds a lock that serializes submits for a DAG across
	// processes until unlock is called.
	LockDAG(ctx context.Context, dagName string) (unlock func(), err error)
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
