// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (s *Service) retryRun(ctx context.Context, cmd RetryRunCommand) (*RetriedRun, error) {
	if cmd.Status.Status == core.Queued {
		return &RetriedRun{
			DAGRun:         cmd.Status.DAGRun(),
			Status:         cmd.Status,
			PreviousStatus: *cmd.Status,
		}, nil
	}
	previousStatus := *cmd.Status

	updatedStatus, swapped, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.Status.DAGRun(),
		cmd.Status.AttemptID,
		cmd.Status.Status,
		func(latest *exec.DAGRunStatus) error {
			now := time.Now()
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
				DAGRun:         updatedStatus.DAGRun(),
				Status:         updatedStatus,
				PreviousStatus: previousStatus,
			}, nil
		}
		return nil, ErrRetryStaleLatest
	}

	return &RetriedRun{
		DAGRun:         updatedStatus.DAGRun(),
		Status:         updatedStatus,
		PreviousStatus: previousStatus,
	}, nil
}

func (s *Service) undoRetryRun(ctx context.Context, cmd UndoRetryRunCommand) error {
	if cmd.QueuedStatus == nil {
		return nil
	}
	_, _, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.DAGRun,
		cmd.QueuedStatus.AttemptID,
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = cmd.PreviousStatus.Status
			latest.QueuedAt = cmd.PreviousStatus.QueuedAt
			latest.TriggerType = cmd.PreviousStatus.TriggerType
			latest.AutoRetryCount = cmd.PreviousStatus.AutoRetryCount
			return nil
		},
	)
	return err
}
