// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const conditionTestStaleThreshold = time.Hour

func TestLocalLaunchFailedOnlyClassifiesExecutionPathErrors(t *testing.T) {
	t.Parallel()

	require.False(t, scheduler.LocalLaunchFailedForTest(errors.New("read status failed")))
	require.True(t, scheduler.LocalLaunchFailedForTest(
		scheduler.NewStartupExecutionErrorForTest(errors.New("launcher failed")),
	))
}

func TestQueueProcessorRecordsConcurrencyLimitQueuedCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil)
	runningAttempt := f.createQueuedAttempt("running-run", nil)
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, maxConcurrencyReachedConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorSkipsQueuedConditionRefreshWhenLivenessUnavailable(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeLocal,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			procStore: func(store exec.ProcStore) exec.ProcStore {
				return &queueConditionProcStore{
					ProcStore:     store,
					isRunAliveErr: errors.New("liveness unavailable"),
				}
			},
		},
	)
	runningAttempt := f.createQueuedAttempt("running-run", nil)
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Empty(t, status.Conditions)
	require.Equal(t, 0, f.casCount("waiting-run"))
}

func TestQueueProcessorSkipsQueuedConditionRefreshForFreshDistributedLease(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil)
	attempt := f.enqueueRun("leased-run", nil)
	runRef := exec.NewDAGRunRef(f.dag.Name, "leased-run")
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attempt.ID()),
		DAGRun:          runRef,
		Root:            runRef,
		AttemptID:       attempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("leased-run")
	require.Empty(t, status.Conditions)
	require.Equal(t, 0, f.casCount("leased-run"))
}

func TestQueueProcessorKeepsFreshQueuedCondition(t *testing.T) {
	t.Parallel()

	checkedAt := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Second)
	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil)
	runningAttempt := f.createQueuedAttempt("running-run", nil)
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	conditions := []exec.DAGRunCondition{
		exec.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			checkedAt,
		),
		exec.NewDAGRunCondition(
			"ConcurrencyReady",
			"False",
			"MaxConcurrencyReached",
			"The queue active-run concurrency limit has been reached.",
			checkedAt,
		),
	}
	f.enqueueRun("waiting-run", conditions)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Equal(t, conditions, status.Conditions)
	require.Equal(t, 0, f.casCount("waiting-run"))
}

