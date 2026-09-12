// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
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

	t.Run("ReportsNothingRunningAsAnEmptyJSONList", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		out := runPsWithStdout(t, th, cmd.Ps(), []string{"ps", "--format", "json"})

		var entries []map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &entries))
		assert.Empty(t, entries)
	})

	t.Run("RejectsAnUnknownFormat", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		root := &cobra.Command{Use: "root"}
		root.AddCommand(cmd.Ps())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(test.WithConfigFlag([]string{"ps", "--format", "yaml"}, th.Config))

		err := root.ExecuteContext(th.Context)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "table")
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

		out = runPsWithStdout(t, th, cmd.Ps(), []string{"ps", "--format", "json"})
		var entries []struct {
			Name      string `json:"name"`
			DAGRunID  string `json:"dagRunId"`
			AttemptID string `json:"attemptId"`
			StartedAt string `json:"startedAt"`
			Group     string `json:"group"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &entries))
		require.Len(t, entries, 1)
		assert.Equal(t, dag.Name, entries[0].Name)
		assert.Equal(t, runID, entries[0].DAGRunID)
		assert.Equal(t, "attempt-1", entries[0].AttemptID)
		assert.Equal(t, startedAt.Format(time.RFC3339), entries[0].StartedAt)
		assert.Equal(t, dag.ProcGroup(), entries[0].Group)
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
