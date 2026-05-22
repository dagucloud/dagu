// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	osexec "os/exec"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

type queueDispatchDeps struct {
	queueStore          exec.QueueStore
	dagRunStore         exec.DAGRunStore
	procStore           exec.ProcStore
	dagRunLeaseStore    exec.DAGRunLeaseStore
	dispatchTaskStore   exec.DispatchTaskStore
	dagExecutor         *DAGExecutor
	isSuspended         IsSuspendedFunc
	backoffConfig       BackoffConfig
	leaseStaleThreshold time.Duration
	isClosed            func() bool
	wakeUp              func()
}

// queueDispatcher owns queue-item dispatch decisions after a queue has capacity.
type queueDispatcher struct {
	queueStore          exec.QueueStore
	dagRunStore         exec.DAGRunStore
	procStore           exec.ProcStore
	dagRunLeaseStore    exec.DAGRunLeaseStore
	dispatchTaskStore   exec.DispatchTaskStore
	dagExecutor         *DAGExecutor
	isSuspended         IsSuspendedFunc
	backoffConfig       BackoffConfig
	leaseStaleThreshold time.Duration
	isClosed            func() bool
	wakeUp              func()
}

type queueDispatchBatch struct {
	items          []exec.QueuedItemData
	maxConcurrency int
	aliveCount     int
}

func newQueueDispatcher(deps queueDispatchDeps) *queueDispatcher {
	if deps.isSuspended == nil {
		deps.isSuspended = func(context.Context, string) bool { return false }
	}
	if deps.isClosed == nil {
		deps.isClosed = func() bool { return false }
	}
	if deps.wakeUp == nil {
		deps.wakeUp = func() {}
	}
	return &queueDispatcher{
		queueStore:          deps.queueStore,
		dagRunStore:         deps.dagRunStore,
		procStore:           deps.procStore,
		dagRunLeaseStore:    deps.dagRunLeaseStore,
		dispatchTaskStore:   deps.dispatchTaskStore,
		dagExecutor:         deps.dagExecutor,
		isSuspended:         deps.isSuspended,
		backoffConfig:       deps.backoffConfig,
		leaseStaleThreshold: deps.leaseStaleThreshold,
		isClosed:            deps.isClosed,
		wakeUp:              deps.wakeUp,
	}
}

func (d *queueDispatcher) selectDispatchBatch(
	ctx context.Context,
	queueName string,
	items []exec.QueuedItemData,
	maxConcurrency int,
	inflightCount int,
) (queueDispatchBatch, error) {
	localAliveCount, err := d.procStore.CountAlive(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count alive processes", tag.Error(err), tag.Queue(queueName))
		return queueDispatchBatch{}, fmt.Errorf("count alive processes: %w", err)
	}

	distributedAliveCount, err := d.countActiveDistributedRuns(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count distributed leases", tag.Error(err), tag.Queue(queueName))
		return queueDispatchBatch{}, fmt.Errorf("count distributed leases: %w", err)
	}
	outstandingDispatchCount, err := d.countOutstandingDispatchReservations(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count outstanding distributed dispatch reservations", tag.Error(err), tag.Queue(queueName))
		return queueDispatchBatch{}, fmt.Errorf("count outstanding distributed dispatch reservations: %w", err)
	}
	aliveCount := localAliveCount + distributedAliveCount
	freeSlots := maxConcurrency - aliveCount - inflightCount - outstandingDispatchCount

	logger.Debug(ctx, "Queue capacity check",
		tag.MaxConcurrency(maxConcurrency),
		tag.Alive(aliveCount),
		slog.Int("outstanding-dispatches", outstandingDispatchCount),
		tag.Count(freeSlots),
	)

	if freeSlots <= 0 {
		logger.Debug(ctx, "Max concurrency reached",
			tag.MaxConcurrency(maxConcurrency),
			tag.Alive(aliveCount),
		)
		return queueDispatchBatch{}, nil
	}

	runnableItems, err := d.selectRunnableQueueItems(ctx, items, freeSlots)
	if err != nil {
		logger.Error(ctx, "Failed to select runnable queue items", tag.Error(err), tag.Queue(queueName))
		return queueDispatchBatch{}, fmt.Errorf("select runnable queue items: %w", err)
	}
	if len(runnableItems) == 0 {
		logger.Debug(ctx, "No queue items eligible for a new dispatch attempt")
		return queueDispatchBatch{}, nil
	}

	return queueDispatchBatch{
		items:          runnableItems,
		maxConcurrency: maxConcurrency,
		aliveCount:     aliveCount,
	}, nil
}

