// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const freshDistributedTestThreshold = time.Hour

type syncBuffer struct {
	buf  *bytes.Buffer
	lock sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (n int, err error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}

type queueFixture struct {
	t                *testing.T
	ctx              context.Context
	logBuffer        *syncBuffer
	dagRunRepository *persis.DAGRunRepository
	leaseStore       dispatch.DAGRunLeaseStore
	dispatchStore    dispatch.DispatchTaskStore
	distributedDir   string
	queueStore       *store.QueueStore
	procRepository   queueProcessRepository
	processor        *QueueProcessor
	dag              *ir.DAG
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	t.Parallel()

	tmpDir := t.TempDir()
	distributedDir := filepath.Join(tmpDir, "distributed")
	leaseCollection := file.NewCollection(filepath.Join(distributedDir, "leases"))
	logBuffer := &syncBuffer{buf: new(bytes.Buffer)}
	ctx := logger.WithFixedLogger(context.Background(), logger.NewLogger(
		logger.WithDebug(), logger.WithFormat("text"), logger.WithWriter(logBuffer),
	))

	return &queueFixture{
		t: t, ctx: ctx, logBuffer: logBuffer,
		distributedDir:   distributedDir,
		dagRunRepository: testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true}),
		leaseStore:       store.NewDAGRunLeaseStore(leaseCollection),
		dispatchStore:    store.NewDispatchTaskStore(file.NewCollection(distributedDir)),
		queueStore:       store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue"))),
		procRepository:   newSchedulerTestProcRepository(filepath.Join(tmpDir, "proc"), nil),
	}
}

func newSchedulerTestProcRepository(procDir string, cfg *config.Config) *persis.ProcRepository {
	opts := []proc.StoreOption{}
	if cfg != nil {
		opts = append(opts,
			proc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
			proc.WithStaleThreshold(cfg.Proc.StaleThreshold),
		)
	}
	return persis.NewProcRepository(proc.New(procDir, opts...))
}

func TestWithDispatchTaskStoreClearsAdmissionStore(t *testing.T) {
	t.Parallel()

	distributedDir := filepath.Join(t.TempDir(), "distributed")
	admissionStore := store.NewDispatchTaskStore(file.NewCollection(distributedDir))
	processor := &QueueProcessor{dispatchAdmissionStore: admissionStore}

	WithDispatchTaskStore(&mockDispatchTaskStore{})(processor)

	assert.Nil(t, processor.dispatchAdmissionStore)
}

func (f *queueFixture) withDAG(name string, maxActiveRuns int) *queueFixture {
	f.dag = &ir.DAG{
		Name: name, MaxActiveRuns: maxActiveRuns,
		YamlData: fmt.Appendf(nil, "name: %s\nmax_active_runs: %d\nsteps:\n  - name: test\n    command: echo hello", name, maxActiveRuns),
		Steps:    []ir.Step{{Name: "test", Command: "echo hello"}},
	}
	return f
}

func (f *queueFixture) enqueueRuns(n int) *queueFixture {
	for i := 1; i <= n; i++ {
		runID := fmt.Sprintf("run-%d", i)
		run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(f.t, err)
		require.NoError(f.t, run.Open(f.ctx))
		st := ir.InitialStatus(f.dag)
		st.Status, st.DAGRunID = ir.Queued, runID
		require.NoError(f.t, run.Write(f.ctx, st))
		require.NoError(f.t, run.Close(f.ctx))
		require.NoError(f.t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityHigh, ir.NewDAGRunRef(f.dag.Name, runID)))
	}
	return f
}

