// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestListShowsDefinedDAGs(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "ls")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "simple")
	require.Contains(t, result.Stdout(), "long_running")
	require.Contains(t, result.Stdout(), "failing")
}

func TestListPatternFiltersByName(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "ls", "long")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "long_running")
	require.NotContains(t, result.Stdout(), "simple")
	require.NotContains(t, result.Stdout(), "failing")
}

func TestListNextShowsScheduleOrDash(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	dagu.WriteFile("scheduled.yaml", "schedule: \"0 0 * * *\"\nworking_dir: .\nsteps:\n  - id: noop\n    run: \"true\"\n")

	result := dagu.RunWithEnv(env, "ls", "-n")
	result.ExpectExitCode(0)

	rows := nextRunByName(result.Stdout())
	require.Contains(t, rows, "scheduled", "ls -n output must include a row for the scheduled DAG")
	require.NotEqual(t, "-", rows["scheduled"], "a scheduled DAG must show a next run time, not -")
	require.Equal(t, "-", rows["simple"], "an unscheduled DAG must show - for next run")
}

func TestListLastShowsRunStatus(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	started := dagu.RunWithEnv(env, "start", "simple.yaml")
	started.ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "ls", "-l", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "Succeeded")
}

func TestListHistorySummaryShowsRecentRuns(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	started := dagu.RunWithEnv(env, "start", "simple.yaml")
	started.ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "ls", "-H", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "HISTORY")
	require.Regexp(t, `(?m)^simple\s+.*Succeeded`, result.Stdout())
}

// nextRunByName maps DAG name -> NEXT_RUN cell from `dagu ls -n` output. Both
// columns are single whitespace-free tokens (a name, an RFC 3339 timestamp,
// or "-"), so splitting each row on whitespace is sufficient regardless of
// tabwriter padding.
func nextRunByName(stdout string) map[string]string {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	result := make(map[string]string, len(lines))
	for _, line := range lines[1:] { // skip header row
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		result[fields[0]] = fields[1]
	}
	return result
}
