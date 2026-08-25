// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
)

const queueAgeWarningThreshold = 2 * time.Minute
const queueProcessMinInterval = 3 * time.Second
const queueProcessFallbackInterval = 30 * time.Second
const queueScanItemLimit = 100
const queueHeadItemLimit = 1
const maxConcurrentQueueScans = 8
const maxConcurrentDispatchHandoffs = 8

var (
	errProcessorClosed              = errors.New("processor closed")
	errNotStarted                   = errors.New("execution not started")
	errExecutionExitedBeforeStartup = errors.New("execution exited before startup")
	errRunLivenessUnavailable       = errors.New("run liveness unavailable")
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

type startupExecutionError struct {
	err error
}

func newStartupExecutionError(err error) error {
	if err == nil {
		return nil
	}
	return startupExecutionError{err: err}
}

func (e startupExecutionError) Error() string {
	return e.err.Error()
}

func (e startupExecutionError) Unwrap() error {
	return e.err
}

// QueueProcessor is responsible for processing queued DAG runs.
type QueueProcessor struct {
	queueStore             queuedomain.QueueStore
	dagRunRepository       *persis.DAGRunRepository
	procRepository         queueProcessRepository
	dagRunLeaseStore       dispatch.DAGRunLeaseStore
	dispatchTaskStore      dispatch.DispatchTaskStore
	dispatchAdmissionStore dispatch.DispatchAdmissionStore
	workerHeartbeatStore   dispatch.WorkerHeartbeatStore
	workerStaleAfter       time.Duration
	dagExecutor            *DAGExecutor
	isSuspended            IsSuspendedFunc
	queues                 sync.Map // map[string]*queue
	wakeUpCh               chan struct{}
	dispatchHandoffs       chan struct{}
	quit                   chan struct{}
	wg                     sync.WaitGroup
	stopOnce               sync.Once
	prevTime               time.Time
	lock                   sync.Mutex
	backoffConfig          BackoffConfig
	leaseStaleThreshold    time.Duration
}

type queue struct {
	maxConcurrency  int
	isGlobal        bool // true if this queue is defined in config (global queue)
	scanCursor      string
	scanGeneration  queueScanGeneration
	scanRetryAt     time.Time
	scanRetryNeeded bool
	generationSet   bool
	parked          bool
	inflight        atomic.Int32
	mu              sync.Mutex
}

type workerEligibilityGeneration struct {
	fingerprint [sha256.Size]byte
	enabled     bool
	available   bool
}

type queueScanGeneration struct {
	queueRevision int64
	workers       workerEligibilityGeneration
	capacity      queueCapacityGeneration
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

func (q *queue) scanPosition(generation queueScanGeneration, now time.Time) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.generationSet || q.scanGeneration != generation {
		q.scanCursor = ""
		q.scanGeneration = generation
		q.scanRetryAt = time.Time{}
		q.scanRetryNeeded = false
		q.generationSet = true
		q.parked = false
	} else if q.parked && !q.scanRetryAt.IsZero() && !now.Before(q.scanRetryAt) {
		q.scanRetryAt = time.Time{}
		q.scanRetryNeeded = false
		q.parked = false
	}
	return q.scanCursor, q.parked
}

func (q *queue) advanceScan(generation queueScanGeneration, cursor string, retryNeeded bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.generationSet || q.scanGeneration != generation {
		q.scanRetryNeeded = false
	}
	q.scanGeneration = generation
	q.scanRetryAt = time.Time{}
	q.scanRetryNeeded = q.scanRetryNeeded || retryNeeded
	q.generationSet = true
	q.scanCursor = cursor
	q.parked = false
}

func (q *queue) parkScan(generation queueScanGeneration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scanGeneration = generation
	q.scanRetryAt = time.Time{}
	q.scanRetryNeeded = false
	q.generationSet = true
	q.scanCursor = ""
	q.parked = true
}

func (q *queue) deferScan(generation queueScanGeneration, retryAt time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scanGeneration = generation
	q.scanRetryAt = retryAt
	q.scanRetryNeeded = false
	q.generationSet = true
	q.scanCursor = ""
	q.parked = true
}

func (q *queue) finishScan(generation queueScanGeneration, retryNeeded bool, retryAt time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.generationSet && q.scanGeneration == generation {
		retryNeeded = retryNeeded || q.scanRetryNeeded
	}
	q.scanGeneration = generation
	q.scanRetryNeeded = false
	q.generationSet = true
	q.scanCursor = ""
	q.parked = true
	q.scanRetryAt = time.Time{}
	if retryNeeded {
		q.scanRetryAt = retryAt
	}
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
func WithDAGRunLeaseStore(store dispatch.DAGRunLeaseStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.dagRunLeaseStore = store
	}
}

// WithWorkerHeartbeatStore sets the shared worker heartbeat store.
func WithWorkerHeartbeatStore(store dispatch.WorkerHeartbeatStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.workerHeartbeatStore = store
	}
}

