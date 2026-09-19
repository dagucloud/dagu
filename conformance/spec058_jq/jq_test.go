// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec058_jq holds black-box conformance tests for the jq.filter action.
package spec058_jq_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestJQFilter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		want    string
	}{
		{"basic.yaml", "\"World\"\n"},
		{"raw_mode.yaml", "World\n"},
		{"raw_null.yaml", "\n"},
		{"args.yaml", "Alice:5:$who\nAlice:10:$who\n"},
		{"empty_args.yaml", "Alice|${env.who}\n"},
		{"with_input_file.yaml", "\"Metropolis\"\n"},
		{"with_data_file_prefix.yaml", "\"Metropolis\"\n"},
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

func TestJQConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message string
	}{
		{"missing_filter.yaml", "with.filter is required"},
		{"both_data_and_input.yaml", "does not allow both with.data and with.input"},
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
