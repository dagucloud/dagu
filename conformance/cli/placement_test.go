// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// dagu start sends a run with a worker selector, from the DAG or from
// --worker-label, to the coordinator, as the start request does. With no
// coordinator to reach, the command fails and the run does not run on the
// Dagu host.
func TestStartWithSelectorNeedsCoordinator(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"start", "worker_selector.yaml"},
		{"start", "--worker-label", "test=true", "no_selector.yaml"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(sharedEnv(t), args...)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("no coordinators available")
			dagu.ExpectNoFile("placed.out")
		})
	}
}

// With coordinator.enabled false, dagu start runs a DAG with a worker
// selector on the Dagu host.
func TestStartWithSelectorWithoutCoordinator(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := append(sharedEnv(t), "DAGU_COORDINATOR_ENABLED=false")
	dagu.RunWithEnv(env, "start", "worker_selector.yaml").ExpectExitCode(0)
	dagu.ExpectFileContent("placed.out", "placed")
}
