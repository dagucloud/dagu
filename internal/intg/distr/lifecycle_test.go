// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	runtimeagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancellation_SingleTask(t *testing.T) {
	t.Run("cancellationPropagatesToRemoteWorker", func(t *testing.T) {
		f := newTestFixture(t, fmt.Sprintf(`
name: cancel-test
worker_selector:
  test: "true"
steps:
  - name: long-task
    run: %s
`, test.ShellQuote(test.Sleep(60*time.Second))))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		var dagRunID string
		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			if status.Status == core.Running {
				dagRunID = status.DAGRunID
				return true
			}
			return false
		}, 10*time.Second, 1000*time.Millisecond, "DAG should start running")

		startTime := time.Now()
		require.NoError(t, f.stop(dagRunID))

		status := f.waitForStatusIn([]core.Status{core.Aborted, core.Failed}, 15*time.Second)

		elapsed := time.Since(startTime)
		assert.Less(t, elapsed, 10*time.Second, "cancellation should be quick")
		assert.Contains(t, []core.Status{core.Aborted, core.Failed}, status.Status)
	})
}

func TestCancellation_SubDAG(t *testing.T) {
	t.Run("parentCancelPropagatesToChildOnWorker", func(t *testing.T) {
		f := newTestFixture(t, fmt.Sprintf(`
steps:
  - action: dag.run
    with:
      dag: dotest
params:
  - URL: default_value
---
name: dotest
worker_selector:
  foo: bar
steps:
  - name: long-sleep
    run: %s
`, test.ShellQuote(test.Sleep(30*time.Second))), withLabels(map[string]string{"foo": "bar"}))
		defer f.cleanup()

		require.NoError(t, f.start())
		f.startScheduler(30 * time.Second)

		var dagRunID string
		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			if status.Status == core.Running {
				dagRunID = status.DAGRunID
				return true
			}
			return false
		}, 10*time.Second, 200*time.Millisecond, "Timeout waiting for DAG to start running")

		require.NoError(t, f.stop(dagRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Aborted || status.Status == core.Failed
		}, 15*time.Second, 500*time.Millisecond, "Timeout waiting for DAG to be cancelled")

		finalStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Contains(t, []core.Status{core.Aborted, core.Failed}, finalStatus.Status)
	})

	t.Run("cancelPropagatesToSubDAGOnWorker", func(t *testing.T) {
		f := newTestFixture(t, fmt.Sprintf(`
steps:
  - name: run-local-on-worker
    action: dag.run
    with:
      dag: local-sub
    output: RESULT

---
name: local-sub
worker_selector:
  type: test-worker
steps:
  - name: worker-task
    run: %s
    output: MESSAGE
`, test.ShellQuote(test.Sleep(1000*time.Second))), withLabels(map[string]string{"type": "test-worker"}))

		runID := uuid.New().String()
		attemptID := uuid.New().String()
		// The parent runs in-process in this test, so register its proc heartbeat
		// before using the runtime manager to stop it.
		proc, err := f.coord.ProcStore.Acquire(f.coord.Context, f.dagWrapper.ProcGroup(), exec.ProcMeta{
			StartedAt:    time.Now().Unix(),
			Name:         f.dagWrapper.Name,
			DAGRunID:     runID,
			AttemptID:    attemptID,
			RootName:     f.dagWrapper.Name,
			RootDAGRunID: runID,
		})
		require.NoError(t, err)
		defer func() {
			require.NoError(t, proc.Stop(f.coord.Context))
		}()

		agent := f.dagWrapper.Agent(
			test.WithDAGRunID(runID),
			test.WithAgentOptions(runtimeagent.Options{AttemptID: attemptID}),
		)
		ctx := agent.Context

		errCh := make(chan error, 1)
		go func() {
			errCh <- agent.Run(ctx)
		}()

		rootRef := exec.NewDAGRunRef(f.dagWrapper.Name, runID)
		var subRunID string
		require.Eventually(t, func() bool {
			attempt, err := f.dagWrapper.DAGRunStore.FindAttempt(ctx, rootRef)
			if err != nil {
				return false
			}
			status, err := attempt.ReadStatus(ctx)
			if err != nil || status == nil || status.Status != core.Running {
				return false
			}

			for _, node := range status.Nodes {
				if node.Step.Name != "run-local-on-worker" || node.Status != core.NodeRunning || len(node.SubRuns) == 0 {
					continue
				}
				subRunID = node.SubRuns[0].DAGRunID
				return subRunID != ""
			}
			return false
		}, 30*time.Second, 100*time.Millisecond, "expected parent DAG to start sub DAG before cancellation")

		require.Eventually(t, func() bool {
			status, err := f.dagWrapper.DAGRunMgr.FindSubDAGRunStatus(ctx, rootRef, subRunID)
			return err == nil && status != nil && status.Status == core.Running
		}, 30*time.Second, 100*time.Millisecond, "expected sub DAG to reach running state before cancellation")

		require.NoError(t, f.stop(runID))

		f.dagWrapper.AssertLatestStatus(t, core.Aborted)

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for parent DAG cancellation")
		}

		require.Eventually(t, func() bool {
			subStatus, err := f.dagWrapper.DAGRunMgr.FindSubDAGRunStatus(ctx, rootRef, subRunID)
			return err == nil && subStatus != nil && subStatus.Status == core.Aborted
		}, 30*time.Second, 100*time.Millisecond, "expected sub DAG to become aborted after parent cancellation")
	})
}

