// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// NewPreparedStore returns the prepared execution attempt for BeginAttempt.
func NewPreparedStore(base Store, attempt Attempt) Store {
	return &preparedStore{base: base, attempt: attempt}
}

type preparedStore struct {
	base    Store
	attempt Attempt
}

func (s *preparedStore) BeginAttempt(_ context.Context, req BeginAttemptRequest) (Attempt, error) {
	if s.attempt == nil {
		return nil, fmt.Errorf("prepared attempt is required")
	}
	if req.AttemptID != "" && s.attempt.ID() != req.AttemptID {
		return nil, fmt.Errorf(
			"prepared attempt ID %q does not match requested attempt ID %q",
			s.attempt.ID(),
			req.AttemptID,
		)
	}
	return s.attempt, nil
}

func (s *preparedStore) OpenAttempt(ctx context.Context, ref exec.DAGRunRef) (Attempt, error) {
	if s.base == nil {
		return nil, exec.ErrNoopAttemptNotSupported
	}
	return s.base.OpenAttempt(ctx, ref)
}

func (s *preparedStore) OpenChildAttempt(ctx context.Context, root exec.DAGRunRef, childRunID string) (Attempt, error) {
	if s.base == nil {
		return nil, exec.ErrNoopAttemptNotSupported
	}
	return s.base.OpenChildAttempt(ctx, root, childRunID)
}
