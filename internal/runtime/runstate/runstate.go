// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runstate defines the execution-state port used by the runtime.
package runstate

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Store opens execution state for workflow runs.
type Store interface {
	BeginAttempt(ctx context.Context, req BeginAttemptRequest) (Attempt, error)
	OpenAttempt(ctx context.Context, ref dagrun.DAGRunRef) (Attempt, error)
	OpenChildAttempt(ctx context.Context, root dagrun.DAGRunRef, childRunID string) (Attempt, error)
}

// BeginAttemptRequest describes the workflow run attempt to open for execution.
type BeginAttemptRequest struct {
	DAG        *ir.DAG
	RunID      string
	AttemptID  string
	Retry      bool
	RootDAGRun dagrun.DAGRunRef
}

// Attempt records and reads state for a single workflow execution attempt.
type Attempt interface {
	ID() string
	Open(ctx context.Context) error
	RecordStatus(ctx context.Context, status dagrun.DAGRunStatus) error
	RecordOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error
	ReadStatus(ctx context.Context) (*dagrun.DAGRunStatus, error)
	ReadOutputs(ctx context.Context) (*ir.DAGRunOutputs, error)
	RequestCancel(ctx context.Context) error
	CancelRequested(ctx context.Context) (bool, error)
	ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error)
	WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error
	WorkDir() string
	Close(ctx context.Context) error
}
