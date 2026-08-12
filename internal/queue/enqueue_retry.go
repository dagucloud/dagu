// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var ErrRetryStaleLatest = errors.New("retry target is no longer the latest attempt")

const retryEnqueueRollbackTimeout = 10 * time.Second

// EnqueueRetryOptions configure a retry enqueue.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
	// TriggerActor replaces the attributable actor for a user-issued retry.
	// Nil preserves the actor already recorded on the run.
	TriggerActor *string
}

// EnqueueRetry queues a DAG run for retry and records its Queued status.
// It restores the previous status if enqueueing fails and reports whether this
// call added the queue item.
func EnqueueRetry(
	ctx context.Context,
	dagRunStore *dagrun.Repository,
	queueStore QueueStore,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
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
	if status.Status == ir.Queued {
		return false, nil
	}

	dagRun := status.DAGRun()
	var originalStatus *ir.DAGRunStatus
	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *ir.DAGRunStatus) error {
			snapshot := *latest
			originalStatus = &snapshot
			now := time.Now()
			latest.Status = ir.Queued
			latest.QueuedAt = stringutil.FormatTime(now)
			latest.Conditions = nil
			latest.TriggerType = ir.TriggerTypeRetry
			if opts.TriggerActor != nil {
				latest.TriggerActor = *opts.TriggerActor
			}
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
			updatedStatus.Status == ir.Queued {
			return false, nil
		}
		return false, ErrRetryStaleLatest
	}

	var enqueueErr error
	if procGroup := retryProcGroup(dag, updatedStatus); procGroup == "" {
		enqueueErr = errors.New("proc group is empty")
	} else {
		enqueueErr = queueStore.Enqueue(ctx, procGroup, QueuePriorityLow, dagRun)
	}
	if enqueueErr == nil {
		return true, nil
	}

	// The status swap above already published Queued, so every failure past
	// this point must restore the prior status.
	if rollbackErr := rollbackQueuedRetry(ctx, dagRunStore, dagRun, updatedStatus, originalStatus); rollbackErr != nil {
		return false, fmt.Errorf("enqueue retry: %w; rollback queued retry status: %w", enqueueErr, rollbackErr)
	}
	return false, fmt.Errorf("enqueue retry: %w", enqueueErr)
}

func rollbackQueuedRetry(
	ctx context.Context,
	dagRunStore *dagrun.Repository,
	dagRun ir.DAGRunRef,
	queued *ir.DAGRunStatus,
	original *ir.DAGRunStatus,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retryEnqueueRollbackTimeout)
	defer cancel()
	_, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		queued.AttemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = original.Status
			latest.QueuedAt = original.QueuedAt
			latest.Conditions = original.Conditions
			latest.TriggerType = original.TriggerType
			latest.TriggerActor = original.TriggerActor
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

func retryProcGroup(dag *ir.DAG, status *ir.DAGRunStatus) string {
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