func TestCancellation_ConcurrentWorkers(t *testing.T) {
	t.Run("cancellationWithHighConcurrency", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := newTestFixture(t, fmt.Sprintf(`
steps:
  - name: high-concurrency
    action: dag.run
    with:
      dag: child-task
    parallel:
      items:
        - "task1"
        - "task2"
        - "task3"
        - "task4"
        - "task5"
        - "task6"
      max_concurrent: 2

---
name: child-task
worker_selector:
  type: test-worker
steps:
  - name: process
    run: %s
`, test.ShellQuote(test.Sleep(30*time.Second))), withWorkerCount(3), withLabels(map[string]string{"type": "test-worker"}),
			withDAGsDir(tmpDir), withLogPersistence())

		agent := f.dagWrapper.Agent()

		done := make(chan struct{})
		go func() {
			agent.Context = f.coord.Context
			_ = agent.Run(agent.Context)
			close(done)
		}()

		require.Eventually(t, func() bool {
			st, err := f.latestStatus()
			if err != nil || !st.Status.IsActive() || len(st.Nodes) == 0 {
				return false
			}
			concurrentNode := st.Nodes[0]
			return concurrentNode.Status == core.NodeRunning && len(concurrentNode.SubRuns) >= 2
		}, 10*time.Second, 100*time.Millisecond)

		agent.Signal(f.coord.Context, os.Signal(syscall.SIGTERM))

		<-done

		st, err := f.latestStatus()
		require.NoError(t, err)
		require.NotNil(t, st)

		require.GreaterOrEqual(t, len(st.Nodes), 1)
		concurrentNode := st.Nodes[0]
		require.Equal(t, "high-concurrency", concurrentNode.Step.Name)

		require.Contains(t, []core.NodeStatus{core.NodePartiallySucceeded, core.NodeAborted}, concurrentNode.Status)
	})
}