func (d *queueDispatcher) dispatchQueuedItem(ctx context.Context, item exec.QueuedItemData, queueName string, incInflight, decInflight func()) bool {
	if d.isClosed() {
		return false
	}

	data, err := item.Data()
	if err != nil {
		logger.Error(ctx, "Failed to get item data", tag.Error(err))
		return false
	}

	runRef := *data
	runID := runRef.ID
	ctx = logger.WithValues(ctx, tag.RunID(runID))
	logger.Debug(ctx, "Processing queue item", tag.Name(runRef.Name))

	running, err := d.procStore.IsRunAlive(ctx, queueName, runRef)
	if err != nil {
		logger.Error(ctx, "Failed to check if run is alive", tag.Error(err))
		return false
	}
	if running {
		logger.Warn(ctx, "DAG run is already running, discarding")
		return true
	}

	attempt, err := d.dagRunStore.FindAttempt(ctx, runRef)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) {
			logger.Error(ctx, "DAG run not found, discarding")
			return true
		}
		logger.Error(ctx, "Failed to find run", tag.Error(err))
		return false
	}

	if attempt.Hidden() {
		logger.Info(ctx, "DAG run is hidden, discarding")
		return true
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrCorruptedStatusFile) {
			logger.Error(ctx, "Status file is corrupted, marking as invalid", tag.Error(err))
			return true
		}
		logger.Error(ctx, "Failed to read status", tag.Error(err))
		return false
	}

	if status.Status != core.Queued {
		logger.Info(ctx, "Status is not queued, skipping", tag.Status(status.Status.String()))
		return true
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read DAG", tag.Error(err), tag.DAG(runRef.Name))
		return false
	}

	if isSchedulerManagedTriggerType(status.TriggerType) && isSuspendedDAG(ctx, d.isSuspended, status, dag) {
		if err := d.dropSuspendedQueuedRun(ctx, queueName, runRef, attempt.ID(), status); err != nil {
			logger.Error(ctx, "Failed to drop suspended queued DAG run", tag.Error(err))
		}
		return false
	}

	if schedTime, err := time.Parse(time.RFC3339, status.ScheduleTime); err == nil {
		if queueAge := time.Since(schedTime); queueAge > queueAgeWarningThreshold {
			logger.Warn(ctx, "Queued item has been waiting for dispatch",
				tag.DAG(runRef.Name),
				slog.Duration("queue_age", queueAge),
			)
		}
	}

	incInflight()
	defer decInflight()

	if d.dagExecutor.IsDistributed(dag) {
		return d.dispatchAndWaitForStartup(ctx, queueName, runRef, dag, runID, status)
	}

	execErrCh := make(chan error, 1)
	execDoneCh := make(chan struct{})
	var execDoneErr error
	go func() {
		defer d.wakeUp()
		err := d.dagExecutor.ExecuteDAG(ctx, dag, coordinatorv1.Operation_OPERATION_RETRY, runID, status, status.TriggerType, status.ScheduleTime)
		execDoneErr = err
		close(execDoneCh)
		if err != nil {
			logger.Error(ctx, "Failed to execute DAG", tag.Error(err))
			if isPreStartExecutionFailure(err) {
				select {
				case execErrCh <- err:
				default:
				}
			}
		}
	}()

	return d.waitForStartup(ctx, queueName, runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  execErrCh,
		execDone: func() (bool, error) {
			select {
			case <-execDoneCh:
				return true, execDoneErr
			default:
				return false, nil
			}
		},
	})
}

