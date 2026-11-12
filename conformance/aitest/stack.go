// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aitest

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test/mtls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stack is the services of a distributed run of the AI gateway (Spec 094): a coordinator with mutual
// TLS and the pools of Spec 092, with the scheduler and the server, and the agent
// workers of claude that the test starts. The agent worker takes one run and exits,
// so a test starts a worker for each run.
type Stack struct {
	t    *testing.T
	dagu *harness.Runner
	// Server is the REST client of the server of the services.
	Server *Server
	// Services are the coordinator, the scheduler and the server.
	Services *harness.Process
	env      []string
	port     int
	// Dir is the directory that the fake CLI and the test share. The CLI can run as
	// the restricted user, so the directory is open to each user.
	Dir string
	// agentHome is the home of the agent workers. It has no file of the coordinator.
	agentHome string
	binary    string
	workers   []*harness.Process
}

// StartStack starts the services with the configuration file of the testdata.
// An agent worker needs the host of harness.RequireRestrictedUserHost: the test
// skips otherwise.
func StartStack(t *testing.T, config string) *Stack {
	t.Helper()
	return StartStackAs(t, config, true)
}

// StartStackAs starts the services. A test of a general worker needs no restricted
// user, so it can run with any user ID: it passes needsRoot false.
func StartStackAs(t *testing.T, config string, needsRoot bool) *Stack {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a worker of this suite runs on Linux")
	}
	if needsRoot {
		harness.RequireRestrictedUserHost(t)
	}
	dagu := harness.NewRunner(t)
	InstallFixtures(t, dagu)

	tlsDir := dagu.ProjectPath("tls")
	require.NoError(t, os.MkdirAll(tlsDir, 0o700))
	ca := mtls.NewAuthority(t, tlsDir, "dagu-ca")
	ca.Server(t, "coordinator")
	ca.Client(t, "agent-claude")
	ca.Client(t, "general-worker")

	shared, err := os.MkdirTemp("", "aitest-shared-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(shared) })
	require.NoError(t, os.Chmod(shared, 0o777)) // #nosec G302 -- the restricted user writes in the folder.

	// The restricted user runs the fake CLI: the binary and the script are in a
	// folder that each user can read.
	binDir, err := os.MkdirTemp("", "aitest-agent-bin-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })
	require.NoError(t, os.Chmod(binDir, 0o755)) // #nosec G302 -- the restricted user reads the folder.
	for _, name := range []string{"claude", "relay_cli.py"} {
		data, err := os.ReadFile(dagu.ProjectPath(filepath.Join("bin", name))) // #nosec G304 -- the path is in the project of the test.
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), data, 0o755)) // #nosec G306 G703 -- the name is a fixed file name, and the restricted user must read and run the file.
	}

	home := filepath.Join(t.TempDir(), "dagu")
	s := &Stack{
		t:         t,
		dagu:      dagu,
		Dir:       shared,
		agentHome: filepath.Join(t.TempDir(), "dagu-agent"),
		binary:    filepath.Join(binDir, "claude"),
		port:      harness.FreePort(t),
		env:       []string{"DAGU_HOME=" + home, "AITEST_DIR=" + shared},
	}
	serverPort := harness.FreePort(t)
	s.Services = dagu.StartWithEnv(s.env,
		"start-all", "--config", config,
		"--host=127.0.0.1", fmt.Sprintf("--port=%d", serverPort), "--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1", "--coordinator.advertise=127.0.0.1", fmt.Sprintf("--coordinator.port=%d", s.port),
	)
	s.Server = &Server{
		t: t, base: fmt.Sprintf("http://127.0.0.1:%d/api/v1", serverPort), Home: home, proc: s.Services,
	}
	waitForTCP(t, s.Services, fmt.Sprintf("127.0.0.1:%d", s.port))
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for {
		resp, err := http.Get(s.Server.base + "/health") //nolint:gosec,noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "the server did not answer", "%s", s.Services.Output())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return s
}

