// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/mock"
)

var _ dagrun.Attempt = (*MockAttempt)(nil)

// MockAttempt is a configurable DAG-run attempt for tests.
type MockAttempt struct {
	mock.Mock
	Status *ir.DAGRunStatus
}

func (m *MockAttempt) ID() string {
	return m.Called().String(0)
}

func (m *MockAttempt) Open(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockAttempt) Write(ctx context.Context, status ir.DAGRunStatus) error {
	return m.Called(ctx, status).Error(0)
}

func (m *MockAttempt) Close(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockAttempt) ReadStatus(ctx context.Context) (*ir.DAGRunStatus, error) {
	if m.Status != nil {
		return m.Status, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAGRunStatus), args.Error(1)
}

func (m *MockAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return m.ReadStatus(ctx)
}

func (m *MockAttempt) ReadDAG(ctx context.Context) (*ir.DAG, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

func (m *MockAttempt) SetDAG(dag *ir.DAG) {
	m.Called(dag)
}

func (m *MockAttempt) Abort(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockAttempt) IsAborting(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockAttempt) Hide(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockAttempt) Hidden() bool {
	return m.Called().Bool(0)
}

func (m *MockAttempt) WriteOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	return m.Called(ctx, outputs).Error(0)
}

func (m *MockAttempt) ReadOutputs(ctx context.Context) (*ir.DAGRunOutputs, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAGRunOutputs), args.Error(1)
}

func (m *MockAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error {
	return m.Called(ctx, stepName, messages).Error(0)
}

func (m *MockAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error) {
	args := m.Called(ctx, stepName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ir.LLMMessage), args.Error(1)
}