func TestQueueProcessorDoesNotOverwriteNewerQueuedConditionSet(t *testing.T) {
	t.Parallel()

	checkedAt := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil)
	runningAttempt := f.createQueuedAttempt("running-run", nil)
	require.NoError(t, f.leaseStore.Upsert(f.ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          exec.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            exec.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	conditions := []exec.DAGRunCondition{
		exec.NewDAGRunCondition(
			"Runnable",
			"False",
			"NoMatchingWorker",
			"The DAG-run cannot start because no healthy worker matches the required selector.",
			checkedAt,
		),
		exec.NewDAGRunCondition(
			"WorkerReady",
			"False",
			"NoMatchingWorker",
			"No healthy worker matches the required worker selector.",
			checkedAt,
		),
	}
	f.enqueueRun("waiting-run", conditions)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Equal(t, conditions, status.Conditions)
	require.Equal(t, 0, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsCapacityAndPendingDispatchAdmissionCondition(t *testing.T) {
	t.Parallel()

	dispatchStore := &queueConditionDispatchTaskStore{queueName: "queue-condition"}
	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil, scheduler.WithDispatchTaskStore(dispatchStore))
	attempt := f.enqueueRun("waiting-run", nil)
	runRef := exec.NewDAGRunRef(f.dag.Name, "waiting-run")
	dispatchStore.attemptKey = exec.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attempt.ID())

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, maxConcurrencyReachedWithAssignmentPendingConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsNoDispatchCapacityCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{
		decision: &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedNoCapacity},
	}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, maxConcurrencyReachedConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsDuplicateDispatchAdmissionAsPendingCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{
		decision: &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedDuplicate},
	}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, assignmentPendingConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsAdmissionUnavailableCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{err: errors.New("reservation store unavailable")}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, assignmentUnavailableConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsMissingAttemptIdentityCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{
		decision: &exec.DispatchAdmissionDecision{Reserved: true, ReservationToken: "token-1"},
	}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)
	f.updateStatus("waiting-run", func(status *exec.DAGRunStatus) {
		status.AttemptID = ""
		status.AttemptKey = ""
	})
	f.dagRunStore.blankAttemptID("waiting-run")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, attemptIdentityMissingConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsDAGSnapshotUnavailableCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, nil)
	f.enqueueRun("waiting-run", nil)
	f.dagRunStore.failReadDAG("waiting-run", errors.New("snapshot read failed"))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, dagSnapshotUnavailableConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsNoMatchingWorkerCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{dispatchErr: status.Error(codes.FailedPrecondition, "no workers match the required selector")},
	)
	f.enqueueRun("waiting-run", []exec.DAGRunCondition{
		exec.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			time.Now().UTC().Add(-2*time.Minute),
		),
		exec.NewDAGRunCondition(
			"ConcurrencyReady",
			"False",
			"MaxConcurrencyReached",
			"The queue active-run concurrency limit has been reached.",
			time.Now().UTC().Add(-2*time.Minute),
		),
	})

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, noMatchingWorkerConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsNoAvailableWorkerCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{dispatchErr: status.Error(codes.Unavailable, "no available workers")},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, noAvailableWorkerConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsWorkerDispatchUnavailableCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{dispatchErr: errors.New("coordinator unavailable: internal endpoint 10.0.0.5")},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, workerDispatchUnavailableConditions()...)
	for _, condition := range status.Conditions {
		require.NotContains(t, condition.Message, "coordinator unavailable")
		require.NotContains(t, condition.Message, "10.0.0.5")
	}
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorSkipsDispatchConditionForShutdownCancellation(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{dispatchErr: context.Canceled},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Empty(t, status.Conditions)
	require.Equal(t, 0, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsLaunchFailedCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeLocal,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			executable: filepath.Join(t.TempDir(), "missing-dagu"),
			procStore: func(base exec.ProcStore) exec.ProcStore {
				return &queueConditionProcStore{
					ProcStore:       base,
					isRunAliveDelay: 50 * time.Millisecond,
				}
			},
		},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, launchFailedConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsStartupNotObservedCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, nil)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, startupNotObservedConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsRunLivenessUnavailableCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			procStore: func(base exec.ProcStore) exec.ProcStore {
				return &queueConditionProcStore{
					ProcStore:          base,
					isRunAliveErrAfter: 1,
					isRunAliveErr:      errors.New("proc store unavailable"),
				}
			},
		},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, runLivenessUnavailableConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsQueueStateUnavailableConditionOnCountError(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeLocal,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			procStore: func(base exec.ProcStore) exec.ProcStore {
				return &queueConditionProcStore{
					ProcStore:     base,
					countAliveErr: errors.New("count alive failed"),
				}
			},
		},
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, queueStateUnavailableConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

type queueConditionFixture struct {
	ctx           context.Context
	dag           *core.DAG
	dagRunStore   *countingDAGRunStore
	queueStore    exec.QueueStore
	leaseStore    exec.DAGRunLeaseStore
	dispatchStore exec.DispatchTaskStore
	processor     *scheduler.QueueProcessor
}

type queueConditionFixtureConfig struct {
	executable string
	procStore  func(exec.ProcStore) exec.ProcStore
}

func newQueueConditionFixture(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore exec.DispatchAdmissionStore,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	return newQueueConditionFixtureWithDispatcher(t, mode, admissionStore, &queueConditionDispatcher{}, extraOptions...)
}

func newQueueConditionFixtureWithDispatcher(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore exec.DispatchAdmissionStore,
	dispatcher *queueConditionDispatcher,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	return newQueueConditionFixtureWithConfig(t, mode, admissionStore, dispatcher, queueConditionFixtureConfig{}, extraOptions...)
}

