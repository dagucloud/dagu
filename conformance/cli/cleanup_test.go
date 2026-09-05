// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestCleanupIsAnAliasForRemoveHistory proves the deprecated `dagu cleanup`
// command still works as documented: "Equivalent with rm: dagu rm -H".
func TestCleanupIsAnAliasForRemoveHistory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	dagu.RunWithEnv(env, "start", "simple.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "cleanup", "-y", "simple")
	result.ExpectExitCode(0)

	repeat := dagu.RunWithEnv(env, "rm", "-H", "-f", "simple")
	repeat.ExpectExitCode(0)
	require.Contains(t, repeat.Stdout(), `No runs to delete for DAG "simple"`)
}

func TestCleanupDryRunPreviewsWithoutDeleting(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	dagu.RunWithEnv(env, "start", "simple.yaml").ExpectExitCode(0)

	preview := dagu.RunWithEnv(env, "cleanup", "--dry-run", "-y", "simple")
	preview.ExpectExitCode(0)
	require.Contains(t, preview.Stdout(), `Dry run: Would delete 1 run(s) for DAG "simple"`)

	result := dagu.RunWithEnv(env, "rm", "-H", "-f", "simple")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), `Successfully removed 1 run(s) for DAG "simple"`)
}
