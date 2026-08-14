// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

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
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const (
	schedulerStateVersion = 4
	schedulerStateID      = "state"
	schedulerCheckpointID = "checkpoint"
)

type schedulerStateRecord struct {
	SchemaVersion int                                    `json:"version"`
	DAGs          map[string]schedulerstate.DAGWatermark `json:"dags,omitempty"`
}

type schedulerStateCompatRecord struct {
	SchemaVersion int                                    `json:"version"`
	LastTick      time.Time                              `json:"lastTick"`
	DAGs          map[string]schedulerstate.DAGWatermark `json:"dags,omitempty"`
}

type schedulerCheckpoint struct {
	LastTick time.Time `json:"lastTick"`
}

// SchedulerStateStore persists scheduler state as collection records.
type SchedulerStateStore struct {
	col           persis.Collection
	stateRec      *SingleRecord[schedulerStateRecord]
	checkpointRec *SingleRecord[schedulerCheckpoint]

	mu                     sync.Mutex
	cachedState            *schedulerstate.State
	cachedStateRecordToken string
	cachedStatePayload     []byte
}

var _ schedulerstate.Store = (*SchedulerStateStore)(nil)

// NewSchedulerStateStore creates a scheduler state store backed by col.
func NewSchedulerStateStore(col persis.Collection) *SchedulerStateStore {
	return &SchedulerStateStore{
		col:           col,
		stateRec:      NewSingleRecord[schedulerStateRecord](col, schedulerStateID),
		checkpointRec: NewSingleRecord[schedulerCheckpoint](col, schedulerCheckpointID),
	}
}

// Load reads scheduler state, returning a fresh state when stored data is missing or unusable.
func (s *SchedulerStateStore) Load(ctx context.Context) (*schedulerstate.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok, err := s.cachedStateLocked(ctx); ok || err != nil {
		return cached, err
	}

	var rawState schedulerStateCompatRecord
	found, err := NewSingleRecord[schedulerStateCompatRecord](s.col, schedulerStateID).Load(ctx, &rawState)
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			logger.Warn(ctx, "scheduler state: corrupt state, starting fresh", tag.Error(err))
			state := newSchedulerState()
			s.cacheStateLocked(ctx, state)
			s.cachedStatePayload = nil
			return schedulerstate.Clone(state), nil
		}
		return nil, fmt.Errorf("scheduler state store: get: %w", err)
	}
	if !found {
		state := newSchedulerState()
		s.cacheStateLocked(ctx, state)
		return schedulerstate.Clone(state), nil
	}

	originalVersion := rawState.SchemaVersion
	state := &schedulerstate.State{
		LastTick: rawState.LastTick,
		DAGs:     rawState.DAGs,
	}
	switch originalVersion {
	case schedulerStateVersion, 0, 1, 2, 3:
	default:
		logger.Warn(ctx, "scheduler state: unknown version, starting fresh", tag.Version(fmt.Sprint(originalVersion)))
		state = newSchedulerState()
		s.cacheStateLocked(ctx, state)
		return schedulerstate.Clone(state), nil
	}

	if state.DAGs == nil {
		state.DAGs = make(map[string]schedulerstate.DAGWatermark)
	}
	if checkpoint, ok, checkpointErr := s.loadCheckpoint(ctx); checkpointErr != nil {
		return nil, checkpointErr
	} else if ok {
		state.LastTick = checkpoint.LastTick
	}
	s.cacheStateLocked(ctx, state)
	if originalVersion != schedulerStateVersion || !rawState.LastTick.IsZero() {
		s.cachedStatePayload = nil
	}
	return schedulerstate.Clone(state), nil
}

// Save writes scheduler state when its durable records have changed.
func (s *SchedulerStateStore) Save(ctx context.Context, state *schedulerstate.State) error {
	if state == nil {
		return fmt.Errorf("scheduler state store: state is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stateRecord := newSchedulerStateRecord(state)
	stateData, err := persis.Encode(&stateRecord)
	if err != nil {
		return fmt.Errorf("scheduler state store: encode state: %w", err)
	}
	stateChanged := !bytes.Equal(stateData, s.cachedStatePayload)
	if stateChanged {
		if err := s.stateRec.Save(ctx, &stateRecord); err != nil {
			return fmt.Errorf("scheduler state store: save: %w", err)
		}
	}

	checkpoint := schedulerCheckpoint{LastTick: state.LastTick}
	checkpointChanged := stateChanged || s.cachedState == nil || !state.LastTick.Equal(s.cachedState.LastTick)
	if checkpointChanged {
		if err := s.checkpointRec.Save(ctx, &checkpoint); err != nil {
			return fmt.Errorf("scheduler state store: save checkpoint: %w", err)
		}
	}

	if stateChanged {
		s.cachedStatePayload = append(s.cachedStatePayload[:0], stateData...)
		if token, ok, err := collectionRecordVersion(ctx, s.col, schedulerStateID); ok && err == nil {
			s.cachedStateRecordToken = token
		} else {
			s.cachedStateRecordToken = ""
		}
	}
	s.cachedState = schedulerstate.Clone(state)
	return nil
}

func newSchedulerState() *schedulerstate.State {
	return &schedulerstate.State{
		DAGs: make(map[string]schedulerstate.DAGWatermark),
	}
}

func newSchedulerStateRecord(state *schedulerstate.State) schedulerStateRecord {
	record := schedulerStateRecord{
		SchemaVersion: schedulerStateVersion,
		DAGs:          make(map[string]schedulerstate.DAGWatermark, len(state.DAGs)),
	}
	for dagName, dagState := range state.DAGs {
		record.DAGs[dagName] = schedulerstate.CloneDAGWatermark(dagState)
	}
	return record
}

func (s *SchedulerStateStore) cachedStateLocked(ctx context.Context) (*schedulerstate.State, bool, error) {
	if s.cachedState == nil || s.cachedStateRecordToken == "" {
		return nil, false, nil
	}
	token, ok, err := collectionRecordVersion(ctx, s.col, schedulerStateID)
	if !ok {
		return nil, false, nil
	}
	if errors.Is(err, persis.ErrNotFound) {
		s.clearCacheLocked()
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("scheduler state store: state record: %w", err)
	}
	if token != s.cachedStateRecordToken {
		s.clearCacheLocked()
		return nil, false, nil
	}
	return schedulerstate.Clone(s.cachedState), true, nil
}

func (s *SchedulerStateStore) cacheStateLocked(ctx context.Context, state *schedulerstate.State) {
	s.cachedState = schedulerstate.Clone(state)
	stateRecord := newSchedulerStateRecord(state)
	if data, err := persis.Encode(&stateRecord); err == nil {
		s.cachedStatePayload = data
	}
	if token, ok, err := collectionRecordVersion(ctx, s.col, schedulerStateID); ok && err == nil {
		s.cachedStateRecordToken = token
	} else {
		s.cachedStateRecordToken = ""
	}
}

func (s *SchedulerStateStore) clearCacheLocked() {
	s.cachedStateRecordToken = ""
	s.cachedState = nil
	s.cachedStatePayload = nil
}

func (s *SchedulerStateStore) loadCheckpoint(ctx context.Context) (schedulerCheckpoint, bool, error) {
	var checkpoint schedulerCheckpoint
	found, err := s.checkpointRec.Load(ctx, &checkpoint)
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			logger.Warn(ctx, "scheduler state: corrupt checkpoint, using state fallback", tag.Error(err))
			return schedulerCheckpoint{}, false, nil
		}
		return schedulerCheckpoint{}, false, fmt.Errorf("scheduler state store: get checkpoint: %w", err)
	}
	return checkpoint, found, nil
}
