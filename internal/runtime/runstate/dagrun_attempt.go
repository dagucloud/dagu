// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func wrapDAGRunAttempt(attempt exec.DAGRunAttempt) Attempt {
	return dagRunAttempt{attempt: attempt}
}

type dagRunAttempt struct {
	attempt exec.DAGRunAttempt
}

func (a dagRunAttempt) ID() string {
	return a.attempt.ID()
}

func (a dagRunAttempt) Open(ctx context.Context) error {
	return a.attempt.Open(ctx)
}

func (a dagRunAttempt) RecordStatus(ctx context.Context, status exec.DAGRunStatus) error {
	return a.attempt.Write(ctx, status)
}

func (a dagRunAttempt) RecordOutputs(ctx context.Context, outputs *exec.DAGRunOutputs) error {
	return a.attempt.WriteOutputs(ctx, outputs)
}

func (a dagRunAttempt) ReadStatus(ctx context.Context) (*exec.DAGRunStatus, error) {
	return a.attempt.ReadStatus(ctx)
}

func (a dagRunAttempt) ReadOutputs(ctx context.Context) (*exec.DAGRunOutputs, error) {
	return a.attempt.ReadOutputs(ctx)
}

func (a dagRunAttempt) RequestCancel(ctx context.Context) error {
	return a.attempt.Abort(ctx)
}

func (a dagRunAttempt) CancelRequested(ctx context.Context) (bool, error) {
	return a.attempt.IsAborting(ctx)
}

func (a dagRunAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error) {
	return a.attempt.ReadStepMessages(ctx, stepName)
}

func (a dagRunAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error {
	return a.attempt.WriteStepMessages(ctx, stepName, messages)
}

func (a dagRunAttempt) WorkDir() string {
	return a.attempt.WorkDir()
}

func (a dagRunAttempt) Close(ctx context.Context) error {
	return a.attempt.Close(ctx)
}