func waitForTCP(t *testing.T, process *harness.Process, address string) {
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

// Scenario tells the fake CLI what to do, and clears the findings of the last run.
func (s *Stack) Scenario(name string, files map[string]string) {
	s.t.Helper()
	entries, err := os.ReadDir(s.Dir)
	require.NoError(s.t, err)
	for _, entry := range entries {
		if entry.Name() != "ran" {
			require.NoError(s.t, os.RemoveAll(filepath.Join(s.Dir, entry.Name())))
		}
	}
	s.Write("scenario", name)
	for file, content := range files {
		s.Write(file, content)
	}
}

// Write writes a file of the shared directory.
func (s *Stack) Write(name, content string) {
	s.t.Helper()
	path := filepath.Join(s.Dir, name)
	require.NoError(s.t, os.WriteFile(path, []byte(content), 0o644)) // #nosec G306 -- the restricted user reads the file.
}

// StartAgentWorker starts an agent worker of claude with the binary of the test.
// The worker takes one run and exits.
func (s *Stack) StartAgentWorker(id string) *harness.Process {
	s.t.Helper()
	env := append(append([]string(nil), s.env...), "DAGU_HOME="+s.agentHome)
	worker := s.dagu.StartWithEnv(env,
		"worker", "--config", "relay_worker_agent_config.yaml", "--agent", "claude="+s.binary,
		"--worker.id="+id, "--worker.health-port=0", fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", s.port))
	s.workers = append(s.workers, worker)
	return worker
}

// StartGeneralWorker starts a general worker with the label zone=eu. It has its own
// home. It takes each run that its pool allows, and it does not exit.
func (s *Stack) StartGeneralWorker(id string) *harness.Process {
	s.t.Helper()
	env := append(append([]string(nil), s.env...), "DAGU_HOME="+s.agentHome)
	worker := s.dagu.StartWithEnv(env,
		"worker", "--config", "relay_worker_general_config.yaml",
		"--worker.id="+id, "--worker.health-port=0", fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", s.port))
	s.workers = append(s.workers, worker)
	return worker
}

// Enqueue puts a run in the queue. The scheduler dispatches it to the agent worker.
func (s *Stack) Enqueue(dagFile, runID string) {
	s.t.Helper()
	s.EnqueueWith(dagFile, runID)
}

// EnqueueWith puts a run in the queue with parameters.
func (s *Stack) EnqueueWith(dagFile, runID string, params ...string) {
	s.t.Helper()
	args := []string{"enqueue", "--config", "relay_coordinator_config.yaml", "--run-id=" + runID, dagFile}
	if len(params) > 0 {
		args = append(args, "--")
		args = append(args, params...)
	}
	s.dagu.RunWithEnv(s.env, args...).ExpectExitCode(0)
}

// RunDetails is the part of a DAG run that the tests read.
type RunDetails struct {
	DAGRunDetails struct {
		StatusLabel string `json:"statusLabel"`
		Error       string `json:"error"`
		Nodes       []struct {
			StepName    string `json:"stepName"`
			StatusLabel string `json:"statusLabel"`
			Error       string `json:"error"`
		} `json:"nodes"`
	} `json:"dagRunDetails"`
}

// Finished tells if the run has a final status.
func (r RunDetails) Finished() bool {
	switch r.DAGRunDetails.StatusLabel {
	case "succeeded", "failed", "aborted", "partially_succeeded", "rejected":
		return true
	}
	return false
}

// ErrorText joins each error of the run: the run and its steps.
func (r RunDetails) ErrorText() string {
	parts := []string{r.DAGRunDetails.Error}
	for _, node := range r.DAGRunDetails.Nodes {
		parts = append(parts, node.Error)
	}
	return strings.Join(parts, "\n")
}

// Details reads the run through the REST API. It returns false when the run is unknown.
func (s *Stack) Details(dag, runID string) (RunDetails, bool) {
	s.t.Helper()
	status, body := s.Server.Call(http.MethodGet, "/dag-runs/"+dag+"/"+runID, nil)
	if status != http.StatusOK {
		return RunDetails{}, false
	}
	var details RunDetails
	Decode(s.t, body, &details)
	return details, true
}

// WaitRun waits until the run has a final status and returns it. The workers and
// the services are in the failure message.
func (s *Stack) WaitRun(dag, runID string) RunDetails {
	s.t.Helper()
	deadline := time.Now().Add(2 * harness.WaitTimeout(s.t))
	for time.Now().Before(deadline) {
		if details, ok := s.Details(dag, runID); ok && details.Finished() {
			return details
		}
		time.Sleep(200 * time.Millisecond)
	}
	var output strings.Builder
	output.WriteString(s.Services.Output())
	for _, worker := range s.workers {
		output.WriteString("\n---- worker\n" + worker.Output())
	}
	s.t.Fatalf("the run %s of %s did not finish\n%s", runID, dag, output.String())
	return RunDetails{}
}

// WaitFile waits for a file of the shared directory.
func (s *Stack) WaitFile(name string) {
	s.t.Helper()
	deadline := time.Now().Add(2 * harness.WaitTimeout(s.t))
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(s.Dir, name)); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var output strings.Builder
	output.WriteString(s.Services.Output())
	for _, worker := range s.workers {
		output.WriteString("\n---- worker\n" + worker.Output())
	}
	s.t.Fatalf("the file %s did not appear\n%s", name, output.String())
}

// Read returns a file of the shared directory.
func (s *Stack) Read(name string) string {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, name)) // #nosec G304 -- the path is in the shared directory of the test.
	require.NoError(s.t, err, "the CLI wrote no file %s", name)
	return string(data)
}

