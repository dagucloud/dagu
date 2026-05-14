// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const (
	testProcHeartbeatInterval = 150 * time.Millisecond
	testProcStaleThreshold    = 3 * time.Second
)

func TestProcHeartbeat_StartCommand(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Proc.HeartbeatInterval = testProcHeartbeatInterval
		cfg.Proc.HeartbeatSyncInterval = testProcHeartbeatInterval
		cfg.Proc.StaleThreshold = testProcStaleThreshold
	}))

	dag := th.DAG(t, `
name: proc-heartbeat-start
steps:
  - name: sleep
    run: sleep 2
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	errCh := runCommandAsync(th.Context, cmd.Start(), []string{
		"start",
		"--config", th.Config.Paths.ConfigFileUsed,
		"--run-id", dagRunID,
		dag.Location,
	})

	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	require.Eventually(t, func() bool {
		status, ok := readStatusIfPresent(th, ref)
		return ok && status.Status == core.Running
	}, intgTestTimeout(5*time.Second), 50*time.Millisecond)

	procFile := test.WaitForProcFile(t, th.Config.Paths.ProcDir, dag.ProcGroup(), ref, intgTestTimeout(2*time.Second))
	test.RequireHeartbeatAdvance(t, procFile, intgTestTimeout(3*time.Second))

	require.NoError(t, <-errCh)

	status := test.ReadRunStatus(th.Context, t, th.DAGRunStore, ref)
	require.Equal(t, core.Succeeded, status.Status)
}

func TestProcHeartbeat_RetryCommand(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Proc.HeartbeatInterval = testProcHeartbeatInterval
		cfg.Proc.HeartbeatSyncInterval = testProcHeartbeatInterval
		cfg.Proc.StaleThreshold = testProcStaleThreshold
	}))

	dag := th.DAG(t, `
name: proc-heartbeat-retry
steps:
  - name: sleep
    run: sleep 2
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	createFailedRun(t, th, dag.DAG, dagRunID)

	errCh := runCommandAsync(th.Context, cmd.Retry(), []string{
		"retry",
		"--config", th.Config.Paths.ConfigFileUsed,
		"--run-id", dagRunID,
		dag.Location,
	})

	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	require.Eventually(t, func() bool {
		status, ok := readStatusIfPresent(th, ref)
		return ok && status.Status == core.Running
	}, intgTestTimeout(5*time.Second), 50*time.Millisecond)

	procFile := test.WaitForProcFile(t, th.Config.Paths.ProcDir, dag.ProcGroup(), ref, intgTestTimeout(2*time.Second))
	test.RequireHeartbeatAdvance(t, procFile, intgTestTimeout(3*time.Second))

	require.NoError(t, <-errCh)

	status := test.ReadRunStatus(th.Context, t, th.DAGRunStore, ref)
	require.Equal(t, core.Succeeded, status.Status)
}

func runCommandAsync(ctx context.Context, command *cobra.Command, args []string) chan error {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(command)
	root.SetArgs(args)

	errCh := make(chan error, 1)
	go func() {
		errCh <- root.ExecuteContext(ctx)
	}()
	return errCh
}

func createFailedRun(t *testing.T, th test.Command, dag *core.DAG, dagRunID string) {
	t.Helper()

	attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	logFile := filepath.Join(th.Config.Paths.LogDir, dag.Name, dagRunID+".log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))

	status := transform.NewStatusBuilder(dag).Create(
		dagRunID,
		core.Failed,
		0,
		time.Now(),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(exec.NewDAGRunRef(dag.Name, dagRunID), exec.DAGRunRef{}),
		transform.WithLogFilePath(logFile),
	)

	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, status))
	require.NoError(t, attempt.Close(th.Context))
}

func readStatusIfPresent(th test.Command, dagRun exec.DAGRunRef) (*exec.DAGRunStatus, bool) {
	attempt, err := th.DAGRunStore.FindAttempt(th.Context, dagRun)
	if err != nil {
		return nil, false
	}
	status, err := attempt.ReadStatus(th.Context)
	if err != nil {
		return nil, false
	}
	return status, true
}
