// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec047_sqlite_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestSQLiteActions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file string
		want map[string]int
	}{
		{"positional_params.yaml", map[string]int{"sum": 5}},
		{"import_basic.yaml", map[string]int{"n": 3}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			data, err := os.ReadFile(dagu.ProjectPath("result.json"))
			require.NoError(t, err)
			var row map[string]int
			require.NoError(t, json.Unmarshal(data, &row))
			require.Equal(t, tc.want, row)
		})
	}
}

func TestSQLiteConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, command, field string }{
		{"missing_query.yaml", "validate", "with.query"},
		{"missing_import.yaml", "validate", "with.import"},
		{"unknown_field.yaml", "validate", `unexpected additional properties ["output_formats"]; did you mean "output_format" instead of "output_formats"?`},
		{"missing_dsn.yaml", "start", "dsn"},
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

// A relative file: DSN, import file, and streaming output file all live under
// the step working directory, not the process directory.
func TestSQLiteRelativeWorkingDir(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.WriteFile("work/users.csv", "id,name\n1,Alice\n")
	dagu.Run("start", "relative_working_dir.yaml").ExpectExitCode(0)
	_, err := os.Stat(dagu.ProjectPath("work/app.db"))
	require.NoError(t, err)
	dagu.ExpectFileContains("work/out/users.jsonl", "Alice")
	dagu.ExpectNoFile("app.db")
}
