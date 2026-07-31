// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
)

const watermarkStateID = "state"
const watermarkCheckpointID = "checkpoint"

type schedulerStateFile struct {
	SchemaVersion int                     `json:"version"`
	DAGs          map[string]DAGWatermark `json:"dags,omitempty"`
}

type schedulerStateCompatFile struct {
	SchemaVersion int                     `json:"version"`
	LastTick      time.Time               `json:"lastTick"`
	DAGs          map[string]DAGWatermark `json:"dags,omitempty"`
}

type schedulerCheckpoint struct {
	LastTick time.Time `json:"lastTick"`
}

type recordVersionCollection interface {
	RecordVersion(ctx context.Context, id string) (string, error)
}

// watermarkStore persists scheduler state as collection records.
type watermarkStore struct {
	col           persis.Collection
	rec           *store.SingleRecord[schedulerStateFile]
	checkpointRec *store.SingleRecord[schedulerCheckpoint]

	mu                     sync.Mutex
	cachedState            *SchedulerState
	cachedStateRecordToken string
	cachedStatePayload     []byte
}

// NewWatermarkStore returns a [WatermarkStore] backed by col.
func NewWatermarkStore(col persis.Collection) WatermarkStore {
	return &watermarkStore{
		col:           col,
		rec:           store.NewSingleRecord[schedulerStateFile](col, watermarkStateID),
		checkpointRec: store.NewSingleRecord[schedulerCheckpoint](col, watermarkCheckpointID),
	}
}

// Load reads the scheduler state.
// Returns a fresh empty state if the record is missing or corrupt.
func (s *watermarkStore) Load(ctx context.Context) (*SchedulerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok, err := s.cachedStateLocked(ctx); ok || err != nil {
		return cached, err
	}

	var rawState schedulerStateCompatFile
	found, err := store.NewSingleRecord[schedulerStateCompatFile](s.col, watermarkStateID).Load(ctx, &rawState)
	if err != nil {
		if errors.Is(err, store.ErrCorrupt) {
			logger.Warn(ctx, "watermark: corrupt state, starting fresh", tag.Error(err))
			state := newEmptyWatermarkState()
			s.cacheStateLocked(ctx, state)
			return cloneSchedulerState(state), nil
		}
		return nil, fmt.Errorf("watermark store: get: %w", err)
	}
	if !found {
		state := newEmptyWatermarkState()
		s.cacheStateLocked(ctx, state)
		return cloneSchedulerState(state), nil
	}

	const expected = SchedulerStateVersion
	originalVersion := rawState.SchemaVersion
	state := &SchedulerState{
		Version:  rawState.SchemaVersion,
		LastTick: rawState.LastTick,
		DAGs:     rawState.DAGs,
	}
	switch state.Version {
	case expected:
	case 0, 1, 2, 3:
		migrated, migrateErr := migrateWatermarkState(state.Version, state)
		if migrateErr != nil {
			logger.Warn(ctx, "watermark: failed to migrate state, starting fresh", tag.Error(migrateErr))
			state = newEmptyWatermarkState()
			s.cacheStateLocked(ctx, state)
			return cloneSchedulerState(state), nil
		}
		state = migrated
	default:
		logger.Warn(ctx, "watermark: unknown version, starting fresh", tag.Version(fmt.Sprint(state.Version)))
		state = newEmptyWatermarkState()
		s.cacheStateLocked(ctx, state)
		return cloneSchedulerState(state), nil
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]DAGWatermark)
	}
	if checkpoint, ok, checkpointErr := s.loadCheckpoint(ctx); checkpointErr != nil {
		return nil, checkpointErr
	} else if ok {
		state.LastTick = checkpoint.LastTick
	}
	s.cacheStateLocked(ctx, state)
	if originalVersion != expected || !rawState.LastTick.IsZero() {
		s.cachedStatePayload = nil
	}
	return cloneSchedulerState(state), nil
}

