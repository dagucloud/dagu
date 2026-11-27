// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// dagu start binds a node: required DAG to a worker node and runs it on that
// worker (Spec 089 §Narrowing a run). The Dagu host refuses a run bound to a
// worker node, so the run succeeds only on the worker.
func TestStartBoundToWorkerNode(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"), "DAGU_AUTH_MODE=none"}

	serverPort, coordinatorPort := harness.FreePort(t), harness.FreePort(t)
	services := dagu.StartWithEnv(env,
		"start-all",
		"--host=127.0.0.1",
		fmt.Sprintf("--port=%d", serverPort),
		"--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", coordinatorPort),
	)
	defer services.Stop()
	waitForCoordinator(t, services, fmt.Sprintf("127.0.0.1:%d", coordinatorPort))
	worker := dagu.StartWithEnv(env,
		"worker",
		"--worker.id=gw-1",
		"--worker.health-port=0",
		fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", coordinatorPort),
	)
	defer worker.Stop()
	waitForWorkerNode(t, dagu, env, "gw-1")

	dagu.RunWithEnv(env, "start", "--node", "GW-1", "node_required_direct.yaml",
		"--", "DIR="+filepath.ToSlash(dagu.ProjectPath("."))).ExpectExitCode(0)
	dagu.ExpectFileContent("bound-gw-1", "ran")
}

func waitForCoordinator(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("service exited during startup: %s", process.FailureOutput())
		default:
		}
		if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			require.NoError(t, connection.Close())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service did not listen on %s", address)
}

// waitForWorkerNode waits until the coordinator records the worker as a
// node.
func waitForWorkerNode(t *testing.T, dagu *harness.Runner, env []string, name string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	var last *harness.Result
	for time.Now().Before(deadline) {
		last = dagu.RunWithEnv(env, "nodes", "show", name)
		if last.ExitCode() == 0 && strings.Contains(last.Stdout(), "online") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("worker node %s was not recorded:\nstdout:\n%s\nstderr:\n%s", name, last.Stdout(), last.Stderr())
}
