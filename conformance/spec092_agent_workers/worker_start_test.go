// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// workerAgentProject copies the fixtures and makes the fake claude executable.
// The mode of a file that git checks out depends on the platform, so the test
// sets it.
func workerAgentProject(t *testing.T) (*harness.Runner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("an agent worker runs on Linux: it checks the execute bits of its binary")
	}
	dagu := harness.NewRunner(t)
	binary := dagu.ProjectPath(filepath.Join("worker_bin", "claude"))
	require.NoError(t, os.Chmod(binary, 0o755))
	return dagu, binary
}

// Spec 092, "Start": each failed check stops the worker with the exit code 1,
// before it connects to the coordinator. The error names the option or the key,
// the expected value and the value.
func TestWorkerAgentStartChecks(t *testing.T) {
	t.Parallel()

	dagu, binary := workerAgentProject(t)
	for _, tc := range []struct {
		name    string
		env     []string
		args    []string
		message []string
	}{
		{name: "Form", args: []string{"--agent", "claude"},
			message: []string{`--agent: the value "claude" does not have the form <provider>=<path>`}},
		{name: "TwoOptions", args: []string{"--agent", "claude=" + binary, "--agent", "codex=" + binary},
			message: []string{"--agent: the option is given 2 times: an agent worker serves one provider"}},
		{name: "NotACLIProvider", args: []string{"--agent", "anthropic=" + binary},
			message: []string{`--agent: provider "anthropic" is not a CLI provider: expected one of`, "claude"}},
		{name: "RelativePath", args: []string{"--agent", "claude=bin/claude"},
			message: []string{`--agent: provider "claude": the path "bin/claude" is not absolute`}},
		{name: "MissingBinary", args: []string{"--agent", "claude=/opt/none/claude"},
			message: []string{`--agent: provider "claude": binary /opt/none/claude does not exist`}},
		{name: "MaxActiveRunsFlag", args: []string{"--config", "worker_mtls_config.yaml", "--agent", "claude=" + binary, "--worker.max-active-runs", "4"},
			message: []string{`--agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run`}},
		{name: "MaxActiveRunsVariable", env: []string{"DAGU_WORKER_MAX_ACTIVE_RUNS=4"}, args: []string{"--config", "worker_mtls_config.yaml", "--agent", "claude=" + binary},
			message: []string{`--agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run`}},
		{name: "MaxActiveRunsFile", args: []string{"--config", "worker_max_active_runs_config.yaml", "--agent", "claude=" + binary},
			message: []string{`--agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run`}},
		{name: "Insecure", args: []string{"--agent", "claude=" + binary},
			message: []string{`--agent: key "peer.insecure" is true: an agent worker needs mutual TLS to the coordinator`}},
		{name: "SkipTLSVerify", args: []string{"--config", "worker_mtls_config.yaml", "--agent", "claude=" + binary, "--peer.skip-tls-verify"},
			message: []string{`--agent: key "peer.skip_tls_verify" is true: an agent worker needs mutual TLS to the coordinator`}},
		{name: "NoClientCA", args: []string{"--config", "worker_mtls_config.yaml", "--agent", "claude=" + binary, "--peer.client-ca-file="},
			message: []string{`--agent: key "peer.client_ca_file" is not set: an agent worker needs mutual TLS to the coordinator`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := dagu.RunWithEnv(tc.env, append([]string{"worker", "--worker.health-port=0"}, tc.args...)...)
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// Spec 092, "Reserved labels": a key of worker.labels that starts with agent.
// stops the worker at start, also for a general worker.
func TestWorkerReservedLabelStopsTheWorker(t *testing.T) {
	t.Parallel()

	dagu, _ := workerAgentProject(t)
	result := dagu.Run("worker", "--worker.health-port=0", "--worker.coordinators=127.0.0.1:1",
		"--worker.labels=agent.provider.claude=true")
	result.ExpectExitCode(1)
	result.ExpectStderrContains(`worker label "agent.provider.claude" is reserved`)
}