func newQueueConditionFixtureWithConfig(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore exec.DispatchAdmissionStore,
	dispatcher *queueConditionDispatcher,
	fixtureConfig queueConditionFixtureConfig,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	t.Helper()

	tmp := t.TempDir()
	ctx := context.Background()
	dag := &core.DAG{
		Name:     "queue-condition",
		YamlData: []byte("name: queue-condition\nsteps:\n  - name: test\n    command: echo hello\n"),
		Steps:    []core.Step{{Name: "test", Command: "echo hello"}},
	}
	core.InitializeDefaults(dag)
	dagRunStore := newCountingDAGRunStore(dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false)))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	leaseStore := store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(tmp, "leases")))
	dispatchStore := store.NewDispatchTaskStore(
		file.NewCollection(filepath.Join(tmp, "dispatch")),
		store.WithDispatchReservationTTL(conditionTestStaleThreshold),
	)
	procStore := proc.New(filepath.Join(tmp, "proc"))
	var processorProcStore exec.ProcStore = procStore
	if fixtureConfig.procStore != nil {
		processorProcStore = fixtureConfig.procStore(procStore)
	}
	executable := fixtureConfig.executable
	if executable == "" {
		executable = "/usr/bin/dagu"
	}
	executor := scheduler.NewDAGExecutor(
		dispatcher,
		launcher.NewSubCmdBuilder(&config.Config{Paths: config.PathsConfig{Executable: executable}}),
		mode,
		"",
		nil,
	)
	opts := []scheduler.QueueProcessorOption{
		scheduler.WithBackoffConfig(scheduler.BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         2,
			StartupGracePeriod: 10 * time.Millisecond,
		}),
		scheduler.WithDAGRunLeaseStore(leaseStore),
		scheduler.WithLeaseStaleThreshold(conditionTestStaleThreshold),
	}
	if admissionStore != nil {
		opts = append(opts, scheduler.WithDispatchAdmissionStore(admissionStore))
	}
	opts = append(opts, extraOptions...)
	processor := scheduler.NewQueueProcessor(
		queueStore,
		dagRunStore,
		processorProcStore,
		executor,
		config.Queues{
			Enabled: true,
			Config:  []config.QueueConfig{{Name: dag.Name, MaxActiveRuns: 1}},
		},
		opts...,
	)
	return &queueConditionFixture{
		ctx:           ctx,
		dag:           dag,
		dagRunStore:   dagRunStore,
		queueStore:    queueStore,
		leaseStore:    leaseStore,
		dispatchStore: dispatchStore,
		processor:     processor,
	}
}

