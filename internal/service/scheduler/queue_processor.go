// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

const queueAgeWarningThreshold = 2 * time.Minute
const queueProcessMinInterval = 3 * time.Second

var (
	errProcessorClosed = errors.New("processor closed")
	errNotStarted      = errors.New("execution not started")
)

const suspendedQueueDropReason = "dag schedule suspended before dispatch"

// BackoffConfig holds configuration for exponential backoff retry logic.
type BackoffConfig struct {
	InitialInterval    time.Duration
	MaxInterval        time.Duration
	MaxRetries         int
	StartupGracePeriod time.Duration
}

// DefaultBackoffConfig returns the default backoff configuration.
func DefaultBackoffConfig() BackoffConfig {
	startupGracePeriod := 100 * time.Millisecond
	if runtime.GOOS == "windows" {
		startupGracePeriod = time.Second
	}

	return BackoffConfig{
		InitialInterval:    500 * time.Millisecond,
		MaxInterval:        5 * time.Second,
		MaxRetries:         8,
		StartupGracePeriod: startupGracePeriod,
	}
}

type startupWaitState struct {
	launchedAt time.Time
	execErrCh  <-chan error
}

// QueueProcessor is responsible for processing queued DAG runs.
type QueueProcessor struct {
	queueStore          exec.QueueStore
	dagRunStore         exec.DAGRunStore
	procStore           exec.ProcStore
	dagRunLeaseStore    exec.DAGRunLeaseStore
	dispatchTaskStore   exec.DispatchTaskStore
	dagExecutor         *DAGExecutor
	isSuspended         IsSuspendedFunc
	queues              sync.Map // map[string]*queue
	wakeUpCh            chan struct{}
	quit                chan struct{}
	wg                  sync.WaitGroup
	stopOnce            sync.Once
	prevTime            time.Time
	lock                sync.Mutex
	backoffConfig       BackoffConfig
	leaseStaleThreshold time.Duration
}

type queue struct {
	maxConcurrency int
	isGlobal       bool // true if this queue is defined in config (global queue)
	inflight       atomic.Int32
	mu             sync.Mutex
}

func (q *queue) getMaxConcurrency() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.maxConcurrency
}

func (q *queue) isGlobalQueue() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.isGlobal
}

func (q *queue) getInflight() int {
	return int(q.inflight.Load())
}

func (q *queue) incInflight() { q.inflight.Add(1) }
func (q *queue) decInflight() { q.inflight.Add(-1) }

// QueueProcessorOption is a functional option for configuring QueueProcessor.
type QueueProcessorOption func(*QueueProcessor)

// WithBackoffConfig sets a custom backoff configuration for the processor.
func WithBackoffConfig(cfg BackoffConfig) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.backoffConfig = cfg
	}
}

// WithLeaseStaleThreshold overrides the distributed lease stale threshold used
// for queue concurrency accounting.
func WithLeaseStaleThreshold(threshold time.Duration) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.leaseStaleThreshold = threshold
	}
}

// WithDAGRunLeaseStore sets the shared distributed run lease store.
func WithDAGRunLeaseStore(store exec.DAGRunLeaseStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.dagRunLeaseStore = store
	}
}

// WithDispatchTaskStore sets the shared distributed dispatch reservation store.
func WithDispatchTaskStore(store exec.DispatchTaskStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.dispatchTaskStore = store
	}
}

// WithIsSuspended sets the suspend-flag checker used by the queue processor.
func WithIsSuspended(isSuspended IsSuspendedFunc) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.isSuspended = isSuspended
	}
}

// NewQueueProcessor creates a new QueueProcessor.
func NewQueueProcessor(
	queueStore exec.QueueStore,
	dagRunStore exec.DAGRunStore,
	procStore exec.ProcStore,
	dagExecutor *DAGExecutor,
	queuesConfig config.Queues,
	opts ...QueueProcessorOption,
) *QueueProcessor {
	p := &QueueProcessor{
		queueStore:  queueStore,
		dagRunStore: dagRunStore,
		procStore:   procStore,
		dagExecutor: dagExecutor,
		wakeUpCh:    make(chan struct{}, 1),
		quit:        make(chan struct{}),
		// Seed prevTime in the past so Start()'s initial wake-up is not
		// throttled by the minimum processing interval.
		prevTime:            time.Now().Add(-queueProcessMinInterval),
		backoffConfig:       DefaultBackoffConfig(),
		leaseStaleThreshold: exec.DefaultStaleLeaseThreshold,
		isSuspended:         func(context.Context, string) bool { return false },
	}

	for _, opt := range opts {
		opt(p)
	}

	for _, queueConfig := range queuesConfig.Config {
		conc := max(queueConfig.MaxActiveRuns, 1)
		p.queues.Store(queueConfig.Name, &queue{
			maxConcurrency: conc,
			isGlobal:       true, // Queues from config are global queues
		})
	}

	return p
}

