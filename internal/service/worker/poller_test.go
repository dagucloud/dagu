// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPollerStateTracking tests that the poller correctly tracks connection state via coordinator client
func TestPollerStateTracking(t *testing.T) {
	t.Parallel()

	t.Run("InitialStateIsConnected", func(t *testing.T) {
		t.Parallel()
		mockCoordinatorCli := newMockCoordinatorCli()
		mockHandler := &mockHandler{}
		labels := make(map[string]string)

		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Check initial state
		isConnected, consecutiveFails, lastError := poller.GetState()
		assert.True(t, isConnected)
		assert.Equal(t, 0, consecutiveFails)
		assert.Nil(t, lastError)
	})

	t.Run("StateReflectsDispatcherMetrics", func(t *testing.T) {
		t.Parallel()

		mockCoordinatorCli := newMockCoordinatorCli()
		connectionError := status.Error(codes.Unavailable, "connection refused")

		// Configure coordinator client to return specific metrics
		mockCoordinatorCli.MetricsFunc = func() coordinator.Metrics {
			return coordinator.Metrics{
				IsConnected:      false,
				ConsecutiveFails: 5,
				LastError:        connectionError,
			}
		}

		mockHandler := &mockHandler{}
		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// State should reflect coordinator client metrics
		isConnected, consecutiveFails, lastError := poller.GetState()
		assert.False(t, isConnected)
		assert.Equal(t, 5, consecutiveFails)
		assert.Equal(t, connectionError, lastError)
	})
}

func TestPollerTaskDispatch(t *testing.T) {
	t.Parallel()

	t.Run("DispatchTaskToExecutor", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedTask := &coordinatorv1.Task{
			DagRunId: "test-run-123",
			Target:   "test.yaml",
		}

		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			// Return task once then nil
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				// Return task on first call, then block
				return expectedTask, nil
			}
		}

		var executedTask *coordinatorv1.Task
		mockHandler := &mockHandler{
			ExecuteFunc: func(_ context.Context, task *coordinatorv1.Task) error {
				executedTask = task
				cancel() // Stop poller after executing task
				return nil
			},
		}

		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller
		poller.Run(ctx)

		// Verify task was executed
		require.NotNil(t, executedTask)
		assert.Equal(t, expectedTask.DagRunId, executedTask.DagRunId)
		assert.Equal(t, expectedTask.Target, executedTask.Target)
	})

	t.Run("ContinuePollingAfterTaskExecution", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var pollCount int32
		var executionCount int32

		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			count := atomic.AddInt32(&pollCount, 1)

			// Return tasks for first 3 polls
			if count <= 3 {
				return &coordinatorv1.Task{
					DagRunId: fmt.Sprintf("run-%d", count),
				}, nil
			}

			// Then just wait
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil, nil
			}
		}

		mockHandler := &mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				atomic.AddInt32(&executionCount, 1)
				return nil
			},
		}

		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller in background
		go poller.Run(ctx)

		// Wait for all 3 tasks to be executed
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&executionCount) >= 3
		}, 5*time.Second, 10*time.Millisecond)
		cancel()

		// Should have executed 3 tasks
		assert.Equal(t, int32(3), atomic.LoadInt32(&executionCount))
		assert.GreaterOrEqual(t, atomic.LoadInt32(&pollCount), int32(3))
	})
}

func TestPollerErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("HandleExecutorError", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mockCoordinatorCli := newMockCoordinatorCli()
		var taskReturned bool
		mockCoordinatorCli.PollFunc = func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			if !taskReturned {
				taskReturned = true
				return &coordinatorv1.Task{DagRunId: "error-task"}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
				return nil, nil
			}
		}

		executorError := fmt.Errorf("execution failed")
		var executionAttempted atomic.Bool
		mockHandler := &mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				executionAttempted.Store(true)
				return executorError
			},
		}

		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller in background
		go poller.Run(ctx)

		// Wait for execution attempt
		require.Eventually(t, func() bool {
			return executionAttempted.Load()
		}, 5*time.Second, 10*time.Millisecond)
		cancel()

		// Verify execution was attempted despite error
		assert.True(t, executionAttempted.Load())
	})

	t.Run("ContinueOnPollError", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var pollAttempts int32
		pollError := status.Error(codes.Unavailable, "poll failed")

		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(_ context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			count := atomic.AddInt32(&pollAttempts, 1)

			if count <= 3 {
				// Fail first 3 attempts
				return nil, pollError
			}

			// Then return a task
			return &coordinatorv1.Task{DagRunId: "success-after-retry"}, nil
		}

		var taskExecuted atomic.Bool
		mockHandler := &mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				taskExecuted.Store(true)
				cancel() // Stop after execution
				return nil
			},
		}

		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller (will retry on errors)
		poller.Run(ctx)

		// Should have retried and eventually succeeded
		assert.True(t, taskExecuted.Load())
		assert.GreaterOrEqual(t, atomic.LoadInt32(&pollAttempts), int32(4))
	})
}

func TestPollerContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("StopOnContextCancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())

		var pollStarted atomic.Bool
		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			pollStarted.Store(true)
			// Block until cancelled
			<-ctx.Done()
			return nil, ctx.Err()
		}

		mockHandler := &mockHandler{}
		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller in background
		var wg sync.WaitGroup
		wg.Go(func() {
			poller.Run(ctx)
		})

		// Wait for poll to start
		require.Eventually(t, func() bool {
			return pollStarted.Load()
		}, 5*time.Second, 10*time.Millisecond, "Poll should have started")

		// Cancel and wait for completion
		cancel()
		wg.Wait()
	})

	t.Run("StopExecutionOnContextCancel", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())

		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(_ context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			return &coordinatorv1.Task{DagRunId: "long-task"}, nil
		}

		var executionStarted atomic.Bool
		mockHandler := &mockHandler{
			ExecuteFunc: func(ctx context.Context, _ *coordinatorv1.Task) error {
				executionStarted.Store(true)
				// Simulate long-running task
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Second):
					return nil
				}
			},
		}

		labels := make(map[string]string)
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, labels)

		// Run poller in background
		var wg sync.WaitGroup
		wg.Go(func() {
			poller.Run(ctx)
		})

		// Wait for execution to start
		require.Eventually(t, func() bool {
			return executionStarted.Load()
		}, 5*time.Second, 10*time.Millisecond, "Execution should have started")

		// Cancel should stop execution
		cancel()
		wg.Wait()
	})
}

func TestPollerWithLabels(t *testing.T) {
	t.Parallel()

	t.Run("SendLabelsInPollRequest", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		expectedLabels := map[string]string{
			"region": "us-east-1",
			"type":   "gpu",
		}

		var receivedReq *coordinatorv1.PollRequest
		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.PollFunc = func(_ context.Context, _ backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			receivedReq = req
			cancel() // Stop after first poll
			return nil, nil
		}

		mockHandler := &mockHandler{}
		poller := worker.NewPoller("test-worker", mockCoordinatorCli, mockHandler, 0, expectedLabels)

		// Run poller
		poller.Run(ctx)

		// Verify labels were sent
		require.NotNil(t, receivedReq)
		assert.Equal(t, "test-worker", receivedReq.WorkerId)
		assert.Equal(t, expectedLabels, receivedReq.Labels)
	})
}

var _ coordinator.Client = (*mockCoordinatorCli)(nil)

// mockCoordinatorCli is a mock implementation of coordinator.Client
type mockCoordinatorCli struct {
	PollFunc         func(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)
	DispatchFunc     func(ctx context.Context, task *exec.DispatchTask) error
	MetricsFunc      func() coordinator.Metrics
	CleanupFunc      func(ctx context.Context) error
	HeartbeatFunc    func(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)
	RunHeartbeatFunc func(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error)

	// Internal state tracking
	mu               sync.Mutex
	isConnected      bool
	consecutiveFails int
	lastError        error
}

