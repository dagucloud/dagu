// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/internal/core"
)

// ErrNoopAttemptNotSupported is returned when an operation is not supported by a no-op attempt.
var ErrNoopAttemptNotSupported = errors.New("operation not supported by no-op DAG run attempt")

// noopDAGRunAttempt is a no-op implementation of DAGRunAttempt for remote workers.
// Status is pushed via statusPusher to the coordinator, so local attempt file
// operations are not needed.
type noopDAGRunAttempt struct {
	id  string
	dag *core.DAG
}

var _ DAGRunAttempt = (*noopDAGRunAttempt)(nil)

// NewNoopDAGRunAttempt creates a no-op attempt for remote worker execution.
func NewNoopDAGRunAttempt(id string, dag *core.DAG) DAGRunAttempt {
	return &noopDAGRunAttempt{id: id, dag: dag}
}

func (n *noopDAGRunAttempt) ID() string {
	return n.id
}

func (n *noopDAGRunAttempt) Open(_ context.Context) error {
	return nil
}

func (n *noopDAGRunAttempt) Write(_ context.Context, _ DAGRunStatus) error {
	return nil
}

func (n *noopDAGRunAttempt) Close(_ context.Context) error {
	return nil
}

func (n *noopDAGRunAttempt) ReadStatus(_ context.Context) (*DAGRunStatus, error) {
	return nil, ErrNoopAttemptNotSupported
}

func (n *noopDAGRunAttempt) ReadDAG(_ context.Context) (*core.DAG, error) {
	return n.dag, nil
}

func (n *noopDAGRunAttempt) SetDAG(dag *core.DAG) {
	n.dag = dag
}

func (n *noopDAGRunAttempt) Abort(_ context.Context) error {
	return nil
}

func (n *noopDAGRunAttempt) IsAborting(_ context.Context) (bool, error) {
	return false, nil
}

func (n *noopDAGRunAttempt) Hide(_ context.Context) error {
	return nil
}

func (n *noopDAGRunAttempt) Hidden() bool {
	return false
}

func (n *noopDAGRunAttempt) WriteOutputs(_ context.Context, _ *DAGRunOutputs) error {
	return nil
}

func (n *noopDAGRunAttempt) ReadOutputs(_ context.Context) (*DAGRunOutputs, error) {
	return nil, ErrNoopAttemptNotSupported
}

func (n *noopDAGRunAttempt) WriteStepMessages(_ context.Context, _ string, _ []LLMMessage) error {
	return nil
}

func (n *noopDAGRunAttempt) ReadStepMessages(_ context.Context, _ string) ([]LLMMessage, error) {
	return nil, nil
}

func (n *noopDAGRunAttempt) WorkDir() string {
	return ""
}
