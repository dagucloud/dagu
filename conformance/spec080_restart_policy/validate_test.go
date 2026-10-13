// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec080_restart_policy_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestRestartPolicyValidation(t *testing.T) {
	t.Parallel()

	valid := []string{
		"valid_always.yaml",
		"valid_unless_stopped.yaml",
		"valid_mixed_case.yaml",
		"valid_no.yaml",
		"valid_probe.yaml",
	}
	for _, file := range valid {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", "validate/"+file)
			result.ExpectExitCode(0)
			result.ExpectStderr("")
		})
	}

	t.Run("warning_no_retention.yaml", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "validate/warning_no_retention.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("hist_retention_runs")
	})

	rejected := []struct {
		file  string
		field string
	}{
		{"invalid_value.yaml", "restart_policy"},
		{"invalid_on_failure.yaml", "restart_policy"},
		{"invalid_missing_wait.yaml", "restart_wait_sec"},
		{"invalid_zero_wait.yaml", "restart_wait_sec"},
		{"invalid_with_schedule.yaml", "restart_policy"},
		{"invalid_with_start_stop_schedule.yaml", "restart_policy"},
		{"invalid_with_retry_policy.yaml", "restart_policy"},
	}
	for _, tc := range rejected {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", "validate/"+tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.field)
		})
	}
}

func TestRestartPolicyInTheBaseConfigurationIsRejected(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_BASE_CONFIG=" + dagu.ProjectPath("validate/base_with_policy.yaml")}
	result := dagu.RunWithEnv(env, "validate", "validate/plain.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("restart_policy", "base configuration")
}
