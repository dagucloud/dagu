// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/worker"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkerStart(t *testing.T) {
	t.Run("StartAndStop", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create worker
		w := createTestWorker(t, "test-worker", 5, coord)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker in background
		done := make(chan error, 1)
		go func() {
			done <- w.Start(ctx)
		}()

		// Wait for worker to register via heartbeat
		require.Eventually(t, func() bool {
			workers, err := coord.GetCoordinatorClient(t).GetWorkers(context.Background())
			if err != nil {
				return false
			}
			for _, wk := range workers {
				if wk.WorkerId == "test-worker" {
					return true
				}
			}
			return false
		}, 5*time.Second, 10*time.Millisecond, "Worker did not register via heartbeat")

		// Stop worker by cancelling context
		cancel()

		// Should exit cleanly
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Worker did not stop within timeout")
		}

		// Cleanup should succeed
		err := w.Stop(context.Background())
		assert.NoError(t, err)

		// Cleanup
		th.Cleanup()
	})

	t.Run("MultiplePollers", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)
		t.Cleanup(th.Cleanup)

		maxActiveRuns := 3
		w := createTestWorker(t, "test-worker", maxActiveRuns, coord)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)

		// Track completed task identities instead of inferring completion from
		// the number of poller callbacks racing with dispatch.
		var pollCount atomic.Int32
		processedRuns := make(chan string, 5)
		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, task *coordinatorv1.Task) error {
				pollCount.Add(1)
				select {
				case processedRuns <- task.DagRunId:
				default:
				}
				return nil
			},
		})

		// Start worker first
		go func() {
			done <- w.Start(ctx)
		}()
		var stopOnce sync.Once
		stopWorker := func() {
			stopOnce.Do(func() {
				cancel()
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer stopCancel()
				assert.NoError(t, w.Stop(stopCtx))
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(10 * time.Second):
					t.Error("Worker did not stop within timeout")
				}
			})
		}
		t.Cleanup(stopWorker)

		// Wait for worker to register via heartbeat
		require.Eventually(t, func() bool {
			workers, err := coord.GetCoordinatorClient(t).GetWorkers(context.Background())
			if err != nil {
				return false
			}
			for _, wk := range workers {
				if wk.WorkerId == "test-worker" {
					return true
				}
			}
			return false
		}, 5*time.Second, 10*time.Millisecond, "Worker did not register via heartbeat")

		// Dispatch multiple tasks
		expectedRuns := make(map[string]struct{}, 5)
		for i := range 5 {
			runID := fmt.Sprintf("run-%c", 'a'+i)
			expectedRuns[runID] = struct{}{}
			task := &coordinatorv1.Task{
				DagRunId:   runID,
				Target:     "test.yaml",
				Operation:  coordinatorv1.Operation_OPERATION_START,
				Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
			}
			err := coord.DispatchTask(t, task)
			require.NoError(t, err)
		}

		// Wait for all tasks to be processed
		seenRuns := make(map[string]struct{}, len(expectedRuns))
		timeout := time.After(20 * time.Second)
		for len(seenRuns) < len(expectedRuns) {
			select {
			case runID := <-processedRuns:
				if _, ok := expectedRuns[runID]; ok {
					seenRuns[runID] = struct{}{}
				}
			case <-timeout:
				t.Fatalf("processed %d/%d expected tasks; seen=%v expected=%v", len(seenRuns), len(expectedRuns), seenRuns, expectedRuns)
			}
		}

		// Should have processed multiple tasks
		assert.GreaterOrEqual(t, pollCount.Load(), int32(3))

		// Stop worker
		stopWorker()
	})
}

