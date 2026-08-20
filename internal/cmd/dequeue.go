// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/spf13/cobra"
)

func Dequeue() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "dequeue [flags] <queue-name>",
			Short: "Dequeue a DAG-run from the specified queue",
			Long: `Dequeue a DAG-run from the queue.

Example:
	dagu dequeue default --dag-run=dag_name:my_dag_run_id
	dagu dequeue default
`,
			Args: cobra.ExactArgs(1),
		}, dequeueFlags, runDequeue,
	)
}

var dequeueFlags = []commandLineFlag{paramsFlag, dagRunFlagDequeue}

func runDequeue(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunDequeue(ctx, args)
	}
	requestedQueueName := args[0]

	// Get dag-run reference from the context
	dagRunRef, _ := ctx.StringParam("dag-run")
	if dagRunRef == "" {
		return dequeueFirst(ctx, requestedQueueName)
	}

	dagRun, err := ir.ParseDAGRunRef(dagRunRef)
	if err != nil {
		return fmt.Errorf("failed to parse dag-run reference %s: %w", dagRunRef, err)
	}
	return dequeueQueuedDAGRun(ctx, requestedQueueName, dagRun)
}

// dequeueFirst dequeues the first DAG run from the named queue and processes that run as aborted.
//
// It returns an error if queues are disabled, if removing an item from the queue fails,
// if the queue is empty, if retrieving the dequeued item's DAG-run data fails, or if
// processing the dequeued DAG run fails.
func dequeueFirst(ctx *Context, queueName string) error {
	// Check if queues are enabled
	if !ctx.Config.Queues.Enabled {
		return fmt.Errorf("queues are disabled in configuration")
	}
	for {
		result, err := ctx.Persistence.QueueStore.ListCursor(ctx.Context, queueName, "", 1)
		if err != nil {
			return fmt.Errorf("failed to list queue %s: %w", queueName, err)
		}
		if len(result.Items) == 0 {
			return fmt.Errorf("no dag-run found in queue %s", queueName)
		}

		item := result.Items[0]
		data, err := item.Data()
		if err != nil {
			if _, deleteErr := ctx.Persistence.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); deleteErr != nil {
				return fmt.Errorf("failed to discard unreadable queue head: %w", deleteErr)
			}
			continue
		}

		err = withQueueProcLock(ctx, queueName, func() error {
			if err := queue.AbortQueuedDAGRun(ctx.Context, ctx.Persistence.DAGRunRepository, *data); err != nil {
				return err
			}
			if _, err := ctx.Persistence.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); err != nil {
				return fmt.Errorf("failed to delete dequeued queue item: %w", err)
			}
			return nil
		})
		if err != nil {
			if isQueueAbortSkippable(err) {
				if _, deleteErr := ctx.Persistence.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); deleteErr != nil {
					return fmt.Errorf("failed to discard stale queue head: %w", deleteErr)
				}
				continue
			}
			return mapAbortQueuedDAGRunError(*data, err)
		}

		logger.Info(ctx.Context, "Dequeued dag-run",
			tag.DAG(data.Name),
			tag.RunID(data.ID),
			tag.Queue(queueName),
		)

		return nil
	}
}

// dequeueQueuedDAGRun aborts a queued dag-run and removes its queue entries.
func dequeueQueuedDAGRun(ctx *Context, requestedQueueName string, dagRun ir.DAGRunRef) error {
	// Check if queues are enabled
	if !ctx.Config.Queues.Enabled {
		return fmt.Errorf("queues are disabled in configuration")
	}

	actualQueueName, err := queueNameForDAGRun(ctx, dagRun)
	if err != nil {
		if isQueueLookupFallbackAllowed(err) {
			removed, fallbackErr := removeQueuedDAGRunByQueueName(ctx, requestedQueueName, dagRun)
			if fallbackErr != nil {
				return fallbackErr
			}
			if removed {
				logger.Info(ctx.Context, "Removed orphaned queued dag-run",
					tag.DAG(dagRun.Name),
					tag.RunID(dagRun.ID),
					tag.Queue(requestedQueueName),
				)
				return nil
			}
		}
		return mapAbortQueuedDAGRunError(dagRun, err)
	}

	err = withQueueProcLock(ctx, actualQueueName, func() error {
		if err := queue.AbortQueuedDAGRun(ctx.Context, ctx.Persistence.DAGRunRepository, dagRun); err != nil {
			return err
		}
		if _, err := ctx.Persistence.QueueStore.DequeueByDAGRunID(ctx.Context, actualQueueName, dagRun); err != nil {
			if errors.Is(err, queue.ErrQueueItemNotFound) && actualQueueName == requestedQueueName {
				return nil
			}
			return fmt.Errorf("failed to dequeue dag-run %s from queue %s: %w", dagRun.ID, actualQueueName, err)
		}
		return nil
	})
	if err != nil {
		return mapAbortQueuedDAGRunError(dagRun, err)
	}

	logger.Info(ctx.Context, "Dequeued dag-run",
		tag.DAG(dagRun.Name),
		tag.RunID(dagRun.ID),
		tag.Queue(actualQueueName),
	)

	return nil
}

func removeQueuedDAGRunByQueueName(ctx *Context, queueName string, dagRun ir.DAGRunRef) (bool, error) {
	var removed bool
	err := withQueueProcLock(ctx, queueName, func() error {
		items, err := ctx.Persistence.QueueStore.DequeueByDAGRunID(ctx.Context, queueName, dagRun)
		if err != nil {
			if errors.Is(err, queue.ErrQueueItemNotFound) {
				return nil
			}
			return fmt.Errorf("failed to dequeue dag-run %s from queue %s: %w", dagRun.ID, queueName, err)
		}
		removed = len(items) > 0
		return nil
	})
	if err != nil {
		return false, mapAbortQueuedDAGRunError(dagRun, err)
	}
	return removed, nil
}

func queueNameForDAGRun(ctx *Context, dagRun ir.DAGRunRef) (string, error) {
	attempt, err := ctx.Persistence.DAGRunRepository.FindAttempt(ctx, dagRun)
	if err != nil {
		return "", err
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return "", fmt.Errorf("error reading DAG: %w", err)
	}

	return dag.ProcGroup(), nil
}

func withQueueProcLock(ctx *Context, queueName string, fn func() error) error {
	err := ctx.Persistence.ProcRepository.WithLock(ctx, queueName, fn)
	if persis.IsProcLockError(err) {
		return fmt.Errorf("failed to lock process group %s: %w", queueName, err)
	}
	return err
}

func mapAbortQueuedDAGRunError(dagRun ir.DAGRunRef, err error) error {
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
		return fmt.Errorf("failed to find the record for dag-run ID %s: %w", dagRun.ID, err)
	}

	if notQueuedErr, ok := errors.AsType[*queue.DAGRunNotQueuedError](err); ok {
		if notQueuedErr.HasStatus {
			return fmt.Errorf("dag-run %s is not in queued status but %s", dagRun.ID, notQueuedErr.Status)
		}
		return fmt.Errorf("dag-run %s is not in queued status", dagRun.ID)
	}

	return err
}

func isQueueAbortSkippable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
		return true
	}
	var notQueuedErr *queue.DAGRunNotQueuedError
	return errors.As(err, &notQueuedErr)
}

func isQueueLookupFallbackAllowed(err error) bool {
	return errors.Is(err, dagrun.ErrDAGRunIDNotFound) ||
		errors.Is(err, dagrun.ErrNoStatusData) ||
		errors.Is(err, dagrun.ErrCorruptedStatusData)
}
