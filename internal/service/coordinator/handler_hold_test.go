// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	// The mcp.call steps of the definitions above declare connections.
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/mcp"
)

// holdWait outlasts any test, so a held task stays claimable until a test
// runs the reaper past the deadline with an explicit clock. A short wait
// would let a slow runner pass the deadline before a worker polls.
const holdWait = time.Minute

// holdFixture wires a coordinator over the shared stores with one healthy
// worker, so a task whose selector that worker does not carry has nowhere to
// go until another worker registers.
type holdFixture struct {
	baseDir    string
	h          *Handler
	runs       *mockDAGRunStore
	heartbeats dispatch.WorkerHeartbeatStore
	dispatches dispatch.DispatchTaskStore
	leases     dispatch.DAGRunLeaseStore
}

func newHoldFixture(t *testing.T, wait time.Duration) *holdFixture {
	t.Helper()
	registerCommandExecutorCapsForCoordinatorTest()
	baseDir := t.TempDir()
	f := &holdFixture{
		baseDir:    baseDir,
		runs:       newMockDAGRunStore(),
		heartbeats: newTestWorkerHeartbeatStore(baseDir),
		dispatches: newTestDispatchTaskStore(baseDir),
		leases:     newTestDAGRunLeaseStore(baseDir),
	}
	f.heartbeat(t, "worker-gpu", map[string]string{"type": "gpu"})
	f.h = NewHandler(HandlerConfig{
		DAGRunRepository:     f.runs.repository,
		DispatchTaskStore:    f.dispatches,
		DAGRunLeaseStore:     f.leases,
		WorkerHeartbeatStore: f.heartbeats,
		WorkerWaitTimeout:    wait,
	})
	t.Cleanup(func() { f.h.Close(context.Background()) })
	return f
}

func (f *holdFixture) heartbeat(t *testing.T, workerID string, labels map[string]string) {
	t.Helper()
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: workerID, Labels: labels, LastHeartbeatAt: time.Now().UnixMilli(),
	}))
}

func cpuTask(runID string) *coordinatorv1.Task {
	return &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		DagRunId:       runID,
		Target:         "routed",
		Definition:     "steps:\n  - name: wait\n    run: sleep 1\n",
		WorkerSelector: map[string]string{"type": "cpu"},
	}
}

func (f *holdFixture) status(t *testing.T, runID string) *ir.DAGRunStatus {
	t.Helper()
	attempt := f.runs.attempts[runID]
	require.NotNil(t, attempt, "attempt %s", runID)
	runStatus, err := attempt.ReadStatus(t.Context())
	require.NoError(t, err)
	return runStatus
}

// pollNone asserts that a worker polling with labels is handed nothing. Poll
// long-polls until its context ends, so the wait is bounded here.
func (f *holdFixture) pollNone(t *testing.T, workerID string, labels map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	polled, err := f.h.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: workerID, PollerId: "p-none", Labels: labels})
	if err == nil {
		assert.Nil(t, polled.Task, "worker %s must not be handed a task", workerID)
	}
}

func conditionOf(conditions []ir.DAGRunCondition, conditionType string) (ir.DAGRunCondition, bool) {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition, true
		}
	}
	return ir.DAGRunCondition{}, false
}

// With no healthy worker matching the selector, the dispatch is accepted,
// the run waits as Queued under the queue's own conditions, and the first
// matching worker to poll takes it.
func TestDispatchHoldsUntilMatchingWorker(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-held")})
	require.NoError(t, err)

	runStatus := f.status(t, "run-held")
	assert.Equal(t, ir.Queued, runStatus.Status)
	assert.NotEmpty(t, runStatus.QueuedAt)
	runnable, ok := conditionOf(runStatus.Conditions, ir.DAGRunConditionRunnable)
	require.True(t, ok)
	assert.Equal(t, ir.DAGRunConditionStatusFalse, runnable.Status)
	assert.Equal(t, ir.DAGRunReasonNoMatchingWorker, runnable.Reason)
	workerReady, ok := conditionOf(runStatus.Conditions, ir.DAGRunConditionWorkerReady)
	require.True(t, ok)
	assert.Equal(t, ir.DAGRunConditionStatusFalse, workerReady.Status)
	assert.Equal(t, ir.DAGRunReasonNoMatchingWorker, workerReady.Reason)

	// The worker that does not match is not handed it.
	f.pollNone(t, "worker-gpu", map[string]string{"type": "gpu"})

	// A matching worker takes it as soon as it registers.
	f.heartbeat(t, "worker-cpu", map[string]string{"type": "cpu"})
	polled, err := f.h.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: "worker-cpu", PollerId: "p1", Labels: map[string]string{"type": "cpu"},
	})
	require.NoError(t, err)
	require.NotNil(t, polled.Task)
	assert.Equal(t, "run-held", polled.Task.DagRunId)
	assert.Equal(t, map[string]string{"type": "cpu"}, polled.Task.WorkerSelector)
}

