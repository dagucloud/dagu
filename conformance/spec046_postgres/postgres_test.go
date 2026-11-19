// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec046_postgres_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestPostgresConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		command   string
		wantError string
	}{
		{"query.yaml", "validate", ""},
		{"import.yaml", "validate", ""},
		{"missing_query.yaml", "validate", "with.query"},
		{"missing_import.yaml", "validate", "with.import"},
		{"unknown_field.yaml", "validate", `unexpected additional properties ["output_formats"]; did you mean "output_format" instead of "output_formats"?`},
		{"missing_dsn.yaml", "start", "dsn"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			if tc.wantError == "" {
				result.ExpectExitCode(0)
			} else {
				result.ExpectNonZeroExitCode()
				result.ExpectStderrContains(tc.wantError)
			}
		})
	}
}
