// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestRemoveRequiresHistoryOrDefinition(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "rm", "simple")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("--history", "--definition")
}

func TestRemoveOlderThanRequiresHistory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "rm", "-d", "-t", "10d", "simple")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("--older-than", "--history")
}

func TestRemoveWithoutForceIsCancelled(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "rm", "-d", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "Cancelled.")
	require.FileExists(t, dagu.ProjectPath("simple.yaml"))
}

func TestRemoveDefinitionWithForce(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "rm", "-d", "-f", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), `Successfully deleted DAG definition "simple"`)
	dagu.ExpectNoFile("simple.yaml")
}

func TestRemoveHistoryWithForce(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	started := dagu.RunWithEnv(env, "start", "simple.yaml")
	started.ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "rm", "-H", "-f", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), `Successfully removed 1 run(s) for DAG "simple"`)

	repeat := dagu.RunWithEnv(env, "rm", "-H", "-f", "simple")
	repeat.ExpectExitCode(0)
	require.Contains(t, repeat.Stdout(), `No runs to delete for DAG "simple"`)
}

func TestRemoveHistoryDryRunPreviewsWithoutDeleting(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	started := dagu.RunWithEnv(env, "start", "simple.yaml")
	started.ExpectExitCode(0)

	preview := dagu.RunWithEnv(env, "rm", "-H", "--dry-run", "-f", "simple")
	preview.ExpectExitCode(0)
	require.Contains(t, preview.Stdout(), `Dry run: Would delete 1 run(s) for DAG "simple"`)

	// The preview must not have actually deleted anything: a real removal
	// afterward still finds the same run to delete.
	result := dagu.RunWithEnv(env, "rm", "-H", "-f", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), `Successfully removed 1 run(s) for DAG "simple"`)
}

// TestRemoveDefinitionRefusedWhileActive proves "Definition deletion is
// refused while the DAG has alive processes": attempting -d against a DAG
// with an in-flight run must fail without deleting anything, even with
// --force.
func TestRemoveDefinitionRefusedWhileActive(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	proc := startBackgroundAndWaitFor(t, dagu, env, "started.out", "start", "long_running.yaml")
	defer proc.Stop()

	result := dagu.RunWithEnv(env, "rm", "-d", "-f", "long_running")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("alive process")
	require.FileExists(t, dagu.ProjectPath("long_running.yaml"))
}
