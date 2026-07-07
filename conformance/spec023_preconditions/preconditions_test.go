// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec023_preconditions_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestRuntimeValueMatchCommandSubstitutionUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name    string
		file    string
		output  string
		content string
		setup   func(*harness.Runner)
	}{
		{
			name:    "dag-level backtick substitution matches expected value",
			file:    "root_value_match_backtick.yaml",
			output:  "root-backtick.txt",
			content: "root\n",
		},
		{
			name:    "step-level backtick substitution matches expected value",
			file:    "step_value_match_backtick.yaml",
			output:  "step-backtick.txt",
			content: "step\n",
		},
		{
			name:    "step-level dollar paren substitution matches regex expected value",
			file:    "step_value_match_dollar.yaml",
			output:  "step-dollar.txt",
			content: "dollar\n",
		},
		{
			name:    "dagu references resolve before command substitution",
			file:    "value_match_resolves_refs_first.yaml",
			output:  "refs-first.txt",
			content: "refs\n",
		},
		{
			name:    "step substitution receives step working directory and env",
			file:    "value_match_step_context.yaml",
			output:  "workspace/context-ran.txt",
			content: "context\n",
			setup: func(dagu *harness.Runner) {
				dagu.Mkdir("workspace")
				dagu.WriteFile("workspace/ready.flag", "")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			if tc.setup != nil {
				tc.setup(dagu)
			}
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestRuntimeCommandCheckPreconditionsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "command_check_shell_substitution.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("command-check.txt", "command\n")
}

func TestRuntimePreconditionOutcomesUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name       string
		file       string
		exitCode   int
		absentFile string
	}{
		{
			name:       "value-match mismatch skips step without running action",
			file:       "value_match_not_met_skips.yaml",
			exitCode:   0,
			absentFile: "not-met-ran.txt",
		},
		{
			name:       "value-match substitution failure fails owning step",
			file:       "value_match_substitution_failure.yaml",
			exitCode:   1,
			absentFile: "failure-ran.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(tc.exitCode)
			dagu.ExpectNoFile(tc.absentFile)
		})
	}
}

func TestValidateDoesNotExecutePreconditionCommandSubstitutionUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "validate_does_not_execute.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	dagu.ExpectNoFile("validate-substitution-ran.txt")
	dagu.ExpectNoFile("validate-runtime-ran.txt")
}
