// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/dagucloud/dagu/internal/persis/store"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
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
	t              *testing.T
	ctx            context.Context
	logBuffer      *syncBuffer
	dagRunStore    exec.DAGRunStore
	leaseStore     exec.DAGRunLeaseStore
	dispatchStore  exec.DispatchTaskStore
	distributedDir string
	queueStore     exec.QueueStore
	procStore      exec.ProcStore
	processor      *QueueProcessor
	dag            *core.DAG
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
		distributedDir: distributedDir,
		dagRunStore:    dagrun.New(filepath.Join(tmpDir, "dag-runs")),
		leaseStore:     store.NewDAGRunLeaseStore(leaseCollection),
		dispatchStore:  store.NewDispatchTaskStore(file.NewCollection(distributedDir)),
		queueStore:     store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue"))),
		procStore:      newSchedulerTestProcStore(filepath.Join(tmpDir, "proc"), nil),
	}
}

func newSchedulerTestProcStore(procDir string, cfg *config.Config) exec.ProcStore {
	opts := []proc.StoreOption{}
	if cfg != nil {
		opts = append(opts,
			proc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
			proc.WithHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
			proc.WithStaleThreshold(cfg.Proc.StaleThreshold),
		)
	}
	return proc.New(procDir, opts...)
}

func (f *queueFixture) withDAG(name string, maxActiveRuns int) *queueFixture {
	f.dag = &core.DAG{
		Name: name, MaxActiveRuns: maxActiveRuns,
		YamlData: fmt.Appendf(nil, "name: %s\nmax_active_runs: %d\nsteps:\n  - name: test\n    command: echo hello", name, maxActiveRuns),
		Steps:    []core.Step{{Name: "test", Command: "echo hello"}},
	}
	return f
}

func (f *queueFixture) enqueueRuns(n int) *queueFixture {
	for i := 1; i <= n; i++ {
		runID := fmt.Sprintf("run-%d", i)
		run, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
		require.NoError(f.t, err)
		require.NoError(f.t, run.Open(f.ctx))
		st := exec.InitialStatus(f.dag)
		st.Status, st.DAGRunID = core.Queued, runID
		require.NoError(f.t, run.Write(f.ctx, st))
		require.NoError(f.t, run.Close(f.ctx))
		require.NoError(f.t, f.queueStore.Enqueue(f.ctx, f.dag.Name, exec.QueuePriorityHigh, exec.NewDAGRunRef(f.dag.Name, runID)))
	}
	return f
}

func (f *queueFixture) withProcessor(cfg config.Queues, opts ...QueueProcessorOption) *queueFixture {
	options := append([]QueueProcessorOption{
		WithBackoffConfig(BackoffConfig{InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond, MaxRetries: 2}),
		WithDAGRunLeaseStore(f.leaseStore),
	}, opts...)
	f.processor = NewQueueProcessor(f.queueStore, f.dagRunStore, f.procStore,
		NewDAGExecutor(nil, launcher.NewSubCmdBuilder(&config.Config{Paths: config.PathsConfig{Executable: "/usr/bin/dagu"}}), config.ExecutionModeLocal, "", nil),
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

func (f *queueFixture) enqueueWithPriority(runID string, priority exec.QueuePriority) {
	f.enqueueToQueue(f.dag.Name, runID, priority)
}

func (f *queueFixture) enqueueRunWithTrigger(runID string, triggerType core.TriggerType) {
	f.enqueueToQueueWithTrigger(f.dag.Name, runID, exec.QueuePriorityHigh, triggerType)
}

func (f *queueFixture) enqueueToQueue(queueName, runID string, priority exec.QueuePriority) {
	f.enqueueToQueueWithTrigger(queueName, runID, priority, core.TriggerTypeUnknown)
}

func (f *queueFixture) enqueueToQueueWithTrigger(queueName, runID string, priority exec.QueuePriority, triggerType core.TriggerType) {
	run, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, run.Open(f.ctx))
	st := exec.InitialStatus(f.dag)
	st.Status, st.DAGRunID = core.Queued, runID
	st.AttemptID = run.ID()
	st.TriggerType = triggerType
	require.NoError(f.t, run.Write(f.ctx, st))
	require.NoError(f.t, run.Close(f.ctx))
	require.NoError(f.t, f.queueStore.Enqueue(f.ctx, queueName, priority, exec.NewDAGRunRef(f.dag.Name, runID)))
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
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), exec.QueuePriorityHigh)
	}

	require.Equal(t, 3, f.getQueue("global-queue").getMaxConcurrency())

	f.processor.ProcessQueueItems(f.ctx, "global-queue")
	assert.Contains(t, f.logs(), "count=3")
}

func TestQueueProcessor_ItemsRemainOnFailure(t *testing.T) {
	f := newQueueFixture(t).withDAG("fifo-dag", 1).enqueueRuns(2).
		withProcessor(config.Queues{Enabled: true, Config: []config.QueueConfig{{Name: "fifo-dag", MaxActiveRuns: 1}}}).
		simulateQueue(1, false)

	f.processor.ProcessQueueItems(f.ctx, "fifo-dag")

	items, err := f.queueStore.List(f.ctx, "fifo-dag")
	require.NoError(t, err)
	require.Len(t, items, 2, "Both items should still be in queue")
}

