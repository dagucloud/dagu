// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
)

// trustEnv sets the secret that the fixtures read and a variable DAGU_ that only
// a trusted step keeps.
var trustEnv = []string{
	"SPEC092_GIT_TOKEN=spec092-token-value",
	"DAGU_SPEC092_MARKER=1",
}

// Spec 092, "Default of the key": in a DAG that has an agent.run step, a step
// with run, command or script is untrusted unless it has untrusted: false. A
// step with secrets and with no untrusted: false does not load. A step with
// untrusted: false and secrets is trusted and gets its secrets. A step with
// another action keeps its behavior. With untrusted.isolation: off in the
// configuration, the untrusted steps run as the process user, so the test
// reads the variables that an untrusted step does not get.
func TestUntrustedDefaultInAgentDAG(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	result := p.startWithEnv("trust_default_agent.yaml", trustEnv...)
	result.ExpectExitCode(0)
	out := verifyOutput(result)

	for _, want := range []string{
		"by_run:untrusted",
		"by_command:untrusted",
		"by_script:untrusted",
		"marked:untrusted",
		"opted_out:trusted",
		"opted_out_listed:trusted",
		"opted_out_listed:has-secret",
		"action:ran",
	} {
		assert.Contains(t, out, want)
	}
	for _, unwanted := range []string{
		"by_run:trusted",
		"by_command:trusted",
		"by_script:trusted",
		"marked:trusted",
		"opted_out:untrusted",
		"opted_out_listed:untrusted",
		"spec092-token-value",
	} {
		assert.NotContains(t, out, unwanted)
	}
	// The agent step and the four untrusted steps each log that isolation is off.
	assert.Equal(t, 5, strings.Count(out, "untrusted: isolation is off: the key untrusted.isolation is off"))
}

// A DAG with no agent.run step keeps the old default: a step is trusted unless
// it has untrusted: true. The key untrusted: false changes nothing.
func TestUntrustedDefaultWithoutAgentDAG(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(append([]string{harness.IsolationOffEnv(t)}, trustEnv...), "start", "trust_default_plain.yaml")
	result.ExpectExitCode(0)
	out := result.Stdout() + result.Stderr()

	assert.Contains(t, out, "by_run:trusted")
	assert.Contains(t, out, "marked:untrusted")
	assert.Contains(t, out, "opted_out:trusted")
	assert.NotContains(t, out, "by_run:untrusted")
	assert.NotContains(t, out, "marked:trusted")
	assert.NotContains(t, out, "opted_out:untrusted")
	assert.Equal(t, 1, strings.Count(out, "untrusted: isolation is off: the key untrusted.isolation is off"))
}

// A trusted step passes an output of an untrusted step through env. This is
// valid also when the source step is untrusted by default.
func TestUntrustedDefaultGateWithEnvValidates(t *testing.T) {
	t.Parallel()

	harness.NewRunner(t).Run("validate", "trust_default_gate_env.yaml").ExpectExitCode(0)
}