func (d *queueDispatcher) dropSuspendedQueuedRun(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	attemptID string,
	status *exec.DAGRunStatus,
) error {
	finishedAt := stringutil.FormatTime(time.Now().UTC())
	currentStatus, swapped, err := d.dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		runRef,
		attemptID,
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = core.Aborted
			latest.FinishedAt = finishedAt
			latest.Error = suspendedQueueDropReason
			latest.WorkerID = ""
			latest.PID = 0
			latest.PIDStartedAt = 0
			latest.LeaseAt = 0
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("abort suspended queued DAG run: %w", err)
	}

	if _, err := d.queueStore.DequeueByDAGRunID(ctx, queueName, runRef); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
		return fmt.Errorf("dequeue suspended queued DAG run: %w", err)
	}

	if swapped {
		logger.Info(ctx, "Dropped queued scheduler-managed run for suspended DAG",
			tag.Status(core.Aborted.String()),
			slog.String("trigger_type", status.TriggerType.String()),
		)
		return nil
	}

	logger.Info(ctx, "Removed stale queued scheduler-managed run for suspended DAG",
		slog.String("trigger_type", status.TriggerType.String()),
		slog.String("current_status", currentStatusString(currentStatus)),
	)
	return nil
}

func (d *queueDispatcher) dispatchAndWaitForStartup(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	dag *core.DAG,
	runID string,
	dagStatus *exec.DAGRunStatus,
) bool {
	policy := backoff.NewExponentialBackoffPolicy(d.backoffConfig.InitialInterval)
	policy.MaxInterval = d.backoffConfig.MaxInterval
	policy.MaxRetries = d.backoffConfig.MaxRetries
	retryCtx := backoff.WithRetryFailureLogLevel(ctx, slog.LevelInfo)

	launchedAt := time.Now()
	var started bool
	dispatched := false

	operation := func(ctx context.Context) error {
		if err := d.checkContextAndQuit(ctx); err != nil {
			return err
		}

		if !dispatched {
			err := d.dagExecutor.ExecuteDAG(ctx, dag, coordinatorv1.Operation_OPERATION_RETRY,
				runID, dagStatus, dagStatus.TriggerType, dagStatus.ScheduleTime)
			if err != nil {
				var staleErr *exec.StaleQueueDispatchError
				if errors.As(err, &staleErr) {
					return backoff.PermanentError(err)
				}
				if errors.Is(err, backoff.ErrPermanent) {
					logger.Error(ctx, "Permanent dispatch failure", tag.Error(err))
					return err
				}
				logger.Warn(ctx, "Transient dispatch failure, will retry", tag.Error(err))
				return err
			}
			dispatched = true
		}

		var err error
		started, err = d.checkStartupStatus(ctx, queueName, runRef, startupWaitState{
			launchedAt: launchedAt,
		})
		return err
	}

	if err := backoff.Retry(retryCtx, operation, policy, nil); err != nil {
		var staleErr *exec.StaleQueueDispatchError
		if errors.As(err, &staleErr) {
			logger.Info(ctx, "Discarding stale distributed queue dispatch",
				tag.DAG(runRef.Name),
				tag.RunID(runRef.ID),
				tag.Queue(queueName),
				tag.Error(staleErr),
			)
			return true
		}
		logger.Error(ctx, "Failed to dispatch DAG after retries", tag.Error(err))
	}

	defer d.wakeUp()
	return started
}

func (d *queueDispatcher) waitForStartup(ctx context.Context, queueName string, runRef exec.DAGRunRef, waitState startupWaitState) bool {
	policy := backoff.NewExponentialBackoffPolicy(d.backoffConfig.InitialInterval)
	policy.MaxInterval = d.backoffConfig.MaxInterval
	policy.MaxRetries = d.backoffConfig.MaxRetries
	if waitState.execDone != nil {
		policy.MaxRetries = 0
	}

	var started bool
	var startupObservationErrors int
	operation := func(ctx context.Context) error {
		var err error
		started, err = d.checkStartupStatus(ctx, queueName, runRef, waitState)
		if shouldBoundLocalStartupError(waitState, err) {
			startupObservationErrors++
			if d.backoffConfig.MaxRetries > 0 && startupObservationErrors > d.backoffConfig.MaxRetries {
				return backoff.PermanentError(err)
			}
		}
		return err
	}

	if err := backoff.Retry(ctx, operation, policy, nil); err != nil {
		logger.Error(ctx, "Failed to execute DAG after retries", tag.Error(err))
	}

	return started
}

