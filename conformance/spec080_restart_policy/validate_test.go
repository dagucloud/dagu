// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec080_restart_policy_test

import (
	"path/filepath"
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
		"valid_empty_schedule.yaml",
		"valid_default_retention.yaml",
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

func TestRestartCommandIsRefused(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
	dagu.RunWithEnv(env, "start", "validate/valid_always.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "restart", "validate/valid_always.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("restart_policy")

	t.Run("without a run", func(t *testing.T) {
		t.Parallel()

		env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
		result := harness.NewRunner(t).RunWithEnv(env, "restart", "validate/valid_always.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("restart_policy")
	})
}
