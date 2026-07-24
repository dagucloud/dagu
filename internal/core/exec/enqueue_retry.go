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

// EnqueueRetryOptions configure a retry enqueue.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
}

// EnqueueRetry queues a DAG run for retry and records its Queued status.
// It restores the previous status if enqueueing fails and reports whether this
// call added the queue item. Queued sub-DAG retries are not supported.
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
	if isSubDAGRetry(status.Root, dagRun) {
		return false, errors.New("enqueue retry: queued sub-DAG retries are not supported")
	}

	var originalStatus *DAGRunStatus
	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *DAGRunStatus) error {
			if isSubDAGRetry(latest.Root, dagRun) {
				return errors.New("queued sub-DAG retries are not supported")
			}
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
		WithCompareAndSwapExpectedAttemptKey(status.AttemptKey),
	)
	if err != nil {
		return false, fmt.Errorf("persist queued retry status: %w", err)
	}
	if !swapped {
		if updatedStatus != nil &&
			updatedStatus.AttemptID == status.AttemptID &&
			updatedStatus.AttemptKey == status.AttemptKey &&
			updatedStatus.Status == core.Queued {
			return false, nil
		}
		return false, ErrRetryStaleLatest
	}

	procGroup := retryProcGroup(dag, updatedStatus)
	var enqueueErr error
	if procGroup == "" {
		enqueueErr = errors.New("proc group is empty")
	} else {
		enqueueErr = queueStore.Enqueue(ctx, procGroup, QueuePriorityLow, dagRun)
	}
	if enqueueErr != nil {
		enqueueErr = fmt.Errorf("enqueue retry: %w", enqueueErr)
		if rollbackErr := rollbackQueuedRetry(ctx, dagRunStore, dagRun, updatedStatus, originalStatus); rollbackErr != nil {
			return false, errors.Join(enqueueErr, rollbackErr)
		}
		return false, enqueueErr
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

func isSubDAGRetry(root, dagRun DAGRunRef) bool {
	return !root.Zero() && root != dagRun
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