// Start starts the queue processor.
func (p *QueueProcessor) Start(ctx context.Context, notifyCh <-chan struct{}) {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Start the main loop of the processor
	p.wg.Go(func() {
		p.loop(ctx)
	})

	p.wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.quit:
				return
			case <-notifyCh:
				p.wakeUp()
			}
		}
	})

	p.wakeUp() // initial execution
}

// Stop stops the queue processor.
func (p *QueueProcessor) Stop() {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.stopOnce.Do(func() {
		close(p.quit)
		p.wg.Wait()
	})
}

func (p *QueueProcessor) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.quit:
			return
		case <-p.wakeUpCh:
		case <-time.After(30 * time.Second):
			// wake up the queue processor on interval in case event is missed
		}

		// Prevent too frequent execution
		select {
		case <-ctx.Done():
			return
		case <-p.quit:
			return
		case <-time.After(time.Until(p.prevTime.Add(queueProcessMinInterval))):
			p.prevTime = time.Now()
		}

		// Now process each queue
		queueList, err := p.queueStore.QueueList(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to get queue list", tag.Error(err))
			continue
		}

		// Initialize queues that don't exist yet
		activeQueues := make(map[string]struct{}, len(queueList))
		for _, queueName := range queueList {
			if _, ok := p.queues.Load(queueName); !ok {
				p.queues.Store(queueName, &queue{
					maxConcurrency: 1,
					isGlobal:       false,
				})
			}
			activeQueues[queueName] = struct{}{}
		}

		// Remove inactive non-global queues
		p.removeInactiveQueues(activeQueues)

		// Process each queue concurrently
		var wg sync.WaitGroup
		for name := range activeQueues {
			wg.Add(1)
			go func(queueName string) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						logger.Error(ctx, "Queue processing panicked",
							tag.Queue(queueName),
							tag.Error(panicToError(r)),
						)
					}
				}()
				queueCtx := logger.WithValues(ctx, tag.Queue(queueName))
				p.ProcessQueueItems(queueCtx, queueName)
			}(name)
		}
		wg.Wait()
	}
}

func (p *QueueProcessor) isClosed() bool {
	select {
	case <-p.quit:
		return true
	default:
		return false
	}
}

