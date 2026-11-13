// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec056_s3 holds black-box conformance tests for S3 action configuration.
package spec056_s3_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestS3Config(t *testing.T) {
	t.Parallel()

	t.Run("accepts configured actions", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.Run("validate", "configured.yaml").ExpectExitCode(0)
	})

	t.Run("rejects an unknown field", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "unknown_field.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`unexpected additional properties ["buckets"]; did you mean "bucket" instead of "buckets"?`)
	})

	for _, tc := range []struct {
		fixture string
		message string
	}{
		{"missing_bucket.yaml", "bucket is required"},
		{"upload_missing_source_and_key.yaml", "source is required for upload"},
		{"download_missing_destination.yaml", "destination is required for download"},
		{"delete_missing_key_and_prefix.yaml", "key or prefix is required for delete"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
