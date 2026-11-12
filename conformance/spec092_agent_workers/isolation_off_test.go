// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Without isolation": with no untrusted.uid and no untrusted.gid, and
// untrusted.isolation on (the default), an untrusted step fails before it starts
// with the reason isolation_required. The message names the three keys.
func TestUntrustedStepFailsWithoutIsolation(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(trustEnv, "start", "trust_default_plain.yaml")
	result.ExpectNonZeroExitCode()
	out := result.Stdout() + result.Stderr()
	for _, want := range []string{
		`untrusted step "marked"`, "isolation_required", "untrusted.uid", "untrusted.gid", "untrusted.isolation",
	} {
		require.Contains(t, out, want)
	}
	assert.NotContains(t, out, "marked:untrusted", "the step did not start")
	assert.NotContains(t, out, "opted_out:", "a step after the failed step did not run")
}

// With untrusted.isolation: off, the same DAG runs, the process logs a warning at
// start that names the key, and the step log says that isolation is off.
func TestIsolationOffRunsWithAWarning(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(trustEnv, "start", "--config", "untrusted_off_config.yaml", "trust_default_plain.yaml")
	result.ExpectExitCode(0)
	out := result.Stdout() + result.Stderr()
	require.Contains(t, out, "marked:untrusted")
	require.Contains(t, out, `untrusted.isolation is \"off\"`)
	require.Contains(t, out, "untrusted: isolation is off: the key untrusted.isolation is off")
}

// An invalid untrusted.isolation, or isolation off together with a restricted user,
// stops the process at start. The error names the keys and the value.
func TestIsolationKeyErrorsStopTheProcess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"untrusted_isolation_bad_config.yaml", []string{`"untrusted.isolation"`, `expected "on" or "off"`, `got "maybe"`}},
		{"untrusted_off_with_uid_config.yaml", []string{`"untrusted.isolation"`, `"untrusted.uid"`, `"untrusted.gid"`, "isolation cannot be off with a restricted user"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("config", "--config", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// The blocks untrusted, ai and agent_tools are file only. A variable that names the
// whole block stops the process at start. The error names the variable and the key,
// and never repeats the value.
func TestFileOnlyBlockVariablesStopTheProcess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ variable, block string }{
		{"DAGU_UNTRUSTED", "untrusted"},
		{"DAGU_AI", "ai"},
		{"DAGU_AGENT_TOOLS", "agent_tools"},
	} {
		t.Run(tc.variable, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{tc.variable + "=a-secret-value"}, "config")
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("the environment variable " + tc.variable + " is not read: set the block " + tc.block + " in the configuration file")
			assert.NotContains(t, result.Stdout()+result.Stderr(), "a-secret-value")
		})
	}
}