func newMockCoordinatorCli() *mockCoordinatorCli {
	return &mockCoordinatorCli{
		isConnected: true,
	}
}

func (m *mockCoordinatorCli) Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	m.mu.Lock()
	pollFunc := m.PollFunc
	m.mu.Unlock()

	if pollFunc != nil {
		task, err := pollFunc(ctx, policy, req)
		m.updateState(err)
		return task, err
	}
	return nil, nil
}

func (m *mockCoordinatorCli) Dispatch(ctx context.Context, req exec.DispatchRequest) error {
	m.mu.Lock()
	dispatchFunc := m.DispatchFunc
	m.mu.Unlock()

	if dispatchFunc != nil {
		return dispatchFunc(ctx, req.Task)
	}
	return nil
}

func (m *mockCoordinatorCli) Metrics() coordinator.Metrics {
	m.mu.Lock()
	metricsFunc := m.MetricsFunc
	if metricsFunc != nil {
		m.mu.Unlock()
		return metricsFunc()
	}

	defer m.mu.Unlock()
	return coordinator.Metrics{
		IsConnected:      m.isConnected,
		ConsecutiveFails: m.consecutiveFails,
		LastError:        m.lastError,
	}
}

func (m *mockCoordinatorCli) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	cleanupFunc := m.CleanupFunc
	m.mu.Unlock()

	if cleanupFunc != nil {
		return cleanupFunc(ctx)
	}
	return nil
}

func (m *mockCoordinatorCli) GetWorkers(_ context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	// Return empty list by default for tests
	return []*coordinatorv1.WorkerInfo{}, nil
}

func (m *mockCoordinatorCli) Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	m.mu.Lock()
	heartbeatFunc := m.HeartbeatFunc
	m.mu.Unlock()

	if heartbeatFunc != nil {
		return heartbeatFunc(ctx, req)
	}
	// Return success by default for tests
	return &coordinatorv1.HeartbeatResponse{}, nil
}

func (m *mockCoordinatorCli) AckTaskClaimTo(_ context.Context, _ exec.HostInfo, _ *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (m *mockCoordinatorCli) RunHeartbeatTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	m.mu.Lock()
	runHeartbeatFunc := m.RunHeartbeatFunc
	m.mu.Unlock()

	if runHeartbeatFunc != nil {
		return runHeartbeatFunc(ctx, owner, req)
	}
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (m *mockCoordinatorCli) ReportStatus(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func (m *mockCoordinatorCli) ReportStatusTo(ctx context.Context, _ exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return m.ReportStatus(ctx, req)
}

func (m *mockCoordinatorCli) StreamLogs(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return nil, nil
}

func (m *mockCoordinatorCli) StreamLogsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return m.StreamLogs(ctx)
}

func (m *mockCoordinatorCli) StreamArtifacts(_ context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return nil, nil
}

func (m *mockCoordinatorCli) StreamArtifactsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return m.StreamArtifacts(ctx)
}

func (m *mockCoordinatorCli) GetDAGRunStatus(_ context.Context, _, _ string, _ *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	return &exec.DAGRunStatusResult{Found: false}, nil
}

func (m *mockCoordinatorCli) GetDAG(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockCoordinatorCli) RequestCancel(_ context.Context, _, _ string, _ *exec.DAGRunRef) error {
	return nil
}

func (m *mockCoordinatorCli) updateState(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		m.isConnected = false
		m.consecutiveFails++
		m.lastError = err
	} else {
		m.isConnected = true
		m.consecutiveFails = 0
		m.lastError = nil
	}
}

// SetPollFunc safely sets the PollFunc for concurrent access
func (m *mockCoordinatorCli) SetPollFunc(f func(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PollFunc = f
}

// SetHeartbeatFunc safely sets the HeartbeatFunc for concurrent access
func (m *mockCoordinatorCli) SetHeartbeatFunc(f func(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.HeartbeatFunc = f
}
