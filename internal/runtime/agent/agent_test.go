// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/sock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filesecret"
	runtimepkg "github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"

	"github.com/stretchr/testify/require"
)

func agentCommandEntry(command string) core.CommandEntry {
	cmd, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		panic(fmt.Errorf("failed to parse command %q: %w", command, err))
	}
	return core.CommandEntry{
		Command:     cmd,
		Args:        args,
		CmdWithArgs: command,
	}
}

func setAllAgentStepCommands(dag *core.DAG, command string) {
	entry := agentCommandEntry(command)
	for i := range dag.Steps {
		dag.Steps[i].Commands = []core.CommandEntry{entry}
	}
}

func waitForFileScript(path string, pollInterval time.Duration) string {
	if runtime.GOOS == "windows" {
		millis := pollInterval.Milliseconds()
		if millis <= 0 {
			millis = 1
		}
		return fmt.Sprintf(`
while (-not (Test-Path %s)) {
  Start-Sleep -Milliseconds %d
}
`, test.PowerShellQuote(path), millis)
	}
	return fmt.Sprintf(`
while [ ! -f %s ]; do
  %s
done
`, test.PosixQuote(path), test.Sleep(pollInterval))
}

func signalFileThenWaitScript(signalPath, waitPath string, pollInterval time.Duration) string {
	return fmt.Sprintf("%s\n%s", writeFileCommand(signalPath, "started"), waitForFileScript(waitPath, pollInterval))
}

func writeFileCommand(path, content string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", test.PowerShellQuote(path), test.PowerShellQuote(content))
	}
	return fmt.Sprintf("printf '%%s' %s > %s", test.PosixQuote(content), test.PosixQuote(path))
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, timeout, 50*time.Millisecond)
}

func waitForCancel(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		require.FailNow(t, "timed out waiting for DAG cancellation")
	}
}

func agentRunStartTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 30 * time.Second
	}
	return 5 * time.Second
}

func pwdCommand() string {
	return test.ForOS("pwd", "(Get-Location).Path")
}