// WithWorkerHeartbeatStaleThreshold sets the worker heartbeat freshness threshold.
func WithWorkerHeartbeatStaleThreshold(threshold time.Duration) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.workerStaleAfter = threshold
	}
}

// WithDispatchTaskStore sets the shared distributed dispatch reservation store.
func WithDispatchTaskStore(store dispatch.DispatchTaskStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.dispatchTaskStore = store
		p.dispatchAdmissionStore = dispatchAdmissionStoreFromTaskStore(store)
	}
}

func WithDispatchAdmissionStore(store dispatch.DispatchAdmissionStore) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.dispatchAdmissionStore = store
	}
}

func dispatchAdmissionStoreFromTaskStore(store dispatch.DispatchTaskStore) dispatch.DispatchAdmissionStore {
	admissionStore, _ := store.(dispatch.DispatchAdmissionStore)
	return admissionStore
}

// WithIsSuspended sets the suspend-flag checker used by the queue processor.
func WithIsSuspended(isSuspended IsSuspendedFunc) QueueProcessorOption {
	return func(p *QueueProcessor) {
		p.isSuspended = isSuspended
	}
}

// NewQueueProcessor creates a new QueueProcessor.
func NewQueueProcessor(
	queueStore queuedomain.QueueStore,
	dagRunRepository *persis.DAGRunRepository,
	procRepository queueProcessRepository,
	dagExecutor *DAGExecutor,
	queuesConfig config.Queues,
	opts ...QueueProcessorOption,
) *QueueProcessor {
	p := &QueueProcessor{
		queueStore:       queueStore,
		dagRunRepository: dagRunRepository,
		procRepository:   procRepository,
		dagExecutor:      dagExecutor,
		wakeUpCh:         make(chan struct{}, 1),
		dispatchHandoffs: make(chan struct{}, maxConcurrentDispatchHandoffs),
		quit:             make(chan struct{}),
		// Seed prevTime in the past so Start()'s initial wake-up is not
		// throttled by the minimum processing interval.
		prevTime:            time.Now().Add(-queueProcessMinInterval),
		backoffConfig:       DefaultBackoffConfig(),
		leaseStaleThreshold: dagrun.DefaultStaleLeaseThreshold,
		workerStaleAfter:    dispatch.DefaultStaleWorkerHeartbeatThreshold,
		isSuspended:         func(context.Context, string) (bool, error) { return false, nil },
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
		case <-time.After(queueProcessFallbackInterval):
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

		p.processActiveQueues(ctx, activeQueues)
	}
}

func (p *QueueProcessor) processActiveQueues(ctx context.Context, activeQueues map[string]struct{}) {
	workerCount := min(len(activeQueues), maxConcurrentQueueScans)
	if workerCount == 0 {
		return
	}
	workerSnapshot := p.loadWorkerHeartbeatSnapshot(ctx)
	workerGeneration := workerSnapshot.generation(p.workerHeartbeatStore != nil, p.workerStaleAfter)

	queueNames := make(chan string)
	var wg sync.WaitGroup
	for range workerCount {
		wg.Go(func() {
			for queueName := range queueNames {
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.Error(ctx, "Queue processing panicked",
								tag.Queue(queueName),
								tag.Error(panicToError(r)),
							)
						}
					}()
					queueCtx := logger.WithValues(ctx, tag.Queue(queueName))
					p.processQueueItems(queueCtx, queueName, workerSnapshot, workerGeneration)
				}()
			}
		})
	}

