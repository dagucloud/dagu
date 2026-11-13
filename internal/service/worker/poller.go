// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	taskClaimAckRetryInterval = 100 * time.Millisecond
	taskClaimAckRetryTimeout  = 30 * time.Second
)

// Poller handles polling for tasks from the coordinator
type Poller struct {
	workerID       string
	coordinatorCli coordinator.Client
	handler        TaskHandler
	index          int
	labels         map[string]string
	// capabilities are the capabilities that the worker announces (Spec 094).
	capabilities []string
	// onRunEnd makes the poller take one run (Spec 092, agent worker). After the
	// first acknowledged claim, the poller runs the task, calls onRunEnd with the
	// result of the final report, and polls no more. A claim that the owner
	// rejects before execution does not count.
	onRunEnd func(error)
}

// NewPoller creates a new poller instance
func NewPoller(workerID string, coordinatorCli coordinator.Client, handler TaskHandler, index int, labels map[string]string) *Poller {
	return &Poller{
		workerID:       workerID,
		coordinatorCli: coordinatorCli,
		handler:        handler,
		index:          index,
		labels:         labels,
	}
}

// Run starts the polling loop
func (p *Poller) Run(ctx context.Context) {
	// Set up retry policy for poll failures only
	basePolicy := backoff.NewExponentialBackoffPolicy(time.Second)
	basePolicy.BackoffFactor = 2.0
	basePolicy.MaxInterval = time.Minute
	basePolicy.MaxRetries = 0 // Retry indefinitely

	policy := backoff.WithJitter(basePolicy, backoff.FullJitter)

	for {
		select {
		case <-ctx.Done():
			logger.Debug(ctx, "Poller stopping due to context cancellation",
				tag.WorkerID(p.workerID),
				tag.PollerIndex(p.index))
			return
		default:
			// Poll for a task
			task, err := p.pollForTask(ctx, policy)
			if err != nil {
				// Context canceled, exit gracefully
				if ctx.Err() != nil {
					return
				}
				// Poll failed, but will be retried by pollForTask
				continue
			}

			// If we got a task, execute it
			if task != nil {
				logger.Info(ctx, "Task received, starting execution",
					tag.WorkerID(p.workerID),
					tag.PollerIndex(p.index),
					tag.RunID(task.DagRunId))

				// The worker refuses a task that its labels do not match (Spec
				// 092, "Match"). The coordinator applies the same rule first.
				if !ir.WorkerLabelsMatchSelector(p.labels, task.WorkerSelector) ||
					!ir.WorkerHasCapabilities(p.capabilities, task.RequiredCapabilities) {
					logger.Error(ctx, "Refusing a task whose worker selector does not match the labels of the worker",
						tag.WorkerID(p.workerID),
						tag.PollerIndex(p.index),
						tag.RunID(task.DagRunId),
						slog.Any("worker-selector", task.WorkerSelector),
						slog.Any("required-capabilities", task.RequiredCapabilities))
					continue
				}

				if err := p.ackTaskClaim(ctx, task); err != nil {
					logger.Error(ctx, "Failed to acknowledge claimed task",
						tag.WorkerID(p.workerID),
						tag.PollerIndex(p.index),
						tag.RunID(task.DagRunId),
						tag.Error(err))
					continue
				}

				if p.onRunEnd != nil {
					if p.runOnce(ctx, task) {
						return
					}
					continue
				}

				// Execute the task using the TaskHandler
				err := p.handler.Handle(ctx, task)
				if err != nil {
					if ctx.Err() != nil {
						// Context cancelled, exit gracefully
						return
					}
					if errors.Is(err, errTaskClaimRejectedBeforeExecution) {
						logger.Info(ctx, "Task claim rejected before execution",
							tag.WorkerID(p.workerID),
							tag.PollerIndex(p.index),
							tag.RunID(task.DagRunId))
						continue
					}
					logger.Error(ctx, "Task execution failed",
						tag.WorkerID(p.workerID),
						tag.PollerIndex(p.index),
						tag.RunID(task.DagRunId),
						tag.Error(err))
				} else {
					logger.Info(ctx, "Task execution completed successfully",
						tag.WorkerID(p.workerID),
						tag.PollerIndex(p.index),
						tag.RunID(task.DagRunId))
				}
			}
			// Continue polling for the next task
		}
	}
}

