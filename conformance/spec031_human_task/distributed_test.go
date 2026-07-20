// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec031_human_task_test

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestDistributedHumanTaskReleasesWorkerAndResumesElsewhere(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	serverPort := harness.FreePort(t)
	coordinatorPort := harness.FreePort(t)

	services := dagu.StartWithEnv(
		env,
		"start-all",
		"--host=127.0.0.1",
		fmt.Sprintf("--port=%d", serverPort),
		"--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", coordinatorPort),
	)
	waitForTCPService(t, services, fmt.Sprintf("127.0.0.1:%d", coordinatorPort))

	workerOne := startWorker(t, dagu, env, coordinatorPort, "worker-1")
	defer workerOne.Stop()
	const workerOneProbeRunID = "spec031-worker-one-probe"
	workerOneProbe := dagu.RunWithEnv(env, "enqueue", "--run-id="+workerOneProbeRunID, "distributed_worker_one_probe.yaml")
	workerOneProbe.ExpectExitCode(0)
	waitForStatus(t, dagu, env, workerOneProbeRunID, "distributed_worker_one_probe.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("distributed-worker-one.txt"), "worker-1\n")

	const runID = "spec031-distributed"
	enqueued := dagu.RunWithEnv(env, "enqueue", "--run-id="+runID, "distributed_root.yaml")
	enqueued.ExpectExitCode(0)
	waitForStatus(t, dagu, env, runID, "distributed_root.yaml", "Waiting")
	waitForFileContent(t, dagu.ProjectPath("distributed-opened.txt"), "worker-1\n")

	const probeRunID = "spec031-distributed-probe"
	probe := dagu.RunWithEnv(env, "enqueue", "--run-id="+probeRunID, "distributed_probe.yaml")
	probe.ExpectExitCode(0)
	waitForStatus(t, dagu, env, probeRunID, "distributed_probe.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("distributed-probe.txt"), "worker-1\n")

	workerOne.Stop()
	workerTwo := startWorker(t, dagu, env, coordinatorPort, "worker-2")
	defer workerTwo.Stop()
	const workerTwoProbeRunID = "spec031-worker-two-probe"
	workerTwoProbe := dagu.RunWithEnv(env, "enqueue", "--run-id="+workerTwoProbeRunID, "distributed_worker_two_probe.yaml")
	workerTwoProbe.ExpectExitCode(0)
	waitForStatus(t, dagu, env, workerTwoProbeRunID, "distributed_worker_two_probe.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("distributed-worker-two.txt"), "worker-2\n")

	completed := complete(
		t,
		dagu,
		env,
		runID,
		"review",
		"distributed_root.yaml",
		"--input=environment=production",
	)
	completed.ExpectExitCode(0)
	completed.ExpectStdout("Completed human task review; DAG-run resume requested.\n")
	completed.ExpectStderr("")

	waitForDistributedSuccess(t, dagu, env, runID, workerTwo, services)
	waitForFileContent(t, dagu.ProjectPath("distributed-result.txt"), "production,worker-2\n")
}

func startWorker(
	t *testing.T,
	dagu *harness.Runner,
	env []string,
	coordinatorPort int,
	workerID string,
) *harness.Process {
	t.Helper()
	workerEnv := append(append([]string(nil), env...), "SPEC031_WORKER_MARKER="+workerID)
	workerEnv = append(workerEnv,
		"DAGU_ENV_PASSTHROUGH_PREFIXES=SPEC031_",
		"SPEC031_OPENED_FILE="+dagu.ProjectPath("distributed-opened.txt"),
		"SPEC031_PROBE_FILE="+dagu.ProjectPath("distributed-probe.txt"),
		"SPEC031_RESULT_FILE="+dagu.ProjectPath("distributed-result.txt"),
		"SPEC031_WORKER_ONE_FILE="+dagu.ProjectPath("distributed-worker-one.txt"),
		"SPEC031_WORKER_TWO_FILE="+dagu.ProjectPath("distributed-worker-two.txt"),
	)
	process := dagu.StartWithEnv(
		workerEnv,
		"worker",
		"--worker.id="+workerID,
		"--worker.max-active-runs=1",
		"--worker.health-port=0",
		"--worker.labels=spec031=true,identity="+workerID,
		fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", coordinatorPort),
	)
	select {
	case <-process.Done():
		t.Fatalf("worker %s exited during startup: %s", workerID, process.FailureOutput())
	case <-time.After(300 * time.Millisecond):
	}
	return process
}

func waitForTCPService(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("service exited during startup: %s", process.FailureOutput())
		default:
		}
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service did not listen on %s", address)
}

func waitForDistributedSuccess(
	t *testing.T,
	dagu *harness.Runner,
	env []string,
	runID string,
	worker *harness.Process,
	services *harness.Process,
) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var status *harness.Result
	for time.Now().Before(deadline) {
		select {
		case <-worker.Done():
			t.Fatalf("worker exited while resuming the DAG-run: %s", worker.FailureOutput())
		case <-services.Done():
			t.Fatalf("services exited while resuming the DAG-run: %s", services.FailureOutput())
		default:
		}
		status = dagu.RunWithEnv(env, "status", "--run-id="+runID, "distributed_root.yaml")
		if status.ExitCode() == 0 && strings.Contains(status.Stdout(), "Succeeded") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	worker.Stop()
	services.Stop()
	t.Fatalf(
		"distributed DAG-run did not succeed\nstatus:\n%s\nworker:\n%s\nservices:\n%s",
		status.Stdout()+status.Stderr(),
		worker.FailureOutput(),
		services.FailureOutput(),
	)
}