func (f *queueFixture) withProcessor(cfg config.Queues, opts ...QueueProcessorOption) *queueFixture {
	options := append([]QueueProcessorOption{
		WithBackoffConfig(BackoffConfig{InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond, MaxRetries: 2}),
		WithDAGRunLeaseStore(f.leaseStore),
	}, opts...)
	f.processor = NewQueueProcessor(f.queueStore, f.dagRunRepository, f.procRepository,
		NewDAGExecutor(nil, launcher.NewSubCmdBuilder(&config.Config{Paths: config.PathsConfig{Executable: "/usr/bin/dagu"}}), config.ExecutionModeLocal, ""),
		cfg, options...,
	)
	f.dispatchStore = store.NewDispatchTaskStore(
		file.NewCollection(f.distributedDir),
		store.WithDispatchReservationTTL(f.processor.leaseStaleThresholdOrDefault()),
	)
	f.processor.dispatchTaskStore = f.dispatchStore
	return f
}

func (f *queueFixture) simulateQueue(maxConcurrency int, isGlobal bool) *queueFixture {
	f.processor.queues.Store(f.dag.Name, &queue{maxConcurrency: maxConcurrency, isGlobal: isGlobal})
	return f
}

func (f *queueFixture) logs() string { return f.logBuffer.String() }

func (f *queueFixture) getQueue(name string) *queue {
	v, ok := f.processor.queues.Load(name)
	require.True(f.t, ok, "Queue %s should exist", name)
	return v.(*queue)
}

func (f *queueFixture) enqueueWithPriority(runID string, priority queuedomain.QueuePriority) {
	f.enqueueToQueue(f.dag.Name, runID, priority)
}

func (f *queueFixture) enqueueRunWithTrigger(runID string, triggerType ir.TriggerType) {
	f.enqueueToQueueWithTrigger(f.dag.Name, runID, queuedomain.QueuePriorityHigh, triggerType)
}

func (f *queueFixture) enqueueToQueue(queueName, runID string, priority queuedomain.QueuePriority) {
	f.enqueueToQueueWithTrigger(queueName, runID, priority, ir.TriggerTypeUnknown)
}

func (f *queueFixture) enqueueToQueueWithTrigger(queueName, runID string, priority queuedomain.QueuePriority, triggerType ir.TriggerType) {
	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, run.Open(f.ctx))
	st := ir.InitialStatus(f.dag)
	st.Status, st.DAGRunID = ir.Queued, runID
	st.AttemptID = run.ID()
	st.TriggerType = triggerType
	require.NoError(f.t, run.Write(f.ctx, st))
	require.NoError(f.t, run.Close(f.ctx))
	require.NoError(f.t, f.queueStore.Enqueue(f.ctx, queueName, priority, ir.NewDAGRunRef(f.dag.Name, runID)))
}

func TestQueueProcessor_LocalQueueAlwaysFIFO(t *testing.T) {
	// Local queue should always use maxConcurrency=1, ignoring DAG's maxActiveRuns=5
	f := newQueueFixture(t).withDAG("local-dag", 5).enqueueRuns(3).
		withProcessor(config.Queues{}).simulateQueue(1, false)

	// Verify initial maxConcurrency is 1
	require.Equal(t, 1, f.getQueue("local-dag").getMaxConcurrency())

	f.processor.ProcessQueueItems(f.ctx, "local-dag")

	// Verify maxConcurrency is STILL 1 (not updated to DAG's 5)
	assert.Equal(t, 1, f.getQueue("local-dag").getMaxConcurrency(), "Local queue should always have maxConcurrency=1")
}

func TestQueueProcessor_GlobalQueue(t *testing.T) {
	f := newQueueFixture(t).withDAG("global-dag", 1).withProcessor(config.Queues{
		Enabled: true, Config: []config.QueueConfig{{Name: "global-queue", MaxActiveRuns: 3}},
	})

	for i := 1; i <= 3; i++ {
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), queuedomain.QueuePriorityHigh)
	}

	require.Equal(t, 3, f.getQueue("global-queue").getMaxConcurrency())

	f.processor.ProcessQueueItems(f.ctx, "global-queue")
	assert.Contains(t, f.logs(), "count=3")
}

