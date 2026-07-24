// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var ErrRetryStaleLatest = errors.New("retry target is no longer the latest attempt")

const retryEnqueueRollbackTimeout = 10 * time.Second

// EnqueueRetryOptions configure a retry enqueue.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
}

// EnqueueRetry queues a DAG run for retry and records its Queued status.
// It restores the previous status if enqueueing fails and reports whether this
// call added the queue item.
func EnqueueRetry(
	ctx context.Context,
	dagRunStore DAGRunStore,
	queueStore QueueStore,
	dag *core.DAG,
	status *DAGRunStatus,
	opts EnqueueRetryOptions,
) (bool, error) {
	if dagRunStore == nil {
		return false, errors.New("enqueue retry: DAG-run store is not configured")
	}
	if queueStore == nil {
		return false, errors.New("enqueue retry: queue store is not configured")
	}
	if status == nil {
		return false, errors.New("enqueue retry: DAG-run status is nil")
	}
	if status.Status == core.Queued {
		return false, nil
	}

	dagRun := status.DAGRun()
	var originalStatus *DAGRunStatus
	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *DAGRunStatus) error {
			snapshot := *latest
			originalStatus = &snapshot
			now := time.Now()
			latest.Status = core.Queued
			latest.QueuedAt = stringutil.FormatTime(now)
			latest.Conditions = nil
			latest.TriggerType = core.TriggerTypeRetry
			if opts.AutoRetry {
				latest.AutoRetryCount++
			}
			if latest.Root.Zero() && !status.Root.Zero() {
				latest.Root = status.Root
			}
			return nil
		},
	)
	if err != nil {
		return false, fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		if updatedStatus != nil &&
			updatedStatus.AttemptID == status.AttemptID &&
			updatedStatus.Status == core.Queued {
			return false, nil
		}
		return false, ErrRetryStaleLatest
	}

	procGroup := retryProcGroup(dag, updatedStatus)
	if procGroup == "" {
		if err := rollbackQueuedRetry(ctx, dagRunStore, dagRun, updatedStatus, originalStatus); err != nil {
			return false, fmt.Errorf("enqueue retry: proc group is empty; rollback queued retry status: %w", err)
		}
		return false, errors.New("enqueue retry: proc group is empty")
	}
	if err := queueStore.Enqueue(ctx, procGroup, QueuePriorityLow, dagRun); err != nil {
		if rollbackErr := rollbackQueuedRetry(ctx, dagRunStore, dagRun, updatedStatus, originalStatus); rollbackErr != nil {
			return false, fmt.Errorf("enqueue retry: %w; rollback queued retry status: %w", err, rollbackErr)
		}
		return false, fmt.Errorf("enqueue retry: %w", err)
	}

	return true, nil
}

func rollbackQueuedRetry(
	ctx context.Context,
	dagRunStore DAGRunStore,
	dagRun DAGRunRef,
	queued *DAGRunStatus,
	original *DAGRunStatus,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retryEnqueueRollbackTimeout)
	defer cancel()
	_, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		queued.AttemptID,
		core.Queued,
		func(latest *DAGRunStatus) error {
			latest.Status = original.Status
			latest.QueuedAt = original.QueuedAt
			latest.Conditions = original.Conditions
			latest.TriggerType = original.TriggerType
			latest.AutoRetryCount = original.AutoRetryCount
			latest.Root = original.Root
			return nil
		},
	)
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("DAG-run state changed before queued retry status could be rolled back")
	}
	return nil
}

func retryProcGroup(dag *core.DAG, status *DAGRunStatus) string {
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
