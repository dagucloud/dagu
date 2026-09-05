// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec049_data holds black-box conformance tests for
// Spec 049: Data Convert and Pick Actions.
package spec049_data_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestDataConvertLive(t *testing.T) {
	t.Run("converts inline JSON data to YAML", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "convert_json_to_yaml.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "age: 30\nname: alice\n", stepStdout(t, result.Stdout()))
	})

	t.Run("reads CSV from a file and converts it to JSON, decoding every field as a string", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("people.csv", "name,age\nalice,30\nbob,25\n")

		result := dagu.Run("start", "convert_csv_file_to_json.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "[\n  {\n    \"age\": \"30\",\n    \"name\": \"alice\"\n  },\n  {\n    \"age\": \"25\",\n    \"name\": \"bob\"\n  }\n]\n",
			stepStdout(t, result.Stdout()))
	})

	t.Run("has_header: false with explicit columns names headerless CSV fields", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "convert_headerless_csv_with_columns.yaml")
		result.ExpectExitCode(0)
		require.Equal(t,
			"[\n  {\n    \"age\": \"30\",\n    \"name\": \"alice\"\n  },\n  {\n    \"age\": \"25\",\n    \"name\": \"bob\"\n  }\n]\n",
			stepStdout(t, result.Stdout()))
	})

	t.Run("headers: false omits the header row from CSV output", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "convert_json_to_csv_no_headers.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "alice,30\nbob,25\n", stepStdout(t, result.Stdout()))
	})

	t.Run("a malformed JSON string in with.data is a runtime decode error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "convert_malformed_json_string.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("failed to decode JSON")
	})
}

func TestDataPickLive(t *testing.T) {
	t.Run("select with raw writes the selected scalar without JSON/YAML encoding", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_select_raw.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "alice\n", stepStdout(t, result.Stdout()))
	})

	t.Run("select without raw defaults to JSON-encoding the selected value", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_select_default_json.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "{\n  \"age\": 30,\n  \"name\": \"alice\"\n}\n", stepStdout(t, result.Stdout()))
	})

	t.Run("with.to overrides the selected value's output format", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_select_to_yaml.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "age: 30\nname: alice\n", stepStdout(t, result.Stdout()))
	})

	// gojq resolves a missing path like .b.c on {"a":1} to a legitimate
	// null (the same way a real jq CLI would), not an evaluation error, so
	// this succeeds -- contrast with pick_invalid_select_syntax.yaml and
	// pick_select_type_error.yaml below, which really do fail.
	t.Run("a select path that legitimately resolves to null succeeds rather than failing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_select_null_succeeds.yaml")
		result.ExpectExitCode(0)
	})

	t.Run("malformed select syntax is a runtime error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_invalid_select_syntax.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`failed to resolve select path`)
	})

	t.Run("a select path that is invalid for the data's shape is a runtime error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "pick_select_type_error.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`failed to resolve select path`)
	})
}

// TestDataValidation proves the data executor's build-time validation:
// unlike several other built-in actions in this session's conformance
// suite, data.convert/data.pick register a real step validator, so every
// one of these configuration errors is caught by dagu validate itself,
// not merely by dagu start.
func TestDataValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{
			name:   "with.from missing",
			file:   "missing_from.yaml",
			errStr: `missing properties: ["from"]`,
		},
		{
			name:   "with.from not one of the supported formats",
			file:   "unsupported_from.yaml",
			errStr: "enum: xml does not equal any of",
		},
		{
			name:   "with.data and with.input both set",
			file:   "data_and_input_conflict.yaml",
			errStr: "oneOf: validated against both",
		},
		{
			name:   "neither with.data nor with.input set",
			file:   "neither_data_nor_input.yaml",
			errStr: "oneOf: did not validate against any",
		},
		{
			name:   "data.pick without with.select",
			file:   "pick_missing_select.yaml",
			errStr: "select is required",
		},
		{
			name:   "data.pick with with.raw and with.to together",
			file:   "pick_raw_and_to_conflict.yaml",
			errStr: "raw and to are mutually exclusive",
		},
		{
			name:   "with.delimiter longer than one character",
			file:   "multi_char_delimiter.yaml",
			errStr: "delimiter must be a single character",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errStr)
		})
	}
}
