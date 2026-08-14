// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package memstore provides an in-memory runtime run-state store.
package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

// Store records run attempts in memory.
type Store struct {
	mu sync.RWMutex

	attempts map[attemptKey]*attemptState
	latest   map[ir.DAGRunRef]attemptKey
	children map[childKey]attemptKey
	counters map[ir.DAGRunRef]int
}

var _ runstate.Store = (*Store)(nil)

// New creates an empty in-memory run-state store.
func New() *Store {
	return &Store{
		attempts: make(map[attemptKey]*attemptState),
		latest:   make(map[ir.DAGRunRef]attemptKey),
		children: make(map[childKey]attemptKey),
		counters: make(map[ir.DAGRunRef]int),
	}
}

// BeginAttempt opens a new attempt for a DAG run.
func (s *Store) BeginAttempt(_ context.Context, req runstate.BeginAttemptRequest) (runstate.Attempt, error) {
	if req.DAG == nil {
		return nil, fmt.Errorf("DAG is required")
	}
	if req.DAG.Name == "" {
		return nil, fmt.Errorf("DAG name is required")
	}
	if req.RunID == "" {
		return nil, fmt.Errorf("dag-run ID is required")
	}
	if err := ir.ValidateDAGRunID(req.RunID); err != nil {
		return nil, err
	}

	ref := ir.NewDAGRunRef(req.DAG.Name, req.RunID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.latest[ref]; exists && !req.Retry {
		return nil, fmt.Errorf("%w: %s", dagrun.ErrDAGRunAlreadyExists, req.RunID)
	}

	s.counters[ref]++
	attemptID := req.AttemptID
	if attemptID == "" {
		attemptID = generatedAttemptID(req.RunID, s.counters[ref])
	}
	key := attemptKey{ref: ref, id: attemptID}
	state := &attemptState{
		messages: make(map[string][]ir.LLMMessage),
	}
	s.attempts[key] = state
	s.latest[ref] = key
	if req.RootDAGRun.ID != "" && req.RootDAGRun.ID != req.RunID {
		s.children[childKey{root: req.RootDAGRun, runID: req.RunID}] = key
	}

	return attempt{store: s, key: key}, nil
}

// OpenAttempt opens the latest attempt for a DAG run.
func (s *Store) OpenAttempt(_ context.Context, ref ir.DAGRunRef) (runstate.Attempt, error) {
	s.mu.RLock()
	key, ok := s.latest[ref]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", dagrun.ErrDAGRunIDNotFound, ref.String())
	}
	return attempt{store: s, key: key}, nil
}

// OpenChildAttempt opens the latest child attempt under a root DAG run.
func (s *Store) OpenChildAttempt(_ context.Context, root ir.DAGRunRef, childRunID string) (runstate.Attempt, error) {
	s.mu.RLock()
	key, ok := s.children[childKey{root: root, runID: childRunID}]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s:%s", dagrun.ErrDAGRunIDNotFound, root.String(), childRunID)
	}
	return attempt{store: s, key: key}, nil
}

type attemptKey struct {
	ref ir.DAGRunRef
	id  string
}

type childKey struct {
	root  ir.DAGRunRef
	runID string
}

type attemptState struct {
	status    *ir.DAGRunStatus
	outputs   *ir.DAGRunOutputs
	messages  map[string][]ir.LLMMessage
	cancelled bool
	workDir   string
}

type attempt struct {
	store *Store
	key   attemptKey
}

var _ runstate.Attempt = attempt{}

func (a attempt) ID() string {
	return a.key.id
}

func (a attempt) Open(_ context.Context) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	_, err := a.stateLocked()
	return err
}

func (a attempt) RecordStatus(_ context.Context, status ir.DAGRunStatus) error {
	cloned, err := cloneStatus(status)
	if err != nil {
		return err
	}

	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.status = cloned
	return nil
}

func (a attempt) RecordOutputs(_ context.Context, outputs *ir.DAGRunOutputs) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.outputs = cloneOutputs(outputs)
	return nil
}

func (a attempt) ReadStatus(_ context.Context) (*ir.DAGRunStatus, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return nil, err
	}
	if state.status == nil {
		return nil, dagrun.ErrNoStatusData
	}
	return cloneStatusValue(state.status)
}

func (a attempt) ReadOutputs(_ context.Context) (*ir.DAGRunOutputs, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return nil, err
	}
	return cloneOutputs(state.outputs), nil
}

func (a attempt) RequestCancel(_ context.Context) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.cancelled = true
	return nil
}

func (a attempt) CancelRequested(_ context.Context) (bool, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return false, err
	}
	return state.cancelled, nil
}

func (a attempt) ReadStepMessages(_ context.Context, stepName string) ([]ir.LLMMessage, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return nil, err
	}
	return cloneMessages(state.messages[stepName]), nil
}

func (a attempt) WriteStepMessages(_ context.Context, stepName string, messages []ir.LLMMessage) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.messages[stepName] = cloneMessages(messages)
	return nil
}

func (a attempt) MaterializeWorkDir(_ context.Context) (string, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return "", err
	}
	return state.workDir, nil
}

func (a attempt) SnapshotWorkDir(_ context.Context, _ string) error {
	return nil
}

func (a attempt) Close(_ context.Context) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	_, err := a.stateLocked()
	return err
}

func (a attempt) stateLocked() (*attemptState, error) {
	state, ok := a.store.attempts[a.key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", dagrun.ErrDAGRunIDNotFound, a.key.ref.String())
	}
	return state, nil
}

func (a attempt) stateRLocked() (*attemptState, error) {
	state, ok := a.store.attempts[a.key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", dagrun.ErrDAGRunIDNotFound, a.key.ref.String())
	}
	return state, nil
}

func generatedAttemptID(runID string, count int) string {
	if count <= 1 {
		return runID
	}
	return runID + "-" + strconv.Itoa(count)
}

func cloneStatus(status ir.DAGRunStatus) (*ir.DAGRunStatus, error) {
	return cloneStatusValue(&status)
}

func cloneStatusValue(status *ir.DAGRunStatus) (*ir.DAGRunStatus, error) {
	if status == nil {
		return nil, nil
	}
	data, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("clone status: %w", err)
	}
	var cloned ir.DAGRunStatus
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone status: %w", err)
	}
	return &cloned, nil
}

func cloneOutputs(outputs *ir.DAGRunOutputs) *ir.DAGRunOutputs {
	if outputs == nil {
		return nil
	}
	return &ir.DAGRunOutputs{
		Metadata: outputs.Metadata,
		Outputs:  maps.Clone(outputs.Outputs),
	}
}

func cloneMessages(messages []ir.LLMMessage) []ir.LLMMessage {
	if len(messages) == 0 {
		return nil
	}
	out := slices.Clone(messages)
	for i := range out {
		out[i].ToolCalls = slices.Clone(out[i].ToolCalls)
		if out[i].Metadata != nil {
			metadata := *out[i].Metadata
			out[i].Metadata = &metadata
		}
	}
	return out
}