func TestWorkerTaskExecution(t *testing.T) {
	t.Run("ExecuteDispatchedTask", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create task
		expectedTask := &coordinatorv1.Task{
			DagRunId:   "test-run-123",
			Target:     "test.yaml",
			Operation:  coordinatorv1.Operation_OPERATION_START,
			Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
		}

		// Create worker
		w := createTestWorker(t, "test-worker", 1, coord)

		// Track task execution
		var executedTask *coordinatorv1.Task
		var wg sync.WaitGroup
		wg.Add(1)

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, task *coordinatorv1.Task) error {
				executedTask = task
				wg.Done()
				return nil
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker first
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for worker to register via heartbeat
		requireWorkerRegistered(t, coord, "test-worker")

		// Dispatch task to coordinator
		err := coord.DispatchTask(t, expectedTask)
		require.NoError(t, err)

		// Wait for task execution
		done := make(chan bool)
		go func() {
			wg.Wait()
			done <- true
		}()

		select {
		case <-done:
			// Task executed
		case <-time.After(5 * time.Second):
			t.Fatal("Task was not executed within timeout")
		}

		// Verify task was executed
		require.NotNil(t, executedTask)
		assert.Equal(t, expectedTask.DagRunId, executedTask.DagRunId)
		assert.Equal(t, expectedTask.Target, executedTask.Target)

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())

		// Cleanup
		th.Cleanup()
	})

	t.Run("HandleTaskExecutionError", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create task
		task := &coordinatorv1.Task{
			DagRunId:   "error-run-123",
			Target:     "test.yaml",
			Operation:  coordinatorv1.Operation_OPERATION_START,
			Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
		}

		// Create worker with failing executor
		w := createTestWorker(t, "test-worker", 1, coord)

		var executionAttempted atomic.Bool
		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				executionAttempted.Store(true)
				return assert.AnError
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker first
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for worker to register via heartbeat
		requireWorkerRegistered(t, coord, "test-worker")

		// Dispatch task
		err := coord.DispatchTask(t, task)
		require.NoError(t, err)

		// Wait for execution attempt
		require.Eventually(t, func() bool {
			return executionAttempted.Load()
		}, 5*time.Second, 10*time.Millisecond, "Task execution was not attempted")

		// Task should have been attempted despite error
		assert.True(t, executionAttempted.Load())

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())

		// Cleanup
		th.Cleanup()
	})
}

func TestWorkerWithLabels(t *testing.T) {
	t.Run("WorkerWithSelectorLabels", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create task that requires specific labels
		task := &coordinatorv1.Task{
			DagRunId:       "labeled-run-123",
			Target:         "test.yaml",
			Operation:      coordinatorv1.Operation_OPERATION_START,
			WorkerSelector: map[string]string{"type": "special", "region": "us-east"},
			Definition:     "name: test\nsteps:\n  - name: step1\n    command: echo hello",
		}

		// Create worker WITHOUT matching labels
		w1 := createTestWorker(t, "worker-1", 1, coord)
		var w1Executed atomic.Bool
		w1.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				w1Executed.Store(true)
				return nil
			},
		})

		// Create worker WITH matching labels
		w2 := worker.NewWorker(
			"worker-2",
			1,
			coord.GetCoordinatorClient(t),
			map[string]string{"type": "special", "region": "us-east", "extra": "value"},
			th.Config,
		)
		var w2Executed atomic.Bool
		w2.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				w2Executed.Store(true)
				return nil
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		w1Done := make(chan error, 1)
		w2Done := make(chan error, 1)
		t.Cleanup(func() {
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			assert.NoError(t, w1.Stop(stopCtx))
			assert.NoError(t, w2.Stop(stopCtx))
			for _, done := range []chan error{w1Done, w2Done} {
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Error("Worker did not stop within timeout")
				}
			}
			th.Cleanup()
		})

		// Start both workers
		go func() { w1Done <- w1.Start(ctx) }()
		go func() { w2Done <- w2.Start(ctx) }()

		// Wait for both workers to register via heartbeat
		requireWorkerRegistered(t, coord, "worker-1")
		requireWorkerRegistered(t, coord, "worker-2")

		// Dispatch task
		err := coord.DispatchTask(t, task)
		require.NoError(t, err)

		// Wait for the labeled worker to execute
		require.Eventually(t, func() bool {
			return w2Executed.Load()
		}, 15*time.Second, 10*time.Millisecond, "Worker with labels did not execute")

		// Only worker with matching labels should execute
		assert.False(t, w1Executed.Load(), "Worker without labels should not execute")
		assert.True(t, w2Executed.Load(), "Worker with labels should execute")

	})
}

