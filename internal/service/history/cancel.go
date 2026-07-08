// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (s *Service) markDispatchCanceled(ctx context.Context, cmd MarkDispatchCanceledCommand) error {
	attempt, err := s.cfg.DAGRunStore.FindAttempt(ctx, cmd.DAGRun)
	if err != nil {
		return err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return err
	}
	if status == nil || status.Status != core.Queued {
		return newRunNotPendingError(status)
	}

	finishedAt := time.Now().UTC().Format(time.RFC3339)
	currentStatus, swapped, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.DAGRun,
		attempt.ID(),
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = core.Aborted
			latest.FinishedAt = finishedAt
			latest.WorkerID = ""
			latest.PID = 0
			latest.PIDStartedAt = 0
			latest.LeaseAt = 0
			return nil
		},
	)
	if err != nil {
		return err
	}
	if !swapped {
		return newRunNotPendingError(currentStatus)
	}

	if err := attempt.Hide(ctx); err != nil {
		logger.Warn(ctx, "Pending DAG-run was aborted but hiding the attempt failed",
			tag.DAG(cmd.DAGRun.Name),
			tag.RunID(cmd.DAGRun.ID),
			tag.AttemptID(attempt.ID()),
			tag.Error(err),
		)
		return fmt.Errorf("hide aborted attempt: %w", err)
	}

	_, err = s.cfg.DAGRunStore.FindAttempt(ctx, cmd.DAGRun)
	if errors.Is(err, exec.ErrNoStatusData) {
		if err := s.cfg.DAGRunStore.RemoveDAGRun(ctx, cmd.DAGRun); err != nil {
			return fmt.Errorf("remove empty dag-run record: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}

	return nil
}

func newRunNotPendingError(status *exec.DAGRunStatus) *RunNotPendingError {
	if status == nil {
		return &RunNotPendingError{}
	}
	return &RunNotPendingError{
		Status:    status.Status,
		HasStatus: true,
	}
}
