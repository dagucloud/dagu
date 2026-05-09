// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	osrt "runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func workerTaskTimeout() time.Duration {
	if osrt.GOOS == "windows" {
		return 90 * time.Second
	}
	return 30 * time.Second
}

func workerStatusOutputValue(t *testing.T, status *exec.DAGRunStatus, key string) string {
	t.Helper()

	require.NotNil(t, status)
	for _, node := range status.Nodes {
		if node.OutputVariables == nil {
			continue
		}
		value, ok := node.OutputVariables.Load(key)
		if ok {
			result, ok := value.(string)
			require.True(t, ok, "output %q has unexpected type %T", key, value)
			result = strings.TrimPrefix(result, key+"=")
			return result
		}
	}

	t.Fatalf("output %q not found in DAG-run status", key)
	return ""
}

func TestTaskHandler(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	t.Run("HandleQueueDispatch", func(t *testing.T) {
		// This test simulates the queue dispatch scenario:
		// Coordinator first creates a dag-run (during enqueue), then sends OPERATION_RETRY
		// to dispatch it to a worker.
		dagContent := `steps:
  - name: "1"
    run: echo step1
  - name: "2"
    run: echo step2
`
		dag := th.DAG(t, dagContent)

		// First, create an initial dag-run (simulating what coordinator does during enqueue)
		// This creates the status record that retry will use
		spec := th.SubCmdBuilder.Start(dag.DAG, runtime.StartOptions{})
		err := runtime.Start(th.Context, spec)
		require.NoError(t, err)

		// Wait for the initial run to complete
		dag.AssertLatestStatus(t, core.Succeeded)

		// Get the dag-run ID from the completed run
		st, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		dagRunID := st.DAGRunID

		// Create a task with OPERATION_RETRY but no Step (queue dispatch case)
		// This simulates coordinator dispatching a queued task
		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			DagRunId:       dagRunID,
			Target:         dag.Name,
			Definition:     dagContent,
			RootDagRunName: dag.Name,
			RootDagRunId:   dagRunID,
		}

		// Create a context with timeout for the task execution
		taskCtx, cancel := context.WithTimeout(th.Context, workerTaskTimeout())
		defer cancel()

		// Execute the task (retry without step re-runs all steps)
		handler := NewTaskHandler(th.Config)
		err = handler.Handle(taskCtx, task)
		require.NoError(t, err)

		// Verify the DAG ran successfully again
		dag.AssertLatestStatus(t, core.Succeeded)
	})

	t.Run("HandleTaskRetryWithStep", func(t *testing.T) {
		dagContent := `steps:
  - name: "1"
    run: echo step1
  - name: "2"
    run: echo step2
`
		dag := th.DAG(t, dagContent)
		ctx := th.Context
		cli := th.DAGRunMgr

		// First, start a DAG run
		spec := th.SubCmdBuilder.Start(dag.DAG, runtime.StartOptions{})
		err := runtime.Start(th.Context, spec)
		require.NoError(t, err)

		// Wait for the DAG to finish
		dag.AssertLatestStatus(t, core.Succeeded)

		// Get the st to get the dag-run ID
		st, err := cli.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		dagRunID := st.DAGRunID

		// Create a retry task with specific step
		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			DagRunId:       dagRunID,
			Target:         dag.Name,
			Definition:     dagContent,
			RootDagRunName: dag.Name,
			RootDagRunId:   dagRunID,
			Step:           "1",
		}

		// Create a context with timeout for the task execution
		taskCtx, cancel := context.WithTimeout(ctx, workerTaskTimeout())
		defer cancel()

		// Execute the task
		handler := NewTaskHandler(th.Config)
		err = handler.Handle(taskCtx, task)
		require.NoError(t, err)

		// Verify the DAG ran again successfully
		dag.AssertLatestStatus(t, core.Succeeded)
	})

	t.Run("HandleTaskStart", func(t *testing.T) {
		dagContent := `steps:
  - name: "process"
    run: echo processing $1
`
		dag := th.DAG(t, dagContent)
		ctx := th.Context
		cli := th.DAGRunMgr

		// Generate a new dag-run ID
		dagRunID := uuid.Must(uuid.NewV7()).String()

		// Create a start task
		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_START,
			DagRunId:       dagRunID,
			Target:         dag.Name,
			Definition:     dagContent,
			RootDagRunName: dag.Name,
			RootDagRunId:   dagRunID,
			Params:         "param1=value1",
			ScheduleTime:   "2026-03-13T10:00:00Z",
		}

		// Create a context with timeout for the task execution
		taskCtx, cancel := context.WithTimeout(ctx, workerTaskTimeout())
		defer cancel()

		// Execute the task
		handler := NewTaskHandler(th.Config)
		err := handler.Handle(taskCtx, task)
		require.NoError(t, err)

		// Verify the DAG ran successfully
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify the params were passed
		status, err := cli.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, dagRunID, status.DAGRunID)
		require.Equal(t, "param1=value1", status.Params)
		require.Equal(t, "2026-03-13T10:00:00Z", status.ScheduleTime)
	})

	t.Run("HandleTaskInvalidOperation", func(t *testing.T) {
		ctx := th.Context

		// Create a task with invalid operation
		task := &coordinatorv1.Task{
			Operation:  coordinatorv1.Operation_OPERATION_UNSPECIFIED,
			DagRunId:   "test-id",
			Target:     "test-dag",
			Definition: "steps:\n  - name: step1\n    command: echo test\n",
		}

		// Execute the task
		handler := NewTaskHandler(th.Config)
		err := handler.Handle(ctx, task)
		require.Error(t, err)
		require.Contains(t, err.Error(), "operation not specified")
	})

	t.Run("HandleTaskStartPreservesExplicitEnv", func(t *testing.T) {
		t.Setenv("WORKER_TASK_START_ENV", "from-host")

		dagContent := fmt.Sprintf(`env:
  - EXPORTED_SECRET: ${WORKER_TASK_START_ENV}
steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("EXPORTED_SECRET", "WORKER_TASK_START_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		task := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)

		handler := NewTaskHandler(th.Config)
		err := handler.Handle(th.Context, task)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, runID)
		require.NoError(t, err)
		require.NotNil(t, status)
		require.Equal(t, core.Succeeded, status.Status)
		require.Equal(t, "from-host|", workerStatusOutputValue(t, status, "RESULT"))
	})

	t.Run("HandleTaskRetryPreservesExplicitEnv", func(t *testing.T) {
		t.Setenv("WORKER_TASK_RETRY_ENV", "from-host")

		dagContent := fmt.Sprintf(`env:
  - EXPORTED_SECRET: ${WORKER_TASK_RETRY_ENV}
steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("EXPORTED_SECRET", "WORKER_TASK_RETRY_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		handler := NewTaskHandler(th.Config)

		startTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)
		err := handler.Handle(th.Context, startTask)
		require.NoError(t, err)

		initialAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		initialStatus, err := initialAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, "from-host|", workerStatusOutputValue(t, initialStatus, "RESULT"))

		retryTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_RETRY,
			runID,
			runtimeexec.WithPreviousStatus(initialStatus),
		)
		err = handler.Handle(th.Context, retryTask)
		require.NoError(t, err)

		retriedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		require.NotEqual(t, initialAttempt.ID(), retriedAttempt.ID())

		retriedStatus, err := retriedAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, retriedStatus.Status)
		require.Equal(t, "from-host|", workerStatusOutputValue(t, retriedStatus, "RESULT"))
	})

	t.Run("HandleTaskStartPreservesKubernetesDiscoveryEnv", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
		t.Setenv("KUBERNETES_SERVICE_PORT", "443")
		t.Setenv("WORKER_TASK_HOST_ONLY_ENV", "host-only")

		dagContent := fmt.Sprintf(`steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("KUBERNETES_SERVICE_HOST", "KUBERNETES_SERVICE_PORT", "WORKER_TASK_HOST_ONLY_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		task := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)

		handler := NewTaskHandler(th.Config)
		err := handler.Handle(th.Context, task)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, runID)
		require.NoError(t, err)
		require.NotNil(t, status)
		require.Equal(t, core.Succeeded, status.Status)
		require.Equal(t, "10.43.0.1|443|", workerStatusOutputValue(t, status, "RESULT"))
	})

	t.Run("HandleTaskRetryPreservesKubernetesDiscoveryEnv", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
		t.Setenv("KUBERNETES_SERVICE_PORT", "443")
		t.Setenv("WORKER_TASK_HOST_ONLY_ENV", "host-only")

		dagContent := fmt.Sprintf(`steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("KUBERNETES_SERVICE_HOST", "KUBERNETES_SERVICE_PORT", "WORKER_TASK_HOST_ONLY_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		handler := NewTaskHandler(th.Config)

		startTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)
		err := handler.Handle(th.Context, startTask)
		require.NoError(t, err)

		initialAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		initialStatus, err := initialAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, "10.43.0.1|443|", workerStatusOutputValue(t, initialStatus, "RESULT"))

		retryTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_RETRY,
			runID,
			runtimeexec.WithPreviousStatus(initialStatus),
		)
		err = handler.Handle(th.Context, retryTask)
		require.NoError(t, err)

		retriedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		require.NotEqual(t, initialAttempt.ID(), retriedAttempt.ID())

		retriedStatus, err := retriedAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, retriedStatus.Status)
		require.Equal(t, "10.43.0.1|443|", workerStatusOutputValue(t, retriedStatus, "RESULT"))
	})

	t.Run("HandleTaskStartPreservesConfiguredEnvPassthrough", func(t *testing.T) {
		t.Setenv("DAGU_ENV_PASSTHROUGH", "WORKER_TASK_EXACT_ENV")
		t.Setenv("DAGU_ENV_PASSTHROUGH_PREFIXES", "WORKER_TASK_PREFIX_")
		t.Setenv("WORKER_TASK_EXACT_ENV", "exact-value")
		t.Setenv("WORKER_TASK_PREFIX_TOKEN", "prefix-value")
		t.Setenv("WORKER_TASK_HOST_ONLY_ENV", "host-only")
		th := test.Setup(t, test.WithBuiltExecutable())

		dagContent := fmt.Sprintf(`steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("WORKER_TASK_EXACT_ENV", "WORKER_TASK_PREFIX_TOKEN", "WORKER_TASK_HOST_ONLY_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		task := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)

		handler := NewTaskHandler(th.Config)
		err := handler.Handle(th.Context, task)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, runID)
		require.NoError(t, err)
		require.NotNil(t, status)
		require.Equal(t, core.Succeeded, status.Status)
		require.Equal(t, "exact-value|prefix-value|", workerStatusOutputValue(t, status, "RESULT"))
	})

	t.Run("HandleTaskRetryPreservesConfiguredEnvPassthrough", func(t *testing.T) {
		t.Setenv("DAGU_ENV_PASSTHROUGH", "WORKER_TASK_EXACT_ENV")
		t.Setenv("DAGU_ENV_PASSTHROUGH_PREFIXES", "WORKER_TASK_PREFIX_")
		t.Setenv("WORKER_TASK_EXACT_ENV", "exact-value")
		t.Setenv("WORKER_TASK_PREFIX_TOKEN", "prefix-value")
		t.Setenv("WORKER_TASK_HOST_ONLY_ENV", "host-only")
		th := test.Setup(t, test.WithBuiltExecutable())

		dagContent := fmt.Sprintf(`steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("WORKER_TASK_EXACT_ENV", "WORKER_TASK_PREFIX_TOKEN", "WORKER_TASK_HOST_ONLY_ENV"))
		dag := th.DAG(t, dagContent)
		runID := uuid.Must(uuid.NewV7()).String()
		handler := NewTaskHandler(th.Config)

		startTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_START,
			runID,
		)
		err := handler.Handle(th.Context, startTask)
		require.NoError(t, err)

		initialAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		initialStatus, err := initialAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, "exact-value|prefix-value|", workerStatusOutputValue(t, initialStatus, "RESULT"))

		retryTask := runtimeexec.CreateTask(
			dag.Name,
			dagContent,
			coordinatorv1.Operation_OPERATION_RETRY,
			runID,
			runtimeexec.WithPreviousStatus(initialStatus),
		)
		err = handler.Handle(th.Context, retryTask)
		require.NoError(t, err)

		retriedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		require.NotEqual(t, initialAttempt.ID(), retriedAttempt.ID())

		retriedStatus, err := retriedAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, retriedStatus.Status)
		require.Equal(t, "exact-value|prefix-value|", workerStatusOutputValue(t, retriedStatus, "RESULT"))
	})
}