func (f *queueConditionFixture) createQueuedAttempt(runID string, conditions []exec.DAGRunCondition) exec.DAGRunAttempt {
	attempt, err := f.dagRunStore.CreateAttempt(f.ctx, f.dag, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
	if err != nil {
		panic(err)
	}
	if err := attempt.Open(f.ctx); err != nil {
		panic(err)
	}
	status := exec.InitialStatus(f.dag)
	status.DAGRunID = runID
	status.AttemptID = attempt.ID()
	status.Status = core.Queued
	status.Conditions = conditions
	if err := attempt.Write(f.ctx, status); err != nil {
		panic(err)
	}
	if err := attempt.Close(f.ctx); err != nil {
		panic(err)
	}
	return attempt
}

func (f *queueConditionFixture) enqueueRun(runID string, conditions []exec.DAGRunCondition) exec.DAGRunAttempt {
	attempt := f.createQueuedAttempt(runID, conditions)
	if err := f.queueStore.Enqueue(f.ctx, f.dag.Name, exec.QueuePriorityHigh, exec.NewDAGRunRef(f.dag.Name, runID)); err != nil {
		panic(err)
	}
	return attempt
}

func (f *queueConditionFixture) readStatus(runID string) *exec.DAGRunStatus {
	attempt, err := f.dagRunStore.FindAttempt(f.ctx, exec.NewDAGRunRef(f.dag.Name, runID))
	if err != nil {
		panic(err)
	}
	status, err := attempt.ReadStatus(f.ctx)
	if err != nil {
		panic(err)
	}
	return status
}

func (f *queueConditionFixture) updateStatus(runID string, mutate func(*exec.DAGRunStatus)) {
	attempt, err := f.dagRunStore.DAGRunStore.FindAttempt(f.ctx, exec.NewDAGRunRef(f.dag.Name, runID))
	if err != nil {
		panic(err)
	}
	status, err := attempt.ReadStatus(f.ctx)
	if err != nil {
		panic(err)
	}
	mutate(status)
	if err := attempt.Open(f.ctx); err != nil {
		panic(err)
	}
	if err := attempt.Write(f.ctx, *status); err != nil {
		panic(err)
	}
	if err := attempt.Close(f.ctx); err != nil {
		panic(err)
	}
}

func (f *queueConditionFixture) casCount(runID string) int {
	return f.dagRunStore.casCount(runID)
}

type expectedQueuedCondition struct {
	conditionType string
	status        string
	reason        string
	message       string
}

func requireQueuedConditions(t *testing.T, status *exec.DAGRunStatus, expected ...expectedQueuedCondition) {
	t.Helper()

	require.Equal(t, core.Queued, status.Status)
	require.Len(t, status.Conditions, len(expected))

	byType := make(map[string]exec.DAGRunCondition, len(status.Conditions))
	for _, condition := range status.Conditions {
		byType[condition.Type] = condition
		checkedAt, err := time.Parse(time.RFC3339, condition.CheckedAt)
		require.NoError(t, err)
		require.WithinDuration(t, time.Now(), checkedAt, time.Minute)
	}

	for _, want := range expected {
		condition, ok := byType[want.conditionType]
		require.True(t, ok, "missing condition type %q in %v", want.conditionType, status.Conditions)
		require.Equal(t, want.status, condition.Status)
		require.Equal(t, want.reason, condition.Reason)
		require.Equal(t, want.message, condition.Message)
	}
}

func maxConcurrencyReachedConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "MaxConcurrencyReached",
			message:       "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		},
		{
			conditionType: "ConcurrencyReady",
			status:        "False",
			reason:        "MaxConcurrencyReached",
			message:       "The queue active-run concurrency limit has been reached.",
		},
	}
}

func assignmentPendingConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "AssignmentPending",
			message:       "The DAG-run is waiting while Dagu assigns it to a worker.",
		},
		{
			conditionType: "WorkerAssignmentReady",
			status:        "Unknown",
			reason:        "AssignmentPending",
			message:       "Worker assignment is already pending.",
		},
	}
}

func maxConcurrencyReachedWithAssignmentPendingConditions() []expectedQueuedCondition {
	conditions := maxConcurrencyReachedConditions()
	return append(conditions, expectedQueuedCondition{
		conditionType: "WorkerAssignmentReady",
		status:        "Unknown",
		reason:        "AssignmentPending",
		message:       "Worker assignment is already pending.",
	})
}

func assignmentUnavailableConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "Unknown",
			reason:        "AssignmentUnavailable",
			message:       "Dagu cannot determine whether worker assignment can proceed.",
		},
		{
			conditionType: "WorkerAssignmentReady",
			status:        "Unknown",
			reason:        "AssignmentUnavailable",
			message:       "Worker assignment is temporarily unavailable.",
		},
	}
}

func attemptIdentityMissingConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "AttemptIdentityMissing",
			message:       "The DAG-run cannot start because its queued attempt identity is incomplete.",
		},
		{
			conditionType: "RunRecordReady",
			status:        "False",
			reason:        "AttemptIdentityMissing",
			message:       "The queued attempt is missing the identity required for worker assignment.",
		},
	}
}

func dagSnapshotUnavailableConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "DAGSnapshotUnavailable",
			message:       "The DAG-run cannot start because its persisted DAG snapshot could not be read.",
		},
		{
			conditionType: "RunRecordReady",
			status:        "False",
			reason:        "DAGSnapshotUnavailable",
			message:       "The queued attempt exists, but its DAG snapshot is unavailable.",
		},
	}
}

func noMatchingWorkerConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "NoMatchingWorker",
			message:       "The DAG-run cannot start because no healthy worker matches the required selector.",
		},
		{
			conditionType: "WorkerReady",
			status:        "False",
			reason:        "NoMatchingWorker",
			message:       "No healthy worker matches the required worker selector.",
		},
	}
}

func noAvailableWorkerConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "NoAvailableWorker",
			message:       "The DAG-run cannot start because no healthy distributed worker is available.",
		},
		{
			conditionType: "WorkerReady",
			status:        "False",
			reason:        "NoAvailableWorker",
			message:       "No healthy distributed worker is available.",
		},
	}
}

func workerDispatchUnavailableConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "Unknown",
			reason:        "WorkerDispatchUnavailable",
			message:       "Dagu attempted worker assignment, but assignment is temporarily unavailable.",
		},
		{
			conditionType: "WorkerAssignmentReady",
			status:        "Unknown",
			reason:        "WorkerDispatchUnavailable",
			message:       "Worker dispatch is temporarily unavailable.",
		},
	}
}

func launchFailedConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "False",
			reason:        "LaunchFailed",
			message:       "The DAG-run cannot start because local launch failed before startup was observed.",
		},
		{
			conditionType: "StartObserved",
			status:        "False",
			reason:        "LaunchFailed",
			message:       "Local launch failed before any started signal was observed.",
		},
	}
}

func startupNotObservedConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "Unknown",
			reason:        "StartupNotObserved",
			message:       "Dagu attempted to start the DAG-run but has not observed a heartbeat, lease, or status transition.",
		},
		{
			conditionType: "StartObserved",
			status:        "False",
			reason:        "StartupNotObserved",
			message:       "No started signal was observed after the startup wait window.",
		},
	}
}

func runLivenessUnavailableConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "StartObserved",
			status:        "Unknown",
			reason:        "RunLivenessUnavailable",
			message:       "Dagu could not check run liveness while waiting for startup.",
		},
	}
}

func queueStateUnavailableConditions() []expectedQueuedCondition {
	return []expectedQueuedCondition{
		{
			conditionType: "Runnable",
			status:        "Unknown",
			reason:        "QueueStateUnavailable",
			message:       "The DAG-run cannot start because queue state could not be checked.",
		},
		{
			conditionType: "QueueReady",
			status:        "Unknown",
			reason:        "QueueStateUnavailable",
			message:       "Dagu could not inspect queue state needed for dispatch.",
		},
	}
}

type queueConditionAdmissionStore struct {
	decision *exec.DispatchAdmissionDecision
	err      error
}

func (s *queueConditionAdmissionStore) ReserveAdmission(context.Context, exec.DispatchAdmissionRequest) (*exec.DispatchAdmissionDecision, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.decision, nil
}

func (s *queueConditionAdmissionStore) BindAdmission(context.Context, exec.DispatchAdmissionBindRequest) error {
	return nil
}

func (s *queueConditionAdmissionStore) ReleaseAdmissionToken(context.Context, string) error {
	return nil
}

func (s *queueConditionAdmissionStore) FinalizeAdmissionAttempt(context.Context, string) error {
	return nil
}

func (s *queueConditionAdmissionStore) CleanupAdmissions(context.Context, time.Duration) error {
	return nil
}

type queueConditionDispatchTaskStore struct {
	queueName  string
	attemptKey string
}

func (s *queueConditionDispatchTaskStore) Enqueue(context.Context, *exec.DispatchTask) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) ClaimNext(context.Context, exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
}

func (s *queueConditionDispatchTaskStore) GetClaim(context.Context, string) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
}

func (s *queueConditionDispatchTaskStore) ReleaseClaim(context.Context, string) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) CountOutstandingByQueue(_ context.Context, queueName string, _ time.Duration) (int, error) {
	if s.queueName == queueName && s.attemptKey != "" {
		return 1, nil
	}
	return 0, nil
}

