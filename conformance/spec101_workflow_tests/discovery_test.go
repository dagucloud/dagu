// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec101_workflow_tests_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// A test file beside its DAG is not a DAG: dagu ls does not list it and
// dagu start does not run it, but dagu test without arguments runs it
// against the DAG beside it.
func TestTestFileIsNotADAG(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("dags")
	for _, name := range []string{"release.yaml", "release.test.yaml"} {
		data, err := os.ReadFile(dagu.ProjectPath("workflows/" + name))
		require.NoError(t, err)
		dagu.WriteFile("dags/"+name, string(data))
	}
	env := []string{"DAGU_DAGS_DIR=" + dagu.ProjectPath("dags")}

	ls := dagu.RunWithEnv(env, "ls")
	ls.ExpectExitCode(0)
	require.Contains(t, ls.Stdout(), "release")
	ls.ExpectStdoutNotContains("release.test")

	start := dagu.RunWithEnv(env, "start", "release.test")
	start.ExpectNonZeroExitCode()
	start.ExpectStderrContains("dagu test")

	run := dagu.RunWithEnv(env, "test")
	run.ExpectExitCode(0)
	expectLines(t, run.Stdout(),
		"PASS dags/release.test.yaml: deploys the built image (",
		"2 cases: 2 passed, 0 failed\n",
	)
}

// A DAG saved under a test file name no longer runs, so the scheduler warns
// about it once at startup, and dagu test reports it with the same rename
// hint. A real test file beside it draws no warning.
func TestTestNamedDAG(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Mkdir("dags")
	dagu.WriteFile("dags/smoke.test.yaml", "steps:\n  - run: echo smoke\n")
	for _, name := range []string{"release.yaml", "release.test.yaml"} {
		data, err := os.ReadFile(dagu.ProjectPath("workflows/" + name))
		require.NoError(t, err)
		dagu.WriteFile("dags/"+name, string(data))
	}
	env := []string{"DAGU_DAGS_DIR=" + dagu.ProjectPath("dags")}
	const hint = "reads as a DAG, with steps and no cases, but Dagu treats it as a test file and does not run it as a DAG; rename it, for example to smoke-test.yaml"

	scheduler := dagu.StartWithEnv(env, "scheduler")
	require.Eventually(t, func() bool {
		return strings.Contains(scheduler.Output(), "Scheduler started")
	}, harness.WaitTimeout(t), 50*time.Millisecond)
	output := scheduler.Output()
	require.Equal(t, 1, strings.Count(output, "smoke.test.yaml "+hint), output)
	require.NotContains(t, output, "release.test.yaml", output)

	run := dagu.RunWithEnv(env, "test", "dags/smoke.test.yaml")
	run.ExpectExitCode(2)
	expectLines(t, run.Stdout(), "INVALID dags/smoke.test.yaml\n", "  "+hint+"\n")
}
