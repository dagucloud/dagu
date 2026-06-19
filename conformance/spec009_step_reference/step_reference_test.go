// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec009_step_reference_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidateStepReferences(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"valid_id_dependency.yaml",
		"valid_name_dependency.yaml",
		"valid_empty_depends.yaml",
		"valid_null_depends.yaml",
		"valid_non_string_depends.yaml",
		"valid_id_promoted_to_name.yaml",
		"valid_trimmed_identity.yaml",
		"valid_output_reference_by_id.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
		})
	}

	invalidCases := []struct {
		file        string
		stderrParts []string
	}{
		{file: "invalid_duplicate_id.yaml", stderrParts: []string{"duplicate step ID"}},
		{file: "invalid_trimmed_duplicate_id.yaml", stderrParts: []string{"duplicate step ID"}},
		{file: "invalid_duplicate_name.yaml", stderrParts: []string{"step name", "unique"}},
		{file: "invalid_id_syntax.yaml", stderrParts: []string{"invalid step ID format"}},
		{file: "invalid_reserved_id.yaml", stderrParts: []string{"reserved word"}},
		{file: "invalid_id_too_long.yaml", stderrParts: []string{"step ID must be at most 40"}},
		{file: "invalid_name_too_long.yaml", stderrParts: []string{"step name must be at most 255"}},
		{file: "invalid_name_utf8_byte_length.yaml", stderrParts: []string{"step name must be at most 255"}},
		{file: "invalid_id_name_conflict.yaml", stderrParts: []string{"conflicts"}},
		{file: "invalid_dependency_case_mismatch.yaml", stderrParts: []string{"depends on non-existent step"}},
		{file: "invalid_dependency_untrimmed.yaml", stderrParts: []string{"depends on non-existent step"}},
		{file: "invalid_depends_shape.yaml", stderrParts: []string{"depends", "string or array"}},
		{file: "invalid_chain_depends_empty.yaml", stderrParts: []string{"depends field is not allowed"}},
		{file: "invalid_unknown_dependency.yaml", stderrParts: []string{"depends on non-existent step"}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
		})
	}

	noticeCases := []struct {
		file        string
		stderrParts []string
	}{
		{
			file:        "notice_output_reference_by_name.yaml",
			stderrParts: []string{"${steps.build.outputs.artifact}", "reason=unknown_step_id", "steps[1].run"},
		},
		{
			file:        "notice_handler_output_reference.yaml",
			stderrParts: []string{"${steps.build.outputs.artifact}", "reason=namespace_unavailable", "handler_on.success.run"},
		},
	}
	for _, tc := range noticeCases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
		})
	}
}

func TestRuntimeStepReferenceExecution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		output  string
		content string
	}{
		{
			name:    "dependency by id",
			file:    "valid_id_dependency.yaml",
			output:  "id-dependency.txt",
			content: "build\ndeploy\n",
		},
		{
			name:    "dependency by name",
			file:    "valid_name_dependency.yaml",
			output:  "name-dependency.txt",
			content: "build\ndeploy\n",
		},
		{
			name:    "trimmed identity",
			file:    "valid_trimmed_identity.yaml",
			output:  "trimmed-identity.txt",
			content: "producer\nname\nid\n",
		},
		{
			name:    "step output reference by id",
			file:    "valid_output_reference_by_id.yaml",
			output:  "id-output-reference.txt",
			content: "from-id\n",
		},
		{
			name:    "step output reference by name stays literal",
			file:    "notice_output_reference_by_name.yaml",
			output:  "name-output-reference.txt",
			content: "${steps.build.outputs.artifact}\n",
		},
		{
			name:    "handler step output reference stays literal",
			file:    "notice_handler_output_reference.yaml",
			output:  "handler-output-reference.txt",
			content: "${steps.build.outputs.artifact}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestRuntimeStepReferenceCycleFailsBeforeStepsRun(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "invalid_cycle.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("cyclic")
	dagu.ExpectNoFile("cycle-first.txt")
	dagu.ExpectNoFile("cycle-second.txt")
}
