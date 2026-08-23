// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	osexec "os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newStartupTestDispatcher(dagRunRepository *persis.DAGRunRepository, procRepository queueProcessRepository, cfg BackoffConfig) *queueDispatcher {
	return newQueueDispatcher(queueDispatchDeps{
		dagRunRepository: dagRunRepository,
		procRepository:   procRepository,
		backoffConfig:    cfg,
	})
}

type mockLeaseStore struct {
	getFunc         func(context.Context, string) (*dispatch.DAGRunLease, error)
	listByQueueFunc func(context.Context, string) ([]dispatch.DAGRunLease, error)
}

func (m *mockLeaseStore) Upsert(context.Context, dispatch.DAGRunLease) error { return nil }
func (m *mockLeaseStore) Touch(context.Context, string, time.Time) error     { return nil }
func (m *mockLeaseStore) Delete(context.Context, string) error               { return nil }

func (m *mockLeaseStore) Get(ctx context.Context, attemptKey string) (*dispatch.DAGRunLease, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, attemptKey)
	}
	return nil, dispatch.ErrDAGRunLeaseNotFound
}

func (m *mockLeaseStore) ListByQueue(ctx context.Context, queueName string) ([]dispatch.DAGRunLease, error) {
	if m.listByQueueFunc != nil {
		return m.listByQueueFunc(ctx, queueName)
	}
	return nil, nil
}

func (m *mockLeaseStore) ListAll(context.Context) ([]dispatch.DAGRunLease, error) { return nil, nil }

func TestQueueDispatcher_CheckStartupStatus_WithinGraceSkipsAttemptLookup(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")

	procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  make(chan error, 1),
	})

	require.False(t, started)
	require.ErrorIs(t, err, errNotStarted)
	dagRunRepository.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procRepository.AssertExpectations(t)
}

func TestQueueDispatcher_CheckStartupStatus_HeartbeatSkipsAttemptLookup(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")

	procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(true, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  make(chan error, 1),
	})

	require.True(t, started)
	require.NoError(t, err)
	dagRunRepository.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procRepository.AssertExpectations(t)
}

func TestQueueDispatcher_CheckStartupStatus_PreStartExecutionErrorIsPermanent(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")
	execErrCh := make(chan error, 1)
	execErrCh <- errors.New("dispatch failed")

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		StartupGracePeriod: time.Second,
	})

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  execErrCh,
	})

	require.False(t, started)
	require.ErrorIs(t, err, backoff.ErrPermanent)
	dagRunRepository.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procRepository.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_WaitForStartupKeepsLocalLaunchInFlightUntilDone(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")
	attempt := &testutil.MockAttempt{
		Status: &ir.DAGRunStatus{Status: ir.Queued},
	}
	var checks atomic.Int32

	procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil)
	dagRunRepository.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil)

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		InitialInterval:    time.Millisecond,
		MaxInterval:        time.Millisecond,
		MaxRetries:         1,
		StartupGracePeriod: 0,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := dispatcher.waitForStartup(ctx, "test-queue", runRef, startupWaitState{
		launchedAt: time.Now().Add(-time.Second),
		execDone: func() (bool, error) {
			if checks.Add(1) >= 3 {
				cancel()
			}
			return false, nil
		},
	})

	require.False(t, started)
	require.GreaterOrEqual(t, checks.Load(), int32(3))
	dagRunRepository.AssertExpectations(t)
	procRepository.AssertExpectations(t)
}

func TestQueueDispatcher_WaitForStartupBoundsLocalObservationErrors(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")
	storeErr := errors.New("status store unavailable")

	procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Twice()
	dagRunRepository.On("FindAttempt", mock.Anything, runRef).Return(nil, storeErr).Twice()

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		InitialInterval:    time.Millisecond,
		MaxInterval:        time.Millisecond,
		MaxRetries:         1,
		StartupGracePeriod: 0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := dispatcher.waitForStartup(ctx, "test-queue", runRef, startupWaitState{
		launchedAt: time.Now().Add(-time.Second),
		execDone: func() (bool, error) {
			return false, nil
		},
	})

	require.False(t, started)
	dagRunRepository.AssertExpectations(t)
	procRepository.AssertExpectations(t)
}

func TestQueueDispatcher_CheckStartupStatus_AfterGraceFallsBackToStatus(t *testing.T) {
	testCases := []struct {
		name      string
		status    ir.Status
		wantStart bool
		wantErr   error
	}{
		{name: "Queued", status: ir.Queued, wantStart: false, wantErr: errNotStarted},
		{name: "Running", status: ir.Running, wantStart: true},
		{name: "NotStarted", status: ir.NotStarted, wantStart: true},
		{name: "Succeeded", status: ir.Succeeded, wantStart: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dagRunRepository := &mockDAGRunStore{}
			procRepository := &mockProcRepository{}
			runRef := ir.NewDAGRunRef("test-dag", "run-1")
			attempt := &testutil.MockAttempt{
				Status: &ir.DAGRunStatus{Status: tc.status},
			}

			procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()
			dagRunRepository.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil).Once()

			dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
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

			dagRunRepository.AssertExpectations(t)
			procRepository.AssertExpectations(t)
		})
	}
}

