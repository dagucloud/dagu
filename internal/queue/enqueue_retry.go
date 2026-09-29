// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var ErrRetryStaleLatest = errors.New("retry target is no longer the latest attempt")

const (
	retryEnqueueRollbackTimeout = 10 * time.Second
	sourceReleaseTimeout        = 2 * time.Second
	sourceReleasePollInterval   = 25 * time.Millisecond
)

// RunProcesses reports whether the execution that recorded an attempt still
// owns its dag-run.
type RunProcesses interface {
	IsAttemptAlive(ctx context.Context, procGroup string, dagRun ir.DAGRunRef, attemptID string) (bool, error)
}

// EnqueueRetryOptions configure a retry enqueue.
type EnqueueRetryOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries. These consume the
	// DAG-level retry budget at enqueue time.
	AutoRetry bool
	// TriggerActor replaces the attributable actor for a user-issued retry.
	// Nil preserves the actor already recorded on the run.
	TriggerActor *string
	// Processes verifies that the execution which produced the status released
	// the run. User retries wait briefly; automatic retries defer immediately.
	// Nil skips the check.
	Processes RunProcesses
}

// awaitSourceRelease reports whether the execution that recorded status has
// released the dag-run. Its final write could otherwise overwrite the queued
// state. Automatic retries can defer to the next scan instead of polling.
func awaitSourceRelease(
	ctx context.Context,
	processes RunProcesses,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	waitForRelease bool,
) (bool, error) {
	procGroup := retryProcGroup(dag, status)
	if processes == nil || procGroup == "" || status.AttemptID == "" {
		return true, nil
	}
	// Only a finished run has a closing write left to land.
	if status.Status == ir.NotStarted || status.Status.IsActive() {
		return true, nil
	}
	dagRun := status.DAGRun()
	if dagRun.ID == "" {
		return true, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, sourceReleaseTimeout)
	defer cancel()
	alive, err := processes.IsAttemptAlive(waitCtx, procGroup, dagRun, status.AttemptID)
	if err != nil {
		return false, fmt.Errorf("check whether previous dag-run %s is still finalizing: %w", dagRun, err)
	}
	if !alive {
		return true, nil
	}
	if !waitForRelease {
		return false, nil
	}

	ticker := time.NewTicker(sourceReleasePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return false, fmt.Errorf("previous dag-run %s is still finalizing: %w", dagRun, waitCtx.Err())
		case <-ticker.C:
		}
		alive, err = processes.IsAttemptAlive(waitCtx, procGroup, dagRun, status.AttemptID)
		if err != nil {
			return false, fmt.Errorf("check whether previous dag-run %s is still finalizing: %w", dagRun, err)
		}
		if !alive {
			return true, nil
		}
	}
}

// EnqueueRetry queues a DAG run for retry and records its Queued status.
// It restores the previous status if enqueueing fails and reports whether this
// call added the queue item.
func EnqueueRetry(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	queueStore QueueStore,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	opts EnqueueRetryOptions,
) (bool, error) {
	if dagRunRepository == nil {
		return false, errors.New("enqueue retry: DAG-run repository is not configured")
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
	released, err := awaitSourceRelease(ctx, opts.Processes, dag, status, !opts.AutoRetry)
	if err != nil {
		return false, fmt.Errorf("enqueue retry: %w", err)
	}
	if !released {
		return false, nil
	}

	dagRun := status.DAGRun()
	var originalStatus *ir.DAGRunStatus
	updatedStatus, swapped, err := dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		status.AttemptID,
		status.Status,
		func(latest *ir.DAGRunStatus) error {
			snapshot := *latest
			originalStatus = &snapshot
			now := time.Now()
			latest.Status = ir.Queued
			latest.QueuedAt = nextRetryQueuedAt(latest.QueuedAt, now)
			latest.Conditions = nil
			// The queued attempt belongs to no process or worker until the next
			// execution claims it; a stale owner would make it look abandoned.
			latest.WorkerID = ""
			latest.PID = 0
			latest.PIDStartedAt = 0
			latest.LeaseAt = 0
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
		}, persis.DAGRunCompareAndSwapOptions{},
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
	if rollbackErr := rollbackQueuedRetry(ctx, dagRunRepository, dagRun, updatedStatus, originalStatus); rollbackErr != nil {
		return false, fmt.Errorf("enqueue retry: %w; rollback queued retry status: %w", enqueueErr, rollbackErr)
	}
	return false, fmt.Errorf("enqueue retry: %w", enqueueErr)
}

func nextRetryQueuedAt(previous string, now time.Time) string {
	now = now.UTC()
	queuedAt := now.Format(time.RFC3339Nano)
	if queuedAt == previous {
		queuedAt = now.Add(time.Nanosecond).Format(time.RFC3339Nano)
	}
	return queuedAt
}

func rollbackQueuedRetry(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	dagRun ir.DAGRunRef,
	queued *ir.DAGRunStatus,
	original *ir.DAGRunStatus,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retryEnqueueRollbackTimeout)
	defer cancel()
	_, swapped, err := dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		queued.AttemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = original.Status
			latest.QueuedAt = original.QueuedAt
			latest.Conditions = original.Conditions
			latest.WorkerID = original.WorkerID
			latest.PID = original.PID
			latest.PIDStartedAt = original.PIDStartedAt
			latest.LeaseAt = original.LeaseAt
			latest.TriggerType = original.TriggerType
			latest.TriggerActor = original.TriggerActor
			latest.AutoRetryCount = original.AutoRetryCount
			latest.Root = original.Root
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
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
