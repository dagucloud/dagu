// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec081_clickhouse holds black-box conformance tests for ClickHouse action configuration.
package spec081_clickhouse_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestClickHouseConfig(t *testing.T) {
	t.Parallel()

	t.Run("accepts configured actions", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.Run("validate", "configured.yaml").ExpectExitCode(0)
	})

	for _, tc := range []struct {
		fixture string
		message string
	}{
		{"missing_query.yaml", "with.query or with.file is required"},
		{"query_and_file.yaml", "with.query and with.file are mutually exclusive"},
		{"missing_url.yaml", "url is required"},
		{"embedded_credentials.yaml", "credentials must not be embedded in the url"},
		{"invalid_output_format.yaml", "invalid output_format"},
		{"reserved_setting.yaml", "use output_format"},
		{"reserved_compress_setting.yaml", "response is read uncompressed"},
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
