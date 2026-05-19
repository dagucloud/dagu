// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	osexec "os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newStartupTestDispatcher(dagRunStore exec.DAGRunStore, procStore exec.ProcStore, cfg BackoffConfig) *queueDispatcher {
	return newQueueDispatcher(queueDispatchDeps{
		dagRunStore:   dagRunStore,
		procStore:     procStore,
		backoffConfig: cfg,
	})
}

type mockLeaseStore struct {
	getFunc         func(context.Context, string) (*exec.DAGRunLease, error)
	listByQueueFunc func(context.Context, string) ([]exec.DAGRunLease, error)
}

func (m *mockLeaseStore) Upsert(context.Context, exec.DAGRunLease) error { return nil }
func (m *mockLeaseStore) Touch(context.Context, string, time.Time) error { return nil }
func (m *mockLeaseStore) Delete(context.Context, string) error           { return nil }

func (m *mockLeaseStore) Get(ctx context.Context, attemptKey string) (*exec.DAGRunLease, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, attemptKey)
	}
	return nil, exec.ErrDAGRunLeaseNotFound
}

func (m *mockLeaseStore) ListByQueue(ctx context.Context, queueName string) ([]exec.DAGRunLease, error) {
	if m.listByQueueFunc != nil {
		return m.listByQueueFunc(ctx, queueName)
	}
	return nil, nil
}

func (m *mockLeaseStore) ListAll(context.Context) ([]exec.DAGRunLease, error) { return nil, nil }

func TestQueueDispatcher_CheckStartupStatus_WithinGraceSkipsAttemptLookup(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	procStore.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunStore, procStore, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  make(chan error, 1),
	})

	require.False(t, started)
	require.ErrorIs(t, err, errNotStarted)
	dagRunStore.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procStore.AssertExpectations(t)
}

func TestQueueDispatcher_CheckStartupStatus_HeartbeatSkipsAttemptLookup(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	procStore.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(true, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunStore, procStore, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  make(chan error, 1),
	})

	require.True(t, started)
	require.NoError(t, err)
	dagRunStore.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procStore.AssertExpectations(t)
}

func TestQueueDispatcher_CheckStartupStatus_PreStartExecutionErrorIsPermanent(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")
	execErrCh := make(chan error, 1)
	execErrCh <- errors.New("dispatch failed")

	dispatcher := newStartupTestDispatcher(dagRunStore, procStore, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  execErrCh,
	})

	require.False(t, started)
	require.ErrorIs(t, err, backoff.ErrPermanent)
	dagRunStore.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procStore.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_CheckStartupStatus_AfterGraceFallsBackToStatus(t *testing.T) {
	testCases := []struct {
		name      string
		status    core.Status
		wantStart bool
		wantErr   error
	}{
		{name: "Queued", status: core.Queued, wantStart: false, wantErr: errNotStarted},
		{name: "Running", status: core.Running, wantStart: true},
		{name: "NotStarted", status: core.NotStarted, wantStart: true},
		{name: "Succeeded", status: core.Succeeded, wantStart: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dagRunStore := &mockDAGRunStore{}
			procStore := &mockProcStore{}
			runRef := exec.NewDAGRunRef("test-dag", "run-1")
			attempt := &exec.MockDAGRunAttempt{
				Status: &exec.DAGRunStatus{Status: tc.status},
			}

			procStore.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()
			dagRunStore.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil).Once()

			dispatcher := newStartupTestDispatcher(dagRunStore, procStore, BackoffConfig{
				StartupGracePeriod: 50 * time.Millisecond,
			})

			started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
				launchedAt: time.Now().Add(-time.Second),
				execErrCh:  make(chan error, 1),
			})

			require.Equal(t, tc.wantStart, started)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			dagRunStore.AssertExpectations(t)
			procStore.AssertExpectations(t)
		})
	}
}