// runOnce runs the one task of an agent worker. It returns false when the owner
// rejected the claim before execution, so the poller may take another claim.
func (p *Poller) runOnce(ctx context.Context, task *coordinatorv1.Task) bool {
	report := newFinalReport(task.DagRunId)
	err := p.handler.Handle(withFinalReport(ctx, report), task)
	if errors.Is(err, errTaskClaimRejectedBeforeExecution) {
		logger.Info(ctx, "Task claim rejected before execution",
			tag.WorkerID(p.workerID),
			tag.PollerIndex(p.index),
			tag.RunID(task.DagRunId))
		return ctx.Err() != nil
	}
	result := report.result()
	if result != nil {
		logger.Error(ctx, "Agent worker run ended without a final report",
			tag.WorkerID(p.workerID),
			tag.RunID(task.DagRunId),
			tag.Error(result))
	} else {
		logger.Info(ctx, "Agent worker run ended; the worker takes no other run",
			tag.WorkerID(p.workerID),
			tag.RunID(task.DagRunId))
	}
	p.onRunEnd(result)
	return true
}

func (p *Poller) ackTaskClaim(ctx context.Context, task *coordinatorv1.Task) error {
	if task == nil || task.ClaimToken == "" {
		return nil
	}

	owner, err := taskOwner(task)
	if err != nil {
		return err
	}
	if owner.Host == "" {
		return fmt.Errorf("claimed task is missing owner coordinator metadata")
	}

	req := &coordinatorv1.AckTaskClaimRequest{
		WorkerId:   p.workerID,
		ClaimToken: task.ClaimToken,
		AttemptKey: task.AttemptKey,
	}
	var resp *coordinatorv1.AckTaskClaimResponse
	retryCtx, cancel := context.WithTimeout(ctx, taskClaimAckRetryTimeout)
	defer cancel()
	err = backoff.Retry(retryCtx, func(ctx context.Context) error {
		var callErr error
		resp, callErr = p.coordinatorCli.AckTaskClaimTo(ctx, owner, req)
		return callErr
	}, backoff.NewConstantBackoffPolicy(taskClaimAckRetryInterval), isRetryableTaskClaimAckError)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("received nil ack response")
	}
	if !resp.Accepted {
		return fmt.Errorf("task claim rejected: %s", resp.Error)
	}
	return nil
}

func isRetryableTaskClaimAckError(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}

// pollForTask polls the coordinator for a task with retry on failure
func (p *Poller) pollForTask(ctx context.Context, policy backoff.RetryPolicy) (*coordinatorv1.Task, error) {
	pollerID := uuid.New().String()

	// Get current coordinator client state before polling
	beforeMetrics := p.coordinatorCli.Metrics()

	req := &coordinatorv1.PollRequest{
		WorkerId: p.workerID,
		PollerId: pollerID,
		Labels:   p.labels,

		Capabilities: p.capabilities,
	}

	// Use coordinator client's Poll method which handles retries and failover
	task, err := p.coordinatorCli.Poll(ctx, policy, req)
	if err != nil {
		// Get updated metrics after failure
		afterMetrics := p.coordinatorCli.Metrics()

		// Check if this was first failure after being connected
		if beforeMetrics.IsConnected && !afterMetrics.IsConnected {
			// First failure after being connected
			logger.Error(ctx, "Poll failed - lost connection to coordinator",
				tag.Error(err),
				tag.WorkerID(p.workerID),
				tag.PollerID(pollerID),
				tag.PollerIndex(p.index))
		} else {
			// Subsequent failures
			logger.Debug(ctx, "Poll still failing",
				tag.Error(err),
				tag.WorkerID(p.workerID),
				tag.PollerID(pollerID),
				tag.PollerIndex(p.index),
				slog.Int("consecutive-failures", afterMetrics.ConsecutiveFails))
		}
		return nil, err
	}

	// Success - check if we recovered from disconnection
	afterMetrics := p.coordinatorCli.Metrics()
	if !beforeMetrics.IsConnected && afterMetrics.IsConnected && beforeMetrics.ConsecutiveFails > 0 {
		// Recovered from disconnection
		logger.Info(ctx, "Poll succeeded - reconnected to coordinator",
			tag.WorkerID(p.workerID),
			tag.PollerID(pollerID),
			tag.PollerIndex(p.index),
			slog.Int("previous-consecutive-failures", beforeMetrics.ConsecutiveFails))
	}

	// Handle the received task
	if task != nil {
		logger.Info(ctx, "Task received",
			tag.WorkerID(p.workerID),
			tag.PollerID(pollerID),
			tag.PollerIndex(p.index),
			slog.String("root-dag-run-name", task.RootDagRunName),
			slog.String("root-dag-run-id", task.RootDagRunId),
			slog.String("parent-dag-run-name", task.ParentDagRunName),
			slog.String("parent-dag-run-id", task.ParentDagRunId),
			tag.RunID(task.DagRunId))
	}

	return task, nil
}

// GetState returns the current connection state (for monitoring/testing)
func (p *Poller) GetState() (isConnected bool, consecutiveFails int, lastError error) {
	metrics := p.coordinatorCli.Metrics()
	return metrics.IsConnected, metrics.ConsecutiveFails, metrics.LastError
}