func TestDispatchHoldsWhenNoWorkerIsAvailable(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	ctx := t.Context()
	// Make the only worker stale so none is healthy.
	require.NoError(t, f.heartbeats.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-gpu", LastHeartbeatAt: time.Now().Add(-time.Hour).UnixMilli(),
	}))

	task := cpuTask("run-nobody")
	task.WorkerSelector = nil
	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err)

	runStatus := f.status(t, "run-nobody")
	assert.Equal(t, ir.Queued, runStatus.Status)
	workerReady, ok := conditionOf(runStatus.Conditions, ir.DAGRunConditionWorkerReady)
	require.True(t, ok)
	assert.Equal(t, ir.DAGRunReasonNoAvailableWorker, workerReady.Reason)
}

// Once the wait passes with no claim, the run fails naming the selector and
// the task is gone, so a worker arriving later does not start a run its
// owner already saw fail.
func TestDispatchHoldDeadlineFailsAttempt(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-late")})
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, f.status(t, "run-late").Status)

	// Too early: still held.
	ExpireHeldDispatchesForTest(f.h, time.Now())
	assert.Equal(t, ir.Queued, f.status(t, "run-late").Status)

	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
	runStatus := f.status(t, "run-late")
	assert.Equal(t, ir.Failed, runStatus.Status)
	assert.Equal(t, "no worker matched the selector {type: cpu} within 1m0s", runStatus.Error)
	assert.NotEmpty(t, runStatus.FinishedAt)

	f.heartbeat(t, "worker-cpu", map[string]string{"type": "cpu"})
	f.pollNone(t, "worker-cpu", map[string]string{"type": "cpu"})
}

// The lease grace a starting coordinator gives running workers does not
// postpone a held dispatch's own deadline.
func TestDispatchHoldDeadlineEnforcedDuringLeaseGrace(t *testing.T) {
	f := newHoldFixture(t, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer func() {
		cancel()
		f.h.WaitZombieDetector()
	}()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-grace")})
	require.NoError(t, err)
	attempt := f.runs.attempts["run-grace"]
	require.NotNil(t, attempt)

	f.h.StartZombieDetector(ctx, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		runStatus, err := attempt.ReadStatus(ctx)
		return err == nil && runStatus.Status == ir.Failed
	}, coordinatorTestTimeout(5*time.Second), 20*time.Millisecond)
}

// A held task that a worker claims before the deadline is not touched by the
// reaper afterwards.
func TestDispatchHoldClaimedInTimeIsLeftAlone(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-taken")})
	require.NoError(t, err)
	f.heartbeat(t, "worker-cpu", map[string]string{"type": "cpu"})
	polled, err := f.h.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: "worker-cpu", PollerId: "p1", Labels: map[string]string{"type": "cpu"},
	})
	require.NoError(t, err)
	require.NotNil(t, polled.Task)

	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
	assert.Equal(t, ir.Queued, f.status(t, "run-taken").Status, "a claimed task is the worker's to report")
}

// A held task whose run cannot be read past the deadline is kept, so the next
// pass fails the run once the store answers again; dropping the task would
// leave the run queued with nothing behind it.
func TestDispatchHoldKeepsTaskWhenRunUnreadable(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-unread")})
	require.NoError(t, err)

	f.runs.mu.Lock()
	f.runs.findAttemptErr = errors.New("storage unavailable")
	f.runs.mu.Unlock()
	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
	assert.Equal(t, ir.Queued, f.status(t, "run-unread").Status)

	f.runs.mu.Lock()
	f.runs.findAttemptErr = nil
	f.runs.mu.Unlock()
	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
	runStatus := f.status(t, "run-unread")
	assert.Equal(t, ir.Failed, runStatus.Status)
	assert.Equal(t, "no worker matched the selector {type: cpu} within 1m0s", runStatus.Error)
}