func TestCreateTempDAGFile(t *testing.T) {
	path, err := fileutil.CreateTempDAGFile("worker-dags", "simple", []byte("steps:\n  - name: example\n"))
	require.NoError(t, err)

	t.Cleanup(func() { _ = os.Remove(path) })

	require.FileExists(t, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "steps:\n  - name: example\n", string(data))

	expectedDir := filepath.Join(os.TempDir(), "dagu", "worker-dags") + string(os.PathSeparator)
	require.True(t, strings.HasPrefix(path, expectedDir), "expected %q to start with %q", path, expectedDir)
}

func TestTaskHandlerStartWithDefinition(t *testing.T) {
	if osrt.GOOS == "windows" {
		t.Skip("POSIX shell required for fake executable script")
	}

	tmpDir := t.TempDir()
	argsPath := filepath.Join(tmpDir, "args.txt")
	fakeExec := filepath.Join(tmpDir, "fake-dagu.sh")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsPath + "\n"
	err := os.WriteFile(fakeExec, []byte(script), 0o700)
	require.NoError(t, err)

	cfg := &config.Config{
		Paths: config.PathsConfig{
			Executable: fakeExec,
		},
		Core: config.Core{
			BaseEnv: config.NewBaseEnv(nil),
		},
	}

	handler := NewTaskHandler(cfg)

	originalTarget := "workflow.yaml"
	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		DagRunId:       "run-123",
		Target:         originalTarget,
		Definition:     "steps:\n  - name: example\n    command: echo example\n",
		Params:         "foo=bar",
		RootDagRunName: "root-dag",
		RootDagRunId:   "root-run-123",
	}

	err = handler.Handle(context.Background(), task)
	require.NoError(t, err)

	require.NotEqual(t, originalTarget, task.Target)

	argsData, err := os.ReadFile(argsPath)
	require.NoError(t, err)

	argsLines := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	require.Contains(t, argsLines, "start")
	require.Contains(t, argsLines, "--run-id=run-123")
	require.Contains(t, argsLines, "--root=root-dag:root-run-123")
	require.Contains(t, argsLines, "--name=workflow")
	require.Contains(t, argsLines, task.Target)
	require.Contains(t, argsLines, "--")
	require.Contains(t, argsLines, "foo=bar")
	require.NotContains(t, argsLines, "--name=root-dag")

	_, statErr := os.Stat(task.Target)
	require.True(t, os.IsNotExist(statErr), "temporary DAG file should be removed after execution")
}
