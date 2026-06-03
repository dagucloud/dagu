// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runstate defines the execution-state port used by the runtime.
package runstate

import (
	"context"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Store opens execution state for workflow runs.
type Store interface {
	BeginAttempt(ctx context.Context, req BeginAttemptRequest) (Attempt, error)
	OpenAttempt(ctx context.Context, ref exec.DAGRunRef) (Attempt, error)
	OpenChildAttempt(ctx context.Context, root exec.DAGRunRef, childRunID string) (Attempt, error)
}

// BeginAttemptRequest describes the workflow run attempt to open for execution.
type BeginAttemptRequest struct {
	DAG        *core.DAG
	RunID      string
	AttemptID  string
	Retry      bool
	RootDAGRun exec.DAGRunRef
}

// Attempt records and reads state for a single workflow execution attempt.
type Attempt interface {
	ID() string
	Open(ctx context.Context) error
	RecordStatus(ctx context.Context, status exec.DAGRunStatus) error
	RecordOutputs(ctx context.Context, outputs *exec.DAGRunOutputs) error
	ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error)
	ReadOutputs(ctx context.Context) (*exec.DAGRunOutputs, error)
	RequestCancel(ctx context.Context) error
	CancelRequested(ctx context.Context) (bool, error)
	ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error)
	WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error
	WorkDir() string
	Close(ctx context.Context) error
}