func TestAgent_Run(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Parallel()
	}

	t.Run("RunDAG", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent()

		dag.AssertLatestStatus(t, core.NotStarted)

		runDone := make(chan error, 1)
		go func() {
			runDone <- dagAgent.Run(th.Context)
		}()

		runTimeout := 10 * time.Second
		if runtime.GOOS == "windows" {
			runTimeout = 3 * time.Minute
		}

		select {
		case err := <-runDone:
			require.NoError(t, err)
		case <-time.After(runTimeout):
			t.Fatalf("timed out waiting for DAG run to finish after %s", runTimeout)
		}

		dag.AssertLatestStatus(t, core.Succeeded)
	})
	t.Run("DeleteOldHistory", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent()

		// Create a history file by running a DAG
		dagAgent.RunSuccess(t)
		dag.AssertDAGRunCount(t, 1)

		// Set the retention days to 0 (delete all history files except the latest one)
		dag.HistRetentionDays = 0

		// Run the DAG again
		dagAgent = dag.Agent()
		dagAgent.RunSuccess(t)

		// Check if only the latest history file exists
		dag.AssertDAGRunCount(t, 1)
	})
	t.Run("DeleteOldHistoryByRuns", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)

		dag.HistRetentionRuns = 2
		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 1)

		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 2)

		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 2)
	})
	t.Run("AlreadyRunning", func(t *testing.T) {
		th := test.Setup(t)
		runDir := t.TempDir()
		startedFile := filepath.Join(runDir, "started")
		releaseFile := filepath.Join(runDir, "release")
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: wait-until-released
    run: %q
`, signalFileThenWaitScript(startedFile, releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(test.WithDAGRunID("test-dag-run"))
		runDone := false
		runErr := make(chan error, 1)

		go func() {
			// Run the DAG in the background so that it is running
			runErr <- dagAgent.Run(dagAgent.Context)
		}()
		t.Cleanup(func() {
			if runDone {
				return
			}
			_ = os.WriteFile(releaseFile, []byte("cleanup"), 0600)
			select {
			case <-runErr:
			case <-time.After(5 * time.Second):
				dagAgent.Abort()
			}
		})

		require.Eventually(t, func() bool {
			if _, err := os.Stat(startedFile); err != nil {
				if !os.IsNotExist(err) {
					require.NoError(t, err, "failed to stat started marker")
				}
				return false
			}
			return th.DAGRunMgr.IsRunning(context.Background(), dag.DAG, "test-dag-run")
		}, agentRunStartTimeout(), 50*time.Millisecond, "DAG should be running after the blocking step starts")

		alreadyRunningAgent := dag.Agent(test.WithDAGRunID("test-dag-run"))
		err := alreadyRunningAgent.Run(alreadyRunningAgent.Context)
		require.ErrorContains(t, err, "already running")

		require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))

		// Wait for the DAG to finish
		select {
		case err := <-runErr:
			runDone = true
			require.NoError(t, err, "failed to run agent")
		case <-time.After(5 * time.Second):
			require.Fail(t, "DAG did not finish after release")
		}

		status := dagAgent.Status(context.Background())
		st := status.Status
		require.Equal(t, core.Succeeded.String(), st.String(), "expected status %q, got %q", core.Succeeded, st)
		for _, node := range status.Nodes {
			if node.Status == core.NodeSkipped || node.Status == core.NodeSucceeded {
				continue
			}
			t.Errorf("expected node %q to be in success state, got %q", node.Step.Name, node.Status.String())
		}
	})
	t.Run("PreConditionNotMet", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
  - %q
`, "exit 0", "exit 0"))

		// Set a precondition that always fails
		dag.Preconditions = []*core.Condition{
			{Condition: "`" + test.Output("1") + "`", Expected: "0"},
		}

		dagAgent := dag.Agent()
		dagAgent.RunCancel(t)

		// Check if all nodes are not executed
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Aborted.String(), dagRunStatus.Status.String())
		require.Equal(t, core.NodeNotStarted.String(), dagRunStatus.Nodes[0].Status.String())
		require.Equal(t, core.NodeNotStarted.String(), dagRunStatus.Nodes[1].Status.String())
	})
	t.Run("FinishWithError", func(t *testing.T) {
		th := test.Setup(t)
		errDAG := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, "exit 1"))
		dagAgent := errDAG.Agent()
		dagAgent.RunError(t)

		// Check if the status is saved correctly
		require.Equal(t, core.Failed, dagAgent.Status(th.Context).Status)
	})
	t.Run("InitFailurePersistsFinishedAt", func(t *testing.T) {
		th := test.Setup(t)
		blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
		require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0600))

		dag := th.DAG(t, fmt.Sprintf(`working_dir: %q
steps:
  - run: echo hi
`, blockingFile+string(os.PathSeparator)+"subdir"))
		dagAgent := dag.Agent()

		err := dagAgent.Run(th.Context)
		require.ErrorContains(t, err, "failed to create working directory")

		latest, readErr := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, readErr)
		require.Equal(t, core.Failed, latest.Status)
		require.NotEmpty(t, latest.FinishedAt)
	})
	t.Run("UnsupportedSocketTransportContinuesRun", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			SocketServerFactory: fakeSocketServerFactory(
				fmt.Errorf("%w: synthetic unsupported transport", sock.ErrUnsupported),
			),
		}))

		dagAgent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
	})
	t.Run("UnsupportedSocketTransportCanStopWithAbortFlag", func(t *testing.T) {
		th := test.Setup(t)
		runDir := t.TempDir()
		startedFile := filepath.Join(runDir, "started")
		releaseFile := filepath.Join(runDir, "release")
		dagRunID := "test-dag-run-no-socket-stop"
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("cleanup"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, signalFileThenWaitScript(startedFile, releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(
			test.WithDAGRunID(dagRunID),
			test.WithAgentOptions(agent.Options{
				SocketServerFactory: fakeSocketServerFactory(
					fmt.Errorf("%w: synthetic unsupported transport", sock.ErrUnsupported),
				),
			}),
		)
		runErr := make(chan error, 1)
		go func() {
			runErr <- dagAgent.Run(th.Context)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)
		require.Eventually(t, func() bool {
			status, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, dagRunID)
			return err == nil && status != nil && status.Status == core.Running
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, th.DAGRunMgr.Stop(th.Context, dag.DAG, dagRunID))

		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			require.FailNow(t, "timed out waiting for DAG run to stop via abort flag")
		}
		dag.AssertLatestStatus(t, core.Aborted)
	})
	t.Run("SocketStartupFailureRemainsFatal", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			SocketServerFactory: fakeSocketServerFactory(errors.New("synthetic bind failure")),
		}))

		err := dagAgent.Run(th.Context)
		require.ErrorContains(t, err, "failed to start the unix socket server")
		require.ErrorContains(t, err, "synthetic bind failure")
	})
	t.Run("FailureHandlerRunsInline", func(t *testing.T) {
		th := test.Setup(t)
		marker := filepath.Join(t.TempDir(), "failure-marker")
		dag := th.DAG(t, fmt.Sprintf(`handler_on:
  failure:
    run: %q
steps:
  - %q
`, writeFileCommand(marker, "failed"), "exit 1"))
		dagAgent := dag.Agent()
		dagAgent.RunError(t)

		status := dagAgent.Status(th.Context)
		require.Equal(t, core.Failed, status.Status)
		require.NotNil(t, status.OnFailure)
		require.Equal(t, core.NodeSucceeded, status.OnFailure.Status)
		require.NotEmpty(t, status.StartedAt)
		require.NotEmpty(t, status.FinishedAt)

		data, err := os.ReadFile(marker)
		require.NoError(t, err)
		require.Equal(t, "failed", string(data))
	})
	t.Run("FinishWithTimeout", func(t *testing.T) {
		th := test.Setup(t)
		timeoutDAG := th.DAG(t, fmt.Sprintf(`timeout_sec: 2
steps:
  - %q
  - %q
`, test.Sleep(time.Second), test.Sleep(2*time.Second)))
		dagAgent := timeoutDAG.Agent()
		dagAgent.RunError(t)

		// Check if the status is saved correctly
		require.Equal(t, core.Failed, dagAgent.Status(th.Context).Status)
	})
	t.Run("ReceiveSignal", func(t *testing.T) {
		th := test.Setup(t)
		releaseFile := filepath.Join(t.TempDir(), "release")
		dagRunID := "test-dag-run-receive-signal"
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
`, waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(test.WithDAGRunID(dagRunID))
		done := make(chan struct{})

		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		require.Eventually(t, func() bool {
			status, err := th.DAGRunMgr.GetCurrentStatus(context.Background(), dag.DAG, dagRunID)
			if err != nil || status == nil || status.Status != core.Running {
				return false
			}
			return th.DAGRunMgr.IsRunning(context.Background(), dag.DAG, dagRunID)
		}, 2*time.Minute, 250*time.Millisecond, "expected DAG to reach running state before abort")

		// send a signal to cancel the DAG
		dagAgent.Abort()

		waitForCancel(t, done, 30*time.Second)

		// wait for the DAG to be canceled
		dag.AssertLatestStatus(t, core.Aborted)
	})
	t.Run("ExitHandler", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`handler_on:
  exit:
    run: %q
steps:
  - %q
  - %q
`, "exit 0", "exit 0", "exit 0"))
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Check if the DAG is executed successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Succeeded.String(), dagRunStatus.Status.String())
		for _, s := range dagRunStatus.Nodes {
			require.Equal(t, core.NodeSucceeded.String(), s.Status.String())
		}

		// Check if the exit handler is executed
		require.Equal(t, core.NodeSucceeded.String(), dagRunStatus.OnExit.Status.String())
	})
}

func TestAgent_WorkingDirExpansion(t *testing.T) {
	t.Run("WorkingDirWithEnvVar", func(t *testing.T) {
		th := test.Setup(t)
		// Set up a temp directory and env var
		tempDir := t.TempDir()
		t.Setenv("TEST_WORK_DIR", tempDir)

		// Create DAG with WorkingDir using env var
		dag := th.DAG(t, `working_dir: $TEST_WORK_DIR
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Succeeded.String(), dagRunStatus.Status.String())
	})

	t.Run("WorkingDirWithDAGEnvVar", func(t *testing.T) {
		th := test.Setup(t)
		tempDir := t.TempDir()
		origWD, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = os.Chdir(origWD)
		})

		// Create DAG with WorkingDir using DAG-defined env var
		dag := th.DAG(t, `env:
  - CUSTOM_DIR=`+tempDir+`
working_dir: $CUSTOM_DIR
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Succeeded.String(), dagRunStatus.Status.String())
	})

	t.Run("WorkingDirWithTildeExpansion", func(t *testing.T) {
		th := test.Setup(t)

		// Create DAG with WorkingDir using tilde
		dag := th.DAG(t, `working_dir: ~
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Succeeded.String(), dagRunStatus.Status.String())
	})
}