// A held task whose run no longer exists is dropped, so the reaper does not
// report it on every pass.
func TestDispatchHoldDropsTaskOfMissingRun(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-gone")})
	require.NoError(t, err)

	f.runs.mu.Lock()
	delete(f.runs.attempts, "run-gone")
	f.runs.mu.Unlock()
	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))

	expired, err := f.dispatches.ListExpiredHeld(ctx, time.Now().Add(2*holdWait))
	require.NoError(t, err)
	assert.Empty(t, expired, "the task of a missing run is dropped")
}

// The scheduler re-dispatching a queued run keeps today's refusal: the queue
// already holds that run and reads the refusal to keep it queued.
func TestDispatchQueueRedispatchStillRefused(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	ctx := t.Context()

	f.runs.addAttempt(ir.DAGRunRef{Name: "routed", ID: "run-queued"}, &ir.DAGRunStatus{
		Name: "routed", DAGRunID: "run-queued", AttemptID: "queued-attempt", Status: ir.Queued,
	}).SetDAG(&ir.DAG{Name: "routed"})
	task := cpuTask("run-queued")
	task.Operation = coordinatorv1.Operation_OPERATION_RETRY
	previous, err := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
		Name: "routed", DAGRunID: "run-queued", AttemptID: "queued-attempt", Status: ir.Queued,
	})
	require.NoError(t, err)
	task.PreviousStatus = previous

	_, err = f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "no workers match the required selector")
}

func TestDispatchHoldDisabledRefuses(t *testing.T) {
	f := newHoldFixture(t, 0)

	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: cpuTask("run-refused")})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Nil(t, f.runs.attempts["run-refused"], "nothing is created when refused")
}

// A DAG that declares connections goes only to a worker that asks for
// connection tokens. A matching worker without them neither takes it nor
// counts as one that could, so the run waits for an upgraded worker.
func TestDispatchConnectionTaskWaitsForTokenWorker(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	ctx := t.Context()
	gpu := map[string]string{"type": "gpu"}
	connectionTask := func() *coordinatorv1.Task {
		task := cpuTask("run-connection")
		task.WorkerSelector = gpu
		task.Definition = "steps:\n  - name: read\n    action: mcp.call\n    with: {connection: work, tool: search}\n"
		return task
	}

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: connectionTask()})
	require.NoError(t, err)
	held := f.status(t, "run-connection")
	assert.Equal(t, ir.Queued, held.Status)
	workerReady, ok := conditionOf(held.Conditions, ir.DAGRunConditionWorkerReady)
	require.True(t, ok)
	assert.Equal(t, ir.NoConnectionWorkerReadyMessage, workerReady.Message)
	f.pollNone(t, "worker-gpu", gpu)

	polled, err := f.h.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: "worker-new", PollerId: "p1", Labels: gpu, ConnectionTokens: true,
	})
	require.NoError(t, err)
	require.NotNil(t, polled.Task)
	assert.Equal(t, "run-connection", polled.Task.DagRunId)

	refused := newHoldFixture(t, 0)
	_, err = refused.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: connectionTask()})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, "the DAG declares connections, which no matching worker supports; upgrade the matching workers")

	bound := connectionTask()
	bound.WorkerSelector = nil
	bound.TargetWorkerId, bound.BoundNode = "worker-gpu", "worker-gpu"
	_, err = f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: bound})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, `worker node "worker-gpu": no workers match the required selector: the DAG declares connections`)
}

// The declaration load runs no eval: this one fails when it runs, which
// would fail the load and hide the connection.
func TestDeclaresConnectionsRunsNoEval(t *testing.T) {
	task := &coordinatorv1.Task{
		Target:     "mail",
		Definition: "params:\n  - name: v\n    type: string\n    eval: \"`exit 3`\"\nsteps:\n  - name: read\n    action: mcp.call\n    with: {connection: work, tool: search}\n",
	}
	assert.True(t, declaresConnections(t.Context(), task))
}

// A held run whose wait passes says that the matching workers must support
// the DAG's connections.
func TestHeldDispatchFailureNamesConnections(t *testing.T) {
	task := &dispatch.DispatchTask{WorkerSelector: map[string]string{"type": "gpu"}, NeedsConnectionTokens: true}
	assert.Equal(t,
		"no worker matched the selector {type: gpu} within 1m0s; the DAG declares connections, so the matching workers must support them: upgrade older ones",
		heldDispatchFailure(task, time.Minute))
}

