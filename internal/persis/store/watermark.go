// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/service/scheduler"
)

var _ scheduler.WatermarkStore = (*WatermarkStore)(nil)

const watermarkStateID = "state"

// WatermarkStore implements [scheduler.WatermarkStore].
// A single record with ID "state" holds the entire SchedulerState.
type WatermarkStore struct {
	col persis.Collection
}

// NewWatermarkStore creates a WatermarkStore backed by col.
func NewWatermarkStore(col persis.Collection) *WatermarkStore {
	return &WatermarkStore{col: col}
}

// Load reads the scheduler state.
// Returns a fresh empty state if the record is missing or corrupt.
func (s *WatermarkStore) Load(ctx context.Context) (*scheduler.SchedulerState, error) {
	rec, err := s.col.Get(ctx, watermarkStateID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return watermarkNewEmptyState(), nil
		}
		return nil, fmt.Errorf("watermark store: get: %w", err)
	}

	var state scheduler.SchedulerState
	if err := persis.Decode(rec, &state); err != nil {
		slog.Warn("watermark: corrupt state, starting fresh", slog.String("error", err.Error()))
		return watermarkNewEmptyState(), nil
	}

	const expected = scheduler.SchedulerStateVersion
	switch state.Version {
	case expected:
	case 0, 1, 2:
		migrated, migrateErr := watermarkMigrateState(state.Version, &state)
		if migrateErr != nil {
			slog.Warn("watermark: failed to migrate state, starting fresh", slog.String("error", migrateErr.Error()))
			return watermarkNewEmptyState(), nil
		}
		state = *migrated
	default:
		slog.Warn("watermark: unknown version, starting fresh", slog.Int("version", state.Version))
		return watermarkNewEmptyState(), nil
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]scheduler.DAGWatermark)
	}
	return &state, nil
}

// Save writes the scheduler state.
func (s *WatermarkStore) Save(ctx context.Context, state *scheduler.SchedulerState) error {
	if state == nil {
		return fmt.Errorf("watermark store: state is nil")
	}
	data, enc, err := persis.Encode(state)
	if err != nil {
		return fmt.Errorf("watermark store: encode: %w", err)
	}
	now := time.Now().UTC()
	if err := s.col.Put(ctx, &persis.Record{
		ID:        watermarkStateID,
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

func watermarkNewEmptyState() *scheduler.SchedulerState {
	return &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs:    make(map[string]scheduler.DAGWatermark),
	}
}

func watermarkMigrateState(version int, state *scheduler.SchedulerState) (*scheduler.SchedulerState, error) {
	if state == nil {
		return nil, fmt.Errorf("watermark store: state is nil")
	}
	migrated := *state
	switch version {
	case 0:
		migrated.Version = 1
		return watermarkMigrateState(1, &migrated)
	case 1:
		migrated.Version = 2
		return watermarkMigrateState(2, &migrated)
	case 2:
		migrated.Version = scheduler.SchedulerStateVersion
		if migrated.DAGs == nil {
			migrated.DAGs = make(map[string]scheduler.DAGWatermark)
		}
		return &migrated, nil
	default:
		return nil, fmt.Errorf("watermark store: unsupported state version %d", version)
	}
}