func TestAgent_DryRun(t *testing.T) {
	t.Run("DryRun", func(t *testing.T) {
		th := test.Setup(t)

		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
`, "exit 0"))
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{Dry: true}))

		dagAgent.RunSuccess(t)

		curStatus := dagAgent.Status(th.Context)
		require.Equal(t, core.Succeeded, curStatus.Status)

		// Check if the status is not saved
		dag.AssertDAGRunCount(t, 0)
	})
}

func TestAgent_Retry(t *testing.T) {
	t.Parallel()

	t.Run("RetryDAG", func(t *testing.T) {
		th := test.Setup(t)
		// retry DAG that fails
		dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: "1"
    run: %q
  - name: "2"
    run: %q
    continue_on:
      failure: true
    depends: ["1"]
  - name: "3"
    run: %q
    depends: ["2"]
  - name: "4"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    continue_on:
      skipped: true
  - name: "5"
    run: %q
    depends: ["4"]
  - name: "6"
    run: %q
    depends: ["5"]
  - name: "7"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    depends: ["6"]
    continue_on:
      skipped: true
  - name: "8"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
  - name: "9"
    run: %q
`, "exit 0", "exit 1", "exit 0", "exit 0", "exit 1", "exit 0", "exit 0", "exit 0", "exit 1"))
		dagAgent := dag.Agent()

		dagAgent.RunError(t)
		require.Equal(t, 0, dagAgent.Status(th.Context).AutoRetryCount)

		// Modify the DAG to make it successful
		dagRunStatus := dagAgent.Status(th.Context)
		setAllAgentStepCommands(dag.DAG, "exit 0")

		// Retry the DAG and check if it is successful
		dagAgent = dag.Agent(test.WithAgentOptions(agent.Options{
			RetryTarget: &dagRunStatus,
		}))
		dagAgent.RunSuccess(t)
		require.Equal(t, 0, dagAgent.Status(th.Context).AutoRetryCount)

		for _, node := range dagAgent.Status(th.Context).Nodes {
			if node.Status != core.NodeSucceeded &&
				node.Status != core.NodeSkipped {
				t.Errorf("node %q is not successful: %s", node.Step.Name, node.Status)
			}
		}
	})

	t.Run("StepRetry", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: "1"
    run: %q
  - name: "2"
    run: %q
    continue_on:
      failure: true
    depends: ["1"]
  - name: "3"
    run: %q
    depends: ["2"]
  - name: "4"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    continue_on:
      skipped: true
  - name: "5"
    run: %q
    depends: ["4"]
  - name: "6"
    run: %q
    depends: ["5"]
  - name: "7"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    depends: ["6"]
    continue_on:
      skipped: true
  - name: "8"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
  - name: "9"
    run: %q
`, "exit 0", "exit 1", "exit 0", "exit 0", "exit 1", "exit 0", "exit 0", "exit 0", "exit 1"))
		dagAgent := dag.Agent()

		// Run the DAG to get a failed status
		dagAgent.RunError(t)
		dagRunStatus := dagAgent.Status(th.Context)

		// Save FinishedAt for all nodes before retry
		prevFinishedAt := map[string]string{}
		for _, node := range dagRunStatus.Nodes {
			prevFinishedAt[node.Step.Name] = node.FinishedAt
		}

		// Modify the DAG to make all steps successful
		setAllAgentStepCommands(dag.DAG, "exit 0")

		// Wait until the current time (RFC3339, second precision) differs
		// from the previous FinishedAt timestamps so that retried steps
		// are guaranteed to have a newer formatted timestamp.
		prev := time.Now().Format(time.RFC3339)
		for time.Now().Format(time.RFC3339) == prev {
			time.Sleep(10 * time.Millisecond)
		}

		// Retry from step '5' using StepRetry
		dagAgent = dag.Agent(test.WithAgentOptions(agent.Options{
			RetryTarget: &dagRunStatus,
			StepRetry:   "5",
		}))
		err := dagAgent.Run(context.Background())
		require.NoError(t, err)

		// Only node 5 is retried, downstream steps remain untouched
		retried := map[string]struct{}{"5": {}}
		// Node 2 is a false command and should remain failed
		// Downstream nodes (6, 7, 8, 9) should remain in their previous state
		falseSteps := map[string]struct{}{"2": {}}
		// Check that only step '5' is rerun, all other steps remain unchanged
		st := dagAgent.Status(th.Context)

		for _, node := range st.Nodes {
			name := node.Step.Name
			prev := prevFinishedAt[name]
			now := node.FinishedAt

			if _, isRetried := retried[name]; isRetried {
				// Only step '5' should be retried and successful
				if node.Status != core.NodeSucceeded && node.Status != core.NodeSkipped {
					t.Errorf("step %q is not successful or skipped after step retry: %s", name, node.Status)
				}
				// FinishedAt should be fresher (more recent) than before, if it was set
				if prev != "" && now != "" && now <= prev {
					t.Errorf("retried step %q FinishedAt not updated: was %v, now %v", name, prev, now)
				}
			} else {
				// Assert that steps with "false" commands are still failed
				if _, isFalseStep := falseSteps[name]; isFalseStep {
					if node.Status != core.NodeFailed {
						t.Errorf("non-retried step %q (false command) should remain failed after step retry, got: %s", name, node.Status)
					}
				}
				// FinishedAt should be unchanged for all non-retried steps
				if prev != now {
					t.Errorf("non-retried step %q FinishedAt changed after step retry: was %v, now %v", name, prev, now)
				}
			}
		}
	})
}

func TestAgent_HandleHTTP(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Parallel()
	}

	t.Run("HTTPValid", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-valid.release")
		startedFile := filepath.Join(tmpDir, "http-valid.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()
		ctx := th.Context
		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		// Get the status of the DAG
		require.Eventually(t, func() bool {
			rw := mockResponseWriter{}
			dagAgent.HandleHTTP(ctx)(&rw, &http.Request{
				Method: "GET", URL: &url.URL{Path: "/status"},
			})
			if rw.status != http.StatusOK {
				return false
			}

			dagRunStatus, err := exec.StatusFromJSON(rw.body)
			return err == nil && dagRunStatus.Status == core.Running
		}, 10*time.Second, 50*time.Millisecond)

		// Stop the DAG
		dagAgent.Abort()

		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, core.Aborted)
	})
	t.Run("HTTPInvalidRequest", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-invalid.release")
		startedFile := filepath.Join(tmpDir, "http-invalid.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()

		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		rw := mockResponseWriter{}

		// Request with an invalid path
		dagAgent.HandleHTTP(th.Context)(&rw, &http.Request{
			Method: "GET",
			URL:    &url.URL{Path: "/invalid-path"},
		})
		require.Equal(t, http.StatusNotFound, rw.status)

		// Stop the DAG
		dagAgent.Abort()
		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, core.Aborted)
	})
	t.Run("HTTPHandleCancel", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-cancel.release")
		startedFile := filepath.Join(tmpDir, "http-cancel.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()

		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		// Cancel the DAG
		rw := mockResponseWriter{}
		dagAgent.HandleHTTP(th.Context)(&rw, &http.Request{
			Method: "POST",
			URL:    &url.URL{Path: "/stop"},
		})
		require.Equal(t, http.StatusOK, rw.status)
		require.Equal(t, "OK", rw.body)

		// Wait for the DAG to stop
		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, core.Aborted)
	})
}

// Assert that mockResponseWriter implements http.ResponseWriter
var _ http.ResponseWriter = (*mockResponseWriter)(nil)

type mockResponseWriter struct {
	status int
	body   string
	header *http.Header
}

type fakeSocketServer struct {
	serveErr error
}

// fakeSocketServerFactory returns a socket factory with deterministic serve behavior.
func fakeSocketServerFactory(serveErr error) agent.SocketServerFactory {
	return func(string, sock.HTTPHandlerFunc) (agent.SocketServer, error) {
		return &fakeSocketServer{serveErr: serveErr}, nil
	}
}

// Serve reports the configured startup result through the listen channel.
func (s *fakeSocketServer) Serve(_ context.Context, listen chan error) error {
	if listen != nil {
		listen <- s.serveErr
	}
	return s.serveErr
}

// Shutdown is a no-op for the fake socket server.
func (*fakeSocketServer) Shutdown(context.Context) error {
	return nil
}

func (h *mockResponseWriter) Header() http.Header {
	if h.header == nil {
		h.header = &http.Header{}
	}
	return *h.header
}

func (h *mockResponseWriter) Write(body []byte) (int, error) {
	h.body = string(body)
	return len([]byte(h.body)), nil
}

func (h *mockResponseWriter) WriteHeader(statusCode int) {
	h.status = statusCode
}

func TestAgent_OutputCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dag      string
		expected map[string]string
	}{
		{
			name: "SimpleOutput",
			dag: `steps:
  - name: step1
    run: echo "hello"
    output: RESULT`,
			expected: map[string]string{"result": "hello"},
		},
		{
			name: "CamelCaseConversion",
			dag: `steps:
  - name: step1
    run: echo "value"
    output: MY_OUTPUT_VAR`,
			expected: map[string]string{"myOutputVar": "value"},
		},
		{
			name: "StructuredOutputDoesNotParticipate",
			dag: `steps:
  - id: publish
    output:
      label: "value"`,
			expected: map[string]string{},
		},
		{
			name: "MultipleSteps",
			dag: `steps:
  - name: step1
    run: echo "one"
    output: OUTPUT_ONE
  - name: step2
    run: echo "two"
    output: OUTPUT_TWO`,
			expected: map[string]string{"outputOne": "one", "outputTwo": "two"},
		},
		{
			name: "LastOneWins",
			dag: `type: graph
steps:
  - name: step1
    run: echo "first"
    output: RESULT
  - name: step2
    run: echo "second"
    output: RESULT
    depends: [step1]`,
			expected: map[string]string{"result": "second"},
		},
		{
			name: "NoOutputs",
			dag: fmt.Sprintf(`steps:
  - name: step1
    run: %q`, "exit 0"),
			expected: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			th := test.Setup(t)
			dag := th.DAG(t, tc.dag)
			dagAgent := dag.Agent()
			dagAgent.RunSuccess(t)

			outputs := dag.ReadOutputs(t)
			for k, v := range tc.expected {
				require.Equal(t, v, outputs[k], "output %s mismatch", k)
			}
			require.Len(t, outputs, len(tc.expected))
		})
	}
}

func TestAgent_OutputSecretMasking(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	secretValue := "super-secret-token-xyz123"
	secretFile := th.TempFile(t, "secret.txt", []byte(secretValue))

	dag := th.DAG(t, `
secrets:
  - name: API_TOKEN
    provider: file
    key: `+secretFile+`
steps:
  - name: step1
    run: echo "Token is ${API_TOKEN}"
    output: RESPONSE`)

	dagAgent := dag.Agent()
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.NotContains(t, outputs["response"], secretValue, "secret should be masked")
	require.Contains(t, outputs["response"], "*******", "masked placeholder expected")
}

func TestAgent_RegistryRefSecretResolution(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secretStore, err := filesecret.New(t.TempDir(), enc)
	require.NoError(t, err)

	sec, err := secretpkg.New(secretpkg.CreateInput{
		Ref:          "prod/db-password",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(context.Background(), sec, &secretpkg.WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: time.Now().UTC(),
	}))

	dag := th.DAG(t, `
secrets:
  - name: DB_PASSWORD
    ref: prod/db-password
steps:
  - name: step1
    run: test "${DB_PASSWORD}" = "managed-secret" && echo ok
    output: RESPONSE`)

	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{SecretStore: secretStore}))
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.Equal(t, "ok", outputs["response"])
}

func TestAgent_SubDAGRunVisibleWhileRunning(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	releaseFile := filepath.Join(t.TempDir(), "release-child")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("done"), 0600)
	})

	// Hold the child open until the test explicitly releases it so Windows can
	// observe persisted SubRuns without racing the child completion.
	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-slow", fmt.Appendf(nil, `
steps:
  - name: slow-step
    run: %q
`, waitForFileScript(releaseFile, 100*time.Millisecond)))

	// The preceding step must run long enough for the one-shot 100ms status timer
	// to fire (and exhaust itself) BEFORE run-child starts. This replicates the
	// production scenario where the bug manifests.
	parent := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: pre-step
    run: %q
  - name: run-child
    action: dag.run
    with:
      dag: child-slow
    depends:
      - pre-step
`, test.Sleep(time.Second)))

	a := parent.Agent()
	runErr := make(chan error, 1)
	go func() {
		runErr <- a.Run(parent.Context)
	}()

	// SubRuns must be visible in the *stored* status BEFORE the child DAG completes.
	// We use ListRecentStatus which reads from the status.jsonl file on disk, not from
	// the live socket, so it accurately reflects what the API handler would return.
	// Before the fix, this would never become true because SetSubRuns() was called
	// after the progressCh notification, so the children field was never written
	// to status.jsonl while the subdag was running.
	require.Eventually(t, func() bool {
		statuses := th.DAGRunMgr.ListRecentStatus(th.Context, parent.Name, 1)
		if len(statuses) == 0 || statuses[0].Status != core.Running {
			return false
		}
		for _, node := range statuses[0].Nodes {
			if node.Step.Name == "run-child" && node.Status == core.NodeRunning {
				return len(node.SubRuns) > 0
			}
		}
		return false
	}, subDAGVisibleTimeout(), 100*time.Millisecond,
		"SubRuns must be present in stored status while subDAG step is running")

	require.NoError(t, os.WriteFile(releaseFile, []byte("done"), 0600))
	require.NoError(t, <-runErr)
}

