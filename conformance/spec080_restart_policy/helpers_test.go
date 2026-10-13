// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec080_restart_policy_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// quiet is how long a test watches for a restart that must not happen. The
// service fixtures wait one second, so this is several restart waits.
const quiet = 4 * time.Second

type run struct {
	ID         string `json:"dagRunId"`
	Status     string `json:"status"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

func (r run) active() bool {
	return r.Status == "running" || r.Status == "queued" || r.Status == "waiting"
}

// service is a running scheduler over the DAGs of one runtime scenario.
type service struct {
	t       *testing.T
	dagu    *harness.Runner
	env     []string
	port    int
	process *harness.Process
}

// startService starts the scheduler and the API over testdata/runtime/<scenario>.
func startService(t *testing.T, scenario string) *service {
	t.Helper()

	dagu := harness.NewRunner(t)
	dags := dagu.ProjectPath(filepath.Join("runtime", scenario))
	env := []string{
		"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
		"DAGU_DAGS_DIR=" + dags,
	}
	port := harness.FreePort(t)
	process := dagu.StartWithEnv(
		env,
		"start-all",
		"--host=127.0.0.1",
		fmt.Sprintf("--port=%d", port),
		"--dags="+dags,
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", harness.FreePort(t)),
	)

	s := &service{t: t, dagu: dagu, env: env, port: port, process: process}
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("scheduler exited during startup: %s", process.FailureOutput())
		default:
		}
		if response, err := http.Get(s.url("/api/v1/health")); err == nil { //nolint:gosec,noctx
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("scheduler did not become healthy: %s", process.FailureOutput())
	return nil
}

func (s *service) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", s.port, path)
}

// runs lists the runs of a DAG, newest first.
func (s *service) runs(dag string) []run {
	s.t.Helper()
	result := s.dagu.RunWithEnv(s.env, "history", dag, "--format", "json", "--limit", "200")
	result.ExpectExitCode(0)
	var runs []run
	if out := strings.TrimSpace(result.Stdout()); strings.HasPrefix(out, "[") {
		require.NoError(s.t, json.Unmarshal([]byte(out), &runs), out)
	}
	return runs
}

// waitForRuns polls the runs of a DAG until done accepts them.
func (s *service) waitForRuns(dag, what string, done func([]run) bool) []run {
	s.t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(s.t))
	var runs []run
	for time.Now().Before(deadline) {
		runs = s.runs(dag)
		if done(runs) {
			return runs
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.process.Stop()
	s.t.Fatalf("%s: gave up waiting for %s; runs: %+v\n%s", dag, what, runs, s.process.FailureOutput())
	return nil
}

// requireSingleActiveRun fails when the DAG has more than one active run.
func (s *service) requireSingleActiveRun(runs []run) {
	s.t.Helper()
	active := 0
	for _, r := range runs {
		if r.active() {
			active++
		}
	}
	require.LessOrEqual(s.t, active, 1, "a service DAG has at most one active run: %+v", runs)
}

// suspend suspends or resumes a DAG through the API.
func (s *service) suspend(dagFile string, suspended bool) {
	s.t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"suspend": %t}`, suspended))
	response, err := http.Post(s.url("/api/v1/dags/"+dagFile+"/suspend"), "application/json", body) //nolint:gosec,noctx
	require.NoError(s.t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(s.t, http.StatusOK, response.StatusCode)
}

// triggerType returns the trigger type of a run, as the API reports it.
func (s *service) triggerType(dag, runID string) string {
	s.t.Helper()
	response, err := http.Get(s.url("/api/v1/dag-runs/" + dag + "/" + runID)) //nolint:gosec,noctx
	require.NoError(s.t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(s.t, http.StatusOK, response.StatusCode)
	var details struct {
		DAGRunDetails struct {
			TriggerType string `json:"triggerType"`
		} `json:"dagRunDetails"`
	}
	require.NoError(s.t, json.NewDecoder(response.Body).Decode(&details))
	return details.DAGRunDetails.TriggerType
}
