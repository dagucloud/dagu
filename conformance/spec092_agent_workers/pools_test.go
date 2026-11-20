// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Spec 092, "Worker pools", "Configuration": an invalid coordinator.worker_pools
// stops the coordinator at start with the exit code 1. The error names the key.
func TestPoolsInvalidConfigurationStopsTheCoordinator(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"pools_no_tls_config.yaml", []string{"coordinator.worker_pools", `key "peer.cert_file" is not set`}},
		{"pools_invalid_config.yaml", []string{`unknown key "coordinator.worker_pools[0].label"`}},
		{"pools_not_cli_provider_config.yaml", []string{`"coordinator.worker_pools[0].labels.agent.provider.gemini"`, `"gemini" is not a CLI provider`}},
		{"pools_no_dispatchers_config.yaml", []string{`key "coordinator.dispatchers" is not set`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("coordinator", "--config", tc.fixture, "--coordinator.host=127.0.0.1",
				fmt.Sprintf("--coordinator.port=%d", harness.FreePort(t)), "--coordinator.health-port=0")
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// dagu config prints the pools (the name, the identities and the labels) and the
// dispatchers.
func TestPoolsConfigPrintsThePools(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("config", "--config", "pools_coordinator_config.yaml")
	result.ExpectExitCode(0)
	for _, want := range []string{
		"Worker pools (name, identities, labels):",
		"agent-claude:", `agent.provider.claude="true"`,
		"general:", "general-worker", `zone="*"`,
		"Dispatchers (coordinator.dispatchers): coordinator",
	} {
		if !strings.Contains(result.Stdout(), want) {
			t.Fatalf("dagu config does not print %q:\n%s", want, result.Stdout())
		}
	}
}
