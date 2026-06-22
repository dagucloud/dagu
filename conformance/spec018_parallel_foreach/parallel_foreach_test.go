// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec018_parallel_foreach_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestParallelValidation(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"parallel_flat_mapping_valid.yaml",
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
		{
			file:        "parallel_unknown_object_field.yaml",
			stderrParts: []string{"parallel", "unexpected"},
		},
		{
			file:        "parallel_max_concurrent_float.yaml",
			stderrParts: []string{"parallel.max_concurrent", "integer"},
		},
		{
			file:        "parallel_max_concurrent_too_high.yaml",
			stderrParts: []string{"parallel.max_concurrent", "1000"},
		},
		{
			file:        "parallel_nested_mapping_item.yaml",
			stderrParts: []string{"parallel"},
		},
		{
			file:        "parallel_nested_array_item.yaml",
			stderrParts: []string{"parallel"},
		},
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
}

func TestParallelDAGRunRuntime(t *testing.T) {
	t.Parallel()

	t.Run("object items use item fields and duplicate child runs coalesce", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "parallel_dag_run_object_items.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContains(
			"parallel-object-results.txt",
			`"total": 2`,
			`"succeeded": 2`,
			`"CHILD_VALUE": "acct-1/us"`,
			`"CHILD_VALUE": "acct-2/eu"`,
		)
	})

	t.Run("string item source parses runtime params", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "parallel_string_items.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContains(
			"parallel-string-results.txt",
			`"total": 2`,
			`"succeeded": 2`,
			`"CHILD_VALUE": "alpha"`,
			`"CHILD_VALUE": "beta"`,
		)
	})
}

func TestForeachValidation(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"foreach_success_collect.yaml",
		"foreach_string_items.yaml",
		"foreach_zero_items.yaml",
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
}

func TestForeachRuntime(t *testing.T) {
	t.Parallel()

	t.Run("runs inline body with item scope and collect output", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_success_collect.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContains(
			"foreach-success-results.txt",
			`"total":2`,
			`"succeeded":2`,
			`"failed":0`,
			`"key":"one"`,
			`"key":"two"`,
			`"markdown":"https://example.com/one"`,
			`"markdown":"https://example.com/two"`,
		)
	})

	t.Run("string item source must resolve to json array", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_string_items.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContains(
			"foreach-string-results.txt",
			`"total":2`,
			`"markdown":"alpha"`,
			`"markdown":"beta"`,
		)
	})

	t.Run("zero items succeeds with empty output arrays", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_zero_items.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContains(
			"foreach-zero-results.txt",
			`"total":0`,
			`"succeeded":0`,
			`"items":[]`,
			`"outputs":[]`,
		)
	})

	t.Run("duplicate keys fail before body starts", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_duplicate_keys.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("duplicate foreach item key")
		dagu.ExpectNoFile("foreach-duplicate-body-ran.txt")
	})

	t.Run("non json string item source fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_non_json_string_items.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("foreach.items string must resolve to a JSON array")
		dagu.ExpectNoFile("foreach-non-json-ran.txt")
	})
}
