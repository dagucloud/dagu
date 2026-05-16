// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	runtimeagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertEquivalentPath(t *testing.T, expected, actual string) {
	t.Helper()
	require.Equal(t, filepath.Clean(expected), filepath.Clean(filepath.FromSlash(actual)))
}

func dagAgentWithProc(t *testing.T, th test.Helper, dag test.DAG) *test.Agent {
	t.Helper()

	dagRunID, err := th.DAGRunMgr.GenDAGRunID(th.Context)
	require.NoError(t, err)

	compactRunID := strings.ReplaceAll(dagRunID, "-", "")
	if len(compactRunID) > 12 {
		compactRunID = compactRunID[:12]
	}
	attemptID := "attempt-" + compactRunID

	proc, err := th.ProcStore.Acquire(th.Context, dag.ProcGroup(), exec.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         dag.Name,
		DAGRunID:     dagRunID,
		AttemptID:    attemptID,
		RootName:     dag.Name,
		RootDAGRunID: dagRunID,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = proc.Stop(th.Context)
	})

	return dag.Agent(
		test.WithDAGRunID(dagRunID),
		test.WithAgentOptions(runtimeagent.Options{AttemptID: attemptID}),
	)
}

func TestHandlerOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		dagYAML      string
		setupFunc    func(*testing.T, *core.DAG) // Optional: verify DAG parsing
		runFunc      func(*testing.T, context.Context, *test.Agent)
		validateFunc func(*testing.T, *exec.DAGRunStatus)
	}{
		{
			name: "InitHandler_Success",
			dagYAML: `
handler_on:
  init:
    run: "true"

steps:
  - name: step1
    run: "true"
`,
			setupFunc: func(t *testing.T, dag *core.DAG) {
				require.NotNil(t, dag.HandlerOn.Init)
				require.Equal(t, "onInit", dag.HandlerOn.Init.Name)
			},
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.NotNil(t, status.OnInit, "init handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnInit.Status)
			},
		},
		{
			name: "InitHandler_Failure_StopsExecution",
			dagYAML: `
handler_on:
  init:
    run: exit 1
  exit:
    run: "true"

steps:
  - name: step1
    run: "echo should-not-run"
`,
			runFunc: func(_ *testing.T, ctx context.Context, agent *test.Agent) {
				_ = agent.Run(ctx)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				// Init failure causes DAG to be aborted (canceled internally)
				require.Equal(t, core.Aborted, status.Status)
				require.NotNil(t, status.OnInit, "init handler should have been executed")
				require.Equal(t, core.NodeFailed, status.OnInit.Status)

				// Exit handler should have run
				require.NotNil(t, status.OnExit, "exit handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnExit.Status)

				// Steps should not have run
				require.Len(t, status.Nodes, 1)
				require.Equal(t, core.NodeNotStarted, status.Nodes[0].Status)
			},
		},
		{
			name: "InitHandler_PreconditionSkip_StepsRun",
			dagYAML: `
handler_on:
  init:
    run: "echo init-should-not-run"
    preconditions: "false"

steps:
  - name: step1
    run: "true"
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.NotNil(t, status.OnInit, "init handler node should exist")
				require.Equal(t, core.NodeSkipped, status.OnInit.Status)

				// Steps should have run
				require.Equal(t, core.NodeSucceeded, status.Nodes[0].Status)
			},
		},
		{
			name: "InitHandler_DAGPreconditionFails_InitNotRun",
			dagYAML: `
preconditions: "false"

handler_on:
  init:
    run: "echo init-should-not-run"

steps:
  - name: step1
    run: "true"
`,
			runFunc: func(_ *testing.T, ctx context.Context, agent *test.Agent) {
				_ = agent.Run(ctx)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Aborted, status.Status)

				// Init handler should not have run (DAG precondition failed first)
				// OnInit may be nil or NotStarted when the runner doesn't execute it
				if status.OnInit != nil {
					require.Equal(t, core.NodeNotStarted, status.OnInit.Status)
				}

				// Steps should not have run - they remain in NotStarted state when preconditions fail
				// and the runner immediately exits
				require.NotEmpty(t, status.Nodes)
				// The node could be NotStarted or Aborted depending on timing
				nodeStatus := status.Nodes[0].Status
				require.True(t, nodeStatus == core.NodeNotStarted || nodeStatus == core.NodeAborted,
					"expected NotStarted or Aborted, got %v", nodeStatus)
			},
		},
		{
			name: "FailureHandler",
			dagYAML: `
retry_policy:
  limit: 0

handler_on:
  failure:
    run: "true"

steps:
  - name: failing-step
    run: exit 1
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunError(t)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Failed, status.Status)
				require.NotNil(t, status.OnFailure, "failure handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnFailure.Status)
			},
		},
		{
			name: "SuccessHandler",
			dagYAML: `
handler_on:
  success:
    run: "true"

steps:
  - name: passing-step
    run: "true"
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.NotNil(t, status.OnSuccess, "success handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnSuccess.Status)
			},
		},
		{
			name: "ExitHandler",
			dagYAML: `
handler_on:
  exit:
    run: "true"

steps:
  - name: passing-step
    run: "true"
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.NotNil(t, status.OnExit, "exit handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnExit.Status)
			},
		},
		{
			name: "WaitHandler_ExecutedOnWaitStatus",
			dagYAML: `
handler_on:
  wait:
    run: "true"

steps:
  - name: wait-step
    run: "true"
    approval: {}
`,
			setupFunc: func(t *testing.T, dag *core.DAG) {
				require.NotNil(t, dag.HandlerOn.Wait)
				require.Equal(t, "onWait", dag.HandlerOn.Wait.Name)
			},
			runFunc: func(_ *testing.T, ctx context.Context, agent *test.Agent) {
				_ = agent.Run(ctx)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				require.Equal(t, core.Waiting, status.Status)
				require.NotNil(t, status.OnWait, "wait handler should have been executed")
				require.Equal(t, core.NodeSucceeded, status.OnWait.Status)
			},
		},
		{
			name: "WaitHandler_FailureDoesNotBlockWaitStatus",
			dagYAML: `
handler_on:
  wait:
    run: exit 1

steps:
  - name: wait-step
    run: "true"
    approval: {}
`,
			runFunc: func(_ *testing.T, ctx context.Context, agent *test.Agent) {
				_ = agent.Run(ctx)
			},
			validateFunc: func(t *testing.T, status *exec.DAGRunStatus) {
				// DAG should still be in Wait status even if handler failed
				require.Equal(t, core.Waiting, status.Status)
				require.NotNil(t, status.OnWait, "wait handler should have been executed")
				require.Equal(t, core.NodeFailed, status.OnWait.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := test.Setup(t)
			dag := th.DAG(t, tt.dagYAML)

			if tt.setupFunc != nil {
				tt.setupFunc(t, dag.DAG)
			}

			agent := dag.Agent()
			tt.runFunc(t, th.Context, agent)

			tt.validateFunc(t, new(agent.Status(th.Context)))
		})
	}
}

