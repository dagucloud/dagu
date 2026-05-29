// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/store"
)

const watermarkStateID = "state"

// watermarkStore persists [SchedulerState] as a single collection record.
type watermarkStore struct {
	rec *store.SingleRecord[SchedulerState]
}

// NewWatermarkStore returns a [WatermarkStore] backed by col. A single record
// with ID "state" holds the entire [SchedulerState].
func NewWatermarkStore(col persis.Collection) WatermarkStore {
	return &watermarkStore{rec: store.NewSingleRecord[SchedulerState](col, watermarkStateID)}
}

// Load reads the scheduler state.
// Returns a fresh empty state if the record is missing or corrupt.
func (s *watermarkStore) Load(ctx context.Context) (*SchedulerState, error) {
	var state SchedulerState
	found, err := s.rec.Load(ctx, &state)
	if err != nil {
		if errors.Is(err, store.ErrCorrupt) {
			logger.Warn(ctx, "watermark: corrupt state, starting fresh", tag.Error(err))
			return newEmptyWatermarkState(), nil
		}
		return nil, fmt.Errorf("watermark store: get: %w", err)
	}
	if !found {
		return newEmptyWatermarkState(), nil
	}

	const expected = SchedulerStateVersion
	switch state.Version {
	case expected:
	case 0, 1, 2:
		migrated, migrateErr := migrateWatermarkState(state.Version, &state)
		if migrateErr != nil {
			logger.Warn(ctx, "watermark: failed to migrate state, starting fresh", tag.Error(migrateErr))
			return newEmptyWatermarkState(), nil
		}
		state = *migrated
	default:
		logger.Warn(ctx, "watermark: unknown version, starting fresh", tag.Version(fmt.Sprint(state.Version)))
		return newEmptyWatermarkState(), nil
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]DAGWatermark)
	}
	return &state, nil
}

// Save writes the scheduler state.
func (s *watermarkStore) Save(ctx context.Context, state *SchedulerState) error {
	if state == nil {
		return fmt.Errorf("watermark store: state is nil")
	}
	if err := s.rec.Save(ctx, state); err != nil {
		return fmt.Errorf("watermark store: save: %w", err)
	}
	return nil
}

// newEmptyWatermarkState returns a fresh state at the current version with an
// initialized DAGs map.
func newEmptyWatermarkState() *SchedulerState {
	return &SchedulerState{
		Version: SchedulerStateVersion,
		DAGs:    make(map[string]DAGWatermark),
	}
}

// migrateWatermarkState upgrades a legacy state (versions 0 to 2) to the
// current version, returning an error for any version it cannot migrate.
func migrateWatermarkState(version int, state *SchedulerState) (*SchedulerState, error) {
	if state == nil {
		return nil, fmt.Errorf("watermark store: state is nil")
	}
	migrated := *state
	switch version {
	case 0:
		migrated.Version = 1
		return migrateWatermarkState(1, &migrated)
	case 1:
		migrated.Version = 2
		return migrateWatermarkState(2, &migrated)
	case 2:
		migrated.Version = SchedulerStateVersion
		if migrated.DAGs == nil {
			migrated.DAGs = make(map[string]DAGWatermark)
		}
		return &migrated, nil
	default:
		return nil, fmt.Errorf("watermark store: unsupported state version %d", version)
	}
}
