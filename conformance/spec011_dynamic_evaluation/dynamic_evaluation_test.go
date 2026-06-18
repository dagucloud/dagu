// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec011_dynamic_evaluation_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

type runtimeCase struct {
	name    string
	file    string
	output  string
	content string
	setup   func(*testing.T)
}

func TestRuntimeDynamicEvaluation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX command snippets")
	}

	cases := []runtimeCase{
		{
			name:    "backtick substitution populates param",
			file:    "params_eval_backtick.yaml",
			output:  "backtick.txt",
			content: "20260131\n",
		},
		{
			name:    "dollar paren substitution populates param",
			file:    "params_eval_dollar.yaml",
			output:  "dollar.txt",
			content: "20260131\n",
		},
		{
			name:    "dagu refs resolve before command substitution",
			file:    "params_eval_reference_order.yaml",
			output:  "reference-order.txt",
			content: "prod-api\n",
		},
		{
			name:   "scoped env resolves before command substitution",
			file:   "params_eval_env_order.yaml",
			output: "env-order.txt",
			setup: func(t *testing.T) {
				t.Setenv("SPEC011_DYNAMIC_VALUE", "scope")
			},
			content: "scope-prod\n",
		},
		{
			name:    "failed eval falls back to default",
			file:    "params_eval_failure_fallback.yaml",
			output:  "fallback.txt",
			content: "fallback-date\n",
		},
		{
			name:    "dollar paren text outside params eval stays literal",
			file:    "outside_params_eval_dollar_literal.yaml",
			output:  "outside-dollar.txt",
			content: "$(printf should-not-run)\n",
		},
		{
			name:    "backtick text outside params eval stays literal",
			file:    "outside_params_eval_backtick_literal.yaml",
			output:  "outside-backtick.txt",
			content: "`printf should-not-run`\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestRuntimeDynamicEvaluationWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("fixtures use Windows command snippets")
	}

	cases := []runtimeCase{
		{
			name:    "backtick substitution populates param",
			file:    "win_eval_backtick.yaml",
			output:  "windows-backtick.txt",
			content: "20260131\r\n",
		},
		{
			name:    "dollar paren substitution populates param",
			file:    "win_eval_dollar.yaml",
			output:  "windows-dollar.txt",
			content: "20260131\r\n",
		},
		{
			name:    "dagu refs resolve before command substitution",
			file:    "win_eval_ref_order.yaml",
			output:  "windows-reference-order.txt",
			content: "prod-api\r\n",
		},
		{
			name:   "scoped env resolves before command substitution",
			file:   "win_eval_env_order.yaml",
			output: "windows-env-order.txt",
			setup: func(t *testing.T) {
				t.Setenv("SPEC011_DYNAMIC_VALUE", "scope")
			},
			content: "scope-prod\r\n",
		},
		{
			name:    "failed eval falls back to default",
			file:    "win_eval_fallback.yaml",
			output:  "windows-fallback.txt",
			content: "fallback-date\r\n",
		},
		{
			name:    "dollar paren text outside params eval stays literal",
			file:    "win_outside_dollar.yaml",
			output:  "windows-outside-dollar.txt",
			content: "$(Write-Output should-not-run)\r\n",
		},
		{
			name:    "backtick text outside params eval stays literal",
			file:    "win_outside_backtick.yaml",
			output:  "windows-outside-backtick.txt",
			content: "`Write-Output should-not-run`\r\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"DAGU_DEFAULT_SHELL=powershell"}, "start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestDynamicEvaluationFailureWithoutDefaultFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX command snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "params_eval_failure_no_default.yaml")
	result.ExpectExitCode(1)
	result.ExpectStderrContains("params", "eval failed")
	dagu.ExpectNoFile("should-not-run.txt")
}

func TestDynamicEvaluationFailureWithoutDefaultFailsWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("fixture uses Windows command snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"DAGU_DEFAULT_SHELL=powershell"}, "start", "win_eval_no_default.yaml")
	result.ExpectExitCode(1)
	result.ExpectStderrContains("params", "eval failed")
	dagu.ExpectNoFile("windows-should-not-run.txt")
}

func TestValidateParsesButDoesNotExecuteDynamicEvaluation(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX command snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "validate_does_not_execute.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	dagu.ExpectNoFile("validate-executed.txt")
}

func TestValidateParsesButDoesNotExecuteDynamicEvaluationWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("fixture uses Windows command snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"DAGU_DEFAULT_SHELL=powershell"}, "validate", "win_validate_no_exec.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	dagu.ExpectNoFile("windows-validate-executed.txt")
}
