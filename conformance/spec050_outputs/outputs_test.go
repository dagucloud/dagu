// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec050_outputs holds black-box conformance tests for
// Spec 050: Outputs Write Action.
package spec050_outputs_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestOutputsWriteLive(t *testing.T) {
	t.Run("writes multiple values in one step, readable as stepid.outputs.key", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_and_read.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "greeting=hello count=3", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a value is resolved (env vars, and so on) the same as any other with: field", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_dynamic_value.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "from-env", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a non-scalar value round-trips as JSON when referenced downstream", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_list_value.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `["a","b"]`, lastStepStdout(t, result.Stdout()))
	})
}

// TestOutputsWriteValidation proves outputs.write's build-time validation:
// like data.convert/data.pick (see Spec 049), it registers a real step
// validator, so every one of these configuration errors is caught by dagu
// validate itself, not merely by dagu start. with.values missing entirely
// or present but empty is caught by a custom check (the registered JSON
// Schema alone would accept an absent or empty values object); a wrong
// type for with.values or an unsupported extra with: field is caught by
// the schema itself.
func TestOutputsWriteValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{
			name:   "with.values missing entirely",
			file:   "missing_values.yaml",
			errStr: "values is required for write",
		},
		{
			name:   "with.values present but empty",
			file:   "empty_values.yaml",
			errStr: "values must not be empty",
		},
		{
			name:   "with.values not an object",
			file:   "values_not_object.yaml",
			errStr: `type: not-an-object has type "string", want "object"`,
		},
		{
			name:   "an unsupported field alongside with.values",
			file:   "unsupported_field.yaml",
			errStr: `unexpected additional properties ["extra"]`,
		},
		{
			name:   "with.values contains an empty key",
			file:   "empty_key.yaml",
			errStr: "values contains an empty key",
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