func shouldBoundLocalStartupError(waitState startupWaitState, err error) bool {
	return waitState.execDone != nil &&
		err != nil &&
		!errors.Is(err, errNotStarted) &&
		!errors.Is(err, backoff.ErrPermanent)
}

func (d *queueDispatcher) checkStartupStatus(ctx context.Context, queueName string, runRef exec.DAGRunRef, waitState startupWaitState) (bool, error) {
	if err := d.checkContextAndQuit(ctx); err != nil {
		return false, err
	}
	if err := readStartupExecutionError(waitState.execErrCh); err != nil {
		logger.Warn(ctx, "DAG execution failed before startup was observed", tag.Error(err))
		return false, backoff.PermanentError(err)
	}

	isAlive, err := d.procStore.IsRunAlive(ctx, queueName, runRef)
	if err != nil {
		logger.Warn(ctx, "Failed to check run liveness", tag.Error(err), tag.Queue(queueName), tag.RunID(runRef.ID))
	} else if isAlive {
		logger.Info(ctx, "DAG run has started (heartbeat detected)")
		return true, nil
	}
	execDone, execDoneErr := waitState.executionDone()
	if d.inStartupGracePeriod(waitState.launchedAt) && d.dagRunLeaseStore == nil && !execDone {
		return false, errNotStarted
	}

	attempt, err := d.dagRunStore.FindAttempt(ctx, runRef)
	if err != nil {
		logger.Debug(ctx, "Failed to read attempt, keep checking")
		return false, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return false, err
	}

	if status.Status != core.Queued {
		logger.Info(ctx, "DAG execution has started or finished", tag.Status(status.Status.String()))
		return true, nil
	}
	if execDone {
		if execDoneErr != nil {
			return false, backoff.PermanentError(execDoneErr)
		}
		return false, backoff.PermanentError(errExecutionExitedBeforeStartup)
	}
	started, err := d.hasFreshDistributedLease(ctx, queueName, runRef, attempt, status)
	if err != nil {
		logger.Warn(ctx, "Failed to check distributed run lease",
			tag.Error(err),
			tag.Queue(queueName),
			tag.RunID(runRef.ID),
		)
	} else if started {
		logger.Info(ctx, "DAG run has started (distributed lease detected)")
		return true, nil
	}
	if d.inStartupGracePeriod(waitState.launchedAt) {
		return false, errNotStarted
	}
	if err != nil {
		return false, err
	}

	return false, errNotStarted
}

func (d *queueDispatcher) inStartupGracePeriod(launchedAt time.Time) bool {
	grace := d.backoffConfig.StartupGracePeriod
	return grace > 0 && time.Since(launchedAt) < grace
}

func (d *queueDispatcher) selectRunnableQueueItems(
	ctx context.Context,
	items []exec.QueuedItemData,
	freeSlots int,
) ([]exec.QueuedItemData, error) {
	if freeSlots <= 0 {
		return nil, nil
	}

	runnable := make([]exec.QueuedItemData, 0, min(freeSlots, len(items)))
	for _, item := range items {
		if len(runnable) >= freeSlots {
			break
		}
		if d.dispatchTaskStore != nil {
			runRef, err := item.Data()
			if err != nil {
				logger.Error(ctx, "Failed to get item data while selecting runnable queue items", tag.Error(err))
				continue
			}
			reserved, err := d.hasOutstandingDispatchReservation(ctx, *runRef)
			if err != nil {
				return nil, err
			}
			if reserved {
				logger.Debug(ctx, "Skipping queue item with outstanding distributed dispatch reservation",
					tag.RunID(runRef.ID),
				)
				continue
			}
		}
		runnable = append(runnable, item)
	}

	return runnable, nil
}

