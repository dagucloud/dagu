// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tests_test

import "testing"

func Test003ValueResolutionValidate(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"consts_literal_values.yaml",
		"params_schema_reference.yaml",
		"resolve_consts_list.yaml",
		"resolve_mixed_case_step_output.yaml",
		"unqualified_reference.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "003_value_resolution")

			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			dagu.ExpectNoFile("executed.txt")
		})
	}

	invalidCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "invalid consts value type",
			file:        "consts_invalid_value_type.yaml",
			stderrParts: []string{"consts", "literal"},
		},
		{
			name:        "non-finite numeric consts value",
			file:        "consts_non_finite_number.yaml",
			stderrParts: []string{"consts", "finite"},
		},
		{
			name:        "unknown consts reference",
			file:        "missing_reference.yaml",
			stderrParts: []string{"consts", "missing"},
		},
		{
			name:        "undeclared params reference",
			file:        "undeclared_params_reference.yaml",
			stderrParts: []string{"params", "region"},
		},
		{
			name:        "unknown step reference",
			file:        "unknown_step_reference.yaml",
			stderrParts: []string{"steps", "build"},
		},
		{
			name:        "unknown declared step output",
			file:        "unknown_step_output_reference.yaml",
			stderrParts: []string{"steps", "image"},
		},
		{
			name:        "unknown namespace",
			file:        "unknown_namespace_reference.yaml",
			stderrParts: []string{"namespace", "vars"},
		},
		{
			name:        "namespace-only reference",
			file:        "namespace_only_reference.yaml",
			stderrParts: []string{"params", "${params.<name>}"},
		},
		{
			name:        "malformed reference",
			file:        "malformed_reference.yaml",
			stderrParts: []string{"malformed", "${consts.service"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "003_value_resolution")

			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
		})
	}
}

func Test003ValueResolutionRun(t *testing.T) {
	t.Parallel()

	runCases := []struct {
		name    string
		file    string
		args    []string
		output  string
		content string
	}{
		{
			name:    "consts reference",
			file:    "resolve_consts.yaml",
			output:  "consts.txt",
			content: "api true 1000 1.5\n",
		},
		{
			name:    "consts list reference",
			file:    "resolve_consts_list.yaml",
			output:  "consts_list.txt",
			content: "http://localhost/api\n",
		},
		{
			name:    "params reference",
			file:    "resolve_params.yaml",
			args:    []string{`--params=environment=prod`},
			output:  "params.txt",
			content: "prod\n",
		},
		{
			name:    "step output reference",
			file:    "resolve_step_output.yaml",
			output:  "step_output.txt",
			content: "v1.2.3\n",
		},
		{
			name:    "mixed-case step ID output reference",
			file:    "resolve_mixed_case_step_output.yaml",
			output:  "mixed_case_step_output.txt",
			content: "v1.2.3\n",
		},
		{
			name:    "shell variable remains for shell expansion",
			file:    "shell_variable.yaml",
			output:  "shell_variable.txt",
			content: "shell-expanded\n",
		},
		{
			name:    "command substitution text is not evaluated by Dagu",
			file:    "command_substitution_text.yaml",
			output:  "command_substitution.txt",
			content: "$(touch dollar_command_executed) `touch backtick_executed` ok\n",
		},
	}
	for _, tc := range runCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "003_value_resolution")
			args := append([]string{"start"}, tc.args...)
			args = append(args, tc.file)

			result := dagu.Run(args...)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
			dagu.ExpectNoFile("dollar_command_executed")
			dagu.ExpectNoFile("backtick_executed")
		})
	}
}

func Test003ValueResolutionRunFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "missing reference",
			file:        "missing_reference.yaml",
			stderrParts: []string{"consts", "missing"},
		},
		{
			name:        "dagu-looking shorthand",
			file:        "dagu_shorthand_reference.yaml",
			stderrParts: []string{"$consts.service", "invalid"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "003_value_resolution")

			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile("should_not_run.txt")
		})
	}
}
