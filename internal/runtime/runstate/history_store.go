// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

type historyStoreOption func(*historyStore)

// WithPreparedAttempt reuses an attempt that was opened by Dagu before runtime execution.
func WithPreparedAttempt(attempt exec.DAGRunAttempt) historyStoreOption {
	return func(s *historyStore) {
		s.preparedAttempt = attempt
	}
}

// NewHistoryStore uses Dagu's run history store as the runtime run-state store.
func NewHistoryStore(store exec.DAGRunStore, opts ...historyStoreOption) Store {
	s := &historyStore{store: store}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type historyStore struct {
	store           exec.DAGRunStore
	preparedAttempt exec.DAGRunAttempt
}

func (s *historyStore) BeginAttempt(ctx context.Context, req BeginAttemptRequest) (Attempt, error) {
	if s.store == nil {
		return wrapDAGRunAttempt(exec.NewNoopDAGRunAttempt(noopAttemptID(req), req.DAG)), nil
	}

	if req.DAG != nil && req.DAG.HistRetentionRuns == 0 {
		if _, err := s.store.RemoveOldDAGRuns(ctx, req.DAG.Name, req.DAG.HistRetentionDays); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	var attempt exec.DAGRunAttempt
	if s.preparedAttempt != nil {
		if req.AttemptID != "" && s.preparedAttempt.ID() != req.AttemptID {
			return nil, fmt.Errorf(
				"prepared attempt ID %q does not match requested attempt ID %q",
				s.preparedAttempt.ID(),
				req.AttemptID,
			)
		}
		s.preparedAttempt.SetDAG(req.DAG)
		attempt = s.preparedAttempt
	} else {
		created, err := s.store.CreateAttempt(ctx, req.DAG, time.Now(), req.RunID, dagRunAttemptOptions(req))
		if err != nil {
			return nil, err
		}
		attempt = created
	}

	if req.DAG != nil && req.DAG.HistRetentionRuns > 0 {
		if _, err := s.store.RemoveOldDAGRuns(ctx, req.DAG.Name, 0, exec.WithRetentionRuns(req.DAG.HistRetentionRuns)); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	return wrapDAGRunAttempt(attempt), nil
}

func (s *historyStore) OpenAttempt(ctx context.Context, ref exec.DAGRunRef) (Attempt, error) {
	if s.store == nil {
		return nil, exec.ErrNoopAttemptNotSupported
	}
	attempt, err := s.store.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return wrapDAGRunAttempt(attempt), nil
}

func (s *historyStore) OpenChildAttempt(ctx context.Context, root exec.DAGRunRef, childRunID string) (Attempt, error) {
	if s.store == nil {
		return nil, exec.ErrNoopAttemptNotSupported
	}
	attempt, err := s.store.FindSubAttempt(ctx, root, childRunID)
	if err != nil {
		return nil, err
	}
	return wrapDAGRunAttempt(attempt), nil
}

func dagRunAttemptOptions(req BeginAttemptRequest) exec.NewDAGRunAttemptOptions {
	opts := exec.NewDAGRunAttemptOptions{
		Retry:     req.Retry,
		AttemptID: req.AttemptID,
	}
	if req.RootDAGRun.ID != "" && req.RootDAGRun.ID != req.RunID {
		opts.RootDAGRun = &req.RootDAGRun
	}
	return opts
}

func noopAttemptID(req BeginAttemptRequest) string {
	if req.AttemptID != "" {
		return req.AttemptID
	}
	return req.RunID
}