func TestQueueProcessor_PermanentStartupFailureIsFailedAndDequeued(t *testing.T) {
	f := newQueueFixture(t).withDAG("fifo-dag", 1)
	f.enqueueRunWithTrigger("run-1", ir.TriggerTypeManual)
	f.enqueueRunWithTrigger("run-2", ir.TriggerTypeManual)
	f.withProcessor(config.Queues{
		Enabled: true,
		Config:  []config.QueueConfig{{Name: "fifo-dag", MaxActiveRuns: 1}},
	}).simulateQueue(1, false)
	f.processor.dagExecutor = NewDAGExecutor(
		nil,
		launcher.NewSubCmdBuilder(&config.Config{
			Paths: config.PathsConfig{Executable: filepath.Join(t.TempDir(), "missing-dagu")},
		}),
		config.ExecutionModeLocal,
		"",
	)

	f.processor.ProcessQueueItems(f.ctx, "fifo-dag")

	items, err := f.queueStore.List(f.ctx, "fifo-dag")
	require.NoError(t, err)
	require.Len(t, items, 1)

	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef("fifo-dag", "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, ir.Failed, status.Status)
	assert.NotEmpty(t, status.Error)
	assert.NotEmpty(t, status.FinishedAt)
}

func TestQueueProcessor_PriorityOrdering(t *testing.T) {
	f := newQueueFixture(t).withDAG("priority-dag", 1).withProcessor(config.Queues{})

	// Enqueue low priority first, then high priority
	f.enqueueWithPriority("low-1", queuedomain.QueuePriorityLow)
	f.enqueueWithPriority("low-2", queuedomain.QueuePriorityLow)
	f.enqueueWithPriority("high-1", queuedomain.QueuePriorityHigh)
	f.enqueueWithPriority("high-2", queuedomain.QueuePriorityHigh)

	// Queue reads should return high priority first, then low priority.
	expectedOrder := []string{"high-1", "high-2", "low-1", "low-2"}
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, len(expectedOrder))
	for i, expectedID := range expectedOrder {
		ref, err := items[i].Data()
		require.NoError(t, err)
		assert.Equal(t, expectedID, ref.ID)
	}
}

func TestQueueProcessor_ConcurrencyLimit(t *testing.T) {
	f := newQueueFixture(t).withDAG("conc-dag", 1).enqueueRuns(3).
		withProcessor(config.Queues{}).simulateQueue(1, false)

	// Process with maxConcurrency=1
	f.processor.ProcessQueueItems(f.ctx, "conc-dag")

	// Should only process 1 item at a time, leaving 2 in queue
	items, err := f.queueStore.List(f.ctx, "conc-dag")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(items), 2, "Concurrency limit should prevent processing all at once")
}

func TestQueueProcessor_PreservesSameRunItemEnqueuedDuringDispatch(t *testing.T) {
	f := newQueueFixture(t).withDAG("same-run-redispatch", 1).enqueueRuns(1).
		withProcessor(config.Queues{}).simulateQueue(1, false)

	initialItems, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, initialItems, 1)
	initialItemID := initialItems[0].ID()
	runRef := ir.NewDAGRunRef(f.dag.Name, "run-1")

	procRepository := &mockProcRepository{}
	procRepository.On("CountAlive", mock.Anything, f.dag.Name).Return(0, nil).Once()
	procRepository.On("IsRunAlive", mock.Anything, f.dag.Name, runRef).Return(false, nil).Once()
	var enqueueErr error
	procRepository.On("IsRunAlive", mock.Anything, f.dag.Name, runRef).
		Run(func(mock.Arguments) {
			enqueueErr = f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityLow, runRef)
		}).
		Return(true, nil).
		Once()

	f.processor.procRepository = procRepository
	f.processor.dagExecutor = NewDAGExecutor(&mockDispatcher{}, nil, config.ExecutionModeDistributed, "")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	require.NoError(t, enqueueErr)
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.NotEqual(t, initialItemID, items[0].ID())
	remainingRef, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, runRef, *remainingRef)
	procRepository.AssertExpectations(t)
}