func TestWorkerHeartbeat(t *testing.T) {
	t.Run("SendsHeartbeats", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create worker
		w := createTestWorker(t, "heartbeat-worker", 3, coord)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Start worker
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for heartbeat to register the worker with expected stats
		require.Eventually(t, func() bool {
			workers, err := coord.GetCoordinatorClient(t).GetWorkers(context.Background())
			if err != nil {
				return false
			}
			for _, wk := range workers {
				if wk.WorkerId == "heartbeat-worker" {
					return wk.TotalPollers == 3 && wk.LastHeartbeatAt != 0
				}
			}
			return false
		}, 5*time.Second, 10*time.Millisecond, "Worker should be registered via heartbeats")

		// Get workers from coordinator to verify heartbeats
		workers, err := coord.GetCoordinatorClient(t).GetWorkers(context.Background())
		require.NoError(t, err)

		// Should have our worker registered
		var found bool
		for _, wk := range workers {
			if wk.WorkerId == "heartbeat-worker" {
				found = true
				assert.Equal(t, int32(3), wk.TotalPollers)
				assert.NotZero(t, wk.LastHeartbeatAt)
				break
			}
		}
		assert.True(t, found, "Worker should be registered via heartbeats")

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())

		// Cleanup
		th.Cleanup()
	})
}

func TestWorkerStopWithoutStart(t *testing.T) {
	t.Run("StopUnstartedWorker", func(t *testing.T) {
		labels := make(map[string]string)

		// Create a mock coordinator client that doesn't connect
		mockCoordinatorCli := newMockCoordinatorCli()
		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, labels, &config.Config{})
		w.SetHandler(&mockHandler{ExecutionTime: 0})

		// Stop should work without error even if not started
		err := w.Stop(context.Background())
		require.NoError(t, err)
	})
}

func TestWorkerDefaultID(t *testing.T) {
	t.Run("GeneratesDefaultIDWhenEmpty", func(t *testing.T) {
		labels := make(map[string]string)

		// Create a mock coordinator client
		mockCoordinatorCli := newMockCoordinatorCli()

		// Create worker with empty ID
		w := worker.NewWorker("", 1, mockCoordinatorCli, labels, &config.Config{})
		require.NotNil(t, w)

		// Worker should have generated a default ID (hostname@pid format)
		// We can't check the exact value, but we can verify it's not empty
		// by starting and stopping the worker (which uses the ID for logging)
		w.SetHandler(&mockHandler{ExecutionTime: 0})

		// Stop should work without error
		err := w.Stop(context.Background())
		require.NoError(t, err)
	})
}

func TestRunningTaskTracking(t *testing.T) {
	t.Run("TrackRunningTasks", func(t *testing.T) {
		// Setup test environment
		coord := test.SetupCoordinator(t, test.WithStatusPersistence())
		th := test.Setup(t)

		// Create worker that holds tasks
		w := createTestWorker(t, "task-tracker", 3, coord)

		var activeTasksMu sync.Mutex
		activeTasks := make(map[string]bool)
		taskStarted := make(chan string, 5)
		releaseTasks := make(chan struct{})

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(ctx context.Context, task *coordinatorv1.Task) error {
				activeTasksMu.Lock()
				activeTasks[task.DagRunId] = true
				activeTasksMu.Unlock()

				taskStarted <- task.DagRunId

				// Hold task until released or context cancelled
				select {
				case <-releaseTasks:
				case <-ctx.Done():
				}

				activeTasksMu.Lock()
				delete(activeTasks, task.DagRunId)
				activeTasksMu.Unlock()
				return nil
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for worker to register via heartbeat
		requireWorkerRegistered(t, coord, "task-tracker")

		// Create first 3 tasks to fill all pollers
		for i := range 3 {
			task := &coordinatorv1.Task{
				DagRunId:   "task-" + string(rune('a'+i)),
				Target:     "test.yaml",
				Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
			}
			err := coord.DispatchTask(t, task)
			require.NoError(t, err)
		}

		// Wait for all 3 tasks to start
		for range 3 {
			select {
			case <-taskStarted:
				// Task started
			case <-time.After(5 * time.Second):
				t.Fatal("Tasks did not start within timeout")
			}
		}

		// Check that we have 3 active tasks (they should still be running)
		activeTasksMu.Lock()
		activeCount := len(activeTasks)
		activeTasksMu.Unlock()
		assert.Equal(t, 3, activeCount, "Should have 3 active tasks")

		// Release all held tasks so pollers become free
		close(releaseTasks)

		// Wait for released tasks to drain before dispatching more work.
		require.Eventually(t, func() bool {
			activeTasksMu.Lock()
			defer activeTasksMu.Unlock()
			return len(activeTasks) == 0
		}, 5*time.Second, 10*time.Millisecond, "Released tasks did not finish")

		// Dispatch 2 more tasks
		for i := 3; i < 5; i++ {
			task := &coordinatorv1.Task{
				DagRunId:   "task-" + string(rune('a'+i)),
				Target:     "test.yaml",
				Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
			}
			err := coord.DispatchTask(t, task)
			require.NoError(t, err)
		}

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())

		// Cleanup
		th.Cleanup()
	})
}