func TestQueueDispatcher_CheckStartupStatus_AfterGracePropagatesLeaseLookupError(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	leaseStore := &mockLeaseStore{
		getFunc: func(context.Context, string) (*dispatch.DAGRunLease, error) {
			return nil, errors.New("lease store unavailable")
		},
	}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")
	attempt := &testutil.MockAttempt{
		Status: &ir.DAGRunStatus{
			Status:    ir.Queued,
			AttemptID: "attempt-1",
		},
	}

	procRepository.On("IsRunAlive", mock.Anything, "test-queue", runRef).Return(false, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil).Once()

	dispatcher := newStartupTestDispatcher(dagRunRepository.repository(), procRepository, BackoffConfig{
		StartupGracePeriod: 50 * time.Millisecond,
	})
	dispatcher.dagRunLeaseStore = leaseStore

	started, err := dispatcher.checkStartupStatus(context.Background(), "test-queue", runRef, startupWaitState{
		launchedAt: time.Now().Add(-time.Second),
		execErrCh:  make(chan error, 1),
	})

	require.False(t, started)
	require.EqualError(t, err, "lease store unavailable")
	dagRunRepository.AssertExpectations(t)
	procRepository.AssertExpectations(t)
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

// mockDispatcher implements dispatch.Dispatcher for testing dispatch behavior.
type mockDispatcher struct {
	callCount atomic.Int32
	mu        sync.Mutex
	lastReq   dispatch.DispatchRequest
	errFunc   func(callNum int32) error
}

func (m *mockDispatcher) Dispatch(_ context.Context, req dispatch.DispatchRequest) error {
	m.mu.Lock()
	m.lastReq = req
	m.mu.Unlock()
	n := m.callCount.Add(1)
	if m.errFunc != nil {
		return m.errFunc(n)
	}
	return nil
}

func (m *mockDispatcher) LastRequest() dispatch.DispatchRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReq
}

func (m *mockDispatcher) Cleanup(_ context.Context) error { return nil }

func (m *mockDispatcher) GetDAGRunStatus(_ context.Context, _, _ string, _ *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	return nil, nil
}

func (m *mockDispatcher) RequestCancel(_ context.Context, _, _ string, _ *ir.DAGRunRef) error {
	return nil
}

func TestQueueDispatcher_DispatchFailureReturnsToQueueScan(t *testing.T) {
	procRepository := &mockProcRepository{}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")

	disp := &mockDispatcher{
		errFunc: func(int32) error {
			return errors.New("no available workers")
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "")
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeScheduler}

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		procRepository: procRepository,
		dagExecutor:    dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status, "")
	require.False(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procRepository.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_StaleQueueDispatchIsDiscarded(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}

	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return backoff.PermanentError(&queuedomain.StaleQueueDispatchError{
				Reason: "queued attempt was superseded",
			})
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "")
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeScheduler}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunRepository: dagRunRepository.repository(),
		procRepository:   procRepository,
		dagExecutor:      dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status, "")
	require.True(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procRepository.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_RawStaleQueueDispatchIsDiscarded(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}

	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return &queuedomain.StaleQueueDispatchError{Reason: "queued attempt was superseded"}
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "")
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{Status: ir.Queued, TriggerType: ir.TriggerTypeScheduler}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunRepository: dagRunRepository.repository(),
		procRepository:   procRepository,
		dagExecutor:      dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status, "")
	require.True(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procRepository.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
}

func TestQueueDispatcher_DispatchAndWaitForStartup_PermanentErrorLeavesRunQueued(t *testing.T) {
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	attempt := &testutil.MockAttempt{}

	disp := &mockDispatcher{
		errFunc: func(_ int32) error {
			return backoff.PermanentError(errors.New("no workers match the required selector"))
		},
	}

	dagExec := NewDAGExecutor(disp, nil, config.ExecutionModeDistributed, "")
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Name:        "test-dag",
		DAGRunID:    "run-1",
		AttemptID:   "attempt-1",
		Status:      ir.Queued,
		TriggerType: ir.TriggerTypeScheduler,
	}
	runRef := ir.NewDAGRunRef("test-dag", "run-1")
	dagRunRepository.On("FindAttempt", mock.Anything, runRef).Return(attempt, nil).Once()
	attempt.On("Hidden").Return(false).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Once()
	dagRunRepository.On(
		"CompareAndSwapLatestAttemptStatus",
		mock.Anything,
		runRef,
		"attempt-1",
		ir.Queued,
		mock.Anything,
	).Return(status, true, nil).Once()

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunRepository: dagRunRepository.repository(),
		procRepository:   procRepository,
		dagExecutor:      dagExec,
		backoffConfig: BackoffConfig{
			InitialInterval:    10 * time.Millisecond,
			MaxInterval:        50 * time.Millisecond,
			MaxRetries:         5,
			StartupGracePeriod: 10 * time.Millisecond,
		},
	})

	started := dispatcher.dispatchAndWaitForStartup(context.Background(), "test-queue", runRef, dag, "run-1", status, "")
	require.False(t, started)
	require.Equal(t, int32(1), disp.callCount.Load())
	procRepository.AssertNotCalled(t, "IsRunAlive", mock.Anything, mock.Anything, mock.Anything)
	dagRunRepository.AssertExpectations(t)
	attempt.AssertExpectations(t)
}