// ProcessQueueItems processes items in the specified queue.
// It returns true if there are more items to process in the queue.
func (p *QueueProcessor) ProcessQueueItems(ctx context.Context, queueName string) {
	if p.isClosed() {
		return
	}

	v, ok := p.queues.Load(queueName)
	if !ok {
		logger.Warn(ctx, "Queue not found in processor config")
		return
	}
	q := v.(*queue)
	logger.Debug(ctx, "Processing queue", tag.MaxConcurrency(q.getMaxConcurrency()))

	items, err := p.queueStore.List(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to get queued items", tag.Error(err))
		return
	}

	if len(items) == 0 {
		logger.Debug(ctx, "No item found")
		return
	}

	defer p.wakeUp()

	localAliveCount, err := p.procStore.CountAlive(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count alive processes", tag.Error(err), tag.Queue(queueName))
		return
	}

	distributedAliveCount, err := p.countActiveDistributedRuns(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count distributed leases", tag.Error(err), tag.Queue(queueName))
		return
	}
	outstandingDispatchCount, err := p.countOutstandingDispatchReservations(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count outstanding distributed dispatch reservations", tag.Error(err), tag.Queue(queueName))
		return
	}
	aliveCount := localAliveCount + distributedAliveCount

	maxConcurrency := q.getMaxConcurrency()
	inflightCount := q.getInflight()
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
		return
	}

	runnableItems, err := p.selectRunnableQueueItems(ctx, items, freeSlots)
	if err != nil {
		logger.Error(ctx, "Failed to select runnable queue items", tag.Error(err), tag.Queue(queueName))
		return
	}
	if len(runnableItems) == 0 {
		logger.Debug(ctx, "No queue items eligible for a new dispatch attempt")
		return
	}
	logger.Info(ctx, "Processing batch of items",
		tag.Count(len(runnableItems)),
		tag.MaxConcurrency(maxConcurrency),
		tag.Alive(aliveCount),
	)

	var wg sync.WaitGroup
	leaseStore, useLeases := p.queueStore.(exec.QueueLeaseStore)
	leaseOwner := queueLeaseOwner(queueName)
	for _, item := range runnableItems {
		wg.Add(1)
		go func(queuedItem exec.QueuedItemData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error(ctx, "Queue item processing panicked", tag.Error(panicToError(r)))
				}
			}()
			processItem := queuedItem
			leaseToken := ""
			if useLeases {
				leasedItem, err := leaseStore.ClaimByItemID(ctx, queueName, queuedItem.ID(), leaseOwner, p.leaseStaleThresholdOrDefault())
				if err != nil {
					if !errors.Is(err, exec.ErrQueueItemNotFound) {
						logger.Error(ctx, "Failed to claim queue item lease", tag.Error(err))
					}
					return
				}
				processItem = leasedItem
				leaseToken = leasedItem.LeaseToken()
			}
			if !p.processDAG(ctx, processItem, queueName, q.incInflight, q.decInflight) {
				if leaseToken != "" {
					if err := leaseStore.ReleaseLease(ctx, queueName, leaseToken); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
						logger.Error(ctx, "Failed to release queue item lease", tag.Error(err))
					}
				}
				return
			}
			if leaseToken != "" {
				if err := leaseStore.AckLease(ctx, queueName, leaseToken); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
					logger.Error(ctx, "Failed to acknowledge queue item lease", tag.Error(err))
				}
				return
			}
			data, err := processItem.Data()
			if err != nil {
				logger.Error(ctx, "Failed to get item data", tag.Error(err))
				return
			}
			if _, err := p.queueStore.DequeueByDAGRunID(ctx, queueName, *data); err != nil {
				if errors.Is(err, exec.ErrQueueItemNotFound) {
					return
				}
				logger.Error(ctx, "Failed to dequeue item", tag.Error(err))
			}
		}(item)
	}
	wg.Wait()
}

func queueLeaseOwner(queueName string) string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s:%d:%s", hostname, os.Getpid(), queueName)
}

func (p *QueueProcessor) processDAG(ctx context.Context, item exec.QueuedItemData, queueName string, incInflight, decInflight func()) bool {
	if p.isClosed() {
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

	running, err := p.procStore.IsRunAlive(ctx, queueName, runRef)
	if err != nil {
		logger.Error(ctx, "Failed to check if run is alive", tag.Error(err))
		return false
	}
	if running {
		logger.Warn(ctx, "DAG run is already running, discarding")
		return true
	}

	attempt, err := p.dagRunStore.FindAttempt(ctx, runRef)
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

	if isSchedulerManagedTriggerType(status.TriggerType) && isSuspendedDAG(ctx, p.isSuspended, status, dag) {
		if err := p.dropSuspendedQueuedRun(ctx, queueName, runRef, attempt.ID(), status); err != nil {
			logger.Error(ctx, "Failed to drop suspended queued DAG run", tag.Error(err))
		}
		return false
	}

	// Log a warning if the item has been queued for too long.
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

	// For distributed execution, dispatch synchronously inside the retry loop
	// so transient "no available workers" errors are retried with backoff.
	if p.dagExecutor.IsDistributed(dag) {
		return p.dispatchAndWaitForStartup(ctx, queueName, runRef, dag, runID, status)
	}

	// For local execution, launch in a goroutine and poll for startup.
	execErrCh := make(chan error, 1)
	go func() {
		defer p.wakeUp()
		if err := p.dagExecutor.ExecuteDAG(ctx, dag, coordinatorv1.Operation_OPERATION_RETRY, runID, status, status.TriggerType, status.ScheduleTime); err != nil {
			logger.Error(ctx, "Failed to execute DAG", tag.Error(err))
			if isPreStartExecutionFailure(err) {
				select {
				case execErrCh <- err:
				default:
				}
			}
		}
	}()

	return p.waitForStartup(ctx, queueName, runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  execErrCh,
	})
}

