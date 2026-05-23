// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package watermark implements [scheduler.WatermarkStore] using a [persis.Collection].
package watermark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/service/scheduler"
)

var _ scheduler.WatermarkStore = (*Store)(nil)

const stateID = "state"

// Store implements [scheduler.WatermarkStore].
// A single record with ID "state" holds the entire SchedulerState.
type Store struct {
	col persis.Collection
}

// New creates a Store backed by col.
func New(col persis.Collection) *Store {
	return &Store{col: col}
}

// Load reads the scheduler state.
// Returns a fresh empty state if the record is missing or corrupt.
func (s *Store) Load(ctx context.Context) (*scheduler.SchedulerState, error) {
	rec, err := s.col.Get(ctx, stateID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return newEmptyState(), nil
		}
		return nil, fmt.Errorf("watermark store: get: %w", err)
	}

	var state scheduler.SchedulerState
	if err := persis.Decode(rec, &state); err != nil {
		slog.Warn("watermark: corrupt state, starting fresh", slog.String("error", err.Error()))
		return newEmptyState(), nil
	}

	const expected = scheduler.SchedulerStateVersion
	switch state.Version {
	case expected:
	case 0, 1, 2:
		migrated, migrateErr := migrateState(state.Version, &state)
		if migrateErr != nil {
			slog.Warn("watermark: failed to migrate state, starting fresh", slog.String("error", migrateErr.Error()))
			return newEmptyState(), nil
		}
		state = *migrated
	default:
		slog.Warn("watermark: unknown version, starting fresh", slog.Int("version", state.Version))
		return newEmptyState(), nil
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]scheduler.DAGWatermark)
	}
	return &state, nil
}

// Save writes the scheduler state.
func (s *Store) Save(ctx context.Context, state *scheduler.SchedulerState) error {
	data, enc, err := persis.Encode(state)
	if err != nil {
		return fmt.Errorf("watermark store: encode: %w", err)
	}
	now := time.Now().UTC()
	if err := s.col.Put(ctx, &persis.Record{
		ID:        stateID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("watermark store: put: %w", err)
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func newEmptyState() *scheduler.SchedulerState {
	return &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs:    make(map[string]scheduler.DAGWatermark),
	}
}

func migrateState(version int, state *scheduler.SchedulerState) (*scheduler.SchedulerState, error) {
	if state == nil {
		return nil, fmt.Errorf("watermark state is nil")
	}
	migrated := *state
	switch version {
	case 0:
		migrated.Version = 1
		return migrateState(1, &migrated)
	case 1:
		migrated.Version = 2
		return migrateState(2, &migrated)
	case 2:
		migrated.Version = scheduler.SchedulerStateVersion
		if migrated.DAGs == nil {
			migrated.DAGs = make(map[string]scheduler.DAGWatermark)
		}
		return &migrated, nil
	default:
		return nil, fmt.Errorf("unsupported watermark state version %d", version)
	}
}
