// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
)

// ErrCorrupt reports that a record exists but its payload could not be decoded.
// It is distinct from a backend read failure so callers can choose to recover
// (for example, start from a fresh value) rather than propagate the error.
var ErrCorrupt = errors.New("persis/store: corrupt record")

// SingleRecord persists one value at a fixed record ID within a collection.
//
// It owns the mechanical encode/decode and Get/Put/Delete against the
// collection; callers layer their own policy — defaults, error handling, and
// concurrency — on top. It is the shared core of the single-record
// control-plane stores (license activation, upgrade-check cache, agent config,
// GitHub-dispatch tracking, scheduler watermark).
//
// SingleRecord performs no locking of its own. The underlying collection is
// safe for concurrent use, but a caller needing read-modify-write atomicity
// must serialize access itself.
type SingleRecord[T any] struct {
	col persis.Collection
	id  string
}

// NewSingleRecord returns a SingleRecord addressing record id within col.
func NewSingleRecord[T any](col persis.Collection, id string) *SingleRecord[T] {
	return &SingleRecord[T]{col: col, id: id}
}

// Load fetches the record and decodes it into dst.
//
// When no record exists it returns found=false and leaves dst untouched, so a
// caller may pre-populate dst with defaults before calling Load and keep them
// when the record is absent. A backend read failure is returned unchanged. A
// payload that cannot be decoded is reported as an error satisfying
// errors.Is(err, [ErrCorrupt]).
func (s *SingleRecord[T]) Load(ctx context.Context, dst *T) (found bool, err error) {
	rec, err := s.col.Get(ctx, s.id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := persis.Decode(rec, dst); err != nil {
		return false, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return true, nil
}

// Save encodes v and writes it as the record, stamping the current time.
func (s *SingleRecord[T]) Save(ctx context.Context, v *T) error {
	data, err := persis.Encode(v)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.col.Put(ctx, &persis.Record{
		ID:        s.id,
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// Delete removes the record. A missing record is not an error.
func (s *SingleRecord[T]) Delete(ctx context.Context) error {
	return s.col.Delete(ctx, s.id)
}
