// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// gpuDAG runs its one step on a worker labeled gpu.
const gpuDAG = "worker_selector:\n  gpu: \"true\"\nsteps:\n  - id: noop\n    run: \"true\"\n"

// TestDistributedProjectRuns proves Spec 102 "Distributed Execution": a run
// of a project other than the default one, and a child of a library DAG's
// run, go to a worker, and the coordinator records each in the stores of
// the run's project, where the command that started it finds its status.
func TestDistributedProjectRuns(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	in.write(t, "projects/weather/gpu.yaml", gpuDAG)
	in.write(t, "library/remote.yaml", gpuDAG)
	library := in.write(t, "library/etl.yaml", callerOf("remote"))
	stop := startCluster(t, in)
	defer stop()

	in.run(true, nil, "start", "--project", "weather", "gpu").ExpectExitCode(0)
	status := in.run(true, nil, "status", "--project", "weather", "gpu")
	status.ExpectExitCode(0)
	require.Contains(t, strings.ToLower(status.Stdout()), "succeeded")
	// The default project has no DAG named gpu and records no run of it.
	in.run(true, nil, "status", "gpu").ExpectNonZeroExitCode()

	// The child's run is recorded under its root's, among the library's runs.
	in.run(true, nil, "start", library).ExpectExitCode(0)
}

// startCluster starts a coordinator and a worker labeled gpu for in, with
// projects on, and returns what stops them.
func startCluster(t *testing.T, in *libraryInstall) func() {
	t.Helper()
	env := append(slices.Clone(in.env), "DAGU_EXPERIMENTAL_PROJECTS=true")
	port := harness.FreePort(t)
	coordinator := in.dagu.StartWithEnv(env,
		"coordinator",
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", port),
		"--coordinator.health-port=0",
	)
	waitForPort(t, coordinator, fmt.Sprintf("127.0.0.1:%d", port))
	worker := in.dagu.StartWithEnv(env,
		"worker",
		"--worker.id=gpu-1",
		"--worker.health-port=0",
		"--worker.labels=gpu=true",
		fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", port),
	)
	return func() {
		worker.Stop()
		coordinator.Stop()
	}
}

func waitForPort(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("service exited during startup: %s", process.FailureOutput())
		default:
		}
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service did not listen on %s", address)
}
