// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	apiTestProcHeartbeatInterval = 150 * time.Millisecond
	apiTestProcStaleThreshold    = time.Second
)

func apiProcEventuallyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 6
	}
	return base
}

type staleLocalRunFixture struct {
	server   test.Server
	dag      test.DAG
	dagRunID string
	ref      exec.DAGRunRef
}

func TestServerProcHeartbeat_StartAPI(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Proc.HeartbeatInterval = apiTestProcHeartbeatInterval
		cfg.Proc.HeartbeatSyncInterval = apiTestProcHeartbeatInterval
		cfg.Proc.StaleThreshold = apiTestProcStaleThreshold
	}))

	release := newHoldFile(t)
	spec := fmt.Sprintf(`
steps:
  - name: sleep
    run: |
%s`, indentCommandBlock(holdUntilFileExistsCommand(release), 6))
	dagName := "api-proc-heartbeat"
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	resp := server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).
		Send(t)

	var execResp api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &execResp)
	require.NotEmpty(t, execResp.DagRunId)

	ref := exec.NewDAGRunRef(dagName, execResp.DagRunId)
	require.Eventually(t, func() bool {
		statusResp := server.Client().Get(fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId)).
			ExpectStatus(http.StatusOK).
			Send(t)
		var details api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &details)
		return details.DagRun.Status == api.Status(core.Running)
	}, apiProcEventuallyTimeout(5*time.Second), 50*time.Millisecond)

	require.Eventually(t, func() bool {
		alive, err := server.ProcStore.IsRunAlive(server.Context, dagName, ref)
		return err == nil && alive
	}, apiProcEventuallyTimeout(10*time.Second), 50*time.Millisecond)

	releaseHoldFile(t, release)

	require.Eventually(t, func() bool {
		statusResp := server.Client().Get(fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId)).
			ExpectStatus(http.StatusOK).
			Send(t)
		var details api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &details)
		return details.DagRun.Status == api.Status(core.Succeeded)
	}, apiProcEventuallyTimeout(15*time.Second), 50*time.Millisecond)
}

func TestServerRepairsStaleLocalRunOnRead(t *testing.T) {
	fixture := setupStaleLocalRun(t, "api-stale-local-repair")

	resp := fixture.server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", fixture.dag.Name, fixture.dagRunID)).
		ExpectStatus(http.StatusOK).
		Send(t)

	var details api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.Equal(t, api.Status(core.Failed), details.DagRunDetails.Status)
	requireFailedStaleLocalRun(t, fixture.server, fixture.ref)
}

func TestServerRepairsStaleLocalLatestRunOnRead(t *testing.T) {
	fixture := setupStaleLocalRun(t, "api-stale-local-latest-repair")

	resp := fixture.server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/latest", fixture.dag.Name)).
		ExpectStatus(http.StatusOK).
		Send(t)

	var details api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.Equal(t, api.Status(core.Failed), details.DagRunDetails.Status)
	require.Equal(t, fixture.dagRunID, details.DagRunDetails.DagRunId)
	requireFailedStaleLocalRun(t, fixture.server, fixture.ref)
}

func TestServerRepairsStaleLocalLatestScopedRunOnRead(t *testing.T) {
	fixture := setupStaleLocalRun(t, "api-stale-local-scoped-latest-repair")

	resp := fixture.server.Client().Get(fmt.Sprintf("/api/v1/dags/%s/dag-runs/latest", fixture.dag.FileName())).
		ExpectStatus(http.StatusOK).
		Send(t)

	var details api.GetDAGDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.Equal(t, api.Status(core.Failed), details.DagRun.Status)
	require.Equal(t, fixture.dagRunID, details.DagRun.DagRunId)
	requireFailedStaleLocalRun(t, fixture.server, fixture.ref)
}

func setupStaleLocalRun(t *testing.T, dagName string) staleLocalRunFixture {
	t.Helper()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Proc.HeartbeatInterval = 50 * time.Millisecond
		cfg.Proc.HeartbeatSyncInterval = 50 * time.Millisecond
		cfg.Proc.StaleThreshold = 100 * time.Millisecond
	}))

	dag := server.DAG(t, fmt.Sprintf(`
name: %s
steps:
  - name: step1
    run: sleep 2
`, dagName))

	dagRunID := uuid.Must(uuid.NewV7()).String()
	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	attempt, err := server.DAGRunStore.CreateAttempt(server.Context, dag.DAG, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	logFile := filepath.Join(server.Config.Paths.LogDir, dag.Name, dagRunID+".log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))

	status := transform.NewStatusBuilder(dag.DAG).Create(
		dagRunID,
		core.Running,
		0,
		time.Now().Add(-2*time.Second),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(ref, exec.DAGRunRef{}),
		transform.WithLogFilePath(logFile),
	)
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeRunning

	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, status))
	require.NoError(t, attempt.Close(server.Context))

	_ = test.CreateStaleProcFileWithAttempt(
		t,
		server.Config.Paths.ProcDir,
		dag.ProcGroup(),
		ref,
		attempt.ID(),
		time.Now().Add(-2*time.Second),
		time.Second,
	)

	return staleLocalRunFixture{
		server:   server,
		dag:      dag,
		dagRunID: dagRunID,
		ref:      ref,
	}
}

