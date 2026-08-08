// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec033_build_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestBuildWorkflowMaterializationLifecycle(t *testing.T) {
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

	first := run("start", "build.yaml")
	first.ExpectExitCode(0)
	dagu.ExpectTextFileContent("result.txt", "alpha\n")
	dagu.ExpectTextFileContent("build-count.txt", "1\n")
	dagu.ExpectTextFileContent("consume-count.txt", "1\n")
	dagu.ExpectTextFileContent("controlled-count.txt", "1\n")
	dagu.ExpectTextFileContent("always-count.txt", "1\n")

	second := run("start", "build.yaml")
	second.ExpectExitCode(0)
	require.Contains(t, second.Stdout(), "build: reuse (matched)")
	dagu.ExpectTextFileContent("build-count.txt", "1\n")
	dagu.ExpectTextFileContent("consume-count.txt", "1\n")
	dagu.ExpectTextFileContent("controlled-count.txt", "1\n")
	dagu.ExpectTextFileContent("always-count.txt", "2\n")

	dry := run("dry", "build.yaml")
	dry.ExpectExitCode(0)
	require.Contains(t, dry.Stdout(), "build: reuse (matched)")
	dagu.ExpectTextFileContent("build-count.txt", "1\n")
	dagu.ExpectTextFileContent("consume-count.txt", "1\n")

	forced := run("start", "--no-reuse", "build.yaml")
	forced.ExpectExitCode(0)
	require.Contains(t, forced.Stdout(), "build: execute (reuse_disabled)")
	dagu.ExpectTextFileContent("build-count.txt", "2\n")
	dagu.ExpectTextFileContent("consume-count.txt", "2\n")
	dagu.ExpectTextFileContent("controlled-count.txt", "2\n")

	dagu.WriteFile("source.txt", "beta\n")
	changed := run("start", "build.yaml")
	changed.ExpectExitCode(0)
	dagu.ExpectTextFileContent("result.txt", "beta\n")
	dagu.ExpectTextFileContent("build-count.txt", "3\n")
	dagu.ExpectTextFileContent("consume-count.txt", "3\n")
	dagu.ExpectTextFileContent("controlled-count.txt", "3\n")

	dagu.WriteFile("fail.txt", "fail\n")
	failed := run("start", "--no-reuse", "build.yaml")
	failed.ExpectNonZeroExitCode()
	dagu.ExpectTextFileContent("intermediate.txt", "beta\n")
	dagu.ExpectTextFileContent("result.txt", "beta\n")

	require.NoError(t, os.Remove(dagu.ProjectPath("fail.txt")))
	require.NoError(t, os.Remove(dagu.ProjectPath("source.txt")))
	skipped := run("start", "--no-reuse", "build.yaml")
	skipped.ExpectExitCode(0)
	require.Contains(t, skipped.Stdout(), "build: none (precondition_not_met)")
	dagu.ExpectTextFileContent("intermediate.txt", "beta\n")
	dagu.ExpectTextFileContent("result.txt", "beta\n")
}

func TestBuildWorkflowRejectsDuplicateOutputProducers(t *testing.T) {
	dagu := harness.NewRunner(t)
	result := dagu.Run("dry", "duplicate-output.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("multiple producers", "shared.txt")
}