func (s *queueConditionDispatchTaskStore) HasOutstandingAttempt(_ context.Context, attemptKey string, _ time.Duration) (bool, error) {
	return s.attemptKey != "" && s.attemptKey == attemptKey, nil
}

type queueConditionDispatcher struct {
	dispatchErr error
}

func (d *queueConditionDispatcher) Dispatch(context.Context, exec.DispatchRequest) error {
	return d.dispatchErr
}

func (d *queueConditionDispatcher) Cleanup(context.Context) error {
	return nil
}

func (d *queueConditionDispatcher) GetDAGRunStatus(context.Context, string, string, *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (d *queueConditionDispatcher) RequestCancel(context.Context, string, string, *exec.DAGRunRef) error {
	return nil
}

type countingDAGRunStore struct {
	exec.DAGRunStore

	mu                 sync.Mutex
	casByRun           map[string]int
	blankAttemptIDRuns map[string]struct{}
	readDAGErrByRun    map[string]error
}

func newCountingDAGRunStore(store exec.DAGRunStore) *countingDAGRunStore {
	return &countingDAGRunStore{
		DAGRunStore:        store,
		casByRun:           make(map[string]int),
		blankAttemptIDRuns: make(map[string]struct{}),
		readDAGErrByRun:    make(map[string]error),
	}
}

func (s *countingDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	opts ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	s.mu.Lock()
	s.casByRun[dagRun.ID]++
	s.mu.Unlock()
	return s.DAGRunStore.CompareAndSwapLatestAttemptStatus(ctx, dagRun, expectedAttemptID, expectedStatus, mutate, opts...)
}

func (s *countingDAGRunStore) FindAttempt(ctx context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	attempt, err := s.DAGRunStore.FindAttempt(ctx, dagRun)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	_, blankID := s.blankAttemptIDRuns[dagRun.ID]
	readDAGErr := s.readDAGErrByRun[dagRun.ID]
	s.mu.Unlock()
	if !blankID && readDAGErr == nil {
		return attempt, nil
	}
	return &queueConditionAttempt{
		DAGRunAttempt: attempt,
		blankID:       blankID,
		readDAGErr:    readDAGErr,
	}, nil
}

func (s *countingDAGRunStore) blankAttemptID(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blankAttemptIDRuns[runID] = struct{}{}
}

func (s *countingDAGRunStore) failReadDAG(runID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readDAGErrByRun[runID] = err
}

func (s *countingDAGRunStore) casCount(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.casByRun[runID]
}

type queueConditionAttempt struct {
	exec.DAGRunAttempt
	blankID    bool
	readDAGErr error
}

func (a *queueConditionAttempt) ID() string {
	if a.blankID {
		return ""
	}
	return a.DAGRunAttempt.ID()
}

func (a *queueConditionAttempt) ReadDAG(ctx context.Context) (*core.DAG, error) {
	if a.readDAGErr != nil {
		return nil, a.readDAGErr
	}
	return a.DAGRunAttempt.ReadDAG(ctx)
}

type queueConditionProcStore struct {
	exec.ProcStore

	mu                 sync.Mutex
	countAliveErr      error
	isRunAliveDelay    time.Duration
	isRunAliveErrAfter int
	isRunAliveErr      error
	isRunAliveCalls    int
}

func (s *queueConditionProcStore) CountAlive(ctx context.Context, groupName string) (int, error) {
	if s.countAliveErr != nil {
		return 0, s.countAliveErr
	}
	return s.ProcStore.CountAlive(ctx, groupName)
}

func (s *queueConditionProcStore) IsRunAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef) (bool, error) {
	s.mu.Lock()
	s.isRunAliveCalls++
	calls := s.isRunAliveCalls
	errAfter := s.isRunAliveErrAfter
	err := s.isRunAliveErr
	s.mu.Unlock()

	if err != nil && calls > errAfter {
		return false, err
	}
	if s.isRunAliveDelay > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(s.isRunAliveDelay):
		}
	}
	return s.ProcStore.IsRunAlive(ctx, groupName, dagRun)
}