func TestQueueProcessor_CountsFreshDistributedRunsAgainstQueueConcurrency(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-conc-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold)).
		simulateQueue(1, false)

	runningAttempt, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), "running-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, runningAttempt.Open(f.ctx))
	runningStatus := ir.InitialStatus(f.dag)
	runningStatus.Status = ir.Queued
	runningStatus.DAGRunID = "running-run"
	runningStatus.AttemptID = runningAttempt.ID()
	runningStatus.WorkerID = "worker-1"
	require.NoError(t, runningAttempt.Write(f.ctx, runningStatus))
	require.NoError(t, runningAttempt.Close(f.ctx))
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.enqueueRuns(1)

	f.processor.ProcessQueueItems(f.ctx, "distributed-conc-dag")

	items, err := f.queueStore.List(f.ctx, "distributed-conc-dag")
	require.NoError(t, err)
	require.Len(t, items, 1, "fresh distributed lease should consume the only queue slot")
	assert.Contains(t, f.logs(), "Max concurrency reached")
}

func TestQueueProcessor_ProcessQueueItems_FailsClosedOnLeaseCountError(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-count-error-dag", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)

	f.enqueueRuns(1)
	f.processor.dagRunLeaseStore = &mockLeaseStore{
		listByQueueFunc: func(context.Context, string) ([]dispatch.DAGRunLease, error) {
			return nil, errors.New("lease store unavailable")
		},
	}

	f.processor.ProcessQueueItems(f.ctx, "distributed-count-error-dag")

	items, err := f.queueStore.List(f.ctx, "distributed-count-error-dag")
	require.NoError(t, err)
	require.Len(t, items, 1, "queue should remain untouched when distributed lease counting fails")
	assert.Contains(t, f.logs(), "Failed to count distributed leases")
}

func TestQueueProcessor_StaleCorruptLeaseDoesNotBlockCapacityCheck(t *testing.T) {
	f := newQueueFixture(t).withDAG("stale-corrupt-lease-dag", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)

	leaseDir := filepath.Join(f.distributedDir, "leases")
	require.NoError(t, os.MkdirAll(leaseDir, 0o750))
	path := filepath.Join(leaseDir, "stale-corrupt.json")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	old := time.Now().Add(-2 * dagrun.DefaultStaleLeaseThreshold)
	require.NoError(t, os.Chtimes(path, old, old))

	count, err := f.processor.newQueueDispatcher().countActiveDistributedRuns(f.ctx, f.dag.Name)
	require.NoError(t, err)
	assert.Zero(t, count)
	assert.Contains(t, f.logs(), "Removed stale corrupt distributed lease entry")
}

func TestQueueProcessor_ProcessQueueItems_FailsClosedOnOutstandingDispatchCountError(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-dispatch-count-error-dag", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)

	f.enqueueRuns(1)
	f.processor.dispatchTaskStore = &mockDispatchTaskStore{
		countOutstandingByQueueFunc: func(context.Context, string, time.Duration) (int, error) {
			return 0, errors.New("dispatch store unavailable")
		},
	}

	f.processor.ProcessQueueItems(f.ctx, "distributed-dispatch-count-error-dag")

	items, err := f.queueStore.List(f.ctx, "distributed-dispatch-count-error-dag")
	require.NoError(t, err)
	require.Len(t, items, 1, "queue should remain untouched when outstanding dispatch counting fails")
	assert.Contains(t, f.logs(), "Failed to count outstanding distributed dispatch reservations")
}

