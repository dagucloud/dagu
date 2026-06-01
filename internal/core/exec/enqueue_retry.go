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

// EnqueueRetryOptions control how queued retry metadata is persisted.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
	// OnQueued is called after the queued status and queue item are both durably written.
	// Errors from this callback are returned to the caller but do not roll back the
	// already-persisted queue item and status.
	OnQueued func(*DAGRunStatus) error
}

// EnqueueRetry enqueues a DAG run for retry and persists the Queued status.
// It persists the Queued status first, then enqueues, so the queue processor
// always sees the correct status when it picks up the item. If enqueue fails,
// the status is rolled back. Retries respect global queue capacity because
// the queue processor picks them up when capacity is available.
func EnqueueRetry(
	ctx context.Context,
	dagRunStore DAGRunStore,
	queueStore QueueStore,
	dag *core.DAG,
	status *DAGRunStatus,
	opts EnqueueRetryOptions,
) error {
	if status.Status == core.Queued {
		// Already queued (e.g. duplicate retry), treat as success.
		return nil
	}

	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		status.Status,
		func(latest *DAGRunStatus) error {
			latest.Status = core.Queued
			latest.QueuedAt = stringutil.FormatTime(time.Now())
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
		return fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		// Another actor changed the latest attempt first. Treat an already queued
		// retry as success; everything else is a no-op.
		if updatedStatus != nil && updatedStatus.Status == core.Queued {
			return nil
		}
		return ErrRetryStaleLatest
	}

	// Enqueue after status is persisted. If this fails, roll back the status.
	dagRun := status.DAGRun()
	procGroup := retryProcGroup(dag, updatedStatus)
	if procGroup == "" {
		_, _, _ = dagRunStore.CompareAndSwapLatestAttemptStatus(
			ctx,
			dagRun,
			updatedStatus.AttemptID,
			core.Queued,
			func(latest *DAGRunStatus) error {
				latest.Status = status.Status
				latest.QueuedAt = status.QueuedAt
				latest.TriggerType = status.TriggerType
				latest.AutoRetryCount = status.AutoRetryCount
				return nil
			},
		)
		return errors.New("enqueue retry: proc group is empty")
	}
	if err := queueStore.Enqueue(ctx, procGroup, QueuePriorityLow, dagRun); err != nil {
		_, _, _ = dagRunStore.CompareAndSwapLatestAttemptStatus(
			ctx,
			dagRun,
			updatedStatus.AttemptID,
			core.Queued,
			func(latest *DAGRunStatus) error {
				latest.Status = status.Status
				latest.QueuedAt = status.QueuedAt
				latest.TriggerType = status.TriggerType
				latest.AutoRetryCount = status.AutoRetryCount
				return nil
			},
		)
		return fmt.Errorf("enqueue retry: %w", err)
	}

	if opts.OnQueued != nil {
		if err := opts.OnQueued(updatedStatus); err != nil {
			return err
		}
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
