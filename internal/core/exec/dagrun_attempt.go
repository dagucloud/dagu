// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/mock"
)

// NewDAGRunAttemptOptions contains options for creating a new run record
type NewDAGRunAttemptOptions struct {
	// RootDAGRun is the root dag-run reference for this attempt.
	RootDAGRun *DAGRunRef
	// Retry indicates whether this is a retry of a previous run.
	Retry bool
	// AttemptID is an optional attempt ID. If set, this ID is used instead of generating a new one.
	// This is used when the coordinator has already created an attempt and wants the worker
	// to use the same ID for consistency.
	AttemptID string
}

// DAGRunAttempt represents a single execution of a dag-run to record the status and execution details.
type DAGRunAttempt interface {
	// ID returns the identifier for the attempt that is unique within the dag-run.
	ID() string
	// Open prepares the attempt for writing status updates
	Open(ctx context.Context) error
	// Write updates the status of the attempt
	Write(ctx context.Context, status DAGRunStatus) error
	// Close finalizes writing to the attempt
	Close(ctx context.Context) error
	// ReadStatus retrieves the current status of the attempt
	ReadStatus(ctx context.Context) (*DAGRunStatus, error)
	// ReadDAG reads the DAG associated with this run attempt
	ReadDAG(ctx context.Context) (*core.DAG, error)
	// SetDAG sets the DAG for this attempt (must be called before Open for DAG to be persisted)
	SetDAG(dag *core.DAG)
	// Abort requests aborting the attempt
	Abort(ctx context.Context) error
	// IsAborting checks if an abort has been requested for the attempt
	IsAborting(ctx context.Context) (bool, error)
	// Hide marks the attempt as hidden from normal operations.
	// This is useful for preserving previous state visibility when dequeuing.
	Hide(ctx context.Context) error
	// Hidden returns true if the attempt is hidden from normal operations.
	Hidden() bool
	// WriteOutputs writes the collected step outputs for the dag-run.
	// Does nothing if outputs is nil or has no output entries.
	WriteOutputs(ctx context.Context, outputs *DAGRunOutputs) error
	// ReadOutputs reads the collected step outputs for the dag-run.
	// Returns nil if no outputs file exists or if the file is in v1 format.
	ReadOutputs(ctx context.Context) (*DAGRunOutputs, error)
	// WriteStepMessages writes LLM messages for a single step.
	WriteStepMessages(ctx context.Context, stepName string, messages []LLMMessage) error
	// ReadStepMessages reads LLM messages for a single step.
	// Returns nil if no messages exist for the step.
	ReadStepMessages(ctx context.Context, stepName string) ([]LLMMessage, error)
	// WorkDir returns the path to the per-DAG-run working directory.
	// Returns "" if the attempt does not support local storage.
	WorkDir() string
}

var _ DAGRunAttempt = (*MockDAGRunAttempt)(nil)

// MockDAGRunAttempt is a mock implementation of DAGRunAttempt for testing.
type MockDAGRunAttempt struct {
	mock.Mock
	// Status can be set for tests that need to return a specific status without mock setup
	Status *DAGRunStatus
}

func (m *MockDAGRunAttempt) ID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDAGRunAttempt) Open(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) Write(ctx context.Context, status DAGRunStatus) error {
	args := m.Called(ctx, status)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) ReadStatus(ctx context.Context) (*DAGRunStatus, error) {
	if m.Status != nil {
		return m.Status, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*DAGRunStatus), args.Error(1)
}

func (m *MockDAGRunAttempt) ReadDAG(ctx context.Context) (*core.DAG, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.DAG), args.Error(1)
}

func (m *MockDAGRunAttempt) SetDAG(dag *core.DAG) {
	m.Called(dag)
}

func (m *MockDAGRunAttempt) Abort(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) IsAborting(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockDAGRunAttempt) Hide(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) Hidden() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockDAGRunAttempt) WriteOutputs(ctx context.Context, outputs *DAGRunOutputs) error {
	args := m.Called(ctx, outputs)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) ReadOutputs(ctx context.Context) (*DAGRunOutputs, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*DAGRunOutputs), args.Error(1)
}

func (m *MockDAGRunAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []LLMMessage) error {
	args := m.Called(ctx, stepName, messages)
	return args.Error(0)
}

func (m *MockDAGRunAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]LLMMessage, error) {
	args := m.Called(ctx, stepName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]LLMMessage), args.Error(1)
}

func (m *MockDAGRunAttempt) WorkDir() string {
	args := m.Called()
	return args.String(0)
}
