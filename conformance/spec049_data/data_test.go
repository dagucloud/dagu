// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec049_data_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestDataConvert(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "convert_json_to_yaml.yaml")
	result.ExpectExitCode(0)
	data, err := os.ReadFile(dagu.ProjectPath("result.out"))
	require.NoError(t, err)
	require.YAMLEq(t, "name: alice\nage: 30\n", string(data))
}

func TestDataText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, want string }{
		{"pick_select_raw.yaml", "alice\n"},
		{"pick_select_null_succeeds.yaml", "\n"},
		{"text.yaml", "hello text"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent("result.out", tc.want)
		})
	}
}

// data.filter reads each value as written: prices with 万 and 億, and
// dates with 年 and 月, keeping the rows every condition holds for in order.
func TestDataFilter(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "filter_listings.yaml")
	result.ExpectExitCode(0)
	data, err := os.ReadFile(dagu.ProjectPath("result.out"))
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"Honmachi","price":"6,800万円","built":"1990年10月"},{"name":"Tenma","price":"4,280万円","built":"1983年5月"}]`, string(data))
}

func TestDataErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, command, field string }{
		{"missing_from.yaml", "validate", "from"},
		{"pick_missing_select.yaml", "validate", "select"},
		{"convert_malformed_json_string.yaml", "start", "failed to decode JSON"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.field)
		})
	}
}