sendQueues:
	for queueName := range activeQueues {
		select {
		case queueNames <- queueName:
		case <-ctx.Done():
			break sendQueues
		case <-p.quit:
			break sendQueues
		}
	}
	close(queueNames)
	wg.Wait()
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
		queueStore:             p.queueStore,
		dagRunRepository:       p.dagRunRepository,
		procRepository:         p.procRepository,
		dagRunLeaseStore:       p.dagRunLeaseStore,
		dispatchTaskStore:      p.dispatchTaskStore,
		dispatchAdmissionStore: p.dispatchAdmissionStore,
		workerHeartbeatStore:   p.workerHeartbeatStore,
		workerStaleAfter:       p.workerStaleAfter,
		dagExecutor:            p.dagExecutor,
		isSuspended:            p.isSuspended,
		backoffConfig:          p.backoffConfig,
		leaseStaleThreshold:    p.leaseStaleThreshold,
		isClosed:               p.isClosed,
		wakeUp:                 p.wakeUp,
		acquireDispatchHandoff: p.acquireDispatchHandoff,
	})
}

func (p *QueueProcessor) acquireDispatchHandoff(ctx context.Context) (func(), bool) {
	select {
	case p.dispatchHandoffs <- struct{}{}:
		return func() { <-p.dispatchHandoffs }, true
	case <-ctx.Done():
		return nil, false
	case <-p.quit:
		return nil, false
	}
}

// ProcessQueueItems processes items in the specified queue.
func (p *QueueProcessor) ProcessQueueItems(ctx context.Context, queueName string) {
	workerSnapshot := p.loadWorkerHeartbeatSnapshot(ctx)
	p.processQueueItems(
		ctx,
		queueName,
		workerSnapshot,
		workerSnapshot.generation(p.workerHeartbeatStore != nil, p.workerStaleAfter),
	)
}

func (p *QueueProcessor) processQueueItems(
	ctx context.Context,
	queueName string,
	workerSnapshot *workerHeartbeatSnapshot,
	workerGeneration workerEligibilityGeneration,
) {
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
	dispatcher := p.newQueueDispatcher()

	revision, err := p.queueStore.Revision(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to read queue revision", tag.Error(err))
		return
	}
	capacity := dispatcher.queueCapacity(ctx, queueName, q.getMaxConcurrency(), q.getInflight())
	generation := queueScanGeneration{
		queueRevision: revision,
		workers:       workerGeneration,
		capacity:      capacity.queueCapacityGeneration,
	}
	cursor, parked := q.scanPosition(generation, time.Now())
	if parked {
		p.wakeUp()
		logger.Debug(ctx, "Queue scan parked until relevant state changes")
		return
	}

	page, err := p.listQueueScanPage(ctx, queueName, cursor)
	if err != nil {
		logger.Error(ctx, "Failed to get queued items", tag.Error(err))
		return
	}
	items := page.Items
	logger.Debug(ctx, "Loaded bounded queue scan",
		slog.Int("scanned_count", len(items)),
		slog.Bool("has_more", page.HasMore),
	)

	if len(items) == 0 {
		p.finishQueueScan(q, generation, false)
		logger.Debug(ctx, "No item found")
		return
	}

	batch, err := dispatcher.selectDispatchBatch(ctx, queueName, items, capacity, workerSnapshot)
	if err != nil {
		if capacity.err != nil {
			p.parkQueueScan(q, generation)
		}
		return
	}
	if len(batch.items) == 0 {
		if page.HasMore && page.NextCursor != "" {
			q.advanceScan(generation, page.NextCursor, batch.retryScan)
			p.wakeUp()
		} else {
			p.finishQueueScan(q, generation, batch.retryScan)
		}
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
		go func(queuedItem queuedomain.QueuedItemData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error(ctx, "Queue item processing panicked", tag.Error(panicToError(r)))
				}
			}()
			if !dispatcher.dispatchQueuedItem(ctx, queuedItem, queueName, batch, q.incInflight, q.decInflight) {
				return
			}
			if _, err := p.queueStore.DeleteByItemIDs(ctx, queueName, []string{queuedItem.ID()}); err != nil {
				logger.Error(ctx, "Failed to delete processed queue item", tag.Error(err))
			}
		}(item)
	}
	wg.Wait()
	q.deferScan(generation, time.Now().Add(queueProcessFallbackInterval))
	p.wakeUp()
}

