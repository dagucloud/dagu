// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/mock"
)

var _ dagrun.DAGRunAttempt = (*MockDAGRunAttempt)(nil)

// MockDAGRunAttempt is a configurable DAG-run attempt for tests.
type MockDAGRunAttempt struct {
	mock.Mock
	Status *dagrun.DAGRunStatus
}

func (m *MockDAGRunAttempt) ID() string {
	return m.Called().String(0)
}

func (m *MockDAGRunAttempt) Open(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDAGRunAttempt) Write(ctx context.Context, status dagrun.DAGRunStatus) error {
	return m.Called(ctx, status).Error(0)
}

func (m *MockDAGRunAttempt) Close(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDAGRunAttempt) ReadStatus(ctx context.Context) (*dagrun.DAGRunStatus, error) {
	if m.Status != nil {
		return m.Status, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*dagrun.DAGRunStatus), args.Error(1)
}

func (m *MockDAGRunAttempt) ReadDAG(ctx context.Context) (*ir.DAG, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

func (m *MockDAGRunAttempt) SetDAG(dag *ir.DAG) {
	m.Called(dag)
}

func (m *MockDAGRunAttempt) Abort(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDAGRunAttempt) IsAborting(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockDAGRunAttempt) Hide(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDAGRunAttempt) Hidden() bool {
	return m.Called().Bool(0)
}

func (m *MockDAGRunAttempt) WriteOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	return m.Called(ctx, outputs).Error(0)
}

func (m *MockDAGRunAttempt) ReadOutputs(ctx context.Context) (*ir.DAGRunOutputs, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAGRunOutputs), args.Error(1)
}

func (m *MockDAGRunAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error {
	return m.Called(ctx, stepName, messages).Error(0)
}

func (m *MockDAGRunAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error) {
	args := m.Called(ctx, stepName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ir.LLMMessage), args.Error(1)
}

func (m *MockDAGRunAttempt) WorkDir() string {
	return m.Called().String(0)
}
