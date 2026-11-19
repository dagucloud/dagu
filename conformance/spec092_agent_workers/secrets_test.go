// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Errors": dagu validate rejects each wrong use of the keys
// secrets and untrusted.
func TestSecretsAndUntrustedValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		errText []string
	}{
		{"secrets_undeclared.yaml", []string{`step "push"`, `"NOT_DECLARED"`, "does not declare"}},
		{"secrets_duplicate.yaml", []string{`step "push"`, `"GIT_TOKEN" occurs two times`}},
		{"secrets_on_untrusted.yaml", []string{`untrusted step "verify" cannot have the key secrets`}},
		{"secrets_reference.yaml", []string{`step "report"`, `refers to the secret "GIT_TOKEN", which the step does not get`}},
		{"untrusted_action.yaml", []string{"untrusted is valid only on a step with run, command or script, or with the action harness.run"}},
		{"untrusted_container.yaml", []string{"untrusted is not valid on a step with container"}},
		{"untrusted_defaults.yaml", []string{`the key "untrusted" is not valid in defaults`}},
		{"untrusted_false_agent.yaml", []string{"an agent.run step with a CLI provider is always untrusted"}},
		{"trust_default_secrets_true.yaml", []string{`untrusted step "push" cannot have the key secrets`}},
		{"trust_default_secrets_default.yaml", []string{"field 'steps[1].secrets'", "step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets"}},
		{"trust_default_false_action.yaml", []string{"untrusted is valid only on a step with run, command or script"}},
		{"trust_default_gate_reference.yaml", []string{`step "gate"`, "${steps.verify.outputs.verdict}", `an output of the untrusted step "verify"`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errText...)
		})
	}
}

// Spec 092, "Step scope" and "Rules in each process": in a DAG with step
// scope, a step with no list does not get a secret, a step that lists it does,
// and an untrusted step gets no secret and no DAGU_ variable but
// DAGU_OUTPUT_FILE. Without untrusted.uid in the configuration, the untrusted
// step runs as the process user with a line in its log.
func TestSecretsStepScope(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture uses a POSIX shell")
	}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{
		"SPEC092_GIT_TOKEN=spec092-token-value",
		"DAGU_SPEC092_MARKER=1",
		harness.IsolationOffEnv(t),
	}, "start", "secrets_step_scope.yaml")
	result.ExpectExitCode(0)
	out := result.Stdout() + result.Stderr()
	require.Contains(t, out, "plain:no-secret")
	require.Contains(t, out, "plain:has-dagu-marker", "a trusted step keeps the DAGU_ variables")
	require.Contains(t, out, "listed:has-secret")
	require.Contains(t, out, "untrusted:clean")
	require.Contains(t, out, "verdict:clean")
	require.Contains(t, out, "untrusted: isolation is off: the key untrusted.isolation is off")
	require.NotContains(t, out, "spec092-token-value")
}
