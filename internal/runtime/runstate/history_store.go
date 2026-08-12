// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type historyStoreOption func(*historyStore)

// WithPreparedAttempt reuses an attempt that was opened by Dagu before runtime execution.
func WithPreparedAttempt(attempt dagrun.Attempt) historyStoreOption {
	return func(s *historyStore) {
		s.preparedAttempt = attempt
	}
}

// NewHistoryStore adapts a DAG-run repository to the runtime run-state store.
func NewHistoryStore(repository *dagrun.Repository, opts ...historyStoreOption) Store {
	s := &historyStore{repository: repository}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type historyStore struct {
	repository      *dagrun.Repository
	preparedAttempt dagrun.Attempt
}

func (s *historyStore) BeginAttempt(ctx context.Context, req BeginAttemptRequest) (Attempt, error) {
	if s.repository == nil {
		return wrapAttempt(dagrun.NewNoopAttempt(noopAttemptID(req), req.DAG)), nil
	}

	if req.DAG != nil && req.DAG.HistRetentionRuns == 0 {
		if _, err := s.repository.RemoveOldDAGRuns(ctx, req.DAG.Name, req.DAG.HistRetentionDays); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	var attempt dagrun.Attempt
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
		created, err := s.repository.CreateAttempt(ctx, req.DAG, time.Now(), req.RunID, dagRunAttemptOptions(req))
		if err != nil {
			return nil, err
		}
		attempt = created
	}

	if req.DAG != nil && req.DAG.HistRetentionRuns > 0 {
		if _, err := s.repository.RemoveOldDAGRuns(ctx, req.DAG.Name, 0, dagrun.WithRetentionRuns(req.DAG.HistRetentionRuns)); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	return wrapAttempt(attempt), nil
}

func (s *historyStore) OpenAttempt(ctx context.Context, ref ir.DAGRunRef) (Attempt, error) {
	if s.repository == nil {
		return nil, dagrun.ErrNoopAttemptNotSupported
	}
	attempt, err := s.repository.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return wrapAttempt(attempt), nil
}

func (s *historyStore) OpenChildAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (Attempt, error) {
	if s.repository == nil {
		return nil, dagrun.ErrNoopAttemptNotSupported
	}
	attempt, err := s.repository.FindSubAttempt(ctx, root, childRunID)
	if err != nil {
		return nil, err
	}
	return wrapAttempt(attempt), nil
}

func dagRunAttemptOptions(req BeginAttemptRequest) dagrun.CreateAttemptOptions {
	opts := dagrun.CreateAttemptOptions{
		Retry:     req.Retry,
		AttemptID: req.AttemptID,
	}
	if req.RootDAGRun.ID != "" && req.RootDAGRun.ID != req.RunID {
		opts.RootDAGRun = req.RootDAGRun
	}
	return opts
}

func noopAttemptID(req BeginAttemptRequest) string {
	if req.AttemptID != "" {
		return req.AttemptID
	}
	return req.RunID
}
