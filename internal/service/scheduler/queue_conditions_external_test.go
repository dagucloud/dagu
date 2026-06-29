// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"path/filepath"
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
)

const conditionTestStaleThreshold = time.Hour

func TestQueueProcessorRecordsCapacityFullQueuedCondition(t *testing.T) {
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
	requireQueuedCondition(t, status, "QueueCapacityFull", "queue capacity is full")
}

func TestQueueProcessorKeepsFreshQueuedCondition(t *testing.T) {
	t.Parallel()

	checkedAt := time.Now().UTC().Add(-30 * time.Second)
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
	f.enqueueRun("waiting-run", []exec.DAGRunCondition{
		exec.NewQueuedDAGRunCondition(
			"QueueCapacityFull",
			"DAG-run is waiting because queue capacity is full.",
			checkedAt,
		),
	})

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Equal(t, []exec.DAGRunCondition{
		exec.NewQueuedDAGRunCondition(
			"QueueCapacityFull",
			"DAG-run is waiting because queue capacity is full.",
			checkedAt,
		),
	}, status.Conditions)
}

func TestQueueProcessorRecordsPendingDispatchAdmissionCondition(t *testing.T) {
	t.Parallel()

	dispatchStore := &queueConditionDispatchTaskStore{queueName: "queue-condition"}
	f := newQueueConditionFixture(t, config.ExecutionModeLocal, nil, scheduler.WithDispatchTaskStore(dispatchStore))
	attempt := f.enqueueRun("waiting-run", nil)
	runRef := exec.NewDAGRunRef(f.dag.Name, "waiting-run")
	dispatchStore.attemptKey = exec.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attempt.ID())

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedCondition(t, status, "DispatchAdmissionPending", "dispatch admission is still pending")
}

func TestQueueProcessorRecordsRejectedDispatchAdmissionCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{
		decision: &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedNoCapacity},
	}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedCondition(t, status, "DispatchAdmissionRejected", "no_capacity")
}

func TestQueueProcessorRecordsDistributedDispatchFailureCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{dispatchErr: errors.New("worker unavailable")},
	)
	f.enqueueRun("waiting-run", []exec.DAGRunCondition{
		exec.NewQueuedDAGRunCondition(
			"QueueAccepted",
			"DAG-run is waiting in the queue.",
			time.Now().UTC().Add(-2*time.Minute),
		),
	})

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedCondition(t, status, "DistributedDispatchFailed", "worker unavailable")
}

type queueConditionFixture struct {
	ctx           context.Context
	dag           *core.DAG
	dagRunStore   exec.DAGRunStore
	queueStore    exec.QueueStore
	leaseStore    exec.DAGRunLeaseStore
	dispatchStore exec.DispatchTaskStore
	processor     *scheduler.QueueProcessor
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
	t.Helper()

	tmp := t.TempDir()
	ctx := context.Background()
	dag := &core.DAG{
		Name:     "queue-condition",
		YamlData: []byte("name: queue-condition\nsteps:\n  - name: test\n    command: echo hello\n"),
		Steps:    []core.Step{{Name: "test", Command: "echo hello"}},
	}
	core.InitializeDefaults(dag)
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	leaseStore := store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(tmp, "leases")))
	dispatchStore := store.NewDispatchTaskStore(
		file.NewCollection(filepath.Join(tmp, "dispatch")),
		store.WithDispatchReservationTTL(conditionTestStaleThreshold),
	)
	procStore := proc.New(filepath.Join(tmp, "proc"))
	executor := scheduler.NewDAGExecutor(
		dispatcher,
		launcher.NewSubCmdBuilder(&config.Config{Paths: config.PathsConfig{Executable: "/usr/bin/dagu"}}),
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
		procStore,
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

func requireQueuedCondition(t *testing.T, status *exec.DAGRunStatus, reason, messageContains string) {
	t.Helper()

	require.Equal(t, core.Queued, status.Status)
	require.Len(t, status.Conditions, 1)
	condition := status.Conditions[0]
	require.Equal(t, "Queued", condition.Type)
	require.Equal(t, "True", condition.Status)
	require.Equal(t, reason, condition.Reason)
	require.Contains(t, condition.Message, messageContains)
	checkedAt, err := time.Parse(time.RFC3339, condition.CheckedAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), checkedAt, time.Minute)
}

type queueConditionAdmissionStore struct {
	decision *exec.DispatchAdmissionDecision
}

func (s *queueConditionAdmissionStore) ReserveAdmission(context.Context, exec.DispatchAdmissionRequest) (*exec.DispatchAdmissionDecision, error) {
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
