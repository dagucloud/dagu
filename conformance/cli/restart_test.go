// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// restartedRunIDPattern matches restart's own announcement of the new
// dag-run it starts in place of the one it stopped, e.g.:
// `msg="Dag-run restart initiated" dag=restartable run-id=<new-id> file=...`
var restartedRunIDPattern = regexp.MustCompile(`Dag-run restart initiated" dag=\S+ run-id=(\S+)`)

// TestRestartReplacesRunningAttempt proves `dagu restart` stops the active
// run and immediately starts a fresh one with a new run ID: the step marker
// (appended, not overwritten) shows exactly two executions -- the original
// and the restarted one -- the restarted run has a genuinely different run
// ID from the original (a restart that reused the original ID would still
// satisfy the marker-count assertion alone), and the original run ID is no
// longer reported running once restart has taken over.
func TestRestartReplacesRunningAttempt(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const originalRunID = "cli-restart-original"

	proc := startBackgroundAndWaitFor(t, dagu, env, "restart_marks.out", "start", "--run-id="+originalRunID, "restartable.yaml")
	defer proc.Stop()

	restart := dagu.RunWithEnv(env, "restart", "restartable")
	restart.ExpectExitCode(0)

	match := restartedRunIDPattern.FindStringSubmatch(restart.Stderr())
	require.Lenf(t, match, 2, "expected restart to announce the new run's id:\n%s", restart.Stderr())
	require.NotEqual(t, originalRunID, match[1], "restart must start a run with a new id, not reuse the original")

	waitForProcessDone(t, proc)

	dagu.ExpectFileContent("restart_marks.out", "started\nstarted\n")

	// Finalizing the aborted original attempt is asynchronous with the
	// process actually exiting, so poll for its terminal status rather than
	// checking once immediately after the process exits.
	waitForStatus(t, dagu, env, originalRunID, "restartable.yaml", "Aborted")
	waitForStatus(t, dagu, env, match[1], "restartable.yaml", "Succeeded")
}