func (d *queueDispatcher) hasOutstandingDispatchReservation(ctx context.Context, runRef exec.DAGRunRef) (bool, error) {
	if d.dispatchTaskStore == nil {
		return false, nil
	}

	attempt, err := d.dagRunStore.FindAttempt(ctx, runRef)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) {
			return false, nil
		}
		return false, err
	}
	if attempt.Hidden() {
		return false, nil
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrNoStatusData) || errors.Is(err, exec.ErrCorruptedStatusFile) {
			return false, nil
		}
		return false, err
	}
	if status == nil || status.Status != core.Queued {
		return false, nil
	}

	attemptKey := queueAttemptKey(runRef, attempt, status)
	if attemptKey == "" {
		return false, nil
	}
	return d.dispatchTaskStore.HasOutstandingAttempt(ctx, attemptKey, d.leaseStaleThresholdOrDefault())
}

func (d *queueDispatcher) countActiveDistributedRuns(ctx context.Context, queueName string) (int, error) {
	if d.dagRunLeaseStore == nil {
		return 0, nil
	}

	leases, err := d.dagRunLeaseStore.ListByQueue(ctx, queueName)
	if err != nil {
		return 0, fmt.Errorf("list distributed leases for queue %q: %w", queueName, err)
	}

	count := 0
	staleThreshold := d.leaseStaleThresholdOrDefault()
	now := time.Now().UTC()
	for _, lease := range leases {
		if lease.IsFresh(now, staleThreshold) {
			count++
		}
	}
	return count, nil
}

func (d *queueDispatcher) countOutstandingDispatchReservations(ctx context.Context, queueName string) (int, error) {
	if d.dispatchTaskStore == nil {
		return 0, nil
	}
	count, err := d.dispatchTaskStore.CountOutstandingByQueue(ctx, queueName, d.leaseStaleThresholdOrDefault())
	if err != nil {
		return 0, fmt.Errorf("list outstanding distributed dispatches for queue %q: %w", queueName, err)
	}
	return count, nil
}

func (d *queueDispatcher) hasFreshDistributedLease(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
) (bool, error) {
	if d.dagRunLeaseStore == nil || status == nil {
		return false, nil
	}

	attemptID := status.AttemptID
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}
	attemptKey := queueAttemptKey(runRef, attempt, status)
	if attemptKey == "" {
		return false, nil
	}

	lease, err := d.dagRunLeaseStore.Get(ctx, attemptKey)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
			return false, nil
		}
		return false, err
	}
	if lease == nil {
		return false, nil
	}
	if lease.DAGRun != runRef {
		return false, nil
	}
	if queueName != "" && lease.QueueName != "" && lease.QueueName != queueName {
		return false, nil
	}
	if attemptID != "" && lease.AttemptID != "" && lease.AttemptID != attemptID {
		return false, nil
	}

	return lease.IsFresh(time.Now().UTC(), d.leaseStaleThresholdOrDefault()), nil
}

func (d *queueDispatcher) leaseStaleThresholdOrDefault() time.Duration {
	if d.leaseStaleThreshold <= 0 {
		return exec.DefaultStaleLeaseThreshold
	}
	return d.leaseStaleThreshold
}

func (d *queueDispatcher) checkContextAndQuit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		logger.Debug(ctx, "Context canceled")
		return backoff.PermanentError(ctx.Err())
	default:
	}
	if d.isClosed() {
		logger.Info(ctx, "Processor is closed")
		return backoff.PermanentError(errProcessorClosed)
	}
	return nil
}

// isPreStartExecutionFailure reports whether an execution error proves the DAG
// never reached an observable started state. Spawn and dispatch failures should
// abort the startup wait immediately, while process exit errors should continue
// to rely on heartbeat/status because the attempt did start.
func isPreStartExecutionFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var exitErr *osexec.ExitError
	return !errors.As(err, &exitErr)
}
