// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec057_redis holds black-box conformance tests for Redis action configuration.
package spec057_redis_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestRedisConfig(t *testing.T) {
	t.Parallel()

	t.Run("accepts configured actions", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.Run("validate", "configured.yaml").ExpectExitCode(0)
	})

	for _, tc := range []struct {
		command string
		fixture string
		message string
	}{
		{"validate", "command_mismatch.yaml", "command must be \"GET\" for this action"},
		{"validate", "invalid_mode.yaml", "mode"},
		{"validate", "unknown_field.yaml", `unexpected additional properties ["hosts"]; did you mean "host" instead of "hosts"?`},
		{"start", "cluster_missing_fields.yaml", "cluster_addrs is required for cluster mode"},
		{"start", "tls_cert_without_key.yaml", "both tls_cert and tls_key must be provided together"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