func TestCancellation_GracefulShutdown(t *testing.T) {
	t.Run("gracefulShutdownOnSIGTERM", func(t *testing.T) {
		f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: graceful-cancel-test
worker_selector:
  test: "true"
steps:
  - name: task1
    run: %s
  - name: task2
    run: echo "should not run"
    depends: [task1]
`, test.ShellQuote(test.Sleep(30*time.Second))))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Running, 10*time.Second)

		require.NoError(t, f.stop(status.DAGRunID))

		finalStatus := f.waitForStatusIn([]core.Status{core.Aborted, core.Failed}, 15*time.Second)

		require.Contains(t, []core.Status{core.Aborted, core.Failed}, finalStatus.Status)

		for _, node := range finalStatus.Nodes {
			if node.Step.Name == "task2" {
				require.NotEqual(t, core.NodeSucceeded, node.Status, "task2 should not have succeeded")
			}
		}
	})
}

func TestCancellation_ParallelItems(t *testing.T) {
	t.Run("cancelParallelExecutionOnWorkers", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := newTestFixture(t, fmt.Sprintf(`
steps:
  - name: process-items
    action: dag.run
    with:
      dag: child-sleep
    parallel:
      items:
        - "100"
        - "101"
        - "102"
        - "103"
      max_concurrent: 2

---
name: child-sleep
worker_selector:
  type: test-worker
steps:
  - name: sleep
    run: %s
`, test.ShellQuote(test.Sleep(100*time.Second))), withWorkerCount(2), withLabels(map[string]string{"type": "test-worker"}),
			withDAGsDir(tmpDir), withLogPersistence())

		agent := f.dagWrapper.Agent()
		done := make(chan struct{})

		go func() {
			agent.Context = f.coord.Context
			_ = agent.Run(agent.Context)
			close(done)
		}()

		require.Eventually(t, func() bool {
			st, err := f.latestStatus()
			if err != nil || !st.Status.IsActive() {
				return false
			}
			if len(st.Nodes) == 0 {
				return false
			}
			parallelNode := st.Nodes[0]
			return parallelNode.Status == core.NodeRunning
		}, 5*time.Second, 100*time.Millisecond)

		require.Eventually(t, func() bool {
			workerInfo, err := f.coordinatorClient.GetWorkers(f.coord.Context)
			require.NoError(t, err)
			var runningTasks int
			for _, w := range workerInfo {
				runningTasks += len(w.RunningTasks)
			}
			return runningTasks > 0
		}, 5*time.Second, 100*time.Millisecond)

		agent.Signal(f.coord.Context, os.Signal(syscall.SIGINT))

		<-done

		st, err := f.latestStatus()
		require.NoError(t, err)
		require.NotNil(t, st)

		require.GreaterOrEqual(t, len(st.Nodes), 1)
		parallelNode := st.Nodes[0]
		require.Equal(t, "process-items", parallelNode.Step.Name)
		require.Equal(t, core.NodeAborted, parallelNode.Status)
		require.NotEmpty(t, parallelNode.SubRuns)
	})
}

func TestRetry_WithWorkerSelector(t *testing.T) {
	t.Run("retryDispatchesToCoordinator", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: retry-cmd-test
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "task1 executed"
  - name: task2
    run: echo "task2 executed"
    depends: [task1]
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 30*time.Second)
		dagRunID := status.DAGRunID
		f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.retry(dagRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded && status.DAGRunID == dagRunID
		}, distrTestTimeout(25*time.Second), 200*time.Millisecond, "Retry should complete successfully")

		finalStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, finalStatus.Status)
		f.assertAllNodesSucceeded(finalStatus)
	})

	t.Run("retryDispatchesToCoordinator_NoNameField", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "task1 executed"
  - name: task2
    run: echo "task2 executed"
    depends: [task1]
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 30*time.Second)
		dagRunID := status.DAGRunID
		f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.retry(dagRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded && status.DAGRunID == dagRunID
		}, distrTestTimeout(25*time.Second), 200*time.Millisecond, "Retry should complete successfully")

		finalStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, finalStatus.Status)
		f.assertAllNodesSucceeded(finalStatus)
	})
}

func TestRetry_PartialRetry(t *testing.T) {
	t.Run("retryReusesSameRunID", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: partial-retry-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "step1"
  - name: step2
    run: echo "step2"
    depends: [step1]
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 20*time.Second)
		originalRunID := status.DAGRunID
		f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.retry(originalRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded && status.DAGRunID == originalRunID
		}, distrTestTimeout(25*time.Second), 200*time.Millisecond, "Retry should complete with same run ID")

		finalStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, finalStatus.Status)
		require.Equal(t, originalRunID, finalStatus.DAGRunID, "retry should maintain the same run ID")
	})
}

func TestRetry_SharedFSMode(t *testing.T) {
	t.Run("retryWorksWithSharedFSWorker", func(t *testing.T) {
		f := newTestFixture(t, `
name: retry-sharedfs-test
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "sharedfs task1"
`, withWorkerMode(sharedFSMode))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 25*time.Second)
		dagRunID := status.DAGRunID
		f.cleanup()

		ctx, cancel := context.WithTimeout(f.coord.Context, 30*time.Second)
		defer cancel()

		f.schedulerCtx = ctx
		f.schedulerCancel = cancel
		f.startScheduler(30 * time.Second)

		require.NoError(t, f.retry(dagRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded
		}, 25*time.Second, 200*time.Millisecond)
	})

	t.Run("retryWorksWithSharedFSWorker_NoNameField", func(t *testing.T) {
		f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "sharedfs task1"
`, withWorkerMode(sharedFSMode))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 25*time.Second)
		dagRunID := status.DAGRunID
		f.cleanup()

		ctx, cancel := context.WithTimeout(f.coord.Context, 30*time.Second)
		defer cancel()

		f.schedulerCtx = ctx
		f.schedulerCancel = cancel
		f.startScheduler(30 * time.Second)

		require.NoError(t, f.retry(dagRunID))

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded
		}, 25*time.Second, 200*time.Millisecond)
	})
}
