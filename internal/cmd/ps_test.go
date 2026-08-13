// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPsCommand(t *testing.T) {
	t.Run("EmptyWhenNothingRunning", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		out := runPsWithStdout(t, th, cmd.Ps(), []string{"ps"})
		assert.Contains(t, out, "No running processes")
	})

	t.Run("ListsAndFiltersAliveProcess", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: ps-test-dag
steps:
  - name: "1"
    run: echo hello
`)

		runID := "ps-run-1"
		startedAt := time.Date(2026, time.July, 29, 12, 34, 56, 0, time.UTC)
		proc, err := th.ProcRepository.Acquire(th.Context, dag.ProcGroup(), proc.ProcMeta{
			StartedAt:    startedAt.Unix(),
			Name:         dag.Name,
			DAGRunID:     runID,
			AttemptID:    "attempt-1",
			RootName:     dag.Name,
			RootDAGRunID: runID,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = proc.Stop(th.Context)
		})

		out := runPsWithStdout(t, th, cmd.Ps(), []string{"ps"})
		assert.Contains(t, out, dag.Name)
		assert.Contains(t, out, runID)
		assert.Contains(t, out, startedAt.Format(time.RFC3339))

		out = runPsWithStdout(t, th, cmd.Ps(), []string{"ps", "-d", dag.Name, "-r", "ps-run"})
		assert.Contains(t, out, dag.Name)
		assert.Contains(t, out, runID)

		out = runPsWithStdout(t, th, cmd.Ps(), []string{"ps", "-d", "other-dag"})
		assert.Contains(t, out, "No running processes")
	})
}

// runPsWithStdout executes a cobra command and returns captured stdout.
func runPsWithStdout(t *testing.T, th test.Command, c *cobra.Command, args []string) string {
	t.Helper()

	var buf bytes.Buffer
	root := &cobra.Command{Use: "root"}
	root.AddCommand(c)
	root.SetOut(&buf)
	c.SetOut(&buf)
	root.SetArgs(test.WithConfigFlag(args, th.Config))

	err := root.ExecuteContext(th.Context)
	require.NoError(t, err)
	return buf.String()
}
