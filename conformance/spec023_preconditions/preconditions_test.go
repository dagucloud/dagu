// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec023_preconditions_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidatePreconditions(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"valid_string_shortcut.yaml",
		"valid_empty_array.yaml",
		"valid_missing_command_check.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("validate-runtime-ran.txt")
		})
	}

	invalidCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "root preconditions object",
			file:        "invalid_preconditions_object.yaml",
			stderrParts: []string{"preconditions"},
		},
		{
			name:        "array item scalar",
			file:        "invalid_array_item_scalar.yaml",
			stderrParts: []string{"preconditions"},
		},
		{
			name:        "empty string shortcut",
			file:        "invalid_string_empty.yaml",
			stderrParts: []string{"preconditions"},
		},
		{
			name:        "missing condition",
			file:        "invalid_missing_condition.yaml",
			stderrParts: []string{"preconditions", "condition"},
		},
		{
			name:        "empty condition",
			file:        "invalid_condition_empty.yaml",
			stderrParts: []string{"preconditions", "condition"},
		},
		{
			name:        "non-string condition",
			file:        "invalid_condition_non_string.yaml",
			stderrParts: []string{"preconditions", "condition"},
		},
		{
			name:        "non-string expected",
			file:        "invalid_expected_non_string.yaml",
			stderrParts: []string{"preconditions", "expected"},
		},
		{
			name:        "empty expected",
			file:        "invalid_expected_empty.yaml",
			stderrParts: []string{"preconditions", "expected"},
		},
		{
			name:        "non-bool negate",
			file:        "invalid_negate_non_bool.yaml",
			stderrParts: []string{"preconditions", "negate"},
		},
		{
			name:        "unknown field",
			file:        "invalid_unknown_field.yaml",
			stderrParts: []string{"preconditions", "actual"},
		},
		{
			name:        "legacy command field",
			file:        "invalid_legacy_command.yaml",
			stderrParts: []string{"preconditions", "command"},
		},
		{
			name:        "invalid regex",
			file:        "invalid_regex.yaml",
			stderrParts: []string{"preconditions", "expected", "regexp"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}

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

func TestRuntimeDAGLevelPreconditionStatusEffectsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name        string
		file        string
		exitCode    *int
		nonZero     bool
		files       map[string]string
		absentFiles []string
		setup       func(*harness.Runner)
	}{
		{
			name: "value-match not-met aborts before init and steps",
			file: "root_value_match_not_met_aborts.yaml",
			files: map[string]string{
				"abort-ran.txt": "abort\n",
			},
			absentFiles: []string{
				"init-ran.txt",
				"failure-ran.txt",
				"main-ran.txt",
			},
		},
		{
			name:    "value-match substitution error fails before steps",
			file:    "root_value_match_substitution_failure_fails.yaml",
			nonZero: true,
			files: map[string]string{
				"failure-ran.txt": "failure\n",
			},
			absentFiles: []string{
				"abort-ran.txt",
				"init-ran.txt",
				"main-ran.txt",
			},
		},
		{
			name: "command-check not-met aborts before init and steps",
			file: "root_command_check_not_met_aborts.yaml",
			files: map[string]string{
				"abort-ran.txt": "abort\n",
			},
			absentFiles: []string{
				"init-ran.txt",
				"failure-ran.txt",
				"main-ran.txt",
			},
		},
		{
			name:     "dag-level substitution receives root working directory and env",
			file:     "root_value_match_context.yaml",
			exitCode: new(int),
			files: map[string]string{
				"rootctx/root-context-ran.txt": "root-context\n",
			},
			setup: func(dagu *harness.Runner) {
				dagu.Mkdir("rootctx")
				dagu.WriteFile("rootctx/ready.flag", "")
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
			if tc.exitCode != nil {
				result.ExpectExitCode(*tc.exitCode)
			}
			if tc.nonZero {
				result.ExpectNonZeroExitCode()
			}
			for file, content := range tc.files {
				dagu.ExpectFileContent(file, content)
			}
			for _, file := range tc.absentFiles {
				dagu.ExpectNoFile(file)
			}
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

func TestRuntimeStepLevelPreconditionStatusEffectsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name        string
		file        string
		absentFiles []string
	}{
		{
			name: "skipped step blocks dependent step by default",
			file: "step_skip_blocks_dependent.yaml",
			absentFiles: []string{
				"optional-ran.txt",
				"dependent-ran.txt",
			},
		},
		{
			name: "skipped step action is not retried or repeated",
			file: "step_skip_does_not_retry_or_repeat.yaml",
			absentFiles: []string{
				"policy-action.txt",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			for _, file := range tc.absentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func TestRuntimeNegatedPreconditionsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name       string
		file       string
		exitCode   int
		outputFile string
		content    string
		absentFile string
	}{
		{
			name:       "negated value-match mismatch passes",
			file:       "negate_value_mismatch_runs.yaml",
			exitCode:   0,
			outputFile: "negate-value-mismatch.txt",
			content:    "ran\n",
		},
		{
			name:       "negated value-match match skips",
			file:       "negate_value_match_skips.yaml",
			exitCode:   0,
			absentFile: "negate-value-match.txt",
		},
		{
			name:       "negated command-check failure passes",
			file:       "negate_command_failure_runs.yaml",
			exitCode:   0,
			outputFile: "negate-command-failure.txt",
			content:    "ran\n",
		},
		{
			name:       "negated command-check success skips",
			file:       "negate_command_success_skips.yaml",
			exitCode:   0,
			absentFile: "negate-command-success.txt",
		},
		{
			name:       "negation does not convert evaluation error into success",
			file:       "negate_eval_error_fails.yaml",
			exitCode:   1,
			absentFile: "negate-eval-error.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(tc.exitCode)
			if tc.outputFile != "" {
				dagu.ExpectFileContent(tc.outputFile, tc.content)
			}
			if tc.absentFile != "" {
				dagu.ExpectNoFile(tc.absentFile)
			}
		})
	}
}

func TestRuntimeMultiplePreconditionsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "multiple_conditions_source_order.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("condition-order.txt", "12")
	dagu.ExpectNoFile("multiple-conditions-ran.txt")
}

func TestRuntimeCommandCheckDetailsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	t.Run("stdout and stderr are ignored", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "command_check_streams_ignored.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("command-check-stdout.txt", "action-out\n")
		dagu.ExpectFileContent("command-check-stderr.txt", "action-err\n")
	})

	t.Run("missing executable is not met and skips step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "command_check_missing_command_skips.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectNoFile("missing-command-ran.txt")
	})
}

func TestRuntimeValueMatchDetailsUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name       string
		file       string
		outputFile string
		content    string
		absentFile string
	}{
		{
			name:       "expected is literal and not value-resolved",
			file:       "expected_literal_not_resolved.yaml",
			absentFile: "expected-literal-ran.txt",
		},
		{
			name:       "expected does not run command substitution",
			file:       "expected_command_substitution_literal.yaml",
			absentFile: "expected-substitution-ran.txt",
		},
		{
			name:       "unqualified env variables resolve before matching",
			file:       "value_match_env_var_resolves.yaml",
			outputFile: "env-var-resolves-ran.txt",
			content:    "ran\n",
		},
		{
			name:       "successful substitution stderr is ignored",
			file:       "value_match_stderr_ignored.yaml",
			outputFile: "substitution-stderr-ignored.txt",
			content:    "ran\n",
		},
		{
			name:       "literal expected matches one condition line",
			file:       "value_match_line_match.yaml",
			outputFile: "line-match-ran.txt",
			content:    "ran\n",
		},
		{
			name:       "regex matching is case-sensitive",
			file:       "regex_case_sensitive.yaml",
			absentFile: "regex-case-ran.txt",
		},
		{
			name:       "regex matching is not implicitly anchored",
			file:       "regex_unanchored.yaml",
			outputFile: "regex-unanchored-ran.txt",
			content:    "ran\n",
		},
		{
			name:       "step substitution receives managed step env",
			file:       "step_managed_env_substitution.yaml",
			outputFile: "managed-env.txt",
			content:    "managed-env\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			if tc.outputFile != "" {
				dagu.ExpectFileContent(tc.outputFile, tc.content)
			}
			if tc.absentFile != "" {
				dagu.ExpectNoFile(tc.absentFile)
			}
		})
	}
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
