// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
)

var _ runstate.Attempt = (*ExecutionContext)(nil)

// ExecutionContext is the execution-state handle prepared by History.
type ExecutionContext struct {
	dagRun      exec.DAGRunRef
	attempt     exec.DAGRunAttempt
	proc        exec.ProcHandle
	logFile     string
	artifactDir string
}

func newExecutionContext(dagRun exec.DAGRunRef, attempt exec.DAGRunAttempt, proc exec.ProcHandle, logFile, artifactDir string) *ExecutionContext {
	return &ExecutionContext{
		dagRun:      dagRun,
		attempt:     attempt,
		proc:        proc,
		logFile:     logFile,
		artifactDir: artifactDir,
	}
}

// DAGRun returns the DAG run identity this execution context belongs to.
func (e *ExecutionContext) DAGRun() exec.DAGRunRef {
	return e.dagRun
}

// AttemptID returns the prepared attempt identifier.
func (e *ExecutionContext) AttemptID() string {
	return e.ID()
}

// LogFile returns the main DAG-run log file path.
func (e *ExecutionContext) LogFile() string {
	return e.logFile
}

// ArtifactDir returns the DAG-run artifact directory path.
func (e *ExecutionContext) ArtifactDir() string {
	return e.artifactDir
}

// ProcHandle returns the acquired local process handle.
func (e *ExecutionContext) ProcHandle() exec.ProcHandle {
	return e.proc
}

// Release releases the acquired local process handle.
func (e *ExecutionContext) Release(ctx context.Context) error {
	if e.proc == nil {
		return nil
	}
	return e.proc.Stop(ctx)
}

// ID returns the attempt identifier.
func (e *ExecutionContext) ID() string {
	if e == nil || e.attempt == nil {
		return ""
	}
	return e.attempt.ID()
}

// Open prepares execution history for writes.
func (e *ExecutionContext) Open(ctx context.Context) error {
	return e.attempt.Open(ctx)
}

// RecordStatus records the latest attempt status.
func (e *ExecutionContext) RecordStatus(ctx context.Context, status exec.DAGRunStatus) error {
	return e.attempt.Write(ctx, status)
}

// RecordOutputs records collected DAG-run outputs.
func (e *ExecutionContext) RecordOutputs(ctx context.Context, outputs *exec.DAGRunOutputs) error {
	return e.attempt.WriteOutputs(ctx, outputs)
}

// ReadStatus reads the latest attempt status.
func (e *ExecutionContext) ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error) {
	return e.attempt.ReadStatus(ctx)
}

// ReadOutputs reads collected DAG-run outputs.
func (e *ExecutionContext) ReadOutputs(ctx context.Context) (*exec.DAGRunOutputs, error) {
	return e.attempt.ReadOutputs(ctx)
}

// RequestCancel records a cancellation request.
func (e *ExecutionContext) RequestCancel(ctx context.Context) error {
	return e.attempt.Abort(ctx)
}

// CancelRequested reports whether cancellation has been requested.
func (e *ExecutionContext) CancelRequested(ctx context.Context) (bool, error) {
	return e.attempt.IsAborting(ctx)
}

// ReadStepMessages reads persisted LLM messages for a step.
func (e *ExecutionContext) ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error) {
	return e.attempt.ReadStepMessages(ctx, stepName)
}

// WriteStepMessages records LLM messages for a step.
func (e *ExecutionContext) WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error {
	return e.attempt.WriteStepMessages(ctx, stepName, messages)
}

// WorkDir returns the DAG-run working directory.
func (e *ExecutionContext) WorkDir() string {
	return e.attempt.WorkDir()
}

// Close finalizes execution history writes.
func (e *ExecutionContext) Close(ctx context.Context) error {
	return e.attempt.Close(ctx)
}