func (p *QueueProcessor) parkQueueScan(q *queue, generation queueScanGeneration) {
	q.parkScan(generation)
	p.wakeUp()
}

func (p *QueueProcessor) finishQueueScan(q *queue, generation queueScanGeneration, retryNeeded bool) {
	q.finishScan(generation, retryNeeded, time.Now().Add(queueProcessFallbackInterval))
	p.wakeUp()
}

func (p *QueueProcessor) listQueueScanPage(
	ctx context.Context,
	queueName string,
	cursor string,
) (pagination.CursorResult[queuedomain.QueuedItemData], error) {
	for attempt := range 2 {
		head, err := p.queueStore.ListCursor(ctx, queueName, "", queueHeadItemLimit)
		if err != nil || !head.HasMore || len(head.Items) == 0 {
			return head, err
		}

		rotatingCursor := cursor
		if rotatingCursor == "" {
			rotatingCursor = head.NextCursor
		}
		rotating, err := p.queueStore.ListCursor(
			ctx,
			queueName,
			rotatingCursor,
			queueScanItemLimit-queueHeadItemLimit,
		)
		if errors.Is(err, pagination.ErrInvalidCursor) && attempt == 0 {
			logger.Debug(ctx, "Queue scan cursor invalidated; restarting from head")
			cursor = ""
			continue
		}
		if err != nil {
			return pagination.CursorResult[queuedomain.QueuedItemData]{}, err
		}

		items := make([]queuedomain.QueuedItemData, 0, queueScanItemLimit)
		items = append(items, head.Items...)
		seen := map[string]struct{}{head.Items[0].ID(): {}}
		for _, item := range rotating.Items {
			if _, ok := seen[item.ID()]; ok {
				continue
			}
			seen[item.ID()] = struct{}{}
			items = append(items, item)
		}
		return pagination.CursorResult[queuedomain.QueuedItemData]{
			Items:      items,
			HasMore:    rotating.HasMore,
			NextCursor: rotating.NextCursor,
		}, nil
	}

	return pagination.CursorResult[queuedomain.QueuedItemData]{}, pagination.ErrInvalidCursor
}

func (p *QueueProcessor) loadWorkerHeartbeatSnapshot(ctx context.Context) *workerHeartbeatSnapshot {
	snapshot := &workerHeartbeatSnapshot{observedAt: time.Now().UTC()}
	if p.workerHeartbeatStore != nil {
		_, _ = snapshot.load(ctx, p.workerHeartbeatStore)
	}
	return snapshot
}

func (s *workerHeartbeatSnapshot) generation(
	enabled bool,
	staleThreshold time.Duration,
) workerEligibilityGeneration {
	generation := workerEligibilityGeneration{enabled: enabled, available: s.err == nil}
	if !enabled || s.err != nil {
		return generation
	}

	entries := make([]string, 0, len(s.records))
	for _, record := range s.records {
		if !dispatch.WorkerHeartbeatFresh(record, s.observedAt, staleThreshold) {
			continue
		}
		keys := make([]string, 0, len(record.Labels))
		for key := range record.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var entry strings.Builder
		entry.WriteString(strconv.Quote(record.WorkerID))
		for _, key := range keys {
			entry.WriteByte(':')
			entry.WriteString(strconv.Quote(key))
			entry.WriteByte('=')
			entry.WriteString(strconv.Quote(record.Labels[key]))
		}
		entries = append(entries, entry.String())
	}
	sort.Strings(entries)
	generation.fingerprint = sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return generation
}

func currentStatusString(status *ir.DAGRunStatus) string {
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
		return newStartupExecutionError(err)
	default:
		return nil
	}
}

func queueAttemptKey(runRef ir.DAGRunRef, attempt dagrun.Attempt, status *ir.DAGRunStatus) string {
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
	return ir.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attemptID)
}

func (p *QueueProcessor) leaseStaleThresholdOrDefault() time.Duration {
	if p.leaseStaleThreshold <= 0 {
		return dagrun.DefaultStaleLeaseThreshold
	}
	return p.leaseStaleThreshold
}
