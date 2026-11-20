// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/test/mtls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "The agent worker", "Routing" and "Worker pools", end to end: a
// coordinator with mutual TLS and pools, an agent worker of claude and a general
// worker. The run that needs claude goes to the agent worker, which takes this
// one run and exits with the code 0. The run that needs no provider goes to the
// general worker. The agent step names a skill: the skill reaches the agent
// worker, which has its own home, in the workspace bundle. The raw stream of the CLI of the agent worker is on the
// coordinator, next to the stdout log of the step (Spec 091, "Outputs").
//
// An agent worker needs the user ID 0 and the capabilities of Spec 092, "Start".
// Run this test as root, for example in a container with no-new-privileges.
func TestWorkerAgentTakesOneRunAndExits(t *testing.T) {
	harness.RequireRestrictedUserHost(t)
	dagu, fake := workerAgentProject(t)
	binary := workerPublicBinary(t, fake)

	tlsDir := dagu.ProjectPath("tls")
	require.NoError(t, os.MkdirAll(tlsDir, 0o700))
	ca := mtls.NewAuthority(t, tlsDir, "dagu-ca")
	ca.Server(t, "coordinator")
	ca.Client(t, "agent-claude")
	ca.Client(t, "general-worker")

	home := filepath.Join(t.TempDir(), "dagu")
	// The agent step names an endpoint (Spec 091): the coordinator holds it.
	harness.SeedTestEndpoint(t, dagu, home)
	agentFile := dagu.ProjectPath("agent-run.txt")
	plainFile := dagu.ProjectPath("plain-run.txt")
	env := []string{
		"DAGU_HOME=" + home,
		"SPEC092_AGENT_RUN_FILE=" + agentFile,
		"SPEC092_PLAIN_RUN_FILE=" + plainFile,
	}
	coordinatorPort := harness.FreePort(t)
	services := dagu.StartWithEnv(env,
		"start-all", "--config", "pools_coordinator_config.yaml",
		"--host=127.0.0.1", fmt.Sprintf("--port=%d", harness.FreePort(t)), "--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1", "--coordinator.advertise=127.0.0.1", fmt.Sprintf("--coordinator.port=%d", coordinatorPort),
	)
	workerWaitForTCP(t, services, fmt.Sprintf("127.0.0.1:%d", coordinatorPort))

	coordinators := fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", coordinatorPort)
	general := dagu.StartWithEnv(append(env, "SPEC092_WORKER_MARKER=general-1"),
		"worker", "--config", "worker_general_config.yaml", "--worker.id=general-1", "--worker.health-port=0", coordinators)
	// The agent worker has its own home, as a worker with no shared disk has. A
	// file in the home of the coordinator comes from the coordinator.
	agentHome := filepath.Join(t.TempDir(), "dagu-agent")
	agent := dagu.StartWithEnv(append(env, "SPEC092_WORKER_MARKER=agent-1", "DAGU_HOME="+agentHome),
		"worker", "--config", "worker_agent_config.yaml", "--agent", "claude="+binary,
		"--worker.id=agent-1", "--worker.health-port=0", coordinators)

	dagu.RunWithEnv(env, "enqueue", "--config", "pools_coordinator_config.yaml", "--run-id=spec092-agent", "worker_agent_run.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "enqueue", "--config", "pools_coordinator_config.yaml", "--run-id=spec092-plain", "worker_plain_run.yaml").ExpectExitCode(0)

	workerWaitForFile(t, agentFile, "agent-1\n", services, general, agent)
	workerWaitForFile(t, plainFile, "general-1\n", services, general)

	select {
	case <-agent.Done():
	case <-time.After(harness.WaitTimeout(t)):
		t.Fatalf("the agent worker did not exit after its run:\n%s", agent.Output())
	}
	require.Equal(t, 0, agent.ExitCode(), "the agent worker exits with the code 0 after its run:\n%s", agent.FailureOutput())
	require.Contains(t, agent.Output(), "Agent worker run ended; the worker takes no other run")

	// The stdout log of the step is on the coordinator, and the stream is next to
	// it, with the suffix of the local run.
	stream := workerWaitForFileNamed(t, home, "code.stdout.log"+ir.AgentStreamSuffix, services, agent)
	events, err := os.ReadFile(stream) // #nosec G304 -- the path is in the home of the test.
	require.NoError(t, err)
	// The skill of the DAG reached the agent worker in the workspace bundle. The
	// restricted user read it in the agent directory of the attempt and could not
	// write the directory (Spec 091, "Skills"). The folder is mounted as it is, so the
	// file of its references/ directory is there too.
	const result = "done skills: project-rules refs: project-rules/references/checklist.md write:denied"
	assert.Equal(t, `{"type":"system","subtype":"init","model":"sonnet","mcp_servers":[]}`+"\n"+
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"`+result+`"}`+"\n", string(events))
	stdout, err := os.ReadFile(strings.TrimSuffix(stream, ir.AgentStreamSuffix)) // #nosec G304 -- the path is in the home of the test.
	require.NoError(t, err)
	assert.Equal(t, result+"\n", string(stdout))
	// Spec 092, "End": the worker tells the coordinator that it is gone before
	// it exits. The coordinator lists it no more, well before its last
	// heartbeat is stale.
	listed := workerListedIDs(t, tlsDir, coordinatorPort)
	require.NotContains(t, listed, "agent-1", "the coordinator still lists the agent worker after its exit")
	require.Contains(t, listed, "general-1")

	select {
	case <-general.Done():
		t.Fatalf("the general worker exited:\n%s", general.FailureOutput())
	default:
	}
}

// workerListedIDs returns the IDs of the workers that the coordinator lists. The
// call uses the certificate of the coordinator, whose identity is the
// dispatcher of the test.
func workerListedIDs(t *testing.T, tlsDir string, port int) []string {
	t.Helper()
	cfg := coordinator.DefaultConfig()
	cfg.Insecure = false
	cfg.CertFile = filepath.Join(tlsDir, "coordinator.crt")
	cfg.KeyFile = filepath.Join(tlsDir, "coordinator.key")
	cfg.CAFile = filepath.Join(tlsDir, "dagu-ca.crt")
	registry, err := coordinator.NewStaticRegistry([]string{fmt.Sprintf("127.0.0.1:%d", port)})
	require.NoError(t, err)
	client := coordinator.New(registry, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() { _ = client.Cleanup(ctx) }()
	workers, err := client.GetWorkers(ctx)
	require.NoError(t, err)
	ids := make([]string, 0, len(workers))
	for _, worker := range workers {
		ids = append(ids, worker.WorkerId)
	}
	return ids
}

// workerPublicBinary copies the fake claude to a folder that each user can
// read, because the restricted user runs it.
func workerPublicBinary(t *testing.T, fake string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "spec092-agent-bin-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0o755))
	data, err := os.ReadFile(fake)
	require.NoError(t, err)
	binary := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(binary, data, 0o600))
	require.NoError(t, os.Chmod(binary, 0o755))
	return binary
}

func workerWaitForTCP(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("the services exited during startup: %s", process.FailureOutput())
		default:
		}
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the services did not listen on %s", address)
}

// workerWaitForFileNamed waits for a file with the given name under root and
// returns its path.
func workerWaitForFileNamed(t *testing.T, root, name string, processes ...*harness.Process) string {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for {
		var found string
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && entry.Name() == name {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var output strings.Builder
	for _, process := range processes {
		output.WriteString(process.Output())
		output.WriteString("\n----\n")
	}
	t.Fatalf("no file %s under %s\n%s", name, root, output.String())
	return ""
}

func workerWaitForFile(t *testing.T, path, content string, processes ...*harness.Process) {
	t.Helper()
	deadline := time.Now().Add(2 * harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) // #nosec G304 -- the path is in the project of the test.
		if err == nil && string(data) == content {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	var output strings.Builder
	for _, process := range processes {
		output.WriteString(process.Output())
		output.WriteString("\n----\n")
	}
	data, _ := os.ReadFile(path) // #nosec G304 -- the path is in the project of the test.
	t.Fatalf("%s has %q, expected %q\n%s", path, data, content, output.String())
}
