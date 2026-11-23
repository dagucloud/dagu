// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec101_workflow_tests_test

import (
	"os"
	"testing"

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