func TestQueueProcessor_CountsOutstandingDispatchReservationsAgainstQueueConcurrency(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-dispatch-reservation-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold)).
		simulateQueue(1, false)

	f.enqueueRuns(1)

	runRef := ir.NewDAGRunRef(f.dag.Name, "run-1")
	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &dispatch.DispatchTask{
		DAGRunID:   runRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptID:  attempt.ID(),
		AttemptKey: queueAttemptKey(runRef, attempt, status),
	}))

	f.processor.ProcessQueueItems(f.ctx, "distributed-dispatch-reservation-dag")

	items, err := f.queueStore.List(f.ctx, "distributed-dispatch-reservation-dag")
	require.NoError(t, err)
	require.Len(t, items, 1, "outstanding distributed dispatch reservations should consume queue capacity")
	assert.Contains(t, f.logs(), "Max concurrency reached")
}

func TestQueueProcessor_SelectRunnableQueueItemsSkipsOutstandingReservations(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-select-dag", 2).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold)).
		simulateQueue(2, false)

	f.enqueueRuns(2)

	reservedRef := ir.NewDAGRunRef(f.dag.Name, "run-1")
	reservedAttempt, err := f.dagRunRepository.FindAttempt(f.ctx, reservedRef)
	require.NoError(t, err)
	reservedStatus, err := reservedAttempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &dispatch.DispatchTask{
		DAGRunID:   reservedRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptID:  reservedAttempt.ID(),
		AttemptKey: queueAttemptKey(reservedRef, reservedAttempt, reservedStatus),
	}))

	items, err := f.queueStore.List(f.ctx, "distributed-select-dag")
	require.NoError(t, err)

	runnable, err := f.processor.newQueueDispatcher().selectRunnableQueueItems(f.ctx, items, 1)
	require.NoError(t, err)
	require.Len(t, runnable, 1)

	selectedRef, err := runnable[0].Data()
	require.NoError(t, err)
	assert.Equal(t, "run-2", selectedRef.ID)
}

func TestQueueProcessor_StaleOutstandingDispatchReservationsExpire(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-stale-select-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(time.Nanosecond)).
		simulateQueue(1, false)

	f.enqueueRuns(1)

	runRef := ir.NewDAGRunRef(f.dag.Name, "run-1")
	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &dispatch.DispatchTask{
		DAGRunID:   runRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptID:  attempt.ID(),
		AttemptKey: queueAttemptKey(runRef, attempt, status),
	}))

	var count int
	var countErr error
	require.Eventually(t, func() bool {
		count, countErr = f.processor.newQueueDispatcher().countOutstandingDispatchReservations(f.ctx, f.dag.Name)
		return countErr == nil && count == 0
	}, 500*time.Millisecond, 10*time.Millisecond)
	require.NoError(t, countErr)
	assert.Zero(t, count)

	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)

	runnable, err := f.processor.newQueueDispatcher().selectRunnableQueueItems(f.ctx, items, 1)
	require.NoError(t, err)
	require.Len(t, runnable, 1)

	selectedRef, err := runnable[0].Data()
	require.NoError(t, err)
	assert.Equal(t, "run-1", selectedRef.ID)

	pendingEntries, err := os.ReadDir(filepath.Join(f.distributedDir, "pending"))
	if !errors.Is(err, os.ErrNotExist) {
		require.NoError(t, err)
	}
	assert.Empty(t, pendingEntries)
}

