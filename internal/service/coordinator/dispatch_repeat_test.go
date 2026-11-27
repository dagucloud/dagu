// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
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
)

const (
	repeatDefinition = "steps:\n  - name: wait\n    run: sleep 1\n"
	repeatAttemptID  = "a1b2c3d4e5f6"
)

// claimAll claims every queued task and returns how many there were.
func (f *holdFixture) claimAll(t *testing.T) int {
	t.Helper()
	for n := 0; ; n++ {
		claimed, err := f.dispatches.ClaimNext(t.Context(), dispatch.DispatchTaskClaim{
			WorkerID:     "worker-gpu",
			PollerID:     fmt.Sprintf("poller-%d", n),
			Labels:       map[string]string{"type": "gpu"},
			ClaimTimeout: time.Minute,
		})
		require.NoError(t, err)
		if claimed == nil {
			return n
		}
	}
}

func (f *holdFixture) attempt(runID string) *mockAttempt {
	f.runs.mu.Lock()
	defer f.runs.mu.Unlock()
	return f.runs.attempts[runID]
}

func (f *holdFixture) subAttempt(rootID, runID string) *mockAttempt {
	f.runs.mu.Lock()
	defer f.runs.mu.Unlock()
	return f.runs.subAttempts[rootID+":"+runID]
}

func repeatTask(runID string) *coordinatorv1.Task {
	return &coordinatorv1.Task{
		Operation:  coordinatorv1.Operation_OPERATION_START,
		DagRunId:   runID,
		Target:     "routed",
		Definition: repeatDefinition,
		AttemptId:  repeatAttemptID,
	}
}

// Each caller sends its own task shape; a repeat of any of them succeeds for
// the attempt the first call admitted.
func TestDispatchRepeatIsIdempotent(t *testing.T) {
	retried := &ir.DAGRunStatus{Name: "routed", DAGRunID: "run-retry", AttemptID: "prev01", Status: ir.Failed}
	previous, err := convert.DAGRunStatusToProto(retried)
	require.NoError(t, err)

	tests := []struct {
		name    string
		setup   func(*holdFixture)
		task    func() *coordinatorv1.Task
		attempt func(*holdFixture) *mockAttempt
	}{
		{
			// The CLI and the API start a run.
			name:    "Start",
			task:    func() *coordinatorv1.Task { return repeatTask("run-start") },
			attempt: func(f *holdFixture) *mockAttempt { return f.attempt("run-start") },
		},
		{
			// The API retries a finished run.
			name:  "Retry",
			setup: func(f *holdFixture) { f.runs.addAttempt(ir.NewDAGRunRef("routed", "run-retry"), retried) },
			task: func() *coordinatorv1.Task {
				task := repeatTask("run-retry")
				task.Operation = coordinatorv1.Operation_OPERATION_RETRY
				task.PreviousStatus = previous
				return task
			},
			attempt: func(f *holdFixture) *mockAttempt { return f.attempt("run-retry") },
		},
		{
			// A parent run starts a sub-DAG run.
			name: "SubDAG",
			task: func() *coordinatorv1.Task {
				task := repeatTask("run-child")
				task.RootDagRunName, task.RootDagRunId = "parent", "run-root"
				task.ParentDagRunName, task.ParentDagRunId = "parent", "run-root"
				return task
			},
			attempt: func(f *holdFixture) *mockAttempt { return f.subAttempt("run-root", "run-child") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newHoldFixture(t, 0)
			if tt.setup != nil {
				tt.setup(f)
			}

			_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: tt.task()})
			require.NoError(t, err)
			admitted := tt.attempt(f)
			require.NotNil(t, admitted)
			assert.Equal(t, repeatAttemptID, admitted.ID())

			_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: tt.task()})
			require.NoError(t, err)

			assert.Same(t, admitted, tt.attempt(f), "the repeat must not create another attempt")
			assert.Equal(t, 1, f.claimAll(t))
		})
	}
}

// The scheduler dispatches a queued run by its queued attempt; a repeat
// queues it no second time.
func TestDispatchRepeatOfQueuedRun(t *testing.T) {
	f := newHoldFixture(t, 0)
	queued := &ir.DAGRunStatus{Name: "routed", DAGRunID: "run-queued", AttemptID: "queued01", Status: ir.Queued}
	f.runs.addAttempt(ir.NewDAGRunRef("routed", "run-queued"), queued)
	previous, err := convert.DAGRunStatusToProto(queued)
	require.NoError(t, err)
	task := func() *coordinatorv1.Task {
		task := repeatTask("run-queued")
		task.Operation = coordinatorv1.Operation_OPERATION_RETRY
		task.PreviousStatus = previous
		return task
	}

	for range 2 {
		_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task()})
		require.NoError(t, err)
	}

	assert.Equal(t, 1, f.claimAll(t))
}

// A repeat whose definition differs from the attempt it names is refused.
func TestDispatchRepeatConflictIsRefused(t *testing.T) {
	f := newHoldFixture(t, 0)
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: repeatTask("run-conflict")})
	require.NoError(t, err)
	admitted := f.attempt("run-conflict")

	conflicting := repeatTask("run-conflict")
	conflicting.Definition = "steps:\n  - name: other\n    run: echo other\n"
	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: conflicting})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, err.Error(), "different definition")

	assert.Same(t, admitted, f.attempt("run-conflict"))
	assert.Equal(t, 1, f.claimAll(t))
}

// A coordinator that stopped after it created the attempt but before it
// queued the task leaves the admission to the repeat.
func TestDispatchRepeatFinishesInterruptedAdmission(t *testing.T) {
	f := newHoldFixture(t, 0)
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: repeatTask("run-cut")})
	require.NoError(t, err)
	admitted := f.attempt("run-cut")
	require.NoError(t, f.dispatches.DeletePending(t.Context(), f.status(t, "run-cut").AttemptKey))

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: repeatTask("run-cut")})
	require.NoError(t, err)

	assert.Same(t, admitted, f.attempt("run-cut"))
	assert.Equal(t, 1, f.claimAll(t))
}

// A repeat of a dispatch the coordinator rolled back reports that failure
// rather than starting the run.
func TestDispatchRepeatAfterRollbackIsRefused(t *testing.T) {
	registerCommandExecutorCapsForCoordinatorTest()
	f := newHoldFixture(t, 0)
	h := NewHandler(HandlerConfig{
		DAGRunRepository:     f.runs.repository,
		DispatchTaskStore:    &failingDispatchTaskStore{enqueueErr: errors.New("disk full")},
		WorkerHeartbeatStore: f.heartbeats,
	})
	t.Cleanup(func() { h.Close(context.Background()) })

	_, err := h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: repeatTask("run-rollback")})
	require.Error(t, err)
	admitted := f.attempt("run-rollback")

	_, err = h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: repeatTask("run-rollback")})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, err.Error(), "disk full")
	assert.Same(t, admitted, f.attempt("run-rollback"))
}