// Save writes the scheduler state.
func (s *watermarkStore) Save(ctx context.Context, state *SchedulerState) error {
	if state == nil {
		return fmt.Errorf("watermark store: state is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stateFile := schedulerStateFile{
		SchemaVersion: state.Version,
		DAGs:          make(map[string]DAGWatermark, len(state.DAGs)),
	}
	for dagName, dagState := range state.DAGs {
		stateFile.DAGs[dagName] = cloneDAGWatermark(dagState)
	}
	stateData, err := persis.Encode(&stateFile)
	if err != nil {
		return fmt.Errorf("watermark store: encode state: %w", err)
	}
	stateChanged := !bytes.Equal(stateData, s.cachedStatePayload)
	if stateChanged {
		if err := s.rec.Save(ctx, &stateFile); err != nil {
			return fmt.Errorf("watermark store: save: %w", err)
		}
	}

	checkpoint := schedulerCheckpoint{LastTick: state.LastTick}
	checkpointChanged := stateChanged ||
		s.cachedState == nil ||
		!state.LastTick.Equal(s.cachedState.LastTick)
	if checkpointChanged {
		if err := s.checkpointRec.Save(ctx, &checkpoint); err != nil {
			return fmt.Errorf("watermark store: save checkpoint: %w", err)
		}
	}

	if stateChanged {
		s.cachedStatePayload = append(s.cachedStatePayload[:0], stateData...)
		if token, ok, err := s.stateRecordToken(ctx); ok && err == nil {
			s.cachedStateRecordToken = token
		} else {
			s.cachedStateRecordToken = ""
		}
	}
	s.cachedState = cloneSchedulerState(state)
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

// migrateWatermarkState upgrades a legacy state to the
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
		migrated.Version = 3
		return migrateWatermarkState(3, &migrated)
	case 3:
		migrated.Version = SchedulerStateVersion
		if migrated.DAGs == nil {
			migrated.DAGs = make(map[string]DAGWatermark)
		}
		return &migrated, nil
	default:
		return nil, fmt.Errorf("watermark store: unsupported state version %d", version)
	}
}

func (s *watermarkStore) cachedStateLocked(ctx context.Context) (*SchedulerState, bool, error) {
	if s.cachedState == nil || s.cachedStateRecordToken == "" {
		return nil, false, nil
	}
	token, ok, err := s.stateRecordToken(ctx)
	if !ok {
		return nil, false, nil
	}
	if errors.Is(err, persis.ErrNotFound) {
		s.cachedStateRecordToken = ""
		s.cachedState = nil
		s.cachedStatePayload = nil
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("watermark store: state record: %w", err)
	}
	if token != s.cachedStateRecordToken {
		s.cachedStateRecordToken = ""
		s.cachedState = nil
		s.cachedStatePayload = nil
		return nil, false, nil
	}
	return cloneSchedulerState(s.cachedState), true, nil
}

func (s *watermarkStore) cacheStateLocked(ctx context.Context, state *SchedulerState) {
	s.cachedState = cloneSchedulerState(state)
	stateFile := schedulerStateFile{
		SchemaVersion: state.Version,
		DAGs:          make(map[string]DAGWatermark, len(state.DAGs)),
	}
	for dagName, dagState := range state.DAGs {
		stateFile.DAGs[dagName] = cloneDAGWatermark(dagState)
	}
	if data, err := persis.Encode(&stateFile); err == nil {
		s.cachedStatePayload = data
	}
	if token, ok, err := s.stateRecordToken(ctx); ok && err == nil {
		s.cachedStateRecordToken = token
	} else {
		s.cachedStateRecordToken = ""
	}
}

func (s *watermarkStore) loadCheckpoint(ctx context.Context) (schedulerCheckpoint, bool, error) {
	var checkpoint schedulerCheckpoint
	found, err := s.checkpointRec.Load(ctx, &checkpoint)
	if err != nil {
		if errors.Is(err, store.ErrCorrupt) {
			logger.Warn(ctx, "watermark: corrupt checkpoint, using state fallback", tag.Error(err))
			return schedulerCheckpoint{}, false, nil
		}
		return schedulerCheckpoint{}, false, fmt.Errorf("watermark store: get checkpoint: %w", err)
	}
	return checkpoint, found, nil
}

func (s *watermarkStore) stateRecordToken(ctx context.Context) (string, bool, error) {
	col, ok := s.col.(recordVersionCollection)
	if !ok {
		return "", false, nil
	}
	token, err := col.RecordVersion(ctx, watermarkStateID)
	return token, true, err
}