// RanCount returns how many times the fake CLI started.
func (s *Stack) RanCount() int {
	data, err := os.ReadFile(filepath.Join(s.Dir, "ran")) // #nosec G304 -- the path is in the shared directory of the test.
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "ran")
}

// CreateEndpoint creates the endpoint test-endpoint of the preset custom, which accepts the
// scheme http, with the base URL of the fake provider.
func (s *Stack) CreateEndpoint(baseURL, key string, dags ...string) {
	s.t.Helper()
	status, body := s.Server.Call(http.MethodPost, "/ai/endpoints", map[string]any{
		"name": "test-endpoint", "preset": "custom", "protocol": "anthropic", "baseUrl": baseURL,
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": dags}, "key": key,
	})
	require.Equal(s.t, http.StatusCreated, status, body)
}

// ExpectWorkerExit waits for the agent worker to exit with the code 0.
func (s *Stack) ExpectWorkerExit(worker *harness.Process) {
	s.t.Helper()
	select {
	case <-worker.Done():
	case <-time.After(harness.WaitTimeout(s.t)):
		s.t.Fatalf("the agent worker did not exit after its run:\n%s", worker.Output())
	}
	require.Equal(s.t, 0, worker.ExitCode(), "the agent worker exits with the code 0:\n%s", worker.FailureOutput())
}

// AssertKeyNowhere checks that the key of the provider is in no file of the homes,
// of the shared directory and of the project, and in no output of a process. The
// coordinator stores the key encrypted, so its home holds no plain key. The
// placeholder of the CLI is in the files of the shared directory only.
func (s *Stack) AssertKeyNowhere(key string, placeholders ...string) {
	s.t.Helper()
	outputs := []string{s.Services.Output()}
	for _, worker := range s.workers {
		outputs = append(outputs, worker.Output())
	}
	for _, output := range outputs {
		assert.NotContains(s.t, output, key, "an output holds the key")
		for _, placeholder := range placeholders {
			assert.NotContains(s.t, output, placeholder, "an output holds the relay secret")
		}
	}
	scan := func(root string, withPlaceholders bool) int {
		files := 0
		require.NoError(s.t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, readErr := os.ReadFile(path) // #nosec G304 G122 -- the path is in a folder of the test.
			if readErr != nil {
				return nil
			}
			files++
			if strings.Contains(string(data), key) {
				s.t.Errorf("the file %s holds the key", path)
			}
			if withPlaceholders {
				for _, placeholder := range placeholders {
					if strings.Contains(string(data), placeholder) {
						s.t.Errorf("the file %s holds the relay secret", path)
					}
				}
			}
			return nil
		}))
		return files
	}
	require.Positive(s.t, scan(s.Server.Home, true), "the home of the coordinator has files")
	scan(s.agentHome, true)
	scan(s.dagu.ProjectPath("."), false)
	scan(s.Dir, false)
}
