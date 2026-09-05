// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestPsShowsNothingWhenIdle(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "ps")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "No running processes")
}

func TestPsShowsRunningProcess(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-ps-run"

	proc := startBackgroundAndWaitFor(t, dagu, env, "started.out", "start", "--run-id="+runID, "long_running.yaml")
	defer proc.Stop()

	result := dagu.RunWithEnv(env, "ps")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "long_running")
	require.Contains(t, result.Stdout(), runID)
}

func TestPsFiltersByDAGNameAndRunID(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-ps-filter-run"

	proc := startBackgroundAndWaitFor(t, dagu, env, "started.out", "start", "--run-id="+runID, "long_running.yaml")
	defer proc.Stop()

	byName := dagu.RunWithEnv(env, "ps", "-d", "long_running")
	byName.ExpectExitCode(0)
	require.Contains(t, byName.Stdout(), runID)

	wrongName := dagu.RunWithEnv(env, "ps", "-d", "simple")
	wrongName.ExpectExitCode(0)
	require.Contains(t, wrongName.Stdout(), "No running processes")

	byRunID := dagu.RunWithEnv(env, "ps", "-r", "ps-filter")
	byRunID.ExpectExitCode(0)
	require.Contains(t, byRunID.Stdout(), runID)

	wrongRunID := dagu.RunWithEnv(env, "ps", "-r", "does-not-exist")
	wrongRunID.ExpectExitCode(0)
	require.Contains(t, wrongRunID.Stdout(), "No running processes")
}
