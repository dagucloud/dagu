// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec098_sub_dag_run_visibility_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// A child that ran only under a parent's dag.run reports that run as its last
// run, while dagu status still finds no run of the child's own.
func TestSubDAGRunVisibility(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"), "DAGU_DAGS_DIR=."}

	before := dagu.RunWithEnv(env, "ls", "--last", "child")
	before.ExpectExitCode(0)
	require.Equal(t, []string{"child", "-", "-"}, lsRow(t, before.Stdout(), "child"))

	dagu.RunWithEnv(env, "start", "parent.yaml").ExpectExitCode(0)

	after := dagu.RunWithEnv(env, "ls", "--last", "child")
	after.ExpectExitCode(0)
	require.Equal(t, "Succeeded", lsRow(t, after.Stdout(), "child")[1])

	dagu.RunWithEnv(env, "status", "child").ExpectNonZeroExitCode()
}

// lsRow returns the fields of the dagu ls row for name.
func lsRow(t *testing.T, stdout, name string) []string {
	t.Helper()
	for line := range strings.SplitSeq(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return fields
		}
	}
	t.Fatalf("no dagu ls row for %s in:\n%s", name, stdout)
	return nil
}
