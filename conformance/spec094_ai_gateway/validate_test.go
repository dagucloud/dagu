// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec094_ai_gateway_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
)

// Spec 094, "agent.run" and "Errors": dagu validate checks the form of the key
// endpoint. It rejects a value that is not a string, is empty, does not match the
// rule of the name or is a value reference. It does not read the store, so it
// cannot tell if the endpoint exists: a valid name passes.
func TestValidateChecksTheFormOfTheEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"invalid_endpoint_not_a_name.yaml", []string{"with.endpoint.name", "must match ^[a-z][a-z0-9-]{0,63}$"}},
		{"invalid_endpoint_empty.yaml", []string{"with.endpoint.name", "must be a non-empty string"}},
		{"invalid_endpoint_number.yaml", []string{"with.endpoint.name", "must be a non-empty string"}},
		{"invalid_endpoint_reference.yaml", []string{"with.endpoint.name", "must be a literal name, not a reference"}},
		{"invalid_endpoint_too_long.yaml", []string{"with.endpoint.name", "must match ^[a-z][a-z0-9-]{0,63}$"}},
		{"invalid_endpoint_other_provider.yaml", []string{"with.endpoint", `not supported by the provider "codex"`}},
		// An endpoint with a provider in the same with is a contradiction.
		{"invalid_endpoint_chat_completion.yaml", []string{`key "endpoint" and key "provider" contradict each other`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message...)
		})
	}

	dagu := harness.NewRunner(t)
	dagu.Run("validate", "agent_endpoint.yaml").ExpectExitCode(0)

	// A step of the CLI claude with no endpoint is invalid: the DAG does not load.
	missing := dagu.Run("validate", "agent_no_endpoint.yaml")
	missing.ExpectNonZeroExitCode()
	missing.ExpectStderrContains("with.endpoint", "agent.run needs an endpoint; add one on the AI page and name it here")

	// So is a step of the CLI claude with no model.
	noModel := dagu.Run("validate", "agent_no_model.yaml")
	noModel.ExpectNonZeroExitCode()
	noModel.ExpectStderrContains("with.endpoint.model", "agent.run needs a model; choose one that the endpoint lists")
}

// A step that names an endpoint is checked before the CLI starts (Spec 094, "Start
// checks"): an endpoint that the store does not hold fails the step with the reason
// endpoint_unknown. No request goes to a provider. A step with no endpoint is
// refused when the DAG loads, so it never starts. This is the local mode: no
// coordinator and no worker.
func TestStepWithAnUnknownEndpointFailsBeforeTheCLIStarts(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI of the fixtures is a POSIX script")
	}

	dagu := harness.NewRunner(t)
	aitest.InstallFixtures(t, dagu)
	fake := t.TempDir()
	home := filepath.Join(t.TempDir(), "dagu")
	harness.WriteIsolationOff(t, home)
	env := []string{
		"PATH=" + dagu.ProjectPath("bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DAGU_HOME=" + home,
		"DAGU_COORDINATOR_ENABLED=false",
		"FAKE_CLAUDE_DIR=" + fake,
	}

	result := dagu.RunWithEnv(env, "start", "agent_endpoint.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`endpoint "test-endpoint"`, "endpoint_unknown")
	_, err := os.Stat(filepath.Join(fake, "ran"))
	assert.True(t, os.IsNotExist(err), "the CLI did not start")

	missing := dagu.RunWithEnv(env, "start", "agent_no_endpoint.yaml")
	missing.ExpectNonZeroExitCode()
	missing.ExpectStderrContains("with.endpoint", "agent.run needs an endpoint")
	_, err = os.Stat(filepath.Join(fake, "ran"))
	assert.True(t, os.IsNotExist(err), "a step with no endpoint never starts the CLI")

	noModel := dagu.RunWithEnv(env, "start", "agent_no_model.yaml")
	noModel.ExpectNonZeroExitCode()
	noModel.ExpectStderrContains("with.endpoint.model", "agent.run needs a model")
	_, err = os.Stat(filepath.Join(fake, "ran"))
	assert.True(t, os.IsNotExist(err), "a step with no model never starts the CLI")
}
