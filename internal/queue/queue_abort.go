// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGRunNotQueuedError reports that the latest visible attempt is no longer queued.
type DAGRunNotQueuedError struct {
	Status    ir.Status
	HasStatus bool
}

func (e *DAGRunNotQueuedError) Error() string {
	if e == nil || !e.HasStatus {
		return "dag-run is not queued"
	}
	return fmt.Sprintf("dag-run is not queued: %s", e.Status)
}

// AbortQueuedDAGRun marks the latest visible queued attempt as aborted, hides it,
// and removes the dag-run record only when no visible attempts remain.
func AbortQueuedDAGRun(ctx context.Context, dagRunStore *dagrun.Repository, dagRun ir.DAGRunRef) error {
	attempt, err := dagRunStore.FindAttempt(ctx, dagRun)
	if err != nil {
		return err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return err
	}
	if status == nil || status.Status != ir.Queued {
		return newDAGRunNotQueuedError(status)
	}

	finishedAt := time.Now().UTC().Format(time.RFC3339)
	currentStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		attempt.ID(),
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Aborted
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
		return newDAGRunNotQueuedError(currentStatus)
	}

	if err := attempt.Hide(ctx); err != nil {
		logger.Warn(ctx, "Queued DAG-run was aborted but hiding the attempt failed",
			tag.DAG(dagRun.Name),
			tag.RunID(dagRun.ID),
			tag.AttemptID(attempt.ID()),
			tag.Error(err),
		)
		return fmt.Errorf("hide aborted attempt: %w", err)
	}

	_, err = dagRunStore.FindAttempt(ctx, dagRun)
	if errors.Is(err, dagrun.ErrNoStatusData) {
		if err := dagRunStore.RemoveDAGRun(ctx, dagRun); err != nil {
			return fmt.Errorf("remove empty dag-run record: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}

	return nil
}

func newDAGRunNotQueuedError(status *ir.DAGRunStatus) *DAGRunNotQueuedError {
	if status == nil {
		return &DAGRunNotQueuedError{}
	}
	return &DAGRunNotQueuedError{
		Status:    status.Status,
		HasStatus: true,
	}
}
