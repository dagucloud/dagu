// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec033_incremental_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestIncrementalWorkflowMaterializationLifecycle(t *testing.T) {
	dagu := harness.NewRunner(t)
	dagu.Mkdir(".home")
	dagu.Mkdir(".xdg")
	env := []string{
		"DAGU_HOME=" + dagu.ProjectPath(".dagu"),
		"HOME=" + dagu.ProjectPath(".home"),
		"XDG_CONFIG_HOME=" + dagu.ProjectPath(".xdg"),
		"APPDATA=" + dagu.ProjectPath(".home"),
		"USERPROFILE=" + dagu.ProjectPath(".home"),
	}
	run := func(args ...string) *harness.Result {
		return dagu.RunWithEnv(env, args...)
	}

	first := run("start", "incremental.yaml")
	first.ExpectExitCode(0)
	dagu.ExpectFileContent("result.txt", "alpha\n")
	dagu.ExpectFileContent("build-count.txt", "1\n")
	dagu.ExpectFileContent("consume-count.txt", "1\n")
	dagu.ExpectFileContent("controlled-count.txt", "1\n")
	dagu.ExpectFileContent("always-count.txt", "1\n")

	second := run("start", "incremental.yaml")
	second.ExpectExitCode(0)
	require.Contains(t, second.Stdout(), "incremental: reuse (matched)")
	dagu.ExpectFileContent("build-count.txt", "1\n")
	dagu.ExpectFileContent("consume-count.txt", "1\n")
	dagu.ExpectFileContent("controlled-count.txt", "1\n")
	dagu.ExpectFileContent("always-count.txt", "2\n")

	dry := run("dry", "incremental.yaml")
	dry.ExpectExitCode(0)
	require.Contains(t, dry.Stdout(), "incremental: reuse (matched)")
	dagu.ExpectFileContent("build-count.txt", "1\n")
	dagu.ExpectFileContent("consume-count.txt", "1\n")

	forced := run("start", "--no-reuse", "incremental.yaml")
	forced.ExpectExitCode(0)
	require.Contains(t, forced.Stdout(), "incremental: execute (reuse_disabled)")
	dagu.ExpectFileContent("build-count.txt", "2\n")
	dagu.ExpectFileContent("consume-count.txt", "2\n")
	dagu.ExpectFileContent("controlled-count.txt", "2\n")

	dagu.WriteFile("source.txt", "beta\n")
	changed := run("start", "incremental.yaml")
	changed.ExpectExitCode(0)
	dagu.ExpectFileContent("result.txt", "beta\n")
	dagu.ExpectFileContent("build-count.txt", "3\n")
	dagu.ExpectFileContent("consume-count.txt", "3\n")
	dagu.ExpectFileContent("controlled-count.txt", "3\n")

	dagu.WriteFile("fail.txt", "fail\n")
	failed := run("start", "--no-reuse", "incremental.yaml")
	failed.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("intermediate.txt", "beta\n")
	dagu.ExpectFileContent("result.txt", "beta\n")

	require.NoError(t, os.Remove(dagu.ProjectPath("fail.txt")))
	require.NoError(t, os.Remove(dagu.ProjectPath("source.txt")))
	skipped := run("start", "--no-reuse", "incremental.yaml")
	skipped.ExpectExitCode(0)
	require.Contains(t, skipped.Stdout(), "incremental: none (precondition_not_met)")
	dagu.ExpectFileContent("intermediate.txt", "beta\n")
	dagu.ExpectFileContent("result.txt", "beta\n")
}