// A run bound to a worker node fails at once while its worker is offline,
// even when dispatches are held for workers (RFC 013 §Matching).
func TestDispatchToOfflineWorkerNodeRefused(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	task := cpuTask("run-bound")
	task.WorkerSelector = nil
	task.TargetWorkerId, task.BoundNode = "tokyo-gw", "tokyo-gw"

	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t, `worker node "tokyo-gw" is offline`, st.Message())
	assert.Nil(t, f.runs.attempts["run-bound"], "nothing is created when refused")

	f.heartbeat(t, "tokyo-gw", nil)
	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err, "accepted once the worker is online")
}

// Cancelling a held run aborts it and drops the task.
func TestRequestCancelAbortsHeldDispatch(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	ctx := t.Context()

	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: cpuTask("run-cancel")})
	require.NoError(t, err)

	resp, err := f.h.RequestCancel(ctx, &coordinatorv1.RequestCancelRequest{DagName: "routed", DagRunId: "run-cancel"})
	require.NoError(t, err)
	assert.True(t, resp.Accepted, resp.Error)

	runStatus := f.status(t, "run-cancel")
	assert.Equal(t, ir.Aborted, runStatus.Status)
	assert.Equal(t, notStartedCancellationReason, runStatus.Error)

	f.heartbeat(t, "worker-cpu", map[string]string{"type": "cpu"})
	f.pollNone(t, "worker-cpu", map[string]string{"type": "cpu"})
}

// A task waiting for busy workers stays claimable however long it waits, so
// its run starts once a worker frees up.
func TestDropOrphanedDispatchesKeepsWaitingRun(t *testing.T) {
	f := newHoldFixture(t, 0)
	ctx := t.Context()

	task := cpuTask("run-waiting")
	task.WorkerSelector = map[string]string{"type": "gpu"}
	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err)

	DropOrphanedDispatchesForTest(f.h, time.Now().Add(time.Hour))

	polled, err := f.h.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: "worker-gpu", PollerId: "p1", Labels: map[string]string{"type": "gpu"},
	})
	require.NoError(t, err)
	require.NotNil(t, polled.Task)
	assert.Equal(t, "run-waiting", polled.Task.DagRunId)
}

// A long-pending task is dropped once its run no longer awaits it, such as a
// run dequeued or deleted before any worker claimed it, so it neither starts
// later nor holds a queue slot. A run that cannot be read keeps its task for
// the next pass.
func TestDropOrphanedDispatchesChecksRun(t *testing.T) {
	tests := []struct {
		name    string
		moveOn  func(t *testing.T, f *holdFixture, runID string)
		dropped bool
	}{
		{
			name: "aborted",
			moveOn: func(t *testing.T, f *holdFixture, runID string) {
				attempt := f.runs.attempts[runID]
				runStatus := f.status(t, runID)
				runStatus.Status = ir.Aborted
				require.NoError(t, attempt.Write(t.Context(), *runStatus))
			},
			dropped: true,
		},
		{
			name: "missing",
			moveOn: func(_ *testing.T, f *holdFixture, runID string) {
				f.runs.mu.Lock()
				delete(f.runs.attempts, runID)
				f.runs.mu.Unlock()
			},
			dropped: true,
		},
		{
			name: "unreadable",
			moveOn: func(_ *testing.T, f *holdFixture, _ string) {
				f.runs.mu.Lock()
				f.runs.findAttemptErr = errors.New("storage unavailable")
				f.runs.mu.Unlock()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newHoldFixture(t, 0)
			ctx := t.Context()

			task := cpuTask("run-" + tt.name)
			task.WorkerSelector = map[string]string{"type": "gpu"}
			_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
			require.NoError(t, err)
			tt.moveOn(t, f, task.DagRunId)

			DropOrphanedDispatchesForTest(f.h, time.Now().Add(time.Hour))

			stale, err := f.dispatches.ListStalePending(ctx, time.Now().Add(time.Hour))
			require.NoError(t, err)
			if tt.dropped {
				assert.Empty(t, stale)
				return
			}
			require.Len(t, stale, 1)
		})
	}
}