func TestQueueDispatcher_CheckStartupStatus_AfterGracePropagatesLeaseLookupError(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	leaseStore := &mockLeaseStore{
		getFunc: func(context.Context, string) (*exec.DAGRunLease, error) {
			return nil, errors.New("lease store unavailable")
		},
	}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")
	attempt := &exec.MockDAGRunAttempt{
		Status: &exec.DAGRunStatus{
			Status:    core.Queued,
			AttemptID: "attempt-1",
		},
	}

	procStore.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()
	dagRunStore.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunStore, procStore, BackoffConfig{
		StartupGracePeriod: 50 * time.Millisecond,
	})
	dispatcher.dagRunLeaseStore = leaseStore

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now().Add(-time.Second),
		execErrCh:  make(chan error, 1),
	})

	require.False(t, started)
	require.EqualError(t, err, "lease store unavailable")
	dagRunStore.AssertExpectations(t)
	procStore.AssertExpectations(t)
}

func TestIsPreStartExecutionFailure(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "Nil", err: nil, want: false},
		{name: "ContextCanceled", err: context.Canceled, want: false},
		{name: "DeadlineExceeded", err: context.DeadlineExceeded, want: false},
		{name: "ExitError", err: &osexec.ExitError{}, want: false},
		{name: "DispatchFailure", err: errors.New("dispatch failed"), want: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isPreStartExecutionFailure(tc.err))
		})
	}
}

// mockDispatcher implements exec.Dispatcher for testing dispatch behavior.
type mockDispatcher struct {
	callCount atomic.Int32
	errFunc   func(callNum int32) error
}

func (m *mockDispatcher) Dispatch(_ context.Context, _ *coordinatorv1.Task) error {
	n := m.callCount.Add(1)
	if m.errFunc != nil {
		return m.errFunc(n)
	}
	return nil
}

func (m *mockDispatcher) Cleanup(_ context.Context) error { return nil }

func (m *mockDispatcher) GetDAGRunStatus(_ context.Context, _, _ string, _ *exec.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	return nil, nil
}

func (m *mockDispatcher) RequestCancel(_ context.Context, _, _ string, _ *exec.DAGRunRef) error {
	return nil
}

func TestQueueDispatcher_DispatchAndWaitForStartup_TransientRetryThenSuccess(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	// Dispatcher fails twice with a transient error, then succeeds.
	disp := &mockDispatcher{
		errFunc: func(n int32) error {
			if n <= 2 {
				return errors.New("no available workers")
			}
			return nil
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "", nil)
	dag := &core.DAG{Name: "test-dag"}
	status := &exec.DAGRunStatus{Status: core.Queued, TriggerType: core.TriggerTypeScheduler}

	// After dispatch succeeds, the process should become alive.
	procStore.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(true, nil).Once()

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunStore: dagRunStore,
		procStore:   procStore,
		dagExecutor: dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status)
	require.True(t, started)
	require.GreaterOrEqual(t, disp.callCount.Load(), int32(3))
	procStore.AssertExpectations(t)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_StaleQueueDispatchIsDiscarded(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}

	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return backoff.PermanentError(&exec.StaleQueueDispatchError{
				Reason: "queued attempt was superseded",
			})
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "", nil)
	dag := &core.DAG{Name: "test-dag"}
	status := &exec.DAGRunStatus{Status: core.Queued, TriggerType: core.TriggerTypeScheduler}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunStore: dagRunStore,
		procStore:   procStore,
		dagExecutor: dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status)
	require.True(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procStore.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_RawStaleQueueDispatchStopsRetry(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}

	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return &exec.StaleQueueDispatchError{Reason: "queued attempt was superseded"}
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "", nil)
	dag := &core.DAG{Name: "test-dag"}
	status := &exec.DAGRunStatus{Status: core.Queued, TriggerType: core.TriggerTypeScheduler}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunStore: dagRunStore,
		procStore:   procStore,
		dagExecutor: dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status)
	require.True(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procStore.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_PermanentErrorStopsRetry(t *testing.T) {
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}

	// Dispatcher always returns a permanent error (selector mismatch).
	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return backoff.PermanentError(errors.New("no workers match the required selector"))
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "", nil)
	dag := &core.DAG{Name: "test-dag"}
	status := &exec.DAGRunStatus{Status: core.Queued, TriggerType: core.TriggerTypeScheduler}
	runRef := exec.NewDAGRunRef("test-dag", "run-1")

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunStore: dagRunStore,
		procStore:   procStore,
		dagExecutor: dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status)
	require.False(t, started)
	// Should have been called exactly once (permanent error stops retries).
	require.Equal(t, int32(1), disp.callCount.Load())
	procStore.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}
