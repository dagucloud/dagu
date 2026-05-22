// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const queueAgeWarningThreshold = 2 * time.Minute
const queueProcessMinInterval = 3 * time.Second

var (
	errProcessorClosed              = errors.New("processor closed")
	errNotStarted                   = errors.New("execution not started")
	errExecutionExitedBeforeStartup = errors.New("execution exited before startup")
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
	execDone   func() (bool, error)
}

func (s startupWaitState) executionDone() (bool, error) {
	if s.execDone == nil {
		return false, nil
	}
	return s.execDone()
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

func (p *QueueProcessor) newQueueDispatcher() *queueDispatcher {
	return newQueueDispatcher(queueDispatchDeps{
		queueStore:          p.queueStore,
		dagRunStore:         p.dagRunStore,
		procStore:           p.procStore,
		dagRunLeaseStore:    p.dagRunLeaseStore,
		dispatchTaskStore:   p.dispatchTaskStore,
		dagExecutor:         p.dagExecutor,
		isSuspended:         p.isSuspended,
		backoffConfig:       p.backoffConfig,
		leaseStaleThreshold: p.leaseStaleThreshold,
		isClosed:            p.isClosed,
		wakeUp:              p.wakeUp,
	})
}

// ProcessQueueItems processes items in the specified queue.
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
	dispatcher := p.newQueueDispatcher()

	maxConcurrency := q.getMaxConcurrency()
	batch, err := dispatcher.selectDispatchBatch(ctx, queueName, items, maxConcurrency, q.getInflight())
	if err != nil {
		return
	}
	if len(batch.items) == 0 {
		return
	}
	logger.Info(ctx, "Processing batch of items",
		tag.Count(len(batch.items)),
		tag.MaxConcurrency(batch.maxConcurrency),
		tag.Alive(batch.aliveCount),
	)

	var wg sync.WaitGroup
	for _, item := range batch.items {
		wg.Add(1)
		go func(queuedItem exec.QueuedItemData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error(ctx, "Queue item processing panicked", tag.Error(panicToError(r)))
				}
			}()
			if !dispatcher.dispatchQueuedItem(ctx, queuedItem, queueName, q.incInflight, q.decInflight) {
				return
			}
			data, err := queuedItem.Data()
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

func currentStatusString(status *exec.DAGRunStatus) string {
	if status == nil {
		return "unknown"
	}
	return status.Status.String()
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

func readStartupExecutionError(execErrCh <-chan error) error {
	if execErrCh == nil {
		return nil
	}
	select {
	case err := <-execErrCh:
		return err
	default:
		return nil
	}
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
