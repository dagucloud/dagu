// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec058_jq holds black-box conformance tests for
// Spec 058: JQ Filter Action.
package spec058_jq_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestJQFilterLive(t *testing.T) {
	t.Run("with.filter runs against with.data and writes JSON-formatted results", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "basic.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"World\"\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.raw prints a string result without surrounding quotes", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "raw_mode.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "World\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a filter producing multiple values writes each on its own line", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "multi_values.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "1\n2\n3\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.input reads JSON from a file instead of with.data", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("data.json", `{"city": "Metropolis"}`)

		result := dagu.Run("start", "with_input_file.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"Metropolis\"\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a with.data value starting with file:// reads that file instead of parsing the string as JSON", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("data.json", `{"city": "Metropolis"}`)

		result := dagu.Run("start", "with_data_file_prefix.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"Metropolis\"\n", lastStepStdout(t, result.Stdout()))
	})

	// with.data is JSON-encoded once at build time, and the resulting text
	// is then evaluated the same way any other script body is (unlike
	// template.render's with.template, which is deliberately exempted) --
	// so a literal $-prefixed token inside a with.data string is resolved
	// as a value reference before being parsed as JSON.
	t.Run("a literal $-prefixed token inside with.data is resolved before being parsed as JSON", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "data_dollar_substitution.yaml")
		result.ExpectExitCode(0)
		require.JSONEq(t, `{"note": "cost is 9.99 dollars"}`, lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.filter's text is resolved the same way any other command is", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "filter_resolution.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"Resolved\"\n", lastStepStdout(t, result.Stdout()))
	})

	// A jq runtime error on one produced value (as opposed to a filter
	// parse error) does not fail the step: it is written to stderr, and
	// iteration continues to the remaining values.
	t.Run("a jq runtime error on one value is reported on stderr without failing the step or skipping later values", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "runtime_error_continues.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "\"ok1\"\n\"ok2\"\n", lastStepStdout(t, result.Stdout()))

		stderr := lastStepStderr(t, result.Stdout())
		require.Contains(t, stderr, "failed to run jq query")
		require.Contains(t, stderr, "expected an object but got")
	})

	t.Run("a malformed filter fails the step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "parse_error.yaml")
		result.ExpectNonZeroExitCode()
	})

	t.Run("with.input naming a file that does not exist fails the step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "input_file_missing.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("reading input file")
	})

	// A single trailing newline (fmt.Fprintln with no arguments) is all
	// the step writes, so the tree render's own "stdout:" line is omitted
	// entirely -- the same rendering behavior a genuinely empty stdout
	// gets, just from one extra byte.
	t.Run("with.raw on a null result prints only a trailing newline, not the text null", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "raw_null.yaml")
		result.ExpectExitCode(0)
		require.True(t, hasNoStepStdout(result.Stdout()), "expected no stdout log line for a single-newline result:\n%s", result.Stdout())
	})

	t.Run("the default (non-raw) format pretty-prints an object result with four-space indentation", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "object_default_json.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "{\n    \"a\": 1,\n    \"b\": 2\n}\n", lastStepStdout(t, result.Stdout()))
	})
}

// TestJQFilterValidation proves that with.filter is required at DAG-build
// time, and that with.data and with.input are mutually exclusive at build
// time, but that omitting both passes validate and fails only when the
// step actually runs -- jq.filter has no registered step validator, so
// this last check exists only in the executor's own runtime setup code.
func TestJQFilterValidation(t *testing.T) {
	t.Parallel()

	t.Run("with.filter missing fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_filter.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.filter is required")
	})

	t.Run("with.data and with.input together fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "both_data_and_input.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("does not allow both with.data and with.input")
	})

	t.Run("neither with.data nor with.input fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "neither_data_nor_input.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "neither_data_nor_input.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no input provided")
	})
}
