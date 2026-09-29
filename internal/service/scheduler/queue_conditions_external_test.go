// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const conditionTestStaleThreshold = time.Hour

type processRepository interface {
	CountAlive(ctx context.Context, groupName string) (int, error)
	IsRunAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (bool, error)
}

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
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "running-run"),
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
			procRepository: func(store processRepository) processRepository {
				return &queueConditionProcRepository{
					processRepository: store,
					isRunAliveErr:     errors.New("liveness unavailable"),
				}
			},
		},
	)
	runningAttempt := f.createQueuedAttempt("running-run", nil)
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "running-run"),
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
	runRef := ir.NewDAGRunRef(f.dag.Name, "leased-run")
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attempt.ID()),
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
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	conditions := []ir.DAGRunCondition{
		ir.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			checkedAt,
		),
		ir.NewDAGRunCondition(
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
	require.NoError(t, f.leaseStore.Upsert(f.ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(f.dag.Name, "running-run", f.dag.Name, "running-run", runningAttempt.ID()),
		DAGRun:          ir.NewDAGRunRef(f.dag.Name, "running-run"),
		Root:            ir.NewDAGRunRef(f.dag.Name, "running-run"),
		AttemptID:       runningAttempt.ID(),
		QueueName:       f.dag.Name,
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	conditions := []ir.DAGRunCondition{
		ir.NewDAGRunCondition(
			"Runnable",
			"False",
			"NoMatchingWorker",
			"The DAG-run cannot start because no healthy worker matches the required selector.",
			checkedAt,
		),
		ir.NewDAGRunCondition(
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
	runRef := ir.NewDAGRunRef(f.dag.Name, "waiting-run")
	dispatchStore.attemptKey = ir.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attempt.ID())

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, maxConcurrencyReachedWithAssignmentPendingConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsNoDispatchCapacityCondition(t *testing.T) {
	t.Parallel()

	admissionStore := &queueConditionAdmissionStore{
		decision: &dispatch.DispatchAdmissionDecision{Reason: dispatch.DispatchAdmissionRejectedNoCapacity},
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
		decision: &dispatch.DispatchAdmissionDecision{Reason: dispatch.DispatchAdmissionRejectedDuplicate},
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
		decision: &dispatch.DispatchAdmissionDecision{Reserved: true, ReservationToken: "token-1"},
	}
	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, admissionStore)
	f.enqueueRun("waiting-run", nil)
	f.updateStatus("waiting-run", func(status *ir.DAGRunStatus) {
		status.AttemptID = ""
		status.AttemptKey = ""
	})
	f.dagRunRepository.blankAttemptID("waiting-run")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	requireQueuedConditions(t, status, attemptIdentityMissingConditions()...)
	require.Equal(t, 1, f.casCount("waiting-run"))
}

func TestQueueProcessorRecordsDAGSnapshotUnavailableCondition(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixture(t, config.ExecutionModeDistributed, nil)
	f.enqueueRun("waiting-run", nil)
	f.dagRunRepository.failReadDAG("waiting-run", errors.New("snapshot read failed"))

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
	f.enqueueRun("waiting-run", []ir.DAGRunCondition{
		ir.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			time.Now().UTC().Add(-2*time.Minute),
		),
		ir.NewDAGRunCondition(
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

func TestQueueProcessorWaitsForMatchingWorkerBeforeDispatch(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	f.dag.SourceFile = filepath.Join(t.TempDir(), "dag.yaml")
	f.dag.Steps[0].Dependencies = []string{"large-dependency.bin"}
	f.enqueueRun("waiting-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	requireQueuedConditions(t, f.readStatus("waiting-run"), noMatchingWorkerConditions()...)
	require.Zero(t, dispatcher.callCount.Load())
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func TestQueueProcessorMissingSelectedWorker(t *testing.T) {
	t.Parallel()

	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("Stale=%t", stale), func(t *testing.T) {
			t.Parallel()
			dispatcher := &queueConditionDispatcher{}
			heartbeats := store.NewWorkerHeartbeatStore(file.NewCollection(filepath.Join(t.TempDir(), "heartbeats")))
			f := newQueueConditionFixtureWithDispatcher(t, config.ExecutionModeDistributed, nil, dispatcher,
				scheduler.WithWorkerHeartbeatStore(heartbeats))
			f.dag.WorkerSelector = map[string]string{"type": "gpu"}
			f.enqueueRun("waiting-run", nil)
			if stale {
				require.NoError(t, heartbeats.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
					WorkerID: "worker-1", Labels: f.dag.WorkerSelector,
					LastHeartbeatAt: time.Now().Add(-time.Hour).UnixMilli(),
				}))
			}
			f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
			requireQueuedConditions(t, f.readStatus("waiting-run"), noMatchingWorkerConditions()...)
			require.Zero(t, dispatcher.callCount.Load())
		})
	}
}

func TestQueueProcessorWaitsForFreshWorkerBeforeDispatch(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.enqueueRun("waiting-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "stale-worker",
		LastHeartbeatAt: time.Now().Add(-2 * dispatch.DefaultStaleWorkerHeartbeatThreshold).UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	requireQueuedConditions(t, f.readStatus("waiting-run"), noAvailableWorkerConditions()...)
	require.Zero(t, dispatcher.callCount.Load())
}

func TestQueueProcessorUsesConfiguredWorkerHeartbeatFreshness(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
		scheduler.WithWorkerHeartbeatStaleThreshold(time.Minute),
	)
	f.enqueueRun("waiting-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().Add(-45 * time.Second).UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	require.Equal(t, int32(1), dispatcher.callCount.Load())
}

func TestQueueProcessorRecordsAssignmentUnavailableWhenWorkerHeartbeatsCannotBeRead(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(&failingWorkerHeartbeatStore{err: errors.New("heartbeat store unavailable")}),
	)
	f.enqueueRun("waiting-run", nil)

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	requireQueuedConditions(t, f.readStatus("waiting-run"), assignmentUnavailableConditions()...)
	require.Zero(t, dispatcher.callCount.Load())
}

func TestQueueProcessorContinuesPastItemWithoutMatchingWorker(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := &countingWorkerHeartbeatStore{WorkerHeartbeatStore: store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)}
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	f.enqueueRun("gpu-run", nil)
	f.dag.WorkerSelector = map[string]string{"type": "cpu"}
	f.enqueueRun("cpu-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	requireQueuedConditions(t, f.readStatus("gpu-run"), noMatchingWorkerConditions()...)
	require.Equal(t, int32(1), dispatcher.callCount.Load())
	require.Equal(t, int32(1), heartbeatStore.listCount.Load())
}

func TestQueueProcessorRotatesPastBoundedWorkerEligibilityWindow(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
		scheduler.WithWorkerHeartbeatStaleThreshold(conditionTestStaleThreshold),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	for i := range 100 {
		f.enqueueRun(fmt.Sprintf("gpu-run-%03d", i), nil)
	}
	f.dag.WorkerSelector = map[string]string{"type": "cpu"}
	f.enqueueRun("cpu-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	require.Zero(t, dispatcher.callCount.Load())

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	require.Equal(t, int32(1), dispatcher.callCount.Load())
}

func TestQueueProcessorChecksHeadBeforeRotatingWorkerEligibilityWindow(t *testing.T) {
	t.Parallel()

	dispatcher := &queueConditionDispatcher{}
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithDispatcher(
		t,
		config.ExecutionModeDistributed,
		nil,
		dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	f.enqueueRun("head-run", nil)
	for i := range 100 {
		f.enqueueRun(fmt.Sprintf("later-run-%03d", i), nil)
	}
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	require.Empty(t, dispatcher.dispatchedRunIDs())

	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "gpu-worker",
		Labels:          map[string]string{"type": "gpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	require.Equal(t, []string{"head-run"}, dispatcher.dispatchedRunIDs())
}

func TestQueueProcessorWorkerEligibilityDoesNotRetainQueuedStatuses(t *testing.T) {
	t.Parallel()

	statusCache := fileutil.NewCache[*ir.DAGRunStatus]("dag_run_status", 100, time.Hour)
	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{statusCache: statusCache},
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	for i := range 3 {
		f.enqueueRun(fmt.Sprintf("gpu-run-%d", i), nil)
	}
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	require.Zero(t, statusCache.Size())
}

func TestQueueProcessorRecordsWorkerConditionWithoutFullQueueList(t *testing.T) {
	t.Parallel()

	heartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(t.TempDir(), "worker-heartbeats")),
	)
	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeDistributed,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			queueStore: func(base queuedomain.QueueStore) queuedomain.QueueStore {
				return &fullListFailingQueueStore{QueueStore: base}
			},
		},
		scheduler.WithWorkerHeartbeatStore(heartbeatStore),
	)
	f.dag.WorkerSelector = map[string]string{"type": "gpu"}
	f.enqueueRun("waiting-run", nil)
	require.NoError(t, heartbeatStore.Upsert(f.ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID:        "cpu-worker",
		Labels:          map[string]string{"type": "cpu"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
	requireQueuedConditions(t, f.readStatus("waiting-run"), noMatchingWorkerConditions()...)
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

// A fresh condition is an observation, not a latch: the same queue item must
// recover from an old NoMatchingWorker diagnosis and dispatch when a poller returns.
func TestQueueProcessorRecoversBusyWorker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	heartbeats := store.NewWorkerHeartbeatStore(file.NewCollection(filepath.Join(t.TempDir(), "heartbeats")))
	var h *coordinator.Handler
	labels := map[string]string{"type": "gpu"}
	dispatcher := &queueConditionDispatcher{dispatchFn: func(ctx context.Context, req dispatch.DispatchRequest) error {
		task, err := convert.DispatchTaskToProto(req.Task)
		if err != nil {
			return err
		}
		_, err = h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
		return err
	}}
	f := newQueueConditionFixtureWithDispatcher(t, config.ExecutionModeDistributed, nil, dispatcher,
		scheduler.WithWorkerHeartbeatStore(heartbeats),
		scheduler.WithWorkerHeartbeatStaleThreshold(conditionTestStaleThreshold))
	f.ctx = ctx
	h = coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository:     f.dagRunRepository.repository,
		WorkerHeartbeatStore: heartbeats, StaleHeartbeatThreshold: conditionTestStaleThreshold,
	})
	t.Cleanup(func() { h.Close(context.Background()) })
	_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
		WorkerId: "worker-1", Labels: labels,
		Stats: &coordinatorv1.WorkerStats{TotalPollers: 1, BusyPollers: 1},
	})
	require.NoError(t, err)
	f.dag.WorkerSelector = labels
	f.dag.YamlData = []byte("name: queue-condition\nworker_selector:\n  type: gpu\nsteps:\n  - name: test\n    command: echo hello\n")
	var oldConditions []ir.DAGRunCondition
	for _, condition := range noMatchingWorkerConditions() {
		oldConditions = append(oldConditions, ir.NewDAGRunCondition(
			condition.conditionType, condition.status, condition.reason, condition.message, time.Now().UTC()))
	}
	attempt := f.enqueueRun("waiting-run", oldConditions)
	before, err := f.queueStore.List(ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, before, 1)

	f.processor.ProcessQueueItems(ctx, f.dag.Name)
	requireQueuedConditions(t, f.readStatus("waiting-run"), noAvailableWorkerConditions()...)
	waiting, err := f.queueStore.List(ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, waiting, 1)
	require.Equal(t, before[0].ID(), waiting[0].ID())

	// The worker can poll again. No reschedule, re-enqueue or condition aging.
	polled := make(chan *coordinatorv1.PollResponse, 1)
	pollErrors := make(chan error, 1)
	go func() {
		response, err := h.Poll(ctx, &coordinatorv1.PollRequest{
			WorkerId: "worker-1", PollerId: "poller-1", Labels: labels,
		})
		if err != nil {
			pollErrors <- err
			return
		}
		f.updateStatus("waiting-run", func(runStatus *ir.DAGRunStatus) {
			runStatus.Status = ir.Running
			runStatus.WorkerID = "worker-1"
		})
		polled <- response
	}()
	// The normal fallback scan retries within 30 seconds even though the
	// heartbeat labels and queue generation have not changed.
	require.Eventually(t, func() bool {
		f.processor.ProcessQueueItems(ctx, f.dag.Name)
		items, err := f.queueStore.List(ctx, f.dag.Name)
		return err == nil && len(items) == 0
	}, 45*time.Second, 20*time.Millisecond)
	select {
	case response := <-polled:
		require.Equal(t, "waiting-run", response.Task.DagRunId)
		require.Equal(t, attempt.ID(), response.Task.AttemptId)
		require.Equal(t, "worker-1", response.Task.WorkerId)
	case err := <-pollErrors:
		t.Fatalf("poll failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Equal(t, ir.Running, f.readStatus("waiting-run").Status)
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

func TestQueueProcessorFinalizesLaunchFailure(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeLocal,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			executable: filepath.Join(t.TempDir(), "missing-dagu"),
			procRepository: func(base processRepository) processRepository {
				return &queueConditionProcRepository{
					processRepository: base,
					isRunAliveDelay:   50 * time.Millisecond,
				}
			},
		},
	)
	f.enqueueRun("waiting-run", []ir.DAGRunCondition{
		ir.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			time.Now().UTC().Add(-time.Minute),
		),
	})

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Equal(t, ir.Failed, status.Status)
	require.NotEmpty(t, status.Error)
	require.NotEmpty(t, status.FinishedAt)
	require.Empty(t, status.Conditions)
	require.Equal(t, 1, f.casCount("waiting-run"))
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestQueueProcessorPreservesRetryPublishedDuringFailureCleanup(t *testing.T) {
	t.Parallel()

	f := newQueueConditionFixtureWithConfig(
		t,
		config.ExecutionModeLocal,
		nil,
		&queueConditionDispatcher{},
		queueConditionFixtureConfig{
			executable: filepath.Join(t.TempDir(), "missing-dagu"),
			procRepository: func(base processRepository) processRepository {
				return &queueConditionProcRepository{
					processRepository: base,
					isRunAliveDelay:   50 * time.Millisecond,
				}
			},
		},
	)
	f.enqueueRun("waiting-run", nil)
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)
	originalItemID := items[0].ID()

	runRef := ir.NewDAGRunRef(f.dag.Name, "waiting-run")
	f.dagRunRepository.setBeforeCompareAndSwap(func(ctx context.Context) error {
		repository := persis.NewDAGRunRepository(
			f.dagRunRepository.DAGRunStore,
			nil,
			persis.DAGRunRepositoryOptions{LatestStatusToday: false},
		)
		attempt, err := repository.FindAttempt(ctx, runRef)
		if err != nil {
			return err
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return err
		}
		status.Status = ir.Failed
		status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err := attempt.Open(ctx); err != nil {
			return err
		}
		if err := attempt.Write(ctx, *status); err != nil {
			_ = attempt.Close(ctx)
			return err
		}
		if err := attempt.Close(ctx); err != nil {
			return err
		}
		queued, err := queuedomain.EnqueueRetry(
			ctx,
			repository,
			f.queueStore,
			f.dag,
			status,
			queuedomain.EnqueueRetryOptions{Processes: releasedRunProcesses{}},
		)
		if err != nil {
			return err
		}
		if !queued {
			return errors.New("retry was not queued")
		}
		return nil
	})

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.readStatus("waiting-run")
	require.Equal(t, ir.Queued, status.Status)
	items, err = f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotEqual(t, originalItemID, items[0].ID())
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
			procRepository: func(base processRepository) processRepository {
				return &queueConditionProcRepository{
					processRepository:  base,
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
			procRepository: func(base processRepository) processRepository {
				return &queueConditionProcRepository{
					processRepository: base,
					countAliveErr:     errors.New("count alive failed"),
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
	ctx              context.Context
	dag              *ir.DAG
	dagRunRepository *countingDAGRunStore
	queueStore       queuedomain.QueueStore
	leaseStore       dispatch.DAGRunLeaseStore
	dispatchStore    dispatch.DispatchTaskStore
	processor        *scheduler.QueueProcessor
}

type queueConditionFixtureConfig struct {
	executable     string
	procRepository func(processRepository) processRepository
	queueStore     func(queuedomain.QueueStore) queuedomain.QueueStore
	statusCache    *fileutil.Cache[*ir.DAGRunStatus]
}

func newQueueConditionFixture(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore dispatch.DispatchAdmissionStore,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	return newQueueConditionFixtureWithDispatcher(t, mode, admissionStore, &queueConditionDispatcher{}, extraOptions...)
}

func newQueueConditionFixtureWithDispatcher(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore dispatch.DispatchAdmissionStore,
	dispatcher *queueConditionDispatcher,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	return newQueueConditionFixtureWithConfig(t, mode, admissionStore, dispatcher, queueConditionFixtureConfig{}, extraOptions...)
}

func newQueueConditionFixtureWithConfig(
	t *testing.T,
	mode config.ExecutionMode,
	admissionStore dispatch.DispatchAdmissionStore,
	dispatcher *queueConditionDispatcher,
	fixtureConfig queueConditionFixtureConfig,
	extraOptions ...scheduler.QueueProcessorOption,
) *queueConditionFixture {
	t.Helper()

	tmp := t.TempDir()
	ctx := context.Background()
	dag := &ir.DAG{
		Name:     "queue-condition",
		YamlData: []byte("name: queue-condition\nsteps:\n  - name: test\n    command: echo hello\n"),
		Steps:    []ir.Step{{Name: "test", Command: "echo hello"}},
	}
	ir.InitializeDefaults(dag)
	storeOptions := make([]filedagrun.StoreOption, 0, 1)
	if fixtureConfig.statusCache != nil {
		storeOptions = append(storeOptions, filedagrun.WithHistoryFileCache(fixtureConfig.statusCache))
	}
	dagRunRepository := newCountingDAGRunStore(filedagrun.NewStore(filepath.Join(tmp, "dag-runs"), storeOptions...))
	var queueStore queuedomain.QueueStore = store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	if fixtureConfig.queueStore != nil {
		queueStore = fixtureConfig.queueStore(queueStore)
	}
	leaseStore := store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(tmp, "leases")))
	dispatchStore := store.NewDispatchTaskStore(
		file.NewCollection(filepath.Join(tmp, "dispatch")),
		store.WithDispatchReservationTTL(conditionTestStaleThreshold),
	)
	procRepository := persis.NewProcRepository(proc.New(filepath.Join(tmp, "proc")))
	var processorProcRepository processRepository = procRepository
	if fixtureConfig.procRepository != nil {
		processorProcRepository = fixtureConfig.procRepository(procRepository)
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
		dagRunRepository.repository,
		processorProcRepository,
		executor,
		config.Queues{
			Enabled: true,
			Config:  []config.QueueConfig{{Name: dag.Name, MaxActiveRuns: 1}},
		},
		opts...,
	)
	return &queueConditionFixture{
		ctx:              ctx,
		dag:              dag,
		dagRunRepository: dagRunRepository,
		queueStore:       queueStore,
		leaseStore:       leaseStore,
		dispatchStore:    dispatchStore,
		processor:        processor,
	}
}

func (f *queueConditionFixture) createQueuedAttempt(runID string, conditions []ir.DAGRunCondition) dagrun.Attempt {
	attempt, err := f.dagRunRepository.repository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	if err != nil {
		panic(err)
	}
	if err := attempt.Open(f.ctx); err != nil {
		panic(err)
	}
	status := ir.InitialStatus(f.dag)
	status.DAGRunID = runID
	status.AttemptID = attempt.ID()
	status.Status = ir.Queued
	status.Conditions = conditions
	if err := attempt.Write(f.ctx, status); err != nil {
		panic(err)
	}
	if err := attempt.Close(f.ctx); err != nil {
		panic(err)
	}
	return attempt
}

func (f *queueConditionFixture) enqueueRun(runID string, conditions []ir.DAGRunCondition) dagrun.Attempt {
	attempt := f.createQueuedAttempt(runID, conditions)
	if err := f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityHigh, ir.NewDAGRunRef(f.dag.Name, runID)); err != nil {
		panic(err)
	}
	return attempt
}

func (f *queueConditionFixture) readStatus(runID string) *ir.DAGRunStatus {
	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
	if err != nil {
		panic(err)
	}
	status, err := attempt.ReadStatus(f.ctx)
	if err != nil {
		panic(err)
	}
	return status
}

func (f *queueConditionFixture) updateStatus(runID string, mutate func(*ir.DAGRunStatus)) {
	attempt, err := f.dagRunRepository.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
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
	return f.dagRunRepository.casCount(runID)
}

type expectedQueuedCondition struct {
	conditionType string
	status        string
	reason        string
	message       string
}

func requireQueuedConditions(t *testing.T, status *ir.DAGRunStatus, expected ...expectedQueuedCondition) {
	t.Helper()

	require.Equal(t, ir.Queued, status.Status)
	require.Len(t, status.Conditions, len(expected))

	byType := make(map[string]ir.DAGRunCondition, len(status.Conditions))
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
	decision *dispatch.DispatchAdmissionDecision
	err      error
}

func (s *queueConditionAdmissionStore) ReserveAdmission(context.Context, dispatch.DispatchAdmissionRequest) (*dispatch.DispatchAdmissionDecision, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.decision, nil
}

func (s *queueConditionAdmissionStore) BindAdmission(context.Context, dispatch.DispatchAdmissionBindRequest) error {
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

func (s *queueConditionDispatchTaskStore) Enqueue(context.Context, *dispatch.DispatchTask) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) ClaimNext(context.Context, dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
	return nil, dispatch.ErrDispatchTaskNotFound
}

func (s *queueConditionDispatchTaskStore) GetClaim(context.Context, string) (*dispatch.ClaimedDispatchTask, error) {
	return nil, dispatch.ErrDispatchTaskNotFound
}

func (s *queueConditionDispatchTaskStore) ReleaseClaim(context.Context, string) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (s *queueConditionDispatchTaskStore) ListBundleDigests(context.Context) ([]string, error) {
	return nil, nil
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
	dispatchFn  func(context.Context, dispatch.DispatchRequest) error
	callCount   atomic.Int32
	mu          sync.Mutex
	runIDs      []string
}

func (d *queueConditionDispatcher) Dispatch(ctx context.Context, req dispatch.DispatchRequest) error {
	d.callCount.Add(1)
	d.mu.Lock()
	if req.Task != nil {
		d.runIDs = append(d.runIDs, req.Task.DAGRunID)
	}
	d.mu.Unlock()
	if d.dispatchFn != nil {
		return d.dispatchFn(ctx, req)
	}
	return d.dispatchErr
}

func (d *queueConditionDispatcher) dispatchedRunIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.runIDs...)
}

func (d *queueConditionDispatcher) Cleanup(context.Context) error {
	return nil
}

func (d *queueConditionDispatcher) GetDAGRunStatus(context.Context, string, string, *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	return nil, dagrun.ErrDAGRunIDNotFound
}

func (d *queueConditionDispatcher) RequestCancel(context.Context, string, string, *ir.DAGRunRef) error {
	return nil
}

type countingWorkerHeartbeatStore struct {
	dispatch.WorkerHeartbeatStore
	listCount atomic.Int32
}

func (s *countingWorkerHeartbeatStore) List(ctx context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	s.listCount.Add(1)
	return s.WorkerHeartbeatStore.List(ctx)
}

type failingWorkerHeartbeatStore struct {
	dispatch.WorkerHeartbeatStore
	err error
}

func (s *failingWorkerHeartbeatStore) List(context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	return nil, s.err
}

type fullListFailingQueueStore struct {
	queuedomain.QueueStore
}

func (s *fullListFailingQueueStore) List(context.Context, string) ([]queuedomain.QueuedItemData, error) {
	return nil, errors.New("full queue listing disabled")
}

type countingDAGRunStore struct {
	persis.DAGRunStore
	repository *persis.DAGRunRepository

	mu                       sync.Mutex
	casByRun                 map[string]int
	blankAttemptIDRuns       map[string]struct{}
	readDAGErrByRun          map[string]error
	beforeCompareAndSwapOnce func(context.Context) error
}

func newCountingDAGRunStore(store persis.DAGRunStore) *countingDAGRunStore {
	counting := &countingDAGRunStore{
		DAGRunStore:        store,
		casByRun:           make(map[string]int),
		blankAttemptIDRuns: make(map[string]struct{}),
		readDAGErrByRun:    make(map[string]error),
	}
	counting.repository = persis.NewDAGRunRepository(counting, nil, persis.DAGRunRepositoryOptions{
		LatestStatusToday: false,
	})
	return counting
}

func (s *countingDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	s.mu.Lock()
	s.casByRun[req.DAGRun.ID]++
	before := s.beforeCompareAndSwapOnce
	s.beforeCompareAndSwapOnce = nil
	s.mu.Unlock()
	if before != nil {
		if err := before(ctx); err != nil {
			return nil, false, err
		}
	}
	return s.DAGRunStore.CompareAndSwapLatestAttemptStatus(ctx, req)
}

func (s *countingDAGRunStore) setBeforeCompareAndSwap(before func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeCompareAndSwapOnce = before
}

type releasedRunProcesses struct{}

func (releasedRunProcesses) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return false, nil
}