// TestHandlerOn_Abort tests the abort handler which requires special async handling
func TestHandlerOn_Abort(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	tmpDir := t.TempDir()
	releaseFile := filepath.Join(tmpDir, "abort.release")
	startedFile := filepath.Join(tmpDir, "abort.started")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})
	dag := th.DAG(t, `
handler_on:
  abort:
    run: "true"

steps:
  - name: long-running
    run: |
`+indentTestScript(writeFileCommand(startedFile), 6)+`
`+indentTestScript(waitForFileCommand(releaseFile), 6)+`
`)

	// Verify parsing: abort field maps to the canonical abort handler
	require.NotNil(t, dag.HandlerOn.Abort)
	require.Equal(t, "onAbort", dag.HandlerOn.Abort.Name)

	dagAgent := dagAgentWithProc(t, th, dag)

	done := make(chan struct{})
	go func() {
		_ = dagAgent.Run(th.Context)
		close(done)
	}()

	waitForTestFile(t, startedFile, intgTestTimeout(10*time.Second))

	// Abort the DAG
	dagAgent.Abort()

	// Wait for completion
	select {
	case <-done:
	case <-time.After(intgTestTimeout(30 * time.Second)):
		t.Fatal("timed out waiting for aborted DAG run to finish")
	}

	// Verify the abort handler was executed
	status := dagAgent.Status(th.Context)
	require.Equal(t, core.Aborted, status.Status)
	require.NotNil(t, status.OnAbort, "abort handler should have been executed")
	require.Equal(t, core.NodeSucceeded, status.OnAbort.Status)
}