func TestAgent_DAGEnqueueQueuesChildWithoutWaiting(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	releaseFile := filepath.Join(t.TempDir(), "release-child")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("done"), 0600)
	})

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-enqueued", fmt.Appendf(nil, `
params:
  - TARGET: default
  - OTHER: keep
steps:
  - name: wait
    run: %q
`, waitForFileScript(releaseFile, 100*time.Millisecond)))

	parent := th.DAG(t, `
type: graph
steps:
  - name: enqueue-child
    action: dag.enqueue
    with:
      dag: child-enqueued
      params:
        TARGET: async
      queue: background
`)

	a := parent.Agent()
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	subRun := status.Nodes[0].SubRuns[0]
	require.Equal(t, "child-enqueued", subRun.DAGName)
	require.Contains(t, subRun.Params, `TARGET="async"`)

	items, err := th.QueueStore.List(th.Context, "background")
	require.NoError(t, err)
	require.Len(t, items, 1)

	ref, err := items[0].Data()
	require.NoError(t, err)
	require.Equal(t, exec.NewDAGRunRef("child-enqueued", subRun.DAGRunID), *ref)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, *ref)
	require.NoError(t, err)
	childStatus, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Queued, childStatus.Status)
	require.Equal(t, core.TriggerTypeSubDAG, childStatus.TriggerType)
	require.Equal(t, []string{"TARGET=async", "OTHER=keep"}, childStatus.ParamsList)
	require.Equal(t, *ref, childStatus.Root)
	require.True(t, childStatus.Parent.Zero())
}

func TestAgent_DAGEnqueueQueuedChildRunsFromQueue(t *testing.T) {
	t.Parallel()

	outputFile := filepath.Join(t.TempDir(), "child-output")
	th := test.Setup(t,
		test.WithBuiltExecutable(),
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Queues.Enabled = true
			cfg.Queues.Config = append(cfg.Queues.Config, config.QueueConfig{
				Name:          "background",
				MaxActiveRuns: 1,
			})
		}),
	)

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-queue-exec", fmt.Appendf(nil, `
steps:
  - name: write-output
    run: %q
