// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// weatherID is the ID newLibraryInstall registers project weather under.
const weatherID = "5b0c43a2-5f0e-4f55-9d0a-4a8f3c1c2e11"

// workerDAG returns a DAG whose one step runs on a worker labeled gpu and
// writes the path of its run's log to marker, which shows where it ran.
func workerDAG(marker string) string {
	return fmt.Sprintf("worker_selector:\n  gpu: \"true\"\nsteps:\n  - id: mark\n    run: echo \"$DAG_RUN_LOG_FILE\" > %q\n", marker)
}

// TestDistributedProjectRuns proves Spec 102 "Distributed Execution": a
// child of a run of a project other than the default one, and a child of a
// library DAG's run in either project, go to a worker, and the coordinator
// records each among the runs of the parent's project, where the parent
// finds its status. The worker keeps each project's runs apart.
func TestDistributedProjectRuns(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	markers := t.TempDir()
	// The default project has a DAG of the same name, which never runs.
	in.write(t, "dags/gpu.yaml", "steps:\n  - id: noop\n    run: \"true\"\n")
	in.write(t, "projects/weather/gpu.yaml", workerDAG(filepath.Join(markers, "weather")))
	in.write(t, "projects/weather/caller.yaml", callerOf("gpu"))
	in.write(t, "library/remote.yaml", workerDAG(filepath.Join(markers, "library")))
	library := in.write(t, "library/etl.yaml", callerOf("remote"))
	stop := startCluster(t, in)
	defer stop()

	in.run(true, nil, "start", "--project", "weather", "caller").ExpectExitCode(0)
	ranOn(t, filepath.Join(markers, "weather"), "@project-"+weatherID)
	in.run(true, nil, "status", "--project", "weather", "caller").ExpectExitCode(0)
	in.run(true, nil, "status", "gpu").ExpectNonZeroExitCode()

	in.run(true, nil, "start", "--project", "weather", library).ExpectExitCode(0)
	ranOn(t, filepath.Join(markers, "library"), filepath.Join("@project-"+weatherID, "@library"))

	in.run(true, nil, "start", library).ExpectExitCode(0)
	got := ranOn(t, filepath.Join(markers, "library"), "@library")
	require.NotContains(t, got, "@project-")
}

// ranOn reads the log path a step wrote to marker, requires that the step
// ran on worker gpu-1 in the directory named dir, and returns the path.
func ranOn(t *testing.T, marker, dir string) string {
	t.Helper()
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	got := filepath.Clean(strings.TrimSpace(string(data)))
	require.Contains(t, got, filepath.Join("worker-logs", "gpu-1", dir)+string(filepath.Separator))
	return got
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