func TestQueueDispatcher_DistributedDispatchHandsOffWithAdmissionToken(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-admission-dag", 1)
	f.enqueueRuns(1)

	activeStore := store.NewActiveDistributedRunStore(file.NewCollection(filepath.Join(f.distributedDir, "active-runs")))
	dispatchStore := store.NewDispatchTaskStore(
		file.NewCollection(f.distributedDir),
		store.WithDispatchAdmissionLiveness(f.leaseStore, activeStore),
	)

	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)

	runRef := ir.NewDAGRunRef(f.dag.Name, "run-1")
	procRepository := &mockProcRepository{}
	procRepository.On("IsRunAlive", mock.Anything, f.dag.Name, runRef).Return(false, nil).Once()
	dispatcher := &mockDispatcher{}

	queueDispatcher := newQueueDispatcher(queueDispatchDeps{
		queueStore:             f.queueStore,
		dagRunRepository:       f.dagRunRepository,
		procRepository:         procRepository,
		dagRunLeaseStore:       f.leaseStore,
		dispatchTaskStore:      dispatchStore,
		dispatchAdmissionStore: dispatchStore,
		dagExecutor:            NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, ""),
		backoffConfig:          BackoffConfig{InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond, MaxRetries: 2, StartupGracePeriod: time.Second},
		leaseStaleThreshold:    time.Minute,
	})

	dispatched := queueDispatcher.dispatchQueuedItem(f.ctx, items[0], f.dag.Name, queueDispatchBatch{
		maxConcurrency:        1,
		nonAdmissionOccupancy: 0,
	}, func() {}, func() {})
	require.True(t, dispatched)
	assert.NotEmpty(t, dispatcher.LastRequest().AdmissionReservationToken)
	procRepository.AssertExpectations(t)
}

func TestQueueProcessorLimitsConcurrentDispatchHandoffs(t *testing.T) {
	t.Parallel()

	processor := NewQueueProcessor(nil, nil, nil, nil, config.Queues{})
	releases := make([]func(), 0, maxConcurrentDispatchHandoffs)
	for range maxConcurrentDispatchHandoffs {
		release, acquired := processor.acquireDispatchHandoff(context.Background())
		require.True(t, acquired)
		releases = append(releases, release)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, acquired := processor.acquireDispatchHandoff(canceled)
	require.False(t, acquired)

	releases[0]()
	release, acquired := processor.acquireDispatchHandoff(context.Background())
	require.True(t, acquired)
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestQueueProcessor_SuspendedSchedulerManagedQueuedRunsAreAbortedAndDequeued(t *testing.T) {
	triggers := []ir.TriggerType{
		ir.TriggerTypeScheduler,
		ir.TriggerTypeCatchUp,
		ir.TriggerTypeRetry,
	}

	for _, trigger := range triggers {
		t.Run(trigger.String(), func(t *testing.T) {
			dagName := "suspended-" + trigger.String() + "-dag"
			f := newQueueFixture(t).
				withDAG(dagName, 1).
				withProcessor(config.Queues{}, WithIsSuspended(func(_ context.Context, name string) (bool, error) {
					return name == dagName, nil
				})).
				simulateQueue(1, false)

			f.enqueueRunWithTrigger("run-1", trigger)

			f.processor.ProcessQueueItems(f.ctx, dagName)

			items, err := f.queueStore.List(f.ctx, dagName)
			require.NoError(t, err)
			require.Len(t, items, 0)

			attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef(dagName, "run-1"))
			require.NoError(t, err)
			status, err := attempt.ReadStatus(f.ctx)
			require.NoError(t, err)
			assert.Equal(t, ir.Aborted, status.Status)
			assert.Equal(t, suspendedQueueDropReason, status.Error)
			assert.NotEmpty(t, status.FinishedAt)
			assert.Equal(t, trigger, status.TriggerType)
		})
	}
}

func TestQueueProcessor_LeavesSchedulerManagedRunQueuedWhenSuspensionReadFails(t *testing.T) {
	dagName := "suspension-read-error-dag"
	f := newQueueFixture(t).
		withDAG(dagName, 1).
		withProcessor(config.Queues{}, WithIsSuspended(func(context.Context, string) (bool, error) {
			return false, errors.New("read suspend flag")
		})).
		simulateQueue(1, false)

	f.enqueueRunWithTrigger("run-1", ir.TriggerTypeScheduler)
	f.processor.ProcessQueueItems(f.ctx, dagName)

	items, err := f.queueStore.List(f.ctx, dagName)
	require.NoError(t, err)
	require.Len(t, items, 1)

	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef(dagName, "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, status.Status)
}

func TestQueueProcessor_SuspendedManualQueuedRunStillDispatches(t *testing.T) {
	dagName := "suspended-manual-dag"
	f := newQueueFixture(t).withDAG(dagName, 1)
	f.enqueueRunWithTrigger("run-1", ir.TriggerTypeManual)

	items, err := f.queueStore.List(f.ctx, dagName)
	require.NoError(t, err)
	require.Len(t, items, 1)

	runRef := ir.NewDAGRunRef(dagName, "run-1")
	procRepository := &mockProcRepository{}
	procRepository.On("IsRunAlive", mock.Anything, dagName, runRef).Return(false, nil).Once()
	procRepository.On("IsRunAlive", mock.Anything, dagName, runRef).Return(true, nil).Once()
	dispatcher := &mockDispatcher{}

	queueDispatcher := newQueueDispatcher(queueDispatchDeps{
		queueStore:       f.queueStore,
		dagRunRepository: f.dagRunRepository,
		procRepository:   procRepository,
		dagExecutor:      NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, ""),
		isSuspended:      func(_ context.Context, name string) (bool, error) { return name == dagName, nil },
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         2,
			StartupGracePeriod: time.Second,
		},
	})

	dispatched := queueDispatcher.dispatchQueuedItem(f.ctx, items[0], dagName, queueDispatchBatch{
		maxConcurrency:        1,
		nonAdmissionOccupancy: 0,
	}, func() {}, func() {})
	require.True(t, dispatched)
	assert.Equal(t, int32(1), dispatcher.callCount.Load())

	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, status.Status)

	procRepository.AssertExpectations(t)
}