`, writeFileCommand(outputFile, "done")))

	parent := th.DAG(t, `
type: graph
steps:
  - name: enqueue-child
    action: dag.enqueue
    with:
      dag: child-queue-exec
      queue: background
`)

	a := parent.Agent()
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	subRun := status.Nodes[0].SubRuns[0]
	ref := exec.NewDAGRunRef("child-queue-exec", subRun.DAGRunID)

	dagExecutor := scheduler.NewDAGExecutor(
		nil,
		runtimepkg.NewSubCmdBuilder(th.Config),
		th.Config.DefaultExecMode,
		th.Config.Paths.BaseConfig,
		nil,
	)
	processor := scheduler.NewQueueProcessor(
		th.QueueStore,
		th.DAGRunStore,
		th.ProcStore,
		dagExecutor,
		th.Config.Queues,
		scheduler.WithBackoffConfig(scheduler.BackoffConfig{
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     250 * time.Millisecond,
			MaxRetries:      20,
		}),
	)
	processor.ProcessQueueItems(th.Context, "background")

	waitForTestFile(t, outputFile, subDAGVisibleTimeout())
	require.Eventually(t, func() bool {
		childStatus, err := th.DAGRunMgr.GetSavedStatus(th.Context, ref)
		return err == nil && childStatus.Status == core.Succeeded
	}, subDAGVisibleTimeout(), 100*time.Millisecond)

	require.Eventually(t, func() bool {
		processor.ProcessQueueItems(th.Context, "background")
		length, err := th.QueueStore.Len(th.Context, "background")
		return err == nil && length == 0
	}, subDAGVisibleTimeout(), 100*time.Millisecond)
}

func subDAGVisibleTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 90 * time.Second
	}
	return 10 * time.Second
}
