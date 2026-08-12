// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func wrapAttempt(
	attempt dagrun.Attempt,
	repository *dagrun.Repository,
	dagRunWorkspaces dagrun.DAGRunWorkspaceStore,
	workspaceRef dagrun.DAGRunWorkspaceRef,
) Attempt {
	return dagRunAttempt{
		attempt:          attempt,
		repository:       repository,
		dagRunWorkspaces: dagRunWorkspaces,
		workspaceRef:     workspaceRef,
	}
}

type dagRunAttempt struct {
	attempt          dagrun.Attempt
	repository       *dagrun.Repository
	dagRunWorkspaces dagrun.DAGRunWorkspaceStore
	workspaceRef     dagrun.DAGRunWorkspaceRef
}

func (a dagRunAttempt) ID() string {
	return a.attempt.ID()
}

func (a dagRunAttempt) Open(ctx context.Context) error {
	return a.attempt.Open(ctx)
}

func (a dagRunAttempt) RecordStatus(ctx context.Context, status ir.DAGRunStatus) error {
	return a.attempt.Write(ctx, status)
}

func (a dagRunAttempt) RecordOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	return a.attempt.WriteOutputs(ctx, outputs)
}

func (a dagRunAttempt) ReadStatus(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.attempt.ReadStatus(ctx)
}

func (a dagRunAttempt) ReadOutputs(ctx context.Context) (*ir.DAGRunOutputs, error) {
	return a.attempt.ReadOutputs(ctx)
}

func (a dagRunAttempt) RequestCancel(ctx context.Context) error {
	return a.attempt.Abort(ctx)
}

func (a dagRunAttempt) CancelRequested(ctx context.Context) (bool, error) {
	return a.attempt.IsAborting(ctx)
}

func (a dagRunAttempt) ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error) {
	return a.attempt.ReadStepMessages(ctx, stepName)
}

func (a dagRunAttempt) WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error {
	return a.attempt.WriteStepMessages(ctx, stepName, messages)
}

func (a dagRunAttempt) MaterializeWorkspace(ctx context.Context) (string, error) {
	if a.repository != nil {
		return a.repository.MaterializeWorkspace(ctx, a.workspaceRef)
	}
	if a.dagRunWorkspaces != nil {
		return a.dagRunWorkspaces.Materialize(ctx, a.workspaceRef)
	}
	return "", nil
}

func (a dagRunAttempt) SnapshotWorkspace(ctx context.Context, localDir string) error {
	if a.repository != nil {
		return a.repository.SnapshotWorkspace(ctx, a.workspaceRef, localDir)
	}
	if a.dagRunWorkspaces != nil {
		return a.dagRunWorkspaces.Snapshot(ctx, a.workspaceRef, localDir)
	}
	return nil
}

func (a dagRunAttempt) Close(ctx context.Context) error {
	return a.attempt.Close(ctx)
}