type mockDispatchTaskStore struct {
	countOutstandingByQueueFunc func(context.Context, string, time.Duration) (int, error)
	hasOutstandingAttemptFunc   func(context.Context, string, time.Duration) (bool, error)
}

func (m *mockDispatchTaskStore) Enqueue(context.Context, *dispatch.DispatchTask) error {
	return nil
}

func (m *mockDispatchTaskStore) ClaimNext(context.Context, dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
	return nil, nil
}

func (m *mockDispatchTaskStore) GetClaim(context.Context, string) (*dispatch.ClaimedDispatchTask, error) {
	return nil, dispatch.ErrDispatchTaskNotFound
}

func (m *mockDispatchTaskStore) ReleaseClaim(context.Context, string) error {
	return nil
}

func (m *mockDispatchTaskStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (m *mockDispatchTaskStore) CountOutstandingByQueue(ctx context.Context, queueName string, claimTimeout time.Duration) (int, error) {
	if m.countOutstandingByQueueFunc != nil {
		return m.countOutstandingByQueueFunc(ctx, queueName, claimTimeout)
	}
	return 0, nil
}

func (m *mockDispatchTaskStore) HasOutstandingAttempt(ctx context.Context, attemptKey string, claimTimeout time.Duration) (bool, error) {
	if m.hasOutstandingAttemptFunc != nil {
		return m.hasOutstandingAttemptFunc(ctx, attemptKey, claimTimeout)
	}
	return false, nil
}

func TestQueueProcessor_CheckStartupStatusTreatsRunningStatusAsStarted(t *testing.T) {
	f := newQueueFixture(t).withDAG("startup-running-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold))

	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), "running-startup-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	status := ir.InitialStatus(f.dag)
	status.Status = ir.Running
	status.DAGRunID = "running-startup-run"
	status.AttemptID = run.ID()
	require.NoError(t, run.Write(f.ctx, status))
	require.NoError(t, run.Close(f.ctx))

	started, err := f.processor.newQueueDispatcher().checkStartupStatus(
		f.ctx,
		f.dag.Name,
		ir.NewDAGRunRef(f.dag.Name, "running-startup-run"),
		startupWaitState{launchedAt: time.Now().Add(-time.Second)},
	)
	require.NoError(t, err)
	assert.True(t, started)
}

func TestQueueProcessor_CheckStartupStatusTreatsFreshDistributedLeaseAsStarted(t *testing.T) {
	f := newQueueFixture(t).withDAG("startup-lease-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold))

	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), "lease-startup-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	status := ir.InitialStatus(f.dag)
	status.Status = ir.Queued
	status.DAGRunID = "lease-startup-run"
	status.AttemptID = run.ID()
	status.AttemptKey = ir.GenerateAttemptKey(f.dag.Name, "lease-startup-run", f.dag.Name, "lease-startup-run", run.ID())
	require.NoError(t, run.Write(f.ctx, status))
	require.NoError(t, run.Close(f.ctx))

	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      status.AttemptKey,
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
		AttemptID:       run.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	started, err := f.processor.newQueueDispatcher().checkStartupStatus(
		f.ctx,
		f.dag.Name,
		ir.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
		startupWaitState{launchedAt: time.Now().Add(-time.Second)},
	)
	require.NoError(t, err)
	assert.True(t, started)
}

func TestQueueProcessor_GlobalQueueIgnoresDAGMaxActiveRuns(t *testing.T) {
	// Global queue config: MaxActiveRuns=5
	// DAG config: maxActiveRuns=1
	// Expected: Global queue should use 5, NOT be overwritten by DAG's 1
	f := newQueueFixture(t).withDAG("dag-with-low-concurrency", 1).withProcessor(config.Queues{
		Enabled: true, Config: []config.QueueConfig{{Name: "global-queue", MaxActiveRuns: 5}},
	})

	// Enqueue 5 items to the global queue
	for i := 1; i <= 5; i++ {
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), queuedomain.QueuePriorityHigh)
	}

	// Verify initial maxConcurrency is 5 (from global config)
	require.Equal(t, 5, f.getQueue("global-queue").getMaxConcurrency(), "Global queue should have maxConcurrency=5 from config")

	// Process items
	f.processor.ProcessQueueItems(f.ctx, "global-queue")

	// Verify maxConcurrency is STILL 5 (not overwritten by DAG's maxActiveRuns=1)
	assert.Equal(t, 5, f.getQueue("global-queue").getMaxConcurrency(), "Global queue maxConcurrency should NOT be overwritten by DAG")

	// Verify all 5 items were processed in the batch (not just 1)
	assert.Contains(t, f.logs(), "count=5", "Should process 5 items, not 1")
}