func requireFailedStaleLocalRun(t *testing.T, server test.Server, ref exec.DAGRunRef) {
	t.Helper()

	repaired := test.ReadRunStatus(server.Context, t, server.DAGRunStore, ref)
	require.Equal(t, core.Failed, repaired.Status)
	require.Len(t, repaired.Nodes, 1)
	require.Equal(t, core.NodeFailed, repaired.Nodes[0].Status)
	require.Contains(t, repaired.Nodes[0].Error, "stale local process detected")
}

func TestServerRepairsConfirmedStaleDistributedRunOnDetailsRead(t *testing.T) {
	server := test.SetupServer(t)

	dag := server.DAG(t, `
name: api-stale-distributed-repair
steps:
  - name: step1
    run: sleep 2
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	attempt, err := server.DAGRunStore.CreateAttempt(server.Context, dag.DAG, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	logFile := filepath.Join(server.Config.Paths.LogDir, dag.Name, dagRunID+".log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))

	status := transform.NewStatusBuilder(dag.DAG).Create(
		dagRunID,
		core.Running,
		0,
		time.Now().Add(-2*time.Second),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(ref, exec.DAGRunRef{}),
		transform.WithLogFilePath(logFile),
	)
	status.AttemptID = attempt.ID()
	status.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, attempt.ID())
	status.WorkerID = "worker-1"
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeRunning

	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, status))
	require.NoError(t, attempt.Close(server.Context))

	staleAt := time.Now().Add(-2 * time.Minute).UTC()
	require.NoError(t, server.DAGRunLeaseStore.Upsert(server.Context, exec.DAGRunLease{
		AttemptKey:      status.AttemptKey,
		DAGRun:          ref,
		Root:            ref,
		AttemptID:       status.AttemptID,
		QueueName:       dag.Name,
		WorkerID:        status.WorkerID,
		ClaimedAt:       staleAt.UnixMilli(),
		LastHeartbeatAt: staleAt.UnixMilli(),
	}))
	require.NoError(t, server.WorkerHeartbeatStore.Upsert(server.Context, exec.WorkerHeartbeatRecord{
		WorkerID:        status.WorkerID,
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		Stats: &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{},
		},
	}))

	resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", dag.Name, dagRunID)).
		ExpectStatus(http.StatusOK).
		Send(t)

	var details api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.Equal(t, api.Status(core.Failed), details.DagRunDetails.Status)

	repaired := test.ReadRunStatus(server.Context, t, server.DAGRunStore, ref)
	require.Equal(t, core.Failed, repaired.Status)
	require.Len(t, repaired.Nodes, 1)
	require.Equal(t, core.NodeFailed, repaired.Nodes[0].Status)
	require.Contains(t, repaired.Nodes[0].Error, "distributed run lease expired")
}

func TestServerRepairsConfirmedStaleDistributedRunOnDetailsReadWithoutSavedWorkerID(t *testing.T) {
	server := test.SetupServer(t)

	dag := server.DAG(t, `
name: api-stale-distributed-repair-no-worker
steps:
  - name: step1
    run: sleep 2
`)

	dagRunID := uuid.Must(uuid.NewV7()).String()
	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	attempt, err := server.DAGRunStore.CreateAttempt(server.Context, dag.DAG, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	logFile := filepath.Join(server.Config.Paths.LogDir, dag.Name, dagRunID+".log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))

	status := transform.NewStatusBuilder(dag.DAG).Create(
		dagRunID,
		core.NotStarted,
		0,
		time.Now().Add(-2*time.Second),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(ref, exec.DAGRunRef{}),
		transform.WithLogFilePath(logFile),
	)
	status.AttemptID = attempt.ID()
	status.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, attempt.ID())
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeRunning

	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, status))
	require.NoError(t, attempt.Close(server.Context))

	staleAt := time.Now().Add(-2 * time.Minute).UTC()
	require.NoError(t, server.DAGRunLeaseStore.Upsert(server.Context, exec.DAGRunLease{
		AttemptKey:      status.AttemptKey,
		DAGRun:          ref,
		Root:            ref,
		AttemptID:       status.AttemptID,
		QueueName:       dag.Name,
		WorkerID:        "worker-1",
		ClaimedAt:       staleAt.UnixMilli(),
		LastHeartbeatAt: staleAt.UnixMilli(),
	}))
	require.NoError(t, server.WorkerHeartbeatStore.Upsert(server.Context, exec.WorkerHeartbeatRecord{
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		Stats: &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{},
		},
	}))

	resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", dag.Name, dagRunID)).
		ExpectStatus(http.StatusOK).
		Send(t)

	var details api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.Equal(t, api.Status(core.Failed), details.DagRunDetails.Status)

	repaired := test.ReadRunStatus(server.Context, t, server.DAGRunStore, ref)
	require.Equal(t, core.Failed, repaired.Status)
	require.Len(t, repaired.Nodes, 1)
	require.Equal(t, core.NodeFailed, repaired.Nodes[0].Status)
	require.Contains(t, repaired.Nodes[0].Error, "distributed run lease expired")
}
