// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ErrNoopAttemptNotSupported is returned when an operation is not supported by a no-op attempt.
var ErrNoopAttemptNotSupported = errors.New("operation not supported by no-op DAG run attempt")

// noopAttempt is a no-op implementation of Attempt for remote workers.
// Status is pushed to the coordinator, so persistent attempt operations are not needed.
type noopAttempt struct {
	id  string
	dag *ir.DAG
}

var _ Attempt = (*noopAttempt)(nil)

// NewNoopAttempt creates a no-op attempt for remote worker execution.
func NewNoopAttempt(id string, dag *ir.DAG) Attempt {
	return &noopAttempt{id: id, dag: dag}
}

func (n *noopAttempt) ID() string {
	return n.id
}

func (n *noopAttempt) Open(_ context.Context) error {
	return nil
}

func (n *noopAttempt) Write(_ context.Context, _ ir.DAGRunStatus) error {
	return nil
}

func (n *noopAttempt) Close(_ context.Context) error {
	return nil
}

func (n *noopAttempt) ReadStatus(_ context.Context) (*ir.DAGRunStatus, error) {
	return nil, ErrNoopAttemptNotSupported
}

func (n *noopAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return n.ReadStatus(ctx)
}

func (n *noopAttempt) ReadDAG(_ context.Context) (*ir.DAG, error) {
	return n.dag, nil
}

func (n *noopAttempt) SetDAG(dag *ir.DAG) {
	n.dag = dag
}

func (n *noopAttempt) Abort(_ context.Context) error {
	return nil
}

func (n *noopAttempt) IsAborting(_ context.Context) (bool, error) {
	return false, nil
}

func (n *noopAttempt) Hide(_ context.Context) error {
	return nil
}

func (n *noopAttempt) Hidden() bool {
	return false
}

func (n *noopAttempt) WriteOutputs(_ context.Context, _ *ir.DAGRunOutputs) error {
	return nil
}

func (n *noopAttempt) ReadOutputs(_ context.Context) (*ir.DAGRunOutputs, error) {
	return nil, ErrNoopAttemptNotSupported
}

func (n *noopAttempt) WriteStepMessages(_ context.Context, _ string, _ []ir.LLMMessage) error {
	return nil
}

func (n *noopAttempt) ReadStepMessages(_ context.Context, _ string) ([]ir.LLMMessage, error) {
	return nil, nil
}
