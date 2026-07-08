// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
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

	dagRun, err := exec.ParseDAGRunRef(dagRunRef)
	if err != nil {
		return fmt.Errorf("failed to parse dag-run reference %s: %w", dagRunRef, err)
	}
	return dequeueQueuedDAGRun(ctx, requestedQueueName, dagRun)
}

// dequeueFirst dequeues the first DAG run from the named queue and records dispatch cancellation.
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
		result, err := ctx.QueueStore.ListCursor(ctx.Context, queueName, "", 1)
		if err != nil {
			return fmt.Errorf("failed to list queue %s: %w", queueName, err)
		}
		if len(result.Items) == 0 {
			return fmt.Errorf("no dag-run found in queue %s", queueName)
		}

		item := result.Items[0]
		data, err := item.Data()
		if err != nil {
			if _, deleteErr := ctx.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); deleteErr != nil {
				return fmt.Errorf("failed to discard unreadable queue head: %w", deleteErr)
			}
			continue
		}

		err = withQueueProcLock(ctx, queueName, func() error {
			if err := ctx.historyService().MarkDispatchCanceled(ctx.Context, history.MarkDispatchCanceledRequest{DAGRun: *data}); err != nil {
				return err
			}
			if _, err := ctx.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); err != nil {
				return fmt.Errorf("failed to delete dequeued queue item: %w", err)
			}
			return nil
		})
		if err != nil {
			if isDispatchCancelSkippable(err) {
				if _, deleteErr := ctx.QueueStore.DeleteByItemIDs(ctx.Context, queueName, []string{item.ID()}); deleteErr != nil {
					return fmt.Errorf("failed to discard stale queue head: %w", deleteErr)
				}
				continue
			}
			return mapDispatchCancelError(*data, err)
		}

		logger.Info(ctx.Context, "Dequeued dag-run",
			tag.DAG(data.Name),
			tag.RunID(data.ID),
			tag.Queue(queueName),
		)

		return nil
	}
}

// dequeueQueuedDAGRun records dispatch cancellation and removes queue entries.
func dequeueQueuedDAGRun(ctx *Context, requestedQueueName string, dagRun exec.DAGRunRef) error {
	// Check if queues are enabled
	if !ctx.Config.Queues.Enabled {
		return fmt.Errorf("queues are disabled in configuration")
	}

	actualQueueName, err := queueNameForDAGRun(ctx, dagRun)
	if err != nil {
		if isQueueNameLookupFallbackAllowed(err) {
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
		return mapDispatchCancelError(dagRun, err)
	}

	err = withQueueProcLock(ctx, actualQueueName, func() error {
		if err := ctx.historyService().MarkDispatchCanceled(ctx.Context, history.MarkDispatchCanceledRequest{DAGRun: dagRun}); err != nil {
			return err
		}
		if _, err := ctx.QueueStore.DequeueByDAGRunID(ctx.Context, actualQueueName, dagRun); err != nil {
			if errors.Is(err, exec.ErrQueueItemNotFound) && actualQueueName == requestedQueueName {
				return nil
			}
			return fmt.Errorf("failed to dequeue dag-run %s from queue %s: %w", dagRun.ID, actualQueueName, err)
		}
		return nil
	})
	if err != nil {
		return mapDispatchCancelError(dagRun, err)
	}

	logger.Info(ctx.Context, "Dequeued dag-run",
		tag.DAG(dagRun.Name),
		tag.RunID(dagRun.ID),
		tag.Queue(actualQueueName),
	)

	return nil
}

func removeQueuedDAGRunByQueueName(ctx *Context, queueName string, dagRun exec.DAGRunRef) (bool, error) {
	var removed bool
	err := withQueueProcLock(ctx, queueName, func() error {
		items, err := ctx.QueueStore.DequeueByDAGRunID(ctx.Context, queueName, dagRun)
		if err != nil {
			if errors.Is(err, exec.ErrQueueItemNotFound) {
				return nil
			}
			return fmt.Errorf("failed to dequeue dag-run %s from queue %s: %w", dagRun.ID, queueName, err)
		}
		removed = len(items) > 0
		return nil
	})
	if err != nil {
		return false, mapDispatchCancelError(dagRun, err)
	}
	return removed, nil
}

func queueNameForDAGRun(ctx *Context, dagRun exec.DAGRunRef) (string, error) {
	metadata, err := ctx.historyService().DispatchMetadata(ctx.Context, history.DispatchMetadataRequest{
		DAGRun: dagRun,
	})
	if err != nil {
		return "", err
	}
	return metadata.QueueName, nil
}

func withQueueProcLock(ctx *Context, queueName string, fn func() error) error {
	if err := ctx.ProcStore.Lock(ctx, queueName); err != nil {
		return fmt.Errorf("failed to lock process group %s: %w", queueName, err)
	}
	defer ctx.ProcStore.Unlock(ctx, queueName)

	return fn()
}

func mapDispatchCancelError(dagRun exec.DAGRunRef, err error) error {
	if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) {
		return fmt.Errorf("failed to find the record for dag-run ID %s: %w", dagRun.ID, err)
	}

	var notQueuedErr *history.RunNotPendingError
	if errors.As(err, &notQueuedErr) {
		if notQueuedErr.HasStatus {
			return fmt.Errorf("dag-run %s is not pending dispatch but %s", dagRun.ID, notQueuedErr.Status)
		}
		return fmt.Errorf("dag-run %s is not pending dispatch", dagRun.ID)
	}

	return err
}

func isDispatchCancelSkippable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) || errors.Is(err, exec.ErrCorruptedStatusFile) {
		return true
	}
	var notQueuedErr *history.RunNotPendingError
	return errors.As(err, &notQueuedErr)
}

func isQueueNameLookupFallbackAllowed(err error) bool {
	return errors.Is(err, exec.ErrDAGRunIDNotFound) ||
		errors.Is(err, exec.ErrNoStatusData) ||
		errors.Is(err, exec.ErrCorruptedStatusFile)
}
