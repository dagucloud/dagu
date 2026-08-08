// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRmCommand(t *testing.T) {
	t.Run("RequiresHistoryOrDefinitionFlag", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", dag.Name},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one of --history")
	})

	t.Run("DeletesAllHistoryWithForce", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertDAGRunCount(t, 1)

		th.RunCommand(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "--history", "--force", dag.Name},
		})
		dag.AssertDAGRunCount(t, 0)
	})

	t.Run("OlderThanPreservesRecentHistory", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertDAGRunCount(t, 1)

		th.RunCommand(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-H", "-t", "30d", "-f", dag.Name},
		})
		dag.AssertDAGRunCount(t, 1)
	})

	t.Run("OlderThanRequiresHistory", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-d", "-t", "1d", "-f", dag.Name},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--older-than")
	})

	t.Run("DeletesDefinitionWhenNameDiffersFromFile", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: explicit-definition-name
steps:
  - name: "1"
    run: echo "hello"
`)

		th.RunCommand(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "--definition", "--force", dag.Location},
		})

		_, err := th.DAGStore.GetMetadata(th.Context, dag.Location)
		require.Error(t, err)
	})

	t.Run("QuietStillRequiresConfirmation", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		stdin, input, err := os.Pipe()
		require.NoError(t, err)
		originalStdin := os.Stdin
		os.Stdin = stdin
		t.Cleanup(func() {
			os.Stdin = originalStdin
			require.NoError(t, stdin.Close())
		})
		_, err = input.WriteString("n\n")
		require.NoError(t, err)
		require.NoError(t, input.Close())

		th.RunCommand(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "--definition", "--quiet", dag.Location},
		})

		_, err = th.DAGStore.GetMetadata(th.Context, dag.Location)
		require.NoError(t, err)
	})

	t.Run("RefusesDefinitionDeleteWhenAlive", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		release := newHoldFile(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))

		done := make(chan error, 1)
		go func() {
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{
				Args: []string{"start", dag.Location},
			})
		}()

		dag.AssertLatestStatus(t, ir.Running)

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-d", "-f", dag.Name},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "alive process")

		releaseHoldFile(t, release)
		require.NoError(t, <-done)
	})

	t.Run("RefusesDefinitionDeleteForDistributedRun", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: distributed-definition
steps:
  - name: "1"
    run: echo "hello"
`)
		require.NoError(t, th.ActiveDistributedRunStore.Upsert(th.Context, exec.ActiveDistributedRun{
			AttemptKey: "distributed-attempt",
			DAGRun:     exec.NewDAGRunRef(dag.Name, "distributed-run"),
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		}))

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-d", "-f", dag.Location},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "active distributed run")

		_, err = th.DAGStore.GetMetadata(th.Context, dag.Location)
		require.NoError(t, err)
	})

	t.Run("InvalidOlderThan", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-H", "-t", "bogus", "-f", dag.Name},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid --older-than")
	})

	t.Run("RejectsOlderThanOverflow", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
		dag.AssertLatestStatus(t, ir.Succeeded)

		err := th.RunCommandWithError(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-H", "-t", "2562048h", "-f", dag.Name},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid --older-than")
		dag.AssertDAGRunCount(t, 1)
	})

	t.Run("DryRunDoesNotDelete", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertDAGRunCount(t, 1)

		th.RunCommand(t, cmd.Rm(), test.CmdTest{
			Args: []string{"rm", "-H", "--dry-run", dag.Name},
		})
		dag.AssertDAGRunCount(t, 1)
	})
}
