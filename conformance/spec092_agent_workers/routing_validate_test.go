// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Spec 092, "Routing" and "Reserved labels": dagu validate rejects a DAG that
// needs two providers, a key of worker_selector that starts with agent. in a DAG
// or in a step, and a DAG that needs a provider with worker_selector: local.
func TestRoutingValidateRejects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"routing_two_providers.yaml", []string{`the DAG needs the providers "claude" and "codex": a run goes to one agent worker`}},
		{"routing_reserved_selector.yaml", []string{`worker selector "agent.provider.claude"`, "belong to Dagu"}},
		{"routing_reserved_step_selector.yaml", []string{`worker selector "agent.provider.claude"`, "belong to Dagu"}},
		{"routing_local_selector.yaml", []string{`worker_selector "local" is not valid`, `the DAG needs the provider "claude", so a run goes to an agent worker`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// A DAG with one provider is valid. The provider of a sub-DAG does not count
// for the parent, and the sub-DAG is valid on its own.
func TestRoutingValidateAccepts(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"routing_one_provider.yaml", "routing_child.yaml"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			harness.NewRunner(t).Run("validate", fixture).ExpectExitCode(0)
		})
	}
}
