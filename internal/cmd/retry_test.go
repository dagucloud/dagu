// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestRetryCommand(t *testing.T) {
	t.Run("RetryDAGWithFilePath", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `params: "p1"
steps:
  - name: "1"
    run: echo param is $1
`)

		// Run a DAG.
		args := []string{"start", `--params="foo"`, dagFile.Location}
		th.RunCommand(t, cmd.Start(), test.CmdTest{Args: args})

		// Find the dag-run ID.
		dagStore := th.DAGStore
		ctx := context.Background()

		dag, err := dagStore.GetMetadata(ctx, dagFile.Location)
		require.NoError(t, err)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(ctx, dag)
		require.NoError(t, err)
		require.Equal(t, dagRunStatus.Status, core.Succeeded)
		require.NotNil(t, dagRunStatus.Status)

		// Retry with the dag-run ID using file path.
		args = []string{"retry", fmt.Sprintf("--run-id=%s", dagRunStatus.DAGRunID), dagFile.Location}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{`[1=foo]`},
		})
	})

	t.Run("RetryDAGWithName", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `params: "p1"
steps:
  - name: "1"
    run: echo param is $1
`)

		// Run a DAG.
		args := []string{"start", `--params="bar"`, dagFile.Location}
		th.RunCommand(t, cmd.Start(), test.CmdTest{Args: args})

		// Find the dag-run ID.
		dagStore := th.DAGStore
		ctx := context.Background()

		dag, err := dagStore.GetMetadata(ctx, dagFile.Location)
		require.NoError(t, err)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(ctx, dag)
		require.NoError(t, err)
		require.Equal(t, dagRunStatus.Status, core.Succeeded)
		require.NotNil(t, dagRunStatus.Status)

		// Retry with the dag-run ID using DAG name.
		args = []string{"retry", fmt.Sprintf("--run-id=%s", dagRunStatus.DAGRunID), dag.Name}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{`[1=bar]`},
		})
	})

	t.Run("QueuedCatchupRegeneratesLogAndPreservesTriggerType", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `name: queued-catchup-dag
steps:
  - name: "1"
    run: echo queued catchup
`)

		runID := "queued-catchup-run"
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dagFile.DAG, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		scheduleTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		status := transform.NewStatusBuilder(dagFile.DAG).Create(
			runID,
			core.Queued,
			0,
			time.Time{},
			transform.WithAttemptID(attempt.ID()),
			transform.WithTriggerType(core.TriggerTypeCatchUp),
			transform.WithQueuedAt(stringutil.FormatTime(time.Now())),
			transform.WithScheduleTime(stringutil.FormatTime(scheduleTime)),
		)
		writeStatus(t, th.Context, attempt, status)

		args := []string{"retry", fmt.Sprintf("--run-id=%s", runID), dagFile.Location}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: args})

		latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, runID))
		require.NoError(t, err)

		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)
		require.Equal(t, core.TriggerTypeCatchUp, latestStatus.TriggerType)
		require.NotEmpty(t, latestStatus.Log)
		require.FileExists(t, latestStatus.Log)
	})

	t.Run("QueuedRetryCreatesNewAttempt", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `name: queued-retry-dag
steps:
  - name: "1"
    run: echo queued retry
`)

		runID := "queued-retry-run"
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dagFile.DAG, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		logPath := filepath.Join(th.Config.Paths.LogDir, "queued-retry-test.log")
		require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o750))

		status := transform.NewStatusBuilder(dagFile.DAG).Create(
			runID,
			core.Queued,
			0,
			time.Time{},
			transform.WithAttemptID(attempt.ID()),
			transform.WithTriggerType(core.TriggerTypeRetry),
			transform.WithQueuedAt(stringutil.FormatTime(time.Now())),
			transform.WithLogFilePath(logPath),
		)
		writeStatus(t, th.Context, attempt, status)

		args := []string{"retry", fmt.Sprintf("--run-id=%s", runID), dagFile.Location}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: args})

		latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, runID))
		require.NoError(t, err)
		require.NotEqual(t, attempt.ID(), latestAttempt.ID())

		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)
		require.Equal(t, core.TriggerTypeRetry, latestStatus.TriggerType)
	})

	t.Run("QueueDispatchRetryTreatsMissingRunAsStaleDispatch", func(t *testing.T) {
		th := test.SetupCommand(t)
		t.Setenv(exec.EnvKeyQueueDispatchRetry, "1")

		dagFile := th.DAG(t, `name: queue-dispatch-stale-retry
steps:
  - name: "1"
    run: echo stale dispatch
`)

		err := th.RunCommandWithError(t, cmd.Retry(), test.CmdTest{
			Args: []string{"retry", "--run-id=missing-run", dagFile.Location},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "dag-run is not queued")
	})

	t.Run("RetryAllowsRootFlagPointingAtSameRun", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `name: retry-root-same-run
steps:
  - name: "1"
    run: echo retry root
`)

		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=root-same-run", dagFile.Location},
		})

		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args: []string{
				"retry",
				"--run-id=root-same-run",
				"--root=" + dagFile.Name + ":root-same-run",
				dagFile.Location,
			},
		})

		latestAttempt, err := th.DAGRunStore.FindAttempt(
			th.Context,
			exec.NewDAGRunRef(dagFile.Name, "root-same-run"),
		)
		require.NoError(t, err)

		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)
	})

	t.Run("StepRetryPreservesExplicitWorkingDir", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		workDir := t.TempDir()
		shell := test.ForOS("sh", "powershell")
		command := test.ForOS(`      pwd > observed.txt
      if [ ! -f marker ]; then
        touch marker
        exit 1
      fi
      echo retry ok`, `      (Get-Location).Path | Set-Content -Path observed.txt -NoNewline
      if (-not (Test-Path marker)) {
        New-Item -ItemType File marker | Out-Null
        exit 1
      }
      Write-Output "retry ok"`)

		dagFile := th.DAG(t, fmt.Sprintf(`name: retry-working-dir
working_dir: %q
steps:
  - name: target
    run: |
%s
    with:
      shell: %s
`, workDir, command, shell))

		runID := "retry-working-dir-run"
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id", runID, dagFile.Location},
		})
		require.Error(t, err)

		failedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, runID))
		require.NoError(t, err)
		failedStatus, err := failedAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, filepath.Clean(workDir), filepath.Clean(failedStatus.WorkingDir))
		require.Len(t, failedStatus.Nodes, 1)
		require.Equal(t, filepath.Clean(workDir), filepath.Clean(failedStatus.Nodes[0].WorkingDir))

		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args: []string{"retry", "--run-id", runID, "--step", "target", dagFile.Name},
		})

		latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, runID))
		require.NoError(t, err)
		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)

		observed, err := os.ReadFile(filepath.Join(workDir, "observed.txt"))
		require.NoError(t, err)
		expectedDir := filepath.Clean(workDir)
		observedDir := filepath.Clean(strings.TrimSpace(string(observed)))
		if runtime.GOOS == "windows" {
			expectedInfo, err := os.Stat(expectedDir)
			require.NoError(t, err)
			observedInfo, err := os.Stat(observedDir)
			require.NoError(t, err)
			require.True(t, os.SameFile(expectedInfo, observedInfo), "expected %q, got %q", expectedDir, observedDir)
		} else {
			require.Equal(t, expectedDir, observedDir)
		}
	})

	t.Run("QueuedCatchupRetryRestoresEnvSecretsFromPersistedFullDAG", func(t *testing.T) {
		th := test.SetupCommand(t)
		t.Setenv("QUEUED_CATCHUP_SECRET_SOURCE", "from-host")

		dagFile := th.DAG(t, `name: queued-catchup-secret-dag
secrets:
  - name: EXPORTED_SECRET
    provider: env
    key: QUEUED_CATCHUP_SECRET_SOURCE
steps:
  - name: "1"
    run: printf '%s|%s' "$EXPORTED_SECRET" "${QUEUED_CATCHUP_SECRET_SOURCE:-}"
    output: RESULT
`)

		metadataOnly, err := spec.Load(
			th.Context,
			dagFile.Location,
			spec.OnlyMetadata(),
			spec.WithoutEval(),
			spec.SkipSchemaValidation(),
		)
		require.NoError(t, err)
		require.Empty(t, metadataOnly.Secrets)

		runID := "queued-catchup-secret-run"
		scheduleTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
		require.NoError(t, scheduler.EnqueueCatchupRun(
			th.Context,
			th.DAGRunStore,
			th.QueueStore,
			th.Config.Paths.LogDir,
			th.Config.Paths.ArtifactDir,
			th.Config.Paths.BaseConfig,
			metadataOnly,
			runID,
			core.TriggerTypeCatchUp,
			scheduleTime,
		))

		args := []string{"retry", fmt.Sprintf("--run-id=%s", runID), dagFile.Location}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: args})

		latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, runID))
		require.NoError(t, err)

		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)
		require.Equal(t, core.TriggerTypeCatchUp, latestStatus.TriggerType)
		require.Equal(t, "from-host|", test.StatusOutputValue(t, latestStatus, "RESULT"))
	})

	t.Run("TrueRetryKeepsRetryTriggerType", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dagFile := th.DAG(t, `name: retry-trigger-dag
steps:
  - name: "1"
    run: echo retry trigger
`)

		th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})

		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dagFile.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, status.Status)

		args := []string{"retry", fmt.Sprintf("--run-id=%s", status.DAGRunID), dagFile.Location}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: args})

		latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dagFile.Name, status.DAGRunID))
		require.NoError(t, err)

		latestStatus, err := latestAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, latestStatus.Status)
		require.Equal(t, core.TriggerTypeRetry, latestStatus.TriggerType)
	})
}