// TestHandlerOn_EnvironmentVariables tests that special environment variables
// are accessible from each handler type.
//
// Environment variables availability by handler:
//
// | Variable                   | Init   | Success   | Failure    | Abort   | Exit      |
// |----------------------------|--------|-----------|------------|---------|-----------|
// | DAG_NAME                   | ✓      | ✓         | ✓          | ✓       | ✓         |
// | DAG_RUN_ID                 | ✓      | ✓         | ✓          | ✓       | ✓         |
// | DAG_RUN_LOG_FILE           | ✓      | ✓         | ✓          | ✓       | ✓         |
// | DAG_RUN_STEP_NAME          | onInit | onSuccess | onFailure  | onAbort| onExit    |
// | DAG_RUN_STATUS             | running| succeeded | failed     | aborted | succeeded/failed |
// | DAG_RUN_STEP_STDOUT_FILE   | ✗      | ✗         | ✗          | ✗       | ✗         |
// | DAG_RUN_STEP_STDERR_FILE   | ✗      | ✗         | ✗          | ✗       | ✗         |
//
// Note: DAG_RUN_STATUS in init handler is "running" because the DAG run has started
// but steps haven't executed yet. DAG_RUN_STEP_STDOUT_FILE and DAG_RUN_STEP_STDERR_FILE
// are not set for handlers because node.SetupEnv is not called during handler execution.
func TestHandlerOn_EnvironmentVariables(t *testing.T) {
	t.Parallel()

	// Helper to extract value from "KEY=value" format
	extractValue := func(output string) string {
		if _, after, ok := strings.Cut(output, "="); ok {
			return after
		}
		return output
	}

	t.Run("InitHandler_BaseEnvVars", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// Test that basic env vars (DAG_NAME, DAG_RUN_ID, DAG_RUN_LOG_FILE, DAG_RUN_STEP_NAME)
		// are available in the init handler
		dag := th.DAG(t, `
handler_on:
  init:
    run: |
      echo "name:${DAG_NAME}|runid:${DAG_RUN_ID}|logfile:${DAG_RUN_LOG_FILE}|stepname:${DAG_RUN_STEP_NAME}"
    output: INIT_ENV_OUTPUT

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnInit, "init handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnInit.Status)
		require.NotNil(t, status.OnInit.OutputVariables, "init handler should have output variables")

		output, ok := status.OnInit.OutputVariables.Load("INIT_ENV_OUTPUT")
		require.True(t, ok, "INIT_ENV_OUTPUT should be set")

		outputStr := extractValue(output.(string))

		// Verify DAG_NAME is set and non-empty
		assert.Contains(t, outputStr, "name:", "output should contain name prefix")
		assert.NotContains(t, outputStr, "name:|", "DAG_NAME should not be empty")

		// Verify DAG_RUN_ID is set (UUID format)
		assert.Contains(t, outputStr, "runid:", "output should contain runid prefix")
		assert.NotContains(t, outputStr, "runid:|", "DAG_RUN_ID should not be empty")

		// Verify DAG_RUN_LOG_FILE is set and contains .log
		assert.Contains(t, outputStr, "logfile:", "output should contain logfile prefix")
		assert.Contains(t, outputStr, ".log", "DAG_RUN_LOG_FILE should contain .log")

		// Verify DAG_RUN_STEP_NAME is set to "onInit"
		assert.Contains(t, outputStr, "stepname:onInit", "DAG_RUN_STEP_NAME should be 'onInit'")
	})

	t.Run("InitHandler_DAGRunStatus_IsRunning", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// DAG_RUN_STATUS in init handler is "running" because the DAG run has started
		// but steps haven't completed yet. This value is technically correct but not
		// as useful as having the final status (which isn't known yet).
		dag := th.DAG(t, `
handler_on:
  init:
    run: echo "${DAG_RUN_STATUS}"
    output: INIT_STATUS

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnInit)
		require.NotNil(t, status.OnInit.OutputVariables)

		output, ok := status.OnInit.OutputVariables.Load("INIT_STATUS")
		require.True(t, ok)

		// DAG_RUN_STATUS is "running" for init handler because steps haven't completed
		outputStr := extractValue(output.(string))
		assert.Equal(t, "running", outputStr, "DAG_RUN_STATUS should be 'running' in init handler")
	})

	t.Run("SuccessHandler_AllEnvVars", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		dag := th.DAG(t, `
handler_on:
  success:
    run: |
      echo "name:${DAG_NAME}|status:${DAG_RUN_STATUS}|stepname:${DAG_RUN_STEP_NAME}"
    output: SUCCESS_ENV_OUTPUT

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnSuccess, "success handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnSuccess.Status)
		require.NotNil(t, status.OnSuccess.OutputVariables)

		output, ok := status.OnSuccess.OutputVariables.Load("SUCCESS_ENV_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_NAME is set
		assert.NotContains(t, outputStr, "name:|", "DAG_NAME should not be empty")

		// Verify DAG_RUN_STATUS is "succeeded"
		assert.Contains(t, outputStr, "status:succeeded", "DAG_RUN_STATUS should be 'succeeded'")

		// Verify DAG_RUN_STEP_NAME is "onSuccess"
		assert.Contains(t, outputStr, "stepname:onSuccess", "DAG_RUN_STEP_NAME should be 'onSuccess'")
	})

	t.Run("FailureHandler_AllEnvVars", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		dag := th.DAG(t, `
retry_policy:
  limit: 0

handler_on:
  failure:
    run: |
      echo "name:${DAG_NAME}|status:${DAG_RUN_STATUS}|stepname:${DAG_RUN_STEP_NAME}"
    output: FAILURE_ENV_OUTPUT

steps:
  - name: failing-step
    run: exit 1
`)
		agent := dag.Agent()
		agent.RunError(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnFailure, "failure handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnFailure.Status)
		require.NotNil(t, status.OnFailure.OutputVariables)

		output, ok := status.OnFailure.OutputVariables.Load("FAILURE_ENV_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_NAME is set
		assert.NotContains(t, outputStr, "name:|", "DAG_NAME should not be empty")

		// Verify DAG_RUN_STATUS is "failed"
		assert.Contains(t, outputStr, "status:failed", "DAG_RUN_STATUS should be 'failed'")

		// Verify DAG_RUN_STEP_NAME is "onFailure"
		assert.Contains(t, outputStr, "stepname:onFailure", "DAG_RUN_STEP_NAME should be 'onFailure'")
	})

	t.Run("ExitHandler_AllEnvVars_OnSuccess", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		dag := th.DAG(t, `
handler_on:
  exit:
    run: |
      echo "name:${DAG_NAME}|status:${DAG_RUN_STATUS}|stepname:${DAG_RUN_STEP_NAME}"
    output: EXIT_ENV_OUTPUT

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnExit, "exit handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnExit.Status)
		require.NotNil(t, status.OnExit.OutputVariables)

		output, ok := status.OnExit.OutputVariables.Load("EXIT_ENV_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_NAME is set
		assert.NotContains(t, outputStr, "name:|", "DAG_NAME should not be empty")

		// Verify DAG_RUN_STATUS is "succeeded" (exit runs after success)
		assert.Contains(t, outputStr, "status:succeeded", "DAG_RUN_STATUS should be 'succeeded'")

		// Verify DAG_RUN_STEP_NAME is "onExit"
		assert.Contains(t, outputStr, "stepname:onExit", "DAG_RUN_STEP_NAME should be 'onExit'")
	})

	t.Run("ExitHandler_AllEnvVars_OnFailure", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		dag := th.DAG(t, `
handler_on:
  exit:
    run: |
      echo "status:${DAG_RUN_STATUS}"
    output: EXIT_ENV_OUTPUT

steps:
  - name: failing-step
    run: exit 1
`)
		agent := dag.Agent()
		agent.RunError(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnExit, "exit handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnExit.Status)
		require.NotNil(t, status.OnExit.OutputVariables)

		output, ok := status.OnExit.OutputVariables.Load("EXIT_ENV_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_RUN_STATUS is "failed" (exit runs after failure)
		assert.Contains(t, outputStr, "status:failed", "DAG_RUN_STATUS should be 'failed'")
	})

	t.Run("AbortHandler_AllEnvVars", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "abort-env.release")
		startedFile := filepath.Join(tmpDir, "abort-env.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})

		dag := th.DAG(t, `
handler_on:
  abort:
    run: |
      echo "name:${DAG_NAME}|status:${DAG_RUN_STATUS}|stepname:${DAG_RUN_STEP_NAME}"
    output: ABORT_ENV_OUTPUT

steps:
  - name: long-running
    run: |
`+indentTestScript(writeFileCommand(startedFile), 6)+`
`+indentTestScript(waitForFileCommand(releaseFile), 6)+`
`)
		dagAgent := dagAgentWithProc(t, th, dag)

		done := make(chan struct{})
		go func() {
			_ = dagAgent.Run(th.Context)
			close(done)
		}()

		waitForTestFile(t, startedFile, intgTestTimeout(10*time.Second))
		dagAgent.Abort()
		select {
		case <-done:
		case <-time.After(intgTestTimeout(30 * time.Second)):
			t.Fatal("timed out waiting for aborted DAG run to finish")
		}

		status := dagAgent.Status(th.Context)
		require.NotNil(t, status.OnAbort, "abort handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnAbort.Status)
		require.NotNil(t, status.OnAbort.OutputVariables)

		output, ok := status.OnAbort.OutputVariables.Load("ABORT_ENV_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_NAME is set
		assert.NotContains(t, outputStr, "name:|", "DAG_NAME should not be empty")

		// Verify DAG_RUN_STATUS is "aborted"
		assert.Contains(t, outputStr, "status:aborted", "DAG_RUN_STATUS should be 'aborted'")

		// Verify DAG_RUN_STEP_NAME is "onAbort"
		assert.Contains(t, outputStr, "stepname:onAbort", "DAG_RUN_STEP_NAME should be 'onAbort'")
	})

	t.Run("StepOutputVars_NotAvailableInHandlers", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// DAG_RUN_STEP_STDOUT_FILE and DAG_RUN_STEP_STDERR_FILE are NOT set
		// for handlers because node.SetupEnv is not called for handler execution
		dag := th.DAG(t, `
handler_on:
  success:
    run: |
      echo "stdout:${DAG_RUN_STEP_STDOUT_FILE:-UNSET}|stderr:${DAG_RUN_STEP_STDERR_FILE:-UNSET}"
    output: HANDLER_STEP_FILES

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnSuccess)
		require.NotNil(t, status.OnSuccess.OutputVariables)

		output, ok := status.OnSuccess.OutputVariables.Load("HANDLER_STEP_FILES")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Handler steps do not get step-scoped stdout/stderr vars.
		assert.Contains(t, outputStr, "stdout:", "stdout prefix should be present")
		assert.Contains(t, outputStr, "stderr:", "stderr prefix should be present")
		assert.NotContains(t, outputStr, ".out", "DAG_RUN_STEP_STDOUT_FILE should not be set in handler")
		assert.NotContains(t, outputStr, ".err", "DAG_RUN_STEP_STDERR_FILE should not be set in handler")
	})

	t.Run("Handlers_CanAccessStepOutputVariables", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// Handlers can access output variables from steps that have completed
		dag := th.DAG(t, `
handler_on:
  success:
    run: |
      echo "step_output:${STEP_OUTPUT}"
    output: SUCCESS_WITH_STEP_OUTPUT

steps:
  - name: producer
    run: echo "produced_value"
    output: STEP_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnSuccess)
		require.NotNil(t, status.OnSuccess.OutputVariables)

		output, ok := status.OnSuccess.OutputVariables.Load("SUCCESS_WITH_STEP_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Success handler should be able to access step output
		assert.Contains(t, outputStr, "step_output:produced_value", "handler should access step output")
	})

	t.Run("InitHandler_CannotAccessStepOutputVariables", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		initCommand := `echo "step_output:${STEP_OUTPUT:-NOT_YET_AVAILABLE}"`
		if runtime.GOOS == "windows" {
			initCommand = `
if ([string]::IsNullOrEmpty($env:STEP_OUTPUT)) {
  Write-Output "step_output:NOT_YET_AVAILABLE"
} else {
  Write-Output ("step_output:{0}" -f $env:STEP_OUTPUT)
}
`
		}

		// Init handler runs BEFORE steps, so it cannot access step outputs
		dag := th.DAG(t, `
handler_on:
  init:
    run: |
`+indentTestScript(initCommand, 6)+`
    output: INIT_STEP_ACCESS

steps:
  - name: producer
    run: echo "produced_value"
    output: STEP_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnInit)
		require.NotNil(t, status.OnInit.OutputVariables)

		output, ok := status.OnInit.OutputVariables.Load("INIT_STEP_ACCESS")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Init handler runs before any step output exists, so shell fallback is used.
		assert.Equal(t, "step_output:NOT_YET_AVAILABLE", outputStr)
	})

	t.Run("WaitHandler_DAG_WAITING_STEPS_EnvVar", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// DAG_WAITING_STEPS should contain comma-separated list of waiting step names
		dag := th.DAG(t, `
type: graph
handler_on:
  wait:
    run: |
      echo "waiting_steps:${DAG_WAITING_STEPS}"
    output: WAIT_HANDLER_OUTPUT

steps:
  - name: first-step
    run: "true"

  - name: wait-step
    run: "true"
    approval: {}
    depends:
      - first-step
`)
		agent := dag.Agent()
		_ = agent.Run(th.Context)

		status := agent.Status(th.Context)
		require.Equal(t, core.Waiting, status.Status)
		require.NotNil(t, status.OnWait, "wait handler should have been executed")
		require.Equal(t, core.NodeSucceeded, status.OnWait.Status)
		require.NotNil(t, status.OnWait.OutputVariables)

		output, ok := status.OnWait.OutputVariables.Load("WAIT_HANDLER_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_WAITING_STEPS contains the wait step name
		assert.Contains(t, outputStr, "waiting_steps:wait-step",
			"DAG_WAITING_STEPS should contain 'wait-step'")
	})

	t.Run("WaitHandler_EnvVarFormat", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// Verify DAG_WAITING_STEPS format is correct (comma-separated list)
		// This test has a step with a hyphenated name to verify proper formatting
		dag := th.DAG(t, `
type: graph
handler_on:
  wait:
    run: |
      echo "waiting_steps:${DAG_WAITING_STEPS}"
    output: WAIT_HANDLER_OUTPUT

steps:
  - name: setup-step
    run: "true"

  - name: approval-gate
    run: "true"
    approval: {}
    depends:
      - setup-step
`)
		agent := dag.Agent()
		_ = agent.Run(th.Context)

		status := agent.Status(th.Context)
		require.Equal(t, core.Waiting, status.Status)
		require.NotNil(t, status.OnWait)
		require.NotNil(t, status.OnWait.OutputVariables)

		output, ok := status.OnWait.OutputVariables.Load("WAIT_HANDLER_OUTPUT")
		require.True(t, ok)

		outputStr := extractValue(output.(string))

		// Verify DAG_WAITING_STEPS contains the wait step name with proper formatting
		assert.Contains(t, outputStr, "waiting_steps:approval-gate",
			"DAG_WAITING_STEPS should contain 'approval-gate' with correct format")
	})

	t.Run("SuccessHandler_StdoutPathExpandsDAGRunStatus", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		tempDir := t.TempDir()
		stdoutPath := filepath.Join(tempDir, "handler_${DAG_RUN_STATUS}.log")
		stdoutPathForYAML := filepath.ToSlash(stdoutPath)

		dag := th.DAG(t, `
handler_on:
  success:
    stdout: "`+stdoutPathForYAML+`"
    run: echo "handler ran"

steps:
  - name: step1
    run: "true"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.NotNil(t, status.OnSuccess)
		assertEquivalentPath(t, filepath.Join(tempDir, "handler_succeeded.log"), status.OnSuccess.Step.Stdout)

		content, err := os.ReadFile(status.OnSuccess.Step.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(content), "handler ran")
	})

	t.Run("WaitHandler_StdoutPathExpandsWaitingSteps", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		tempDir := t.TempDir()
		stdoutPath := filepath.Join(tempDir, "wait_${DAG_WAITING_STEPS}.log")
		stdoutPathForYAML := filepath.ToSlash(stdoutPath)

		dag := th.DAG(t, `
type: graph
handler_on:
  wait:
    stdout: "`+stdoutPathForYAML+`"
    run: echo "waiting handler"

steps:
  - name: setup-step
    run: "true"

  - name: approval-gate
    run: "true"
    approval: {}
    depends:
      - setup-step
`)
		agent := dag.Agent()
		_ = agent.Run(th.Context)

		status := agent.Status(th.Context)
		require.Equal(t, core.Waiting, status.Status)
		require.NotNil(t, status.OnWait)
		assertEquivalentPath(t, filepath.Join(tempDir, "wait_approval-gate.log"), status.OnWait.Step.Stdout)

		content, err := os.ReadFile(status.OnWait.Step.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(content), "waiting handler")
	})
}