func TestQueueProcessor_PriorityOrdering(t *testing.T) {
	f := newQueueFixture(t).withDAG("priority-dag", 1).withProcessor(config.Queues{})

	// Enqueue low priority first, then high priority
	f.enqueueWithPriority("low-1", exec.QueuePriorityLow)
	f.enqueueWithPriority("low-2", exec.QueuePriorityLow)
	f.enqueueWithPriority("high-1", exec.QueuePriorityHigh)
	f.enqueueWithPriority("high-2", exec.QueuePriorityHigh)

	// Dequeue should return high priority first, then low priority
	expectedOrder := []string{"high-1", "high-2", "low-1", "low-2"}
	for _, expectedID := range expectedOrder {
		item, err := f.queueStore.DequeueByName(f.ctx, f.dag.Name)
		require.NoError(t, err)
		ref, err := item.Data()
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

func TestQueueProcessor_CountsFreshDistributedRunsAgainstQueueConcurrency(t *testing.T) {
	f := newQueueFixture(t).withDAG("distributed-conc-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold)).
		simulateQueue(1, false)

	runningAttempt, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), "running-run", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, runningAttempt.Open(f.ctx))
	runningStatus := exec.InitialStatus(f.dag)
	runningStatus.Status = core.Queued
	runningStatus.DAGRunID = "running-run"
	runningStatus.AttemptID = runningAttempt.ID()
	runningStatus.WorkerID = "worker-1"
	require.NoError(t, runningAttempt.Write(f.ctx, runningStatus))
	require.NoError(t, runningAttempt.Close(f.ctx))
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "running-run"),
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
		listByQueueFunc: func(context.Context, string) ([]exec.DAGRunLease, error) {
			return nil, errors.New("lease store unavailable")
		},
	}

	f.processor.ProcessQueueItems(f.ctx, "distributed-count-error-dag")

	items, err := f.queueStore.List(f.ctx, "distributed-count-error-dag")
	require.NoError(t, err)
	require.Len(t, items, 1, "queue should remain untouched when distributed lease counting fails")
	assert.Contains(t, f.logs(), "Failed to count distributed leases")
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

	runRef := exec.NewDAGRunRef(f.dag.Name, "run-1")
	attempt, err := f.dagRunStore.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &coordinatorv1.Task{
		DagRunId:   runRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptId:  attempt.ID(),
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

	reservedRef := exec.NewDAGRunRef(f.dag.Name, "run-1")
	reservedAttempt, err := f.dagRunStore.FindAttempt(f.ctx, reservedRef)
	require.NoError(t, err)
	reservedStatus, err := reservedAttempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &coordinatorv1.Task{
		DagRunId:   reservedRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptId:  reservedAttempt.ID(),
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
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(500*time.Millisecond)).
		simulateQueue(1, false)

	f.enqueueRuns(1)

	runRef := exec.NewDAGRunRef(f.dag.Name, "run-1")
	attempt, err := f.dagRunStore.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &coordinatorv1.Task{
		DagRunId:   runRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptId:  attempt.ID(),
		AttemptKey: queueAttemptKey(runRef, attempt, status),
	}))
	agePendingDispatchReservationFiles(t, f.distributedDir, 2*time.Second)

	count, err := f.processor.newQueueDispatcher().countOutstandingDispatchReservations(f.ctx, f.dag.Name)
	require.NoError(t, err)
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

func TestQueueProcessor_SuspendedSchedulerManagedQueuedRunsAreAbortedAndDequeued(t *testing.T) {
	triggers := []core.TriggerType{
		core.TriggerTypeScheduler,
		core.TriggerTypeCatchUp,
		core.TriggerTypeRetry,
	}

	for _, trigger := range triggers {
		t.Run(trigger.String(), func(t *testing.T) {
			dagName := "suspended-" + trigger.String() + "-dag"
			f := newQueueFixture(t).
				withDAG(dagName, 1).
				withProcessor(config.Queues{}, WithIsSuspended(func(_ context.Context, name string) bool {
					return name == dagName
				})).
				simulateQueue(1, false)

			f.enqueueRunWithTrigger("run-1", trigger)

			f.processor.ProcessQueueItems(f.ctx, dagName)

			items, err := f.queueStore.List(f.ctx, dagName)
			require.NoError(t, err)
			require.Len(t, items, 0)

			attempt, err := f.dagRunStore.FindAttempt(f.ctx, exec.NewDAGRunRef(dagName, "run-1"))
			require.NoError(t, err)
			status, err := attempt.ReadStatus(f.ctx)
			require.NoError(t, err)
			assert.Equal(t, core.Aborted, status.Status)
			assert.Equal(t, suspendedQueueDropReason, status.Error)
			assert.NotEmpty(t, status.FinishedAt)
			assert.Equal(t, trigger, status.TriggerType)
		})
	}
}