func TestRetryCommand_BuiltExecutableRestoresExplicitEnv(t *testing.T) {
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	markerPath := th.TempFile(t, "retry-marker", nil)
	require.NoError(t, os.Remove(markerPath))

	dag := th.DAG(t, fmt.Sprintf(`name: built-retry-explicit-env
env:
  - EXPORTED_SECRET: ${CMD_RETRY_EXPLICIT_ENV}
steps:
  - name: "capture"
    run: |
      if [ ! -f %[1]q ]; then
        touch %[1]q
        printf '%%s|%%s' "$EXPORTED_SECRET" "${CMD_RETRY_EXPLICIT_ENV:-}"
        exit 1
      fi
      printf '%%s|%%s' "$EXPORTED_SECRET" "${CMD_RETRY_EXPLICIT_ENV:-}"
    with:
      shell: bash
    output: RESULT
`, markerPath))

	_, err := test.RunBuiltCLICommand(t, th.Helper, []string{"CMD_RETRY_EXPLICIT_ENV=from-host"}, "start", dag.Location)
	require.Error(t, err)

	initialStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, core.Failed, initialStatus.Status)

	initialAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, initialStatus.DAGRunID))
	require.NoError(t, err)

	test.RunBuiltCLI(t, th.Helper, nil, "retry", fmt.Sprintf("--run-id=%s", initialStatus.DAGRunID), dag.Location)

	retriedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, initialStatus.DAGRunID))
	require.NoError(t, err)
	require.NotEqual(t, initialAttempt.ID(), retriedAttempt.ID())

	retriedStatus, err := retriedAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, retriedStatus.Status)
	require.Equal(t, "from-host|", test.StatusOutputValue(t, retriedStatus, "RESULT"))
}

func writeStatus(t *testing.T, ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus) {
	t.Helper()

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}
