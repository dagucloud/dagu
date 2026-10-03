// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec018_parallel_foreach_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
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

	t.Run("a failed item body leaves the step partially succeeded with its aggregate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
		const runID = "spec018-foreach-partial"
		result := dagu.RunWithEnv(env, "start", "--run-id="+runID, "foreach_partial_failure.yaml")
		result.ExpectExitCode(0)

		status := dagu.RunWithEnv(env, "status", "--run-id="+runID, "foreach_partial_failure.yaml")
		status.ExpectExitCode(0)
		require.Contains(t, status.Stdout(), "Partially Succeeded")

		var aggregate struct {
			Summary struct {
				Total     int `json:"total"`
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"summary"`
			Items []struct {
				Key    string `json:"key"`
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"items"`
			Outputs []map[string]string `json:"outputs"`
		}
		data, err := os.ReadFile(dagu.ProjectPath("foreach-partial-results.txt"))
		require.NoError(t, err, "the dependent step ran and wrote the aggregate")
		require.NoError(t, json.Unmarshal(data, &aggregate))
		require.Equal(t, 3, aggregate.Summary.Total)
		require.Equal(t, 2, aggregate.Summary.Succeeded)
		require.Equal(t, 1, aggregate.Summary.Failed)
		require.Len(t, aggregate.Items, 3)
		require.Equal(t, "b", aggregate.Items[1].Key)
		require.Equal(t, "failed", aggregate.Items[1].Status)
		require.NotEmpty(t, aggregate.Items[1].Error)
		require.Equal(t, []map[string]string{{"item": "a"}, {"item": "c"}}, aggregate.Outputs, "outputs holds the successful bodies only")
	})

	t.Run("every item body failing fails the step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "foreach_all_fail.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("all 2 item bodies failed")
		dagu.ExpectNoFile("foreach-all-fail-results.txt")
	})
}