func (p *QueueProcessor) dropSuspendedQueuedRun(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	attemptID string,
	status *exec.DAGRunStatus,
) error {
	finishedAt := stringutil.FormatTime(time.Now().UTC())
	currentStatus, swapped, err := p.dagRunStore.CompareAndSwapLatestAttemptStatus(
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
			latest.LeaseAt = 0
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("abort suspended queued DAG run: %w", err)
	}

	if _, err := p.queueStore.DequeueByDAGRunID(ctx, queueName, runRef); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
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

func currentStatusString(status *exec.DAGRunStatus) string {
	if status == nil {
		return "unknown"
	}
	return status.Status.String()
}

// dispatchAndWaitForStartup handles distributed DAG execution by retrying
// dispatch within the backoff loop. This ensures transient "no available workers"
// errors are retried rather than immediately failing.
func (p *QueueProcessor) dispatchAndWaitForStartup(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	dag *core.DAG,
	runID string,
	dagStatus *exec.DAGRunStatus,
) bool {
	policy := backoff.NewExponentialBackoffPolicy(p.backoffConfig.InitialInterval)
	policy.MaxInterval = p.backoffConfig.MaxInterval
	policy.MaxRetries = p.backoffConfig.MaxRetries
	retryCtx := backoff.WithRetryFailureLogLevel(ctx, slog.LevelInfo)

	launchedAt := time.Now()
	var started bool
	dispatched := false

	operation := func(ctx context.Context) error {
		if err := p.checkContextAndQuit(ctx); err != nil {
			return err
		}

		// If not yet dispatched (or last dispatch was a transient failure), try dispatch.
		if !dispatched {
			err := p.dagExecutor.ExecuteDAG(ctx, dag, coordinatorv1.Operation_OPERATION_RETRY,
				runID, dagStatus, dagStatus.TriggerType, dagStatus.ScheduleTime)
			if err != nil {
				var staleErr *exec.StaleQueueDispatchError
				if errors.As(err, &staleErr) {
					return backoff.PermanentError(err)
				}
				// Permanent dispatch error (e.g. selector mismatch): stop retrying.
				if errors.Is(err, backoff.ErrPermanent) {
					logger.Error(ctx, "Permanent dispatch failure", tag.Error(err))
					return err
				}
				// Transient dispatch error (e.g. no available workers): retry.
				logger.Warn(ctx, "Transient dispatch failure, will retry", tag.Error(err))
				return err
			}
			dispatched = true
		}

		// Dispatch succeeded, now poll for startup.
		var err error
		started, err = p.checkStartupStatus(ctx, queueName, runRef, startupWaitState{
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

	defer p.wakeUp()
	return started
}

func (p *QueueProcessor) wakeUp() {
	select {
	case p.wakeUpCh <- struct{}{}:
	default:
	}
}

// removeInactiveQueues removes queues that are no longer active, preserving global queues from config.
func (p *QueueProcessor) removeInactiveQueues(activeQueues map[string]struct{}) {
	var toDelete []string
	p.queues.Range(func(key, value any) bool {
		name, ok := key.(string)
		if !ok {
			return true
		}
		q, ok := value.(*queue)
		if !ok || q.isGlobalQueue() {
			return true
		}
		if _, active := activeQueues[name]; !active {
			toDelete = append(toDelete, name)
		}
		return true
	})
	for _, name := range toDelete {
		p.queues.Delete(name)
	}
}

// waitForStartup waits for the DAG execution to start using exponential backoff.
func (p *QueueProcessor) waitForStartup(ctx context.Context, queueName string, runRef exec.DAGRunRef, waitState startupWaitState) bool {
	policy := backoff.NewExponentialBackoffPolicy(p.backoffConfig.InitialInterval)
	policy.MaxInterval = p.backoffConfig.MaxInterval
	policy.MaxRetries = p.backoffConfig.MaxRetries

	var started bool
	operation := func(ctx context.Context) error {
		var err error
		started, err = p.checkStartupStatus(ctx, queueName, runRef, waitState)
		return err
	}

	if err := backoff.Retry(ctx, operation, policy, nil); err != nil {
		logger.Error(ctx, "Failed to execute DAG after retries", tag.Error(err))
	}

	return started
}

// checkStartupStatus checks if the DAG execution has started.
func (p *QueueProcessor) checkStartupStatus(ctx context.Context, queueName string, runRef exec.DAGRunRef, waitState startupWaitState) (bool, error) {
	if err := p.checkContextAndQuit(ctx); err != nil {
		return false, err
	}
	if err := readStartupExecutionError(waitState.execErrCh); err != nil {
		logger.Warn(ctx, "DAG execution failed before startup was observed", tag.Error(err))
		return false, backoff.PermanentError(err)
	}

	isAlive, err := p.procStore.IsRunAlive(ctx, queueName, runRef)
	if err != nil {
		logger.Warn(ctx, "Failed to check run liveness", tag.Error(err), tag.Queue(queueName), tag.RunID(runRef.ID))
	} else if isAlive {
		logger.Info(ctx, "DAG run has started (heartbeat detected)")
		return true, nil
	}
	if p.inStartupGracePeriod(waitState.launchedAt) && p.dagRunLeaseStore == nil {
		return false, errNotStarted
	}

	attempt, err := p.dagRunStore.FindAttempt(ctx, runRef)
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
	started, err := p.hasFreshDistributedLease(ctx, queueName, runRef, attempt, status)
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
	if p.inStartupGracePeriod(waitState.launchedAt) {
		return false, errNotStarted
	}
	if err != nil {
		return false, err
	}

	return false, errNotStarted
}

func (p *QueueProcessor) inStartupGracePeriod(launchedAt time.Time) bool {
	grace := p.backoffConfig.StartupGracePeriod
	return grace > 0 && time.Since(launchedAt) < grace
}

func readStartupExecutionError(execErrCh <-chan error) error {
	select {
	case err := <-execErrCh:
		return err
	default:
		return nil
	}
}

func (p *QueueProcessor) selectRunnableQueueItems(
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
		if p.dispatchTaskStore != nil {
			runRef, err := item.Data()
			if err != nil {
				logger.Error(ctx, "Failed to get item data while selecting runnable queue items", tag.Error(err))
				continue
			}
			reserved, err := p.hasOutstandingDispatchReservation(ctx, *runRef)
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

// countActiveDistributedRuns counts distributed runs (non-empty WorkerID) that
// belong to the given queue/proc-group and have a fresh lease. These runs are
// invisible to the local procStore but must count against queue concurrency.
func (p *QueueProcessor) countActiveDistributedRuns(ctx context.Context, queueName string) (int, error) {
	if p.dagRunLeaseStore == nil {
		return 0, nil
	}

	leases, err := p.dagRunLeaseStore.ListByQueue(ctx, queueName)
	if err != nil {
		return 0, fmt.Errorf("list distributed leases for queue %q: %w", queueName, err)
	}

	count := 0
	staleThreshold := p.leaseStaleThresholdOrDefault()
	now := time.Now().UTC()
	for _, lease := range leases {
		if lease.IsFresh(now, staleThreshold) {
			count++
		}
	}
	return count, nil
}

func (p *QueueProcessor) countOutstandingDispatchReservations(ctx context.Context, queueName string) (int, error) {
	if p.dispatchTaskStore == nil {
		return 0, nil
	}
	count, err := p.dispatchTaskStore.CountOutstandingByQueue(ctx, queueName, p.leaseStaleThresholdOrDefault())
	if err != nil {
		return 0, fmt.Errorf("list outstanding distributed dispatches for queue %q: %w", queueName, err)
	}
	return count, nil
}

func (p *QueueProcessor) hasOutstandingDispatchReservation(ctx context.Context, runRef exec.DAGRunRef) (bool, error) {
	if p.dispatchTaskStore == nil {
		return false, nil
	}

	attempt, err := p.dagRunStore.FindAttempt(ctx, runRef)
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
	return p.dispatchTaskStore.HasOutstandingAttempt(ctx, attemptKey, p.leaseStaleThresholdOrDefault())
}

func (p *QueueProcessor) hasFreshDistributedLease(
	ctx context.Context,
	queueName string,
	runRef exec.DAGRunRef,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
) (bool, error) {
	if p.dagRunLeaseStore == nil || status == nil {
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

	lease, err := p.dagRunLeaseStore.Get(ctx, attemptKey)
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

	return lease.IsFresh(time.Now().UTC(), p.leaseStaleThresholdOrDefault()), nil
}

func queueAttemptKey(runRef exec.DAGRunRef, attempt exec.DAGRunAttempt, status *exec.DAGRunStatus) string {
	if status == nil {
		return ""
	}

	attemptID := status.AttemptID
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}
	if status.AttemptKey != "" {
		return status.AttemptKey
	}
	if attemptID == "" {
		return ""
	}
	return exec.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attemptID)
}

func (p *QueueProcessor) leaseStaleThresholdOrDefault() time.Duration {
	if p.leaseStaleThreshold <= 0 {
		return exec.DefaultStaleLeaseThreshold
	}
	return p.leaseStaleThreshold
}

// checkContextAndQuit returns a permanent error if context is done or processor is closed.
func (p *QueueProcessor) checkContextAndQuit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		logger.Debug(ctx, "Context canceled")
		return backoff.PermanentError(ctx.Err())
	case <-p.quit:
		logger.Info(ctx, "Processor is closed")
		return backoff.PermanentError(errProcessorClosed)
	default:
		return nil
	}
}