func TestWorkerConnectionFailure(t *testing.T) {
	t.Run("HandleConnectionFailure", func(t *testing.T) {
		// Create worker with a mock coordinator client that always fails
		var pollCount atomic.Int32
		connectionError := status.Error(codes.Unavailable, "connection failed")

		mockCoordinatorCli := newMockCoordinatorCli()

		mockCoordinatorCli.PollFunc = func(_ context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			pollCount.Add(1)
			return nil, connectionError
		}

		labels := make(map[string]string)
		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, labels, &config.Config{})
		w.SetHandler(&mockHandler{})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker
		done := make(chan struct{})
		go func() {
			_ = w.Start(ctx)
			close(done)
		}()

		require.Eventually(t, func() bool {
			return pollCount.Load() > 1
		}, 2*time.Second, 10*time.Millisecond, "Should retry on connection failures")

		// Should have attempted multiple polls despite failures
		assert.Greater(t, pollCount.Load(), int32(1), "Should retry on connection failures")

		// Stop worker
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, w.Stop(stopCtx))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Worker did not stop within timeout")
		}

		// Verify coordinator client is in failed state
		metrics := mockCoordinatorCli.Metrics()
		assert.False(t, metrics.IsConnected)
		assert.Greater(t, metrics.ConsecutiveFails, 0)
		assert.NotNil(t, metrics.LastError)
	})
}

var _ worker.TaskHandler = (*mockHandler)(nil)

type mockHandler struct {
	ExecuteFunc   func(context.Context, *coordinatorv1.Task) error
	ExecutionTime time.Duration
}

