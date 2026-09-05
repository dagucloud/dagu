// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// This file covers flag-level depth for commands whose core behavior is
// already exercised elsewhere: enqueue, status, history, dry, and start.
package cli_test

import (
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

var subdagIDPattern = regexp.MustCompile(`subdag:\s*(\S+)`)

// TestStatusSubRunIDAddressesChildRun proves --sub-run-id lets `dagu status`
// address a child DAG run directly, and that it requires --run-id.
func TestStatusSubRunIDAddressesChildRun(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-depth-parent"

	started := dagu.RunWithEnv(env, "start", "--run-id="+runID, "parent_child.yaml")
	started.ExpectExitCode(0)

	match := subdagIDPattern.FindStringSubmatch(started.Stdout())
	require.Lenf(t, match, 2, "expected a subdag id in start output:\n%s", started.Stdout())
	childRunID := match[1]

	withoutRunID := dagu.RunWithEnv(env, "status", "--sub-run-id="+childRunID, "parent_child.yaml")
	withoutRunID.ExpectNonZeroExitCode()
	withoutRunID.ExpectStderrContains("--sub-run-id requires --run-id")

	withRunID := dagu.RunWithEnv(env, "status", "--run-id="+runID, "--sub-run-id="+childRunID, "parent_child.yaml")
	withRunID.ExpectExitCode(0)
	require.Contains(t, withRunID.Stdout(), "dag: depth_child")
}

// TestEnqueueQueueFlagSelectsQueue proves --queue overrides the DAG's
// default process group as the queue an enqueued run lands in.
func TestEnqueueQueueFlagSelectsQueue(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-depth-enqueue-queue"

	dagu.RunWithEnv(env, "enqueue", "--run-id="+runID, "--queue=custom-queue", "simple.yaml").ExpectExitCode(0)

	// The default queue (the DAG's own name) must be empty...
	wrongQueue := dagu.RunWithEnv(env, "dequeue", "simple")
	wrongQueue.ExpectNonZeroExitCode()
	wrongQueue.ExpectStderrContains("no dag-run found in queue")

	// ...while the overridden queue holds the item.
	rightQueue := dagu.RunWithEnv(env, "dequeue", "custom-queue")
	rightQueue.ExpectExitCode(0)
}

// TestEnqueueLabelsFlowIntoHistory proves --labels on enqueue is recorded and
// that `dagu history --labels` filters by it.
func TestEnqueueLabelsFlowIntoHistory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const labeledRunID = "cli-depth-labeled"
	const plainRunID = "cli-depth-plain"

	dagu.RunWithEnv(env, "enqueue", "--run-id="+labeledRunID, "--labels=env=prod", "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "enqueue", "--run-id="+plainRunID, "simple.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "history", "simple", "--labels=env=prod")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), labeledRunID)
	require.NotContains(t, result.Stdout(), plainRunID)
}

// TestHistoryStatusFilterAndLimit proves --status filters by outcome and
// --limit caps how many results are returned.
func TestHistoryStatusFilterAndLimit(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	dagu.RunWithEnv(env, "start", "--run-id=cli-depth-hist-ok-1", "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "start", "--run-id=cli-depth-hist-ok-2", "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "start", "--run-id=cli-depth-hist-fail-1", "failing.yaml")

	onlyFailed := dagu.RunWithEnv(env, "history", "--status=failed")
	onlyFailed.ExpectExitCode(0)
	require.Contains(t, onlyFailed.Stdout(), "cli-depth-hist-fail-1")
	require.NotContains(t, onlyFailed.Stdout(), "cli-depth-hist-ok-1")
	require.NotContains(t, onlyFailed.Stdout(), "cli-depth-hist-ok-2")

	limited := dagu.RunWithEnv(env, "history", "simple", "--limit=1")
	limited.ExpectExitCode(0)
	matches := regexp.MustCompile(`cli-depth-hist-ok-\d`).FindAllString(limited.Stdout(), -1)
	require.Len(t, matches, 1, "expected --limit=1 to cap results to one run:\n%s", limited.Stdout())
}

// TestDryRunResolvesParamsWithoutExecuting proves `dagu dry --params`
// resolves parameter values (a missing required param would fail dry too)
// while still performing no real action.
func TestDryRunResolvesParamsWithoutExecuting(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "dry", "--params", "greeting=hello-dry", "params_dag.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectNoFile("params_dag.out")

	missing := dagu.RunWithEnv(env, "dry", "params_dag.yaml")
	missing.ExpectNonZeroExitCode()
}

// TestStartLabelsFlowIntoHistory proves --labels on `start` is recorded the
// same way it is for enqueue.
func TestStartLabelsFlowIntoHistory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-depth-start-labeled"

	dagu.RunWithEnv(env, "start", "--run-id="+runID, "--labels=env=prod", "simple.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "history", "simple", "--labels=env=prod")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), runID)
}