func TestQueueProcessor_SuspendedManualQueuedRunStillDispatches(t *testing.T) {
	dagName := "suspended-manual-dag"
	f := newQueueFixture(t).withDAG(dagName, 1)
	f.enqueueRunWithTrigger("run-1", core.TriggerTypeManual)

	items, err := f.queueStore.List(f.ctx, dagName)
	require.NoError(t, err)
	require.Len(t, items, 1)

	runRef := exec.NewDAGRunRef(dagName, "run-1")
	procStore := &mockProcStore{}
	procStore.On("IsRunAlive", mock.Anything, dagName, runRef).Return(false, nil).Once()
	procStore.On("IsRunAlive", mock.Anything, dagName, runRef).Return(true, nil).Once()
	dispatcher := &mockDispatcher{}

	queueDispatcher := newQueueDispatcher(queueDispatchDeps{
		queueStore:  f.queueStore,
		dagRunStore: f.dagRunStore,
		procStore:   procStore,
		dagExecutor: NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "", nil),
		isSuspended: func(_ context.Context, name string) bool { return name == dagName },
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         2,
			StartupGracePeriod: time.Second,
		},
	})

	dispatched := queueDispatcher.dispatchQueuedItem(f.ctx, items[0], dagName, func() {}, func() {})
	require.True(t, dispatched)
	assert.Equal(t, int32(1), dispatcher.callCount.Load())

	attempt, err := f.dagRunStore.FindAttempt(f.ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Queued, status.Status)

	procStore.AssertExpectations(t)
}

type mockDispatchTaskStore struct {
	countOutstandingByQueueFunc func(context.Context, string, time.Duration) (int, error)
	hasOutstandingAttemptFunc   func(context.Context, string, time.Duration) (bool, error)
}

func (m *mockDispatchTaskStore) Enqueue(context.Context, *coordinatorv1.Task) error {
	return nil
}

func (m *mockDispatchTaskStore) ClaimNext(context.Context, exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	return nil, nil
}

func (m *mockDispatchTaskStore) GetClaim(context.Context, string) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
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

func agePendingDispatchReservationFiles(t *testing.T, distributedDir string, age time.Duration) {
	t.Helper()

	pendingDir := filepath.Join(distributedDir, "pending")
	entries, err := os.ReadDir(pendingDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	targetTime := time.Now().Add(-age).UTC().UnixMilli()
	for _, entry := range entries {
		path := filepath.Join(pendingDir, entry.Name())
		data, err := os.ReadFile(path)
		require.NoError(t, err)

		var record map[string]any
		require.NoError(t, json.Unmarshal(data, &record))
		if payload, ok := record["data"].(map[string]any); ok {
			payload["enqueuedAt"] = targetTime
		} else {
			record["enqueuedAt"] = targetTime
		}

		updated, err := json.Marshal(record)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, updated, 0o600))
	}
}

func TestQueueProcessor_CheckStartupStatusTreatsRunningStatusAsStarted(t *testing.T) {
	f := newQueueFixture(t).withDAG("startup-running-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold))

	run, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), "running-startup-run", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	status := exec.InitialStatus(f.dag)
	status.Status = core.Running
	status.DAGRunID = "running-startup-run"
	status.AttemptID = run.ID()
	require.NoError(t, run.Write(f.ctx, status))
	require.NoError(t, run.Close(f.ctx))

	started, err := f.processor.newQueueDispatcher().checkStartupStatus(
		f.ctx,
		f.dag.Name,
		exec.NewDAGRunRef(f.dag.Name, "running-startup-run"),
		startupWaitState{launchedAt: time.Now().Add(-time.Second)},
	)
	require.NoError(t, err)
	assert.True(t, started)
}

func TestQueueProcessor_CheckStartupStatusTreatsFreshDistributedLeaseAsStarted(t *testing.T) {
	f := newQueueFixture(t).withDAG("startup-lease-dag", 1).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold))

	run, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), "lease-startup-run", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	status := exec.InitialStatus(f.dag)
	status.Status = core.Queued
	status.DAGRunID = "lease-startup-run"
	status.AttemptID = run.ID()
	status.AttemptKey = exec.GenerateAttemptKey(f.dag.Name, "lease-startup-run", f.dag.Name, "lease-startup-run", run.ID())
	require.NoError(t, run.Write(f.ctx, status))
	require.NoError(t, run.Close(f.ctx))

	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      status.AttemptKey,
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
		AttemptID:       run.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	started, err := f.processor.newQueueDispatcher().checkStartupStatus(
		f.ctx,
		f.dag.Name,
		exec.NewDAGRunRef(f.dag.Name, "lease-startup-run"),
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
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), exec.QueuePriorityHigh)
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
		f.enqueueToQueue("global-queue", fmt.Sprintf("run-%d", i), exec.QueuePriorityHigh)
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
