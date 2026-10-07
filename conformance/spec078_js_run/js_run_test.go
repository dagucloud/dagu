// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec078_js_run holds black-box conformance tests for the js.run action.
package spec078_js_run_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestJSRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		want    string
	}{
		{"object.yaml", "{\n  \"name\": \"World\",\n  \"n\": 1\n}\n"},
		{"string.yaml", "World\n"},
		{"undefined.yaml", ""},
		{"input.yaml", "Metropolis:2\n"},
		{"input_file_text.yaml", "string\n"},
		{"input_file_auto.yaml", "object:Metropolis\n"},
		{"await.yaml", "4\n"},
		{"input_file_json.yaml", "Metropolis\n"},
		{"template_literal.yaml", "Hello Metropolis\n"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			dagu.WriteFile("data.json", `{"city":"Metropolis"}`)
			dagu.Run("start", tc.fixture).ExpectExitCode(0)

			data, err := os.ReadFile(dagu.ProjectPath("result.out"))
			require.NoError(t, err)
			require.Equal(t, tc.want, string(data))
		})
	}
}

func TestJSRunConsole(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Run("start", "console.yaml").ExpectExitCode(0)
	dagu.ExpectFileContent("result.out", "done\n")
	dagu.ExpectFileContains("err.out", `debug {"n":1}`)
}

func TestJSRunFailures(t *testing.T) {
	t.Parallel()

	t.Run("throw", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "throw.yaml")
		result.ExpectNonZeroExitCode()
		require.Contains(t, result.Stdout()+result.Stderr(), "js: Error: boom (script line 1)")
		dagu.ExpectFileContains("err.out", "Error: boom", "script:1")
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.Run("start", "timeout.yaml").ExpectNonZeroExitCode()
	})
}

func TestJSRunConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message string
	}{
		{"missing_script.yaml", "with.script is required"},
		{"both_inputs.yaml", "does not allow both with.input and with.input_file"},
		{"syntax_error.yaml", "js: compile error"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
