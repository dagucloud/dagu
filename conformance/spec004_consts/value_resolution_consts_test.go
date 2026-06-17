// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec004_consts_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"canonical_list_form.yaml",
		"ordered_const_references.yaml",
		"unbraced_consts_text_is_preserved.yaml",
		"braced_non_reference_text_is_preserved.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)

			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}

	diagnosticCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "self reference preserves and reports diagnostic",
			file:        "consts_self_reference.yaml",
			stderrParts: []string{"${consts.service}", "was left unchanged", "consts.service"},
		},
		{
			name:        "later reference preserves and reports diagnostic",
			file:        "consts_later_reference.yaml",
			stderrParts: []string{"${consts.host}", "was left unchanged", "consts.endpoint"},
		},
		{
			name:        "runtime env reference preserves and reports diagnostic",
			file:        "consts_runtime_env_reference.yaml",
			stderrParts: []string{"${env.SERVICE}", "was left unchanged", "consts.service"},
		},
		{
			name:        "runtime params reference preserves and reports diagnostic",
			file:        "consts_runtime_params_reference.yaml",
			stderrParts: []string{"${params.target}", "was left unchanged", "consts.target"},
		},
		{
			name:        "runtime steps reference preserves and reports diagnostic",
			file:        "consts_runtime_steps_reference.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "was left unchanged", "consts.image"},
		},
		{
			name:        "unknown const reference preserves and reports diagnostic",
			file:        "unknown_const_reference.yaml",
			stderrParts: []string{"${consts.missing}", "was left unchanged", "steps[0].run"},
		},
		{
			name:        "future namespace reference preserves and reports diagnostic",
			file:        "future_namespaces_remain_unresolved.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "was left unchanged", "steps[0].run"},
		},
	}
	for _, tc := range diagnosticCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)

			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			dagu.ExpectNoFile("executed.txt")
		})
	}

	invalidCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "mapping form is forbidden",
			file:        "consts_mapping_form.yaml",
			stderrParts: []string{"consts", "list"},
		},
		{
			name:        "scalar list entry is forbidden",
			file:        "consts_scalar_entry.yaml",
			stderrParts: []string{"consts[0]", "single-entry mappings"},
		},
		{
			name:        "multi-key list entry is forbidden",
			file:        "consts_multi_key_entry.yaml",
			stderrParts: []string{"consts[0]", "exactly one key"},
		},
		{
			name:        "invalid key is forbidden",
			file:        "consts_invalid_key.yaml",
			stderrParts: []string{"consts key", "invalid"},
		},
		{
			name:        "object value is forbidden",
			file:        "consts_object_value.yaml",
			stderrParts: []string{"const", "literal string, number, or boolean"},
		},
		{
			name:        "non-finite number is forbidden",
			file:        "consts_non_finite_number.yaml",
			stderrParts: []string{"const", "finite"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)

			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func TestRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		file          string
		outputFile    string
		outputContent string
	}{
		{
			name:          "ordered consts resolve during execution",
			file:          "runtime_const_resolution.yaml",
			outputFile:    "resolved.txt",
			outputContent: "https://example.test/api/true/3/1.25\n",
		},
		{
			name:          "single-quoted env references are preserved during execution",
			file:          "single_quoted_env_references.yaml",
			outputFile:    "single-quoted-env.txt",
			outputContent: "$SOURCE\n${SOURCE}\n",
		},
		{
			name:          "unbraced consts text is preserved during execution",
			file:          "unbraced_consts_text_is_preserved.yaml",
			outputFile:    "unbraced-consts.txt",
			outputContent: "$consts.service\n",
		},
		{
			name:          "braced non-reference text is preserved during execution",
			file:          "braced_non_reference_text_is_preserved.yaml",
			outputFile:    "braced-non-reference.txt",
			outputContent: "${consts.service.name}\n",
		},
		{
			name:          "unknown const reference is preserved during execution",
			file:          "unknown_const_reference.yaml",
			outputFile:    "unknown-const.txt",
			outputContent: "${consts.missing}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)

			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.outputFile, tc.outputContent)
		})
	}
}
