// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/stretchr/testify/require"
)

// narrowFleet adds a db node to fleet.
var narrowFleet = append(append([]inventory.Node{}, fleet...), inventory.Node{Name: "db01", Host: "10.0.1.21", Labels: []string{"db"}})

// startNarrowed runs dagu start with a DAG file, --node flags, and the param
// DIR, against narrowFleet.
func startNarrowed(t *testing.T, dagu *harness.Runner, file string, extra ...string) *harness.Result {
	t.Helper()
	args := append([]string{"start"}, extra...)
	args = append(args, file, "--", "DIR="+filepath.ToSlash(dagu.ProjectPath(".")))
	return dagu.RunWithEnv([]string{"DAGU_DATA_DIR=" + nodesDataDirWith(t, narrowFleet)}, args...)
}

// A run started with --node narrows its steps with nodes to the listed nodes
// (Spec 089 §Narrowing a run).
func TestNarrowedRuns(t *testing.T) {
	t.Parallel()

	// A step none of whose nodes is listed is skipped, and the steps after it
	// still run. Names are matched without case.
	t.Run("runs the listed nodes and skips a step with none listed", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		startNarrowed(t, dagu, "narrow.yaml", "--node", "WEB03").ExpectExitCode(0)
		require.Equal(t, []string{"web03=succeeded"}, nodeStatuses(t, dagu))
		dagu.ExpectFileContent("notified", "done")
	})

	t.Run("fails when the list leaves every step with nodes without a node", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := startNarrowed(t, dagu, "fanout.yaml", "--node", "db01")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no listed node was selected")
		dagu.ExpectNoFile("child-ran")
	})

	t.Run("binds a node: required DAG to its one node", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		startNarrowed(t, dagu, "node_required_direct.yaml", "--node", "web01").ExpectExitCode(0)
		dagu.ExpectFileContent("bound-web01", "ran")
	})

	// A narrowed run that fails keeps its list when retried.
	t.Run("retry keeps the list", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := []string{"DAGU_DATA_DIR=" + nodesDataDirWith(t, narrowFleet)}
		dagu.RunWithEnv(env, "start", "--run-id=narrowed-1", "--node", "web03", "stop_at_failure.yaml").ExpectNonZeroExitCode()
		require.Equal(t, []string{"web03=failed"}, nodeStatuses(t, dagu))

		// The retry appends its aggregate to the step's stdout file.
		require.NoError(t, fileutil.Remove(dagu.ProjectPath("nodes.json")))
		dagu.RunWithEnv(env, "retry", "--run-id=narrowed-1", "stop_at_failure").ExpectNonZeroExitCode()
		require.Equal(t, []string{"web03=failed"}, nodeStatuses(t, dagu))
	})

	// A new run from a narrowed run keeps its list, as it keeps its params,
	// so it takes no list of its own.
	t.Run("a new run from a run keeps the list", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := []string{"DAGU_DATA_DIR=" + nodesDataDirWith(t, narrowFleet)}
		dir := "DIR=" + filepath.ToSlash(dagu.ProjectPath("."))
		dagu.RunWithEnv(env, "start", "--run-id=narrowed-2", "--node", "web03", "narrow.yaml", "--", dir).ExpectExitCode(0)

		require.NoError(t, fileutil.Remove(dagu.ProjectPath("nodes.json")))
		dagu.RunWithEnv(env, "start", "--from-run-id=narrowed-2", "narrow").ExpectExitCode(0)
		require.Equal(t, []string{"web03=succeeded"}, nodeStatuses(t, dagu))

		result := dagu.RunWithEnv(env, "start", "--from-run-id=narrowed-2", "--node", "web01", "narrow")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("--node cannot be combined with --from-run-id")
	})
}

// A list that names no node of the project, or that the DAG cannot take, is
// refused before any run starts.
func TestNarrowedRunsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		file      string
		nodes     []string
		wantError string
	}{
		{"unknown node", "narrow.yaml", []string{"web1"}, `node "web1" is not a node of the project`},
		{"nothing to narrow", "plain.yaml", []string{"web01"}, "a node list narrows nothing"},
		{"node: required with two nodes", "node_required_direct.yaml", []string{"web01", "web03"}, "starts with exactly one node, not 2"},
		{"node: required with no node", "node_required_direct.yaml", nil, "or started with one node (--node)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var flags []string
			for _, node := range tc.nodes {
				flags = append(flags, "--node", node)
			}
			dagu := harness.NewRunner(t)
			result := startNarrowed(t, dagu, tc.file, flags...)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantError)
			dagu.ExpectNoFile("notified")
			dagu.ExpectNoFile("bound-web01")
		})
	}
}
