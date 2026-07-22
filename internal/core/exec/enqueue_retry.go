// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/uuid"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var (
	ErrRetryStaleLatest        = errors.New("retry target is no longer the latest attempt")
	errRetryQueueIntentChanged = errors.New("retry queue intent changed")
)

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

// EnqueueRetry records and publishes a durable DAG-run retry intent.
// Retries respect global queue capacity because the queue processor picks them
// up when capacity is available.
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
	dagRun := status.DAGRun()
	if status.Status == core.Queued {
		latest, err := retryStatusSnapshot(ctx, dagRunStore, dagRun)
		if err != nil {
			return fmt.Errorf("read queued retry status: %w", err)
		}
		if latest.Status != core.Queued || !sameRetryAttempt(status, latest) {
			return ErrRetryStaleLatest
		}
		if latest.RetryQueueKey == "" || latest.RetryQueuePublished {
			return nil
		}
		return publishRetryQueueIntent(ctx, dagRunStore, queueStore, nil, latest, opts.OnQueued)
	}

	retryQueueKey, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("create retry queue key: %w", err)
	}
	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *DAGRunStatus) error {
			now := time.Now().UTC()
			if latest.ProcGroup == "" {
				latest.ProcGroup = retryProcGroup(dag, latest)
			}
			latest.Status = core.Queued
			latest.QueuedAt = now.Format(time.RFC3339Nano)
			latest.RetryQueueKey = retryQueueKey.String()
			latest.RetryQueuePublished = false
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
		if updatedStatus != nil && updatedStatus.Status == core.Queued {
			if !sameRetryAttempt(status, updatedStatus) {
				return ErrRetryStaleLatest
			}
			if updatedStatus.RetryQueueKey == "" || updatedStatus.RetryQueuePublished {
				return nil
			}
			return publishRetryQueueIntent(ctx, dagRunStore, queueStore, nil, updatedStatus, opts.OnQueued)
		}
		return ErrRetryStaleLatest
	}

	return publishRetryQueueIntent(ctx, dagRunStore, queueStore, dag, updatedStatus, opts.OnQueued)
}

func sameRetryAttempt(expected, current *DAGRunStatus) bool {
	if expected == nil || current == nil || expected.AttemptID != current.AttemptID {
		return false
	}
	return expected.AttemptKey == current.AttemptKey
}

func retryStatusSnapshot(ctx context.Context, dagRunStore DAGRunStore, dagRun DAGRunRef) (*DAGRunStatus, error) {
	attempt, err := dagRunStore.FindAttempt(ctx, dagRun)
	if err != nil {
		return nil, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, ErrNoStatusData
	}
	snapshot := *status
	return &snapshot, nil
}

func publishRetryQueueIntent(
	ctx context.Context,
	dagRunStore DAGRunStore,
	queueStore QueueStore,
	dag *core.DAG,
	status *DAGRunStatus,
	onQueued func(*DAGRunStatus) error,
) error {
	if status == nil || status.Status != core.Queued || status.RetryQueueKey == "" {
		return ErrRetryStaleLatest
	}
	if status.RetryQueuePublished {
		return nil
	}
	procGroup := retryProcGroup(dag, status)
	if procGroup == "" {
		return errors.New("enqueue retry: proc group is empty")
	}
	if err := queueStore.EnsureEnqueued(
		ctx,
		procGroup,
		QueuePriorityLow,
		status.DAGRun(),
		status.RetryQueueKey,
	); err != nil {
		return fmt.Errorf("enqueue retry: %w", err)
	}

	published, err := markRetryQueuePublished(ctx, dagRunStore, status)
	if err != nil {
		return fmt.Errorf("mark retry queue publication: %w", err)
	}
	if onQueued != nil {
		return onQueued(published)
	}
	return nil
}

// PublishRetryQueueIntent publishes an existing durable retry intent.
func PublishRetryQueueIntent(
	ctx context.Context,
	dagRunStore DAGRunStore,
	queueStore QueueStore,
	status *DAGRunStatus,
) error {
	if status == nil || status.Status != core.Queued || status.RetryQueueKey == "" || status.RetryQueuePublished {
		return nil
	}
	return publishRetryQueueIntent(ctx, dagRunStore, queueStore, nil, status, nil)
}

func markRetryQueuePublished(
	ctx context.Context,
	dagRunStore DAGRunStore,
	status *DAGRunStatus,
) (*DAGRunStatus, error) {
	if status.RetryQueuePublished {
		return status, nil
	}
	queueKey := status.RetryQueueKey
	updated, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		core.Queued,
		func(latest *DAGRunStatus) error {
			if latest.RetryQueueKey != queueKey {
				return errRetryQueueIntentChanged
			}
			latest.RetryQueuePublished = true
			return nil
		},
		WithCompareAndSwapExpectedAttemptKey(status.AttemptKey),
	)
	if err != nil {
		if errors.Is(err, errRetryQueueIntentChanged) {
			return nil, ErrRetryStaleLatest
		}
		return nil, err
	}
	if swapped {
		return updated, nil
	}
	if updated != nil && updated.Status != core.Queued {
		return updated, nil
	}
	if updated != nil && updated.RetryQueueKey == queueKey && updated.RetryQueuePublished {
		return updated, nil
	}
	return nil, ErrRetryStaleLatest
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
