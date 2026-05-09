// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestRecordEarlyFailure(t *testing.T) {
	t.Run("RecordsFailureForNewDAGRun", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `
steps:
  - name: step1
    run: echo hello
`)

		dagRunID := "test-run-id-001"
		testErr := errors.New("process acquisition failed")

		// Create Context with required stores
		ctx := &cmd.Context{
			Context:     th.Context,
			Config:      th.Config,
			DAGRunStore: th.DAGRunStore,
		}

		// Record the early failure
		err := ctx.RecordEarlyFailure(dag.DAG, dagRunID, testErr)
		require.NoError(t, err)

		// Verify the failure was recorded
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)
		attempt, err := th.DAGRunStore.FindAttempt(th.Context, ref)
		require.NoError(t, err)
		require.NotNil(t, attempt)

		status, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Failed, status.Status)
		require.Contains(t, status.Error, "process acquisition failed")
	})

	t.Run("RecordsFailureForExistingAttempt", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `
steps:
  - name: step1
    run: echo hello
`)

		// First, run the DAG to create an attempt
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})

		// Get the existing run ID
		latestStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		dagRunID := latestStatus.DAGRunID

		// Now record an early failure for the same run ID
		testErr := errors.New("retry failed due to lock contention")

		ctx := &cmd.Context{
			Context:     th.Context,
			Config:      th.Config,
			DAGRunStore: th.DAGRunStore,
		}

		err = ctx.RecordEarlyFailure(dag.DAG, dagRunID, testErr)
		require.NoError(t, err)

		// Verify the failure was recorded (status should be updated)
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)
		attempt, err := th.DAGRunStore.FindAttempt(th.Context, ref)
		require.NoError(t, err)
		require.NotNil(t, attempt)

		status, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Failed, status.Status)
		require.Contains(t, status.Error, "retry failed due to lock contention")
	})

	t.Run("ReturnsErrorForNilDAG", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		ctx := &cmd.Context{
			Context:     th.Context,
			Config:      th.Config,
			DAGRunStore: th.DAGRunStore,
		}

		err := ctx.RecordEarlyFailure(nil, "some-run-id", errors.New("test error"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "DAG and dag-run ID are required")
	})

	t.Run("ReturnsErrorForEmptyDAGRunID", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `
steps:
  - name: step1
    run: echo hello
`)

		ctx := &cmd.Context{
			Context:     th.Context,
			Config:      th.Config,
			DAGRunStore: th.DAGRunStore,
		}

		err := ctx.RecordEarlyFailure(dag.DAG, "", errors.New("test error"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "DAG and dag-run ID are required")
	})

	t.Run("CanRetryEarlyFailureRecord", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `
steps:
  - name: step1
    run: echo hello
`)

		dagRunID := "early-failure-retry-test"
		testErr := errors.New("initial process acquisition failed")

		// Create Context and record early failure
		ctx := &cmd.Context{
			Context:     th.Context,
			Config:      th.Config,
			DAGRunStore: th.DAGRunStore,
		}

		err := ctx.RecordEarlyFailure(dag.DAG, dagRunID, testErr)
		require.NoError(t, err)

		// Verify initial failure status
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)
		attempt, err := th.DAGRunStore.FindAttempt(th.Context, ref)
		require.NoError(t, err)
		require.NotNil(t, attempt)

		status, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, core.Failed, status.Status)

		// Verify DAG can be read back (required for retry)
		storedDAG, err := attempt.ReadDAG(th.Context)
		require.NoError(t, err)
		require.NotNil(t, storedDAG)
		require.Equal(t, dag.Name, storedDAG.Name)

		// Now retry the early failure record
		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args: []string{"retry", "--run-id", dagRunID, dag.Name},
		})

		// Wait for retry to complete
		require.Eventually(t, func() bool {
			currentStatus, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, dagRunID)
			return err == nil && currentStatus != nil && currentStatus.Status == core.Succeeded
		}, 5*time.Second, 100*time.Millisecond, "Retry should succeed")

		// Verify final status is succeeded
		finalStatus, err := th.DAGRunMgr.GetCurrentStatus(th.Context, dag.DAG, dagRunID)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, finalStatus.Status)
	})
}
