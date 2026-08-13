// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// NewNoopStore creates execution state for runs persisted outside this process.
func NewNoopStore() Store {
	return noopStore{}
}

type noopStore struct{}

func (noopStore) BeginAttempt(_ context.Context, req BeginAttemptRequest) (Attempt, error) {
	id := req.AttemptID
	if id == "" {
		id = req.RunID
	}
	return noopAttempt{Attempt: dagrun.NewNoopAttempt(id, req.DAG)}, nil
}

func (noopStore) OpenAttempt(context.Context, ir.DAGRunRef) (Attempt, error) {
	return nil, dagrun.ErrNoopAttemptNotSupported
}

func (noopStore) OpenChildAttempt(context.Context, ir.DAGRunRef, string) (Attempt, error) {
	return nil, dagrun.ErrNoopAttemptNotSupported
}

type noopAttempt struct {
	dagrun.Attempt
}

func (a noopAttempt) RecordStatus(ctx context.Context, status ir.DAGRunStatus) error {
	return a.Write(ctx, status)
}

func (a noopAttempt) RecordOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	return a.WriteOutputs(ctx, outputs)
}

func (a noopAttempt) RequestCancel(ctx context.Context) error {
	return a.Abort(ctx)
}

func (a noopAttempt) CancelRequested(ctx context.Context) (bool, error) {
	return a.IsAborting(ctx)
}

func (noopAttempt) MaterializeWorkspace(context.Context) (string, error) {
	return "", nil
}

func (noopAttempt) SnapshotWorkspace(context.Context, string) error {
	return nil
}
