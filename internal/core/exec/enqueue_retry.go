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

const retryEnqueueRollbackTimeout = 10 * time.Second

// ErrRetryStaleLatest indicates the retry target changed before it could be enqueued.
var ErrRetryStaleLatest = errors.New("retry target changed before it could be enqueued")

// EnqueueRetryOptions configure how queued retry metadata is persisted.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
}

// EnqueueRetry enqueues a DAG run for retry and persists the Queued status.
// It persists the Queued status first, then enqueues, so the queue processor
// always sees the correct status when it picks up the item. If enqueue fails,
// the status is rolled back. Retries respect global queue capacity because
// the queue processor picks them up when capacity is available. Queued retries
// of sub-DAG attempts are not supported.
func EnqueueRetry(
	ctx context.Context,
	dagRunStore DAGRunStore,
	queueStore QueueStore,
	dag *core.DAG,
	status *DAGRunStatus,
	opts EnqueueRetryOptions,
) error {
	if dagRunStore == nil {
		return errors.New("enqueue retry: DAG-run store is not configured")
	}
	if queueStore == nil {
		return errors.New("enqueue retry: queue store is not configured")
	}
	if status == nil {
		return errors.New("enqueue retry: DAG-run status is nil")
	}
	if status.Status == core.Queued {
		return ErrRetryStaleLatest
	}

	dagRun := status.DAGRun()
	if !status.Root.Zero() && status.Root != dagRun {
		return errors.New("enqueue retry: queued sub-DAG retries are not supported")
	}
	var originalStatus DAGRunStatus
	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *DAGRunStatus) error {
			originalStatus = *latest
			latest.Status = core.Queued
			latest.QueuedAt = stringutil.FormatTime(time.Now())
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
		WithCompareAndSwapExpectedAttemptKey(status.AttemptKey),
	)
	if err != nil {
		return fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		return ErrRetryStaleLatest
	}

	// Enqueue after status is persisted. If this fails, roll back the status.
	procGroup := retryProcGroup(dag, updatedStatus)
	var enqueueErr error
	if procGroup == "" {
		enqueueErr = errors.New("proc group is empty")
	} else {
		enqueueErr = queueStore.Enqueue(ctx, procGroup, QueuePriorityLow, dagRun)
	}
	if enqueueErr != nil {
		enqueueErr = fmt.Errorf("enqueue retry: %w", enqueueErr)
		if rollbackErr := rollbackQueuedRetry(ctx, dagRunStore, dagRun, updatedStatus, &originalStatus); rollbackErr != nil {
			return errors.Join(enqueueErr, rollbackErr)
		}
		return enqueueErr
	}

	return nil
}

func rollbackQueuedRetry(
	ctx context.Context,
	dagRunStore DAGRunStore,
	dagRun DAGRunRef,
	queued *DAGRunStatus,
	original *DAGRunStatus,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retryEnqueueRollbackTimeout)
	defer cancel()

	_, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		rollbackCtx,
		dagRun,
		queued.AttemptID,
		core.Queued,
		func(latest *DAGRunStatus) error {
			latest.Status = original.Status
			latest.QueuedAt = original.QueuedAt
			latest.TriggerType = original.TriggerType
			latest.AutoRetryCount = original.AutoRetryCount
			latest.Root = original.Root
			return nil
		},
		WithCompareAndSwapExpectedAttemptKey(queued.AttemptKey),
	)
	if err != nil {
		return fmt.Errorf("rollback queued retry status: %w", err)
	}
	if !swapped {
		return errors.New("rollback queued retry status: DAG-run state changed")
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
