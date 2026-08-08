// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLsCommand(t *testing.T) {
	t.Run("ListsDAGsAndFiltersByPattern", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagA := th.DAG(t, `name: ls-alpha
steps:
  - name: "1"
    run: echo a
`)
		_ = th.DAG(t, `name: ls-beta
steps:
  - name: "1"
    run: echo b
`)

		out := runLsWithStdout(t, th, cmd.Ls(), []string{"ls"})
		assert.Contains(t, out, "NAME")
		assert.Contains(t, out, "ls-alpha")
		assert.Contains(t, out, "ls-beta")

		out = runLsWithStdout(t, th, cmd.Ls(), []string{"ls", "alpha"})
		assert.Contains(t, out, dagA.Name)
		assert.NotContains(t, out, "ls-beta")
	})

	t.Run("ShowsLastAndHistoryColumns", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: ls-enriched
steps:
  - name: "1"
    run: echo hello
`)
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})
		dag.AssertLatestStatus(t, ir.Succeeded)

		out := runLsWithStdout(t, th, cmd.Ls(), []string{"ls", "-l", "-H", "ls-enriched"})
		assert.Contains(t, out, "LAST_STATUS")
		assert.Contains(t, out, "HISTORY")
		assert.Contains(t, out, "Succeeded")
	})

	t.Run("SortLastReverseHonorsNameTieBreaker", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		_ = th.DAG(t, `name: ls-tie-alpha
steps:
  - name: "1"
    run: echo a
`)
		_ = th.DAG(t, `name: ls-tie-beta
steps:
  - name: "1"
    run: echo b
`)

		// Neither DAG has run, so last-run times are equal (zero); reverse should
		// invert the case-insensitive name tie-breaker.
		outAsc := runLsWithStdout(t, th, cmd.Ls(), []string{"ls", "-t", "ls-tie-"})
		outDesc := runLsWithStdout(t, th, cmd.Ls(), []string{"ls", "-t", "-r", "ls-tie-"})

		assert.Less(t, strings.Index(outAsc, "ls-tie-alpha"), strings.Index(outAsc, "ls-tie-beta"))
		assert.Less(t, strings.Index(outDesc, "ls-tie-beta"), strings.Index(outDesc, "ls-tie-alpha"))
	})

	t.Run("NextUsesSchedulerProjection", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		scheduledAt := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
		overdue := th.DAG(t, fmt.Sprintf(`name: ls-projection-overdue
schedule:
  - at: "%s"
steps:
  - run: echo overdue
`, scheduledAt.Format(time.RFC3339)))
		inactive := th.DAG(t, `name: ls-projection-inactive
schedule:
  - expression: "* * * * *"
    profile: prod
steps:
  - run: echo inactive
`)
		suspended := th.DAG(t, `name: ls-projection-suspended
schedule:
  - expression: "* * * * *"
steps:
  - run: echo suspended
`)
		require.NoError(t, th.DAGStore.ToggleSuspend(th.Context, suspended.FileName(), true))

		future := scheduledAt.Add(24 * time.Hour)
		stateStore := scheduler.NewWatermarkStore(
			file.NewCollection(filepath.Join(th.Config.Paths.DataDir, "scheduler"), file.WithIndentedJSON()),
		)
		require.NoError(t, stateStore.Save(th.Context, &scheduler.SchedulerState{
			Version: scheduler.SchedulerStateVersion,
			DAGs: map[string]scheduler.DAGWatermark{
				overdue.Name: {
					NextRun: &scheduledAt,
					OneOffs: map[string]scheduler.OneOffScheduleState{
						overdue.Schedule[0].Fingerprint(): {
							ScheduledTime: scheduledAt,
							Status:        scheduler.OneOffStatusPending,
						},
					},
				},
				inactive.Name: {},
				suspended.Name: {
					NextRun: &future,
				},
			},
		}))

		out := runLsWithStdout(t, th, cmd.Ls(), []string{"ls", "-n", "ls-projection-"})
		assert.Equal(t, scheduledAt.Format(time.RFC3339), lsRowFields(t, out, overdue.Name)[1])
		assert.Equal(t, "-", lsRowFields(t, out, inactive.Name)[1])
		assert.Equal(t, "-", lsRowFields(t, out, suspended.Name)[1])
		assert.Less(t, strings.Index(out, overdue.Name), strings.Index(out, inactive.Name))
		assert.Less(t, strings.Index(out, overdue.Name), strings.Index(out, suspended.Name))
	})
}

func lsRowFields(t *testing.T, output, name string) []string {
	t.Helper()
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return fields
		}
	}
	t.Fatalf("row %q not found in output:\n%s", name, output)
	return nil
}

// runLsWithStdout executes a cobra command and returns captured stdout.
func runLsWithStdout(t *testing.T, th test.Command, c *cobra.Command, args []string) string {
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
