// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestRestartCommand(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	release := newHoldFile(t)
	dag := th.DAG(t, fmt.Sprintf(`params: "p1"
steps:
  - name: "1"
    run: "echo $1"
  - name: "2"
    run: %q
`, holdUntilFileExistsCommand(release)))

	// Start the DAG to restart.
	done1 := make(chan struct{})
	go func() {
		args := []string{"start", `--params="foo"`, dag.Location}
		th.RunCommand(t, cmd.Start(), test.CmdTest{Args: args})
		close(done1)
	}()

	// Wait for the DAG to be running.
	dag.AssertCurrentStatus(t, core.Running)

	// Restart the DAG.
	done2 := make(chan struct{})
	go func() {
		args := []string{"restart", "--schedule-time=2026-03-13T10:00:00Z", dag.Location}
		th.RunCommand(t, cmd.Restart(), test.CmdTest{Args: args})
		close(done2)
	}()
	releaseDone := releaseHoldFileWhenRecentStatusCountAtLeast(t, th, dag.Name, 2, release)

	// Wait for both executions to complete.
	require.NoError(t, <-releaseDone)
	<-done1
	<-done2

	// Check parameter was the same as the first execution
	loaded, err := spec.Load(th.Context, dag.Location, spec.WithBaseConfig(th.Config.Paths.BaseConfig))
	require.NoError(t, err)

	// Check parameter was the same as the first execution
	recentHistory := th.DAGRunMgr.ListRecentStatus(th.Context, loaded.Name, 2)

	require.Len(t, recentHistory, 2)
	require.Equal(t, recentHistory[0].Params, recentHistory[1].Params)
	require.Equal(t, "2026-03-13T10:00:00Z", recentHistory[0].ScheduleTime)
}

func TestRestartCommand_BuiltExecutableRestoresExplicitEnv(t *testing.T) {
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	t.Setenv("CMD_RESTART_EXPLICIT_ENV", "from-host")

	release := newHoldFile(t)
	dag := th.DAG(t, fmt.Sprintf(`name: built-restart-explicit-env
env:
  - EXPORTED_SECRET: ${CMD_RESTART_EXPLICIT_ENV}
steps:
  - name: "hold"
    run: %q
  - name: "capture"
    run: printf '%%s|%%s' "$EXPORTED_SECRET" "${CMD_RESTART_EXPLICIT_ENV:-}"
    output: RESULT
`, holdUntilFileExistsCommand(release)))

	startDone := make(chan error, 1)
	go func() {
		startDone <- th.ExecuteCommand(cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
	}()

	require.Eventually(t, func() bool {
		status, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, "")
		return err == nil && status != nil && status.Status == core.Running
	}, 10*time.Second, 100*time.Millisecond)

	releaseDone := releaseHoldFileWhenRecentStatusCountAtLeast(t, th, dag.Name, 2, release)
	test.RunBuiltCLI(t, th.Helper, []string{"CMD_RESTART_EXPLICIT_ENV=from-host"}, "restart", dag.Name)
	require.NoError(t, <-releaseDone)

	require.NoError(t, <-startDone)

	latestStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, latestStatus.Status)
	require.Equal(t, "from-host|", test.StatusOutputValue(t, &latestStatus, "RESULT"))

	latestAttempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, latestStatus.DAGRunID))
	require.NoError(t, err)
	latestAttemptStatus, err := latestAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, "from-host|", test.StatusOutputValue(t, latestAttemptStatus, "RESULT"))
}