func (m *mockHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, task)
	}

	// Default behavior: simulate task execution
	if m.ExecutionTime > 0 {
		select {
		case <-time.After(m.ExecutionTime):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// createTestWorker creates a worker with a mock dagrun.Manager and coordinator client for testing
func createTestWorker(t *testing.T, workerID string, maxActiveRuns int, coord *test.Coordinator) *worker.Worker {

	// Create coordinator client for the coordinator
	coordinatorClient := coord.GetCoordinatorClient(t)

	labels := make(map[string]string)
	return worker.NewWorker(workerID, maxActiveRuns, coordinatorClient, labels, &config.Config{})
}

func requireWorkerRegistered(t *testing.T, coord *test.Coordinator, workerID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		workers, err := coord.GetCoordinatorClient(t).GetWorkers(context.Background())
		if err != nil {
			return false
		}
		for _, wk := range workers {
			if wk.WorkerId == workerID {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "Worker %q did not register via heartbeat", workerID)
}

func TestWorkerCancellation(t *testing.T) {
	t.Run("CancelClaimedTaskBeforeExecutionWhenOwnerRejectsAttempt", func(t *testing.T) {
		mockCoordinatorCli := newMockCoordinatorCli()
		var validationCalls atomic.Int32
		executedTaskIDs := make(chan string, 2)

		mockCoordinatorCli.RunHeartbeatFunc = func(_ context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
			callNum := validationCalls.Add(1)
			assert.Equal(t, "coord-a", owner.ID)
			assert.Len(t, req.RunningTasks, 1)
			if callNum == 1 {
				return &coordinatorv1.RunHeartbeatResponse{
					CancelledRuns: []*coordinatorv1.CancelledRun{
						{AttemptKey: "attempt-key-1"},
					},
				}, nil
			}
			return &coordinatorv1.RunHeartbeatResponse{
				CancelledRuns: nil,
			}, nil
		}

		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, map[string]string{}, &config.Config{})

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, task *coordinatorv1.Task) error {
				executedTaskIDs <- task.DagRunId
				return nil
			},
		})

		var pollCalls atomic.Int32
		mockCoordinatorCli.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			switch pollCalls.Add(1) {
			case 1:
				return &coordinatorv1.Task{
					DagRunId:             "run-123",
					Target:               "test.yaml",
					Definition:           "name: test\nsteps:\n  - name: step1\n    command: echo hello",
					AttemptKey:           "attempt-key-1",
					OwnerCoordinatorId:   "coord-a",
					OwnerCoordinatorHost: "127.0.0.1",
					OwnerCoordinatorPort: 1234,
				}, nil
			case 2:
				return &coordinatorv1.Task{
					DagRunId:             "run-456",
					Target:               "test.yaml",
					Definition:           "name: test\nsteps:\n  - name: step1\n    command: echo hello",
					AttemptKey:           "attempt-key-2",
					OwnerCoordinatorId:   "coord-a",
					OwnerCoordinatorHost: "127.0.0.1",
					OwnerCoordinatorPort: 1234,
				}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = w.Start(ctx)
		}()

		require.Eventually(t, func() bool {
			return validationCalls.Load() >= 2
		}, 2*time.Second, 10*time.Millisecond)

		select {
		case runID := <-executedTaskIDs:
			assert.Equal(t, "run-456", runID, "only the replacement task should execute")
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not execute the next task after the rejected claim")
		}

		assert.Never(t, func() bool {
			return validationCalls.Load() > 2
		}, 1200*time.Millisecond, 50*time.Millisecond, "rejected claims must not remain registered for owner heartbeats")

		cancel()
		_ = w.Stop(context.Background())
	})

	t.Run("OwnerValidationFailureDoesNotBlockExecution", func(t *testing.T) {
		mockCoordinatorCli := newMockCoordinatorCli()
		mockCoordinatorCli.RunHeartbeatFunc = func(_ context.Context, _ exec.HostInfo, _ *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
			return nil, errors.New("owner unavailable")
		}

		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, map[string]string{}, &config.Config{})
		executed := make(chan struct{}, 1)
		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				executed <- struct{}{}
				return nil
			},
		})

		var dispatched atomic.Bool
		mockCoordinatorCli.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			if dispatched.Swap(true) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &coordinatorv1.Task{
				DagRunId:             "run-456",
				Target:               "test.yaml",
				Definition:           "name: test\nsteps:\n  - name: step1\n    command: echo hello",
				AttemptKey:           "attempt-key-2",
				OwnerCoordinatorId:   "coord-a",
				OwnerCoordinatorHost: "127.0.0.1",
				OwnerCoordinatorPort: 1234,
			}, nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = w.Start(ctx)
		}()

		select {
		case <-executed:
		case <-time.After(2 * time.Second):
			t.Fatal("task was not executed after transient owner validation failure")
		}

		cancel()
		_ = w.Stop(context.Background())
	})

	t.Run("CancelRunningTaskViaCancellationDirective", func(t *testing.T) {
		// Create a mock coordinator client that returns cancellation directives
		cancelledRunID := "task-to-cancel"
		var heartbeatCount atomic.Int32

		mockCoordinatorCli := newMockCoordinatorCli()

		cancelledAttemptKey := "test-attempt-key-" + cancelledRunID

		// Track when heartbeat is called and return cancellation directive
		mockCoordinatorCli.HeartbeatFunc = func(_ context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			count := heartbeatCount.Add(1)
			// After a few heartbeats, return the cancellation directive
			if count >= 2 {
				return &coordinatorv1.HeartbeatResponse{
					CancelledRuns: []*coordinatorv1.CancelledRun{
						{AttemptKey: cancelledAttemptKey},
					},
				}, nil
			}
			return &coordinatorv1.HeartbeatResponse{}, nil
		}

		labels := make(map[string]string)
		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, labels, &config.Config{})

		// Track if task was cancelled via context
		taskCancelled := make(chan bool, 1)

		// Set up poll to return the task that will be cancelled
		var taskDispatched atomic.Bool
		mockCoordinatorCli.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			if taskDispatched.Swap(true) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &coordinatorv1.Task{
				DagRunId:   cancelledRunID,
				Target:     "test.yaml",
				Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
				AttemptKey: cancelledAttemptKey,
			}, nil
		})

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(ctx context.Context, _ *coordinatorv1.Task) error {
				// Wait for cancellation signal from context
				select {
				case <-ctx.Done():
					taskCancelled <- true
					return ctx.Err()
				case <-time.After(5 * time.Second):
					taskCancelled <- false
					return nil
				}
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for task to be cancelled
		select {
		case cancelled := <-taskCancelled:
			assert.True(t, cancelled, "Task should have been cancelled via context")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for task cancellation")
		}

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())
	})

	t.Run("CancellationIgnoresNonExistentTasks", func(t *testing.T) {
		// Create a mock coordinator client
		mockCoordinatorCli := newMockCoordinatorCli()

		// Return cancellation directive for non-existent task
		mockCoordinatorCli.HeartbeatFunc = func(_ context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			return &coordinatorv1.HeartbeatResponse{
				CancelledRuns: []*coordinatorv1.CancelledRun{
					{AttemptKey: "non-existent-attempt-key"},
				},
			}, nil
		}

		labels := make(map[string]string)
		w := worker.NewWorker("test-worker", 1, mockCoordinatorCli, labels, &config.Config{})

		taskExecuted := make(chan bool, 1)

		// Set up poll to return a task with a DIFFERENT ID than what will be cancelled
		var taskDispatched atomic.Bool
		mockCoordinatorCli.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			if taskDispatched.Swap(true) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &coordinatorv1.Task{
				DagRunId:   "different-task-id",
				Target:     "test.yaml",
				Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
			}, nil
		})

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(_ context.Context, _ *coordinatorv1.Task) error {
				taskExecuted <- true
				return nil
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker
		go func() {
			_ = w.Start(ctx)
		}()

		// Task should complete normally
		select {
		case executed := <-taskExecuted:
			assert.True(t, executed, "Task should have executed normally")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for task execution")
		}

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())
	})

	t.Run("MultipleCancellationsInSingleResponse", func(t *testing.T) {
		// Create a mock coordinator client
		mockCoordinatorCli := newMockCoordinatorCli()

		// Track heartbeats
		var heartbeatCount atomic.Int32

		mockCoordinatorCli.HeartbeatFunc = func(_ context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
			count := heartbeatCount.Add(1)
			// Return multiple cancellation directives after some heartbeats
			if count >= 2 {
				return &coordinatorv1.HeartbeatResponse{
					CancelledRuns: []*coordinatorv1.CancelledRun{
						{AttemptKey: "attempt-key-1"},
						{AttemptKey: "attempt-key-2"},
						{AttemptKey: "attempt-key-3"},
					},
				}, nil
			}
			return &coordinatorv1.HeartbeatResponse{}, nil
		}

		labels := make(map[string]string)
		w := worker.NewWorker("test-worker", 3, mockCoordinatorCli, labels, &config.Config{})

		// Track cancelled tasks
		cancelledTasks := make(chan string, 3)

		w.SetHandler(&mockHandler{
			ExecuteFunc: func(ctx context.Context, task *coordinatorv1.Task) error {
				select {
				case <-ctx.Done():
					cancelledTasks <- task.DagRunId
					return ctx.Err()
				case <-time.After(5 * time.Second):
					return nil
				}
			},
		})

		// Setup poll to return tasks
		var taskIndex atomic.Int32
		mockCoordinatorCli.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
			idx := taskIndex.Add(1)
			if idx <= 3 {
				return &coordinatorv1.Task{
					DagRunId:   fmt.Sprintf("task-%d", idx),
					Target:     "test.yaml",
					Definition: "name: test\nsteps:\n  - name: step1\n    command: echo hello",
					AttemptKey: fmt.Sprintf("attempt-key-%d", idx),
				}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil, nil
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker
		go func() {
			_ = w.Start(ctx)
		}()

		// Wait for cancellations
		cancelledCount := 0
		timeout := time.After(5 * time.Second)

	collectLoop:
		for {
			select {
			case <-cancelledTasks:
				cancelledCount++
				if cancelledCount >= 3 {
					break collectLoop
				}
			case <-timeout:
				break collectLoop
			}
		}

		assert.Equal(t, 3, cancelledCount, "All 3 tasks should have been cancelled")

		// Stop worker
		cancel()
		_ = w.Stop(context.Background())
	})
}
