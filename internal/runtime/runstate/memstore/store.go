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

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
)

// Store records run attempts in memory.
type Store struct {
	mu sync.RWMutex

	attempts map[attemptKey]*attemptState
	latest   map[exec.DAGRunRef]attemptKey
	children map[childKey]attemptKey
	counters map[exec.DAGRunRef]int
}

var _ runstate.Store = (*Store)(nil)

// New creates an empty in-memory run-state store.
func New() *Store {
	return &Store{
		attempts: make(map[attemptKey]*attemptState),
		latest:   make(map[exec.DAGRunRef]attemptKey),
		children: make(map[childKey]attemptKey),
		counters: make(map[exec.DAGRunRef]int),
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
	if err := exec.ValidateDAGRunID(req.RunID); err != nil {
		return nil, err
	}

	ref := exec.NewDAGRunRef(req.DAG.Name, req.RunID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.latest[ref]; exists && !req.Retry {
		return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, req.RunID)
	}

	s.counters[ref]++
	attemptID := req.AttemptID
	if attemptID == "" {
		attemptID = generatedAttemptID(req.RunID, s.counters[ref])
	}
	key := attemptKey{ref: ref, id: attemptID}
	state := &attemptState{
		messages: make(map[string][]exec.LLMMessage),
	}
	s.attempts[key] = state
	s.latest[ref] = key
	if req.RootDAGRun.ID != "" && req.RootDAGRun.ID != req.RunID {
		s.children[childKey{root: req.RootDAGRun, runID: req.RunID}] = key
	}

	return attempt{store: s, key: key}, nil
}

// OpenAttempt opens the latest attempt for a DAG run.
func (s *Store) OpenAttempt(_ context.Context, ref exec.DAGRunRef) (runstate.Attempt, error) {
	s.mu.RLock()
	key, ok := s.latest[ref]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, ref.String())
	}
	return attempt{store: s, key: key}, nil
}

// OpenChildAttempt opens the latest child attempt under a root DAG run.
func (s *Store) OpenChildAttempt(_ context.Context, root exec.DAGRunRef, childRunID string) (runstate.Attempt, error) {
	s.mu.RLock()
	key, ok := s.children[childKey{root: root, runID: childRunID}]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s:%s", exec.ErrDAGRunIDNotFound, root.String(), childRunID)
	}
	return attempt{store: s, key: key}, nil
}

type attemptKey struct {
	ref exec.DAGRunRef
	id  string
}

type childKey struct {
	root  exec.DAGRunRef
	runID string
}

type attemptState struct {
	status    *exec.DAGRunStatus
	outputs   *exec.DAGRunOutputs
	messages  map[string][]exec.LLMMessage
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

func (a attempt) RecordStatus(_ context.Context, status exec.DAGRunStatus) error {
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

func (a attempt) RecordOutputs(_ context.Context, outputs *exec.DAGRunOutputs) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.outputs = cloneOutputs(outputs)
	return nil
}

func (a attempt) ReadStatus(_ context.Context) (*exec.DAGRunStatus, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return nil, err
	}
	if state.status == nil {
		return nil, exec.ErrNoStatusData
	}
	return cloneStatusValue(state.status)
}

func (a attempt) ReadOutputs(_ context.Context) (*exec.DAGRunOutputs, error) {
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

func (a attempt) ReadStepMessages(_ context.Context, stepName string) ([]exec.LLMMessage, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return nil, err
	}
	return cloneMessages(state.messages[stepName]), nil
}

func (a attempt) WriteStepMessages(_ context.Context, stepName string, messages []exec.LLMMessage) error {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	state, err := a.stateLocked()
	if err != nil {
		return err
	}
	state.messages[stepName] = cloneMessages(messages)
	return nil
}

func (a attempt) WorkDir() string {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	state, err := a.stateRLocked()
	if err != nil {
		return ""
	}
	return state.workDir
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
		return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, a.key.ref.String())
	}
	return state, nil
}

func (a attempt) stateRLocked() (*attemptState, error) {
	state, ok := a.store.attempts[a.key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, a.key.ref.String())
	}
	return state, nil
}

func generatedAttemptID(runID string, count int) string {
	if count <= 1 {
		return runID
	}
	return runID + "-" + strconv.Itoa(count)
}

func cloneStatus(status exec.DAGRunStatus) (*exec.DAGRunStatus, error) {
	return cloneStatusValue(&status)
}

func cloneStatusValue(status *exec.DAGRunStatus) (*exec.DAGRunStatus, error) {
	if status == nil {
		return nil, nil
	}
	data, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("clone status: %w", err)
	}
	var cloned exec.DAGRunStatus
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone status: %w", err)
	}
	return &cloned, nil
}

func cloneOutputs(outputs *exec.DAGRunOutputs) *exec.DAGRunOutputs {
	if outputs == nil {
		return nil
	}
	return &exec.DAGRunOutputs{
		Metadata: outputs.Metadata,
		Outputs:  maps.Clone(outputs.Outputs),
	}
}

func cloneMessages(messages []exec.LLMMessage) []exec.LLMMessage {
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