func TestQueueProcessor_GlobalQueueViaLoop(t *testing.T) {
	// This test mimics the real scheduler flow where ProcessQueueItems
	// is called via the loop, not directly.
	f := newQueueFixture(t).withDAG("loop-dag", 1).withProcessor(config.Queues{
		Enabled: true, Config: []config.QueueConfig{{Name: "global-queue", MaxActiveRuns: 3}},
	})

	// Enqueue 3 items BEFORE calling process (mimics real scenario)
	for i := 1; i <= 3; i++ {
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), queuedomain.QueuePriorityHigh)
	}

	// Verify queue list returns the global queue
	queueList, err := f.queueStore.QueueList(f.ctx)
	require.NoError(t, err)
	require.Contains(t, queueList, "global-queue")

	// Simulate what loop() does: check if queue exists in p.queues
	q := f.getQueue("global-queue")
	require.Equal(t, 3, q.getMaxConcurrency(), "Global queue should have maxConcurrency=3")
	require.True(t, q.isGlobalQueue(), "Should be marked as global queue")

	// Process
	f.processor.ProcessQueueItems(f.ctx, "global-queue")

	// Verify all 3 items processed
	t.Logf("Logs: %s", f.logs())
	assert.Contains(t, f.logs(), "count=3", "Should process all 3 items")
	assert.Contains(t, f.logs(), "max-concurrency=3", "maxConcurrency should be 3")
}
