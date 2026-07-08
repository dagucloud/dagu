// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (s *Service) retryRun(ctx context.Context, cmd RetryRunCommand) error {
	if cmd.Status.Status == core.Queued {
		return nil
	}

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
		return fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		if updatedStatus != nil && updatedStatus.Status == core.Queued {
			return nil
		}
		return exec.ErrRetryStaleLatest
	}

	dagRun := cmd.Status.DAGRun()
	procGroup := retryProcGroup(cmd.DAG, updatedStatus)
	if procGroup == "" {
		_, _, _ = s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
			ctx,
			dagRun,
			updatedStatus.AttemptID,
			core.Queued,
			func(latest *exec.DAGRunStatus) error {
				latest.Status = cmd.Status.Status
				latest.QueuedAt = cmd.Status.QueuedAt
				latest.TriggerType = cmd.Status.TriggerType
				latest.AutoRetryCount = cmd.Status.AutoRetryCount
				return nil
			},
		)
		return errors.New("enqueue retry: proc group is empty")
	}
	if err := s.cfg.QueueStore.Enqueue(ctx, procGroup, exec.QueuePriorityLow, dagRun); err != nil {
		_, _, _ = s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
			ctx,
			dagRun,
			updatedStatus.AttemptID,
			core.Queued,
			func(latest *exec.DAGRunStatus) error {
				latest.Status = cmd.Status.Status
				latest.QueuedAt = cmd.Status.QueuedAt
				latest.TriggerType = cmd.Status.TriggerType
				latest.AutoRetryCount = cmd.Status.AutoRetryCount
				return nil
			},
		)
		return fmt.Errorf("enqueue retry: %w", err)
	}

	if cmd.Options.OnQueued != nil {
		if err := cmd.Options.OnQueued(updatedStatus); err != nil {
			return err
		}
	}

	return nil
}

func retryProcGroup(dag *core.DAG, status *exec.DAGRunStatus) string {
	if status != nil && status.ProcGroup != "" {
		return status.ProcGroup
	}
	if dag != nil {
		return dag.ProcGroup()
	}
	if status != nil {
		return status.Name
	}
	return ""
}
