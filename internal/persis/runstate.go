// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

// NewRunStateStore adapts a DAG-run repository to runtime execution state.
func NewRunStateStore(repository *DAGRunRepository, prepared dagrun.Attempt) runstate.Store {
	return &runStateStore{repository: repository, prepared: prepared}
}

type runStateStore struct {
	repository *DAGRunRepository
	prepared   dagrun.Attempt
}

func (s *runStateStore) BeginAttempt(ctx context.Context, req runstate.BeginAttemptRequest) (runstate.Attempt, error) {
	if req.DAG != nil && req.DAG.HistRetentionRuns == 0 {
		if _, err := s.repository.RemoveOldDAGRuns(ctx, req.DAG.Name, req.DAG.HistRetentionDays, DAGRunRetentionOptions{}); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	attempt := s.prepared
	if attempt != nil {
		if req.AttemptID != "" && attempt.ID() != req.AttemptID {
			return nil, fmt.Errorf(
				"prepared attempt ID %q does not match requested attempt ID %q",
				attempt.ID(),
				req.AttemptID,
			)
		}
		attempt.SetDAG(req.DAG)
	} else {
		created, err := s.repository.CreateAttempt(ctx, req.DAG, time.Now(), req.RunID, runStateAttemptOptions(req))
		if err != nil {
			return nil, err
		}
		attempt = created
	}

	if req.DAG != nil && req.DAG.HistRetentionRuns > 0 {
		retentionRuns := req.DAG.HistRetentionRuns
		if _, err := s.repository.RemoveOldDAGRuns(ctx, req.DAG.Name, 0, DAGRunRetentionOptions{
			RetentionRuns: &retentionRuns,
		}); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	return runStateAttempt{
		Attempt:      attempt,
		repository:   s.repository,
		workspaceRef: workspaceRef(req),
	}, nil
}

func (s *runStateStore) OpenAttempt(ctx context.Context, ref ir.DAGRunRef) (runstate.Attempt, error) {
	attempt, err := s.repository.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return runStateAttempt{
		Attempt:      attempt,
		repository:   s.repository,
		workspaceRef: dagrun.WorkspaceRef{RootDAGRun: ref, DAGRun: ref},
	}, nil
}

func (s *runStateStore) OpenChildAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (runstate.Attempt, error) {
	attempt, err := s.repository.FindSubAttempt(ctx, root, childRunID)
	if err != nil {
		return nil, err
	}
	return runStateAttempt{
		Attempt:      attempt,
		repository:   s.repository,
		workspaceRef: dagrun.WorkspaceRef{RootDAGRun: root, DAGRun: ir.DAGRunRef{ID: childRunID}},
	}, nil
}

func workspaceRef(req runstate.BeginAttemptRequest) dagrun.WorkspaceRef {
	name := ""
	if req.DAG != nil {
		name = req.DAG.Name
	}
	ref := ir.NewDAGRunRef(name, req.RunID)
	root := req.RootDAGRun
	if root.Zero() {
		root = ref
	}
	return dagrun.WorkspaceRef{RootDAGRun: root, DAGRun: ref}
}

func runStateAttemptOptions(req runstate.BeginAttemptRequest) DAGRunCreateAttemptOptions {
	opts := DAGRunCreateAttemptOptions{
		Retry:     req.Retry,
		AttemptID: req.AttemptID,
	}
	if req.RootDAGRun.ID != "" && req.RootDAGRun.ID != req.RunID {
		opts.RootDAGRun = req.RootDAGRun
	}
	return opts
}

type runStateAttempt struct {
	dagrun.Attempt
	repository   *DAGRunRepository
	workspaceRef dagrun.WorkspaceRef
}

func (a runStateAttempt) RecordStatus(ctx context.Context, status ir.DAGRunStatus) error {
	return a.Write(ctx, status)
}

func (a runStateAttempt) RecordOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	return a.WriteOutputs(ctx, outputs)
}

func (a runStateAttempt) RequestCancel(ctx context.Context) error {
	return a.Abort(ctx)
}

func (a runStateAttempt) CancelRequested(ctx context.Context) (bool, error) {
	return a.IsAborting(ctx)
}

func (a runStateAttempt) MaterializeWorkspace(ctx context.Context) (string, error) {
	return a.repository.MaterializeWorkspace(ctx, a.workspaceRef)
}

func (a runStateAttempt) SnapshotWorkspace(ctx context.Context, localDir string) error {
	return a.repository.SnapshotWorkspace(ctx, a.workspaceRef, localDir)
}
