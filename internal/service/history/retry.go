// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (s *Service) retryRun(ctx context.Context, cmd RetryRunCommand) (*RetriedRun, error) {
	if err := s.validateRetryRun(cmd); err != nil {
		return nil, err
	}
	if cmd.Status.Status == core.Queued {
		return &RetriedRun{
			DAGRun: cmd.Status.DAGRun(),
			Status: cmd.Status,
			RollbackToken: RetryRollbackToken{
				dagRun:         cmd.Status.DAGRun(),
				queuedStatus:   *cmd.Status,
				previousStatus: *cmd.Status,
			},
		}, nil
	}
	previousStatus := *cmd.Status

	updatedStatus, swapped, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.Status.DAGRun(),
		cmd.Status.AttemptID,
		cmd.Status.Status,
		func(latest *exec.DAGRunStatus) error {
			now := s.now()
			latest.Status = core.Queued
			latest.QueuedAt = stringutil.FormatTime(now)
			latest.Conditions = nil
			latest.TriggerType = core.TriggerTypeRetry
			if cmd.Options.AutoRetry {
				latest.AutoRetryCount++
			}
			if latest.Root.Zero() && !cmd.Status.Root.Zero() {
				latest.Root = cmd.Status.Root
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		if updatedStatus != nil && updatedStatus.Status == core.Queued {
			return &RetriedRun{
				DAGRun: updatedStatus.DAGRun(),
				Status: updatedStatus,
				RollbackToken: RetryRollbackToken{
					dagRun:         updatedStatus.DAGRun(),
					queuedStatus:   *updatedStatus,
					previousStatus: previousStatus,
				},
			}, nil
		}
		return nil, ErrRetryStaleLatest
	}

	return &RetriedRun{
		DAGRun: updatedStatus.DAGRun(),
		Status: updatedStatus,
		RollbackToken: RetryRollbackToken{
			dagRun:         updatedStatus.DAGRun(),
			queuedStatus:   *updatedStatus,
			previousStatus: previousStatus,
		},
	}, nil
}

func (s *Service) undoRetryRun(ctx context.Context, cmd UndoRetryRunCommand) error {
	if err := s.validateUndoRetryRun(cmd); err != nil {
		return err
	}
	_, _, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.RollbackToken.dagRun,
		cmd.RollbackToken.queuedStatus.AttemptID,
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = cmd.RollbackToken.previousStatus.Status
			latest.QueuedAt = cmd.RollbackToken.previousStatus.QueuedAt
			latest.TriggerType = cmd.RollbackToken.previousStatus.TriggerType
			latest.AutoRetryCount = cmd.RollbackToken.previousStatus.AutoRetryCount
			return nil
		},
	)
	return err
}

func (s *Service) validateRetryRun(cmd RetryRunCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if cmd.Status == nil {
		return fmt.Errorf("status is required")
	}
	if err := validateDAGRunRef(cmd.Status.DAGRun()); err != nil {
		return err
	}
	if cmd.Status.AttemptID == "" {
		return fmt.Errorf("attempt ID is required")
	}
	return nil
}

func (s *Service) validateUndoRetryRun(cmd UndoRetryRunCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if err := validateDAGRunRef(cmd.RollbackToken.dagRun); err != nil {
		return err
	}
	if cmd.RollbackToken.queuedStatus.AttemptID == "" {
		return fmt.Errorf("queued attempt ID is required")
	}
	return nil
}