func (s *countingDAGRunStore) FindAttempt(ctx context.Context, dagRun ir.DAGRunRef) (dagrun.Attempt, error) {
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
		Attempt:    attempt,
		blankID:    blankID,
		readDAGErr: readDAGErr,
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
	dagrun.Attempt
	blankID    bool
	readDAGErr error
}

func (a *queueConditionAttempt) ID() string {
	if a.blankID {
		return ""
	}
	return a.Attempt.ID()
}

func (a *queueConditionAttempt) ReadDAG(ctx context.Context) (*ir.DAG, error) {
	if a.readDAGErr != nil {
		return nil, a.readDAGErr
	}
	return a.Attempt.ReadDAG(ctx)
}

type queueConditionProcRepository struct {
	processRepository

	mu                 sync.Mutex
	countAliveErr      error
	isRunAliveDelay    time.Duration
	isRunAliveErrAfter int
	isRunAliveErr      error
	isRunAliveCalls    int
}

func (s *queueConditionProcRepository) CountAlive(ctx context.Context, groupName string) (int, error) {
	if s.countAliveErr != nil {
		return 0, s.countAliveErr
	}
	return s.processRepository.CountAlive(ctx, groupName)
}

func (s *queueConditionProcRepository) IsRunAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (bool, error) {
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
	return s.processRepository.IsRunAlive(ctx, groupName, dagRun)
}
