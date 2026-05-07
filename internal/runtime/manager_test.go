// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/sock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
)

func TestManager(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	t.Run("Valid", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)
		ctx := th.Context

		dagRunID := uuid.Must(uuid.NewV7()).String()
		socketServer, _ := sock.NewServer(
			dag.SockAddr(dagRunID),
			func(w http.ResponseWriter, _ *http.Request) {
				status := transform.NewStatusBuilder(dag.DAG).Create(
					dagRunID, core.Running, 0, time.Now(),
				)
				jsonData, err := json.Marshal(status)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(jsonData)
			},
		)

		listen := make(chan error, 1)
		go func() {
			_ = socketServer.Serve(ctx, listen)
			_ = socketServer.Shutdown(ctx)
		}()
		require.NoError(t, <-listen)

		dag.AssertCurrentStatus(t, core.Running)

		_ = socketServer.Shutdown(ctx)

		dag.AssertCurrentStatus(t, core.NotStarted)
	})
	t.Run("UpdateStatus", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		cli := th.DAGRunMgr

		// Open the Attempt data and write a status before updating it.
		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		err = att.Open(ctx)
		require.NoError(t, err)

		dagRunStatus := testNewStatus(dag.DAG, dagRunID, core.Succeeded, core.NodeSucceeded)

		err = att.Write(ctx, dagRunStatus)
		require.NoError(t, err)
		_ = att.Close(ctx)

		// Get the status and check if it is the same as the one we wrote.
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)
		statusToCheck, err := cli.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded, statusToCheck.Nodes[0].Status)

		// Update the status.
		newStatus := core.NodeFailed
		dagRunStatus.Nodes[0].Status = newStatus

		root := exec.NewDAGRunRef(dag.Name, dagRunID)
		err = cli.UpdateStatus(ctx, root, dagRunStatus)
		require.NoError(t, err)

		statusByDAGRunID, err := cli.GetSavedStatus(ctx, ref)
		require.NoError(t, err)

		require.Equal(t, 1, len(dagRunStatus.Nodes))
		require.Equal(t, newStatus, statusByDAGRunID.Nodes[0].Status)
	})
	t.Run("UpdateSubDAGRunStatus", func(t *testing.T) {
		dag := th.DAG(t, `
steps:
  - name: "1"
    call: tree_child
---
name: tree_child
steps:
  - name: "1"
    command: "exit 0"
---
`)

		spec := th.SubCmdBuilder.Start(dag.DAG, runtime.StartOptions{})
		err := runtime.Start(th.Context, spec)
		require.NoError(t, err)

		var status exec.DAGRunStatus
		require.Eventually(t, func() bool {
			latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
			if err != nil {
				return false
			}
			status = latest
			t.Logf("latest status=%s errors=%v", latest.Status.String(), latest.Errors())
			return latest.Status == core.Succeeded
		}, platformTestDuration(30*time.Second, 4*time.Minute), time.Second)

		// Get the sub dag-run status.
		dagRunID := status.DAGRunID
		subDAGRun := status.Nodes[0].SubRuns[0]

		root := exec.NewDAGRunRef(dag.Name, dagRunID)
		subDAGRunStatus, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, root, subDAGRun.DAGRunID)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded.String(), subDAGRunStatus.Status.String())

		// Update the the sub dag-run status.
		subDAGRunStatus.Nodes[0].Status = core.NodeFailed
		err = th.DAGRunMgr.UpdateStatus(th.Context, root, *subDAGRunStatus)
		require.NoError(t, err)

		// Check if the sub dag-run status is updated.
		subDAGRunStatus, err = th.DAGRunMgr.FindSubDAGRunStatus(th.Context, root, subDAGRun.DAGRunID)
		require.NoError(t, err)
		require.Equal(t, core.NodeFailed.String(), subDAGRunStatus.Nodes[0].Status.String())
	})
	t.Run("InvalidUpdateStatusWithInvalidDAGRunID", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)
		ctx := th.Context
		cli := th.DAGRunMgr

		// update with invalid dag-run ID.
		status := testNewStatus(dag.DAG, "unknown-req-id", core.Failed, core.NodeFailed)

		// Check if the update fails.
		root := exec.NewDAGRunRef(dag.Name, "unknown-req-id")
		err := cli.UpdateStatus(ctx, root, status)
		require.Error(t, err)
	})
	t.Run("GetLatestStatusRepairsStaleRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Failed, latest.Status)
		require.Equal(t, core.NodeFailed, latest.Nodes[0].Status)
		require.Equal(t, "process terminated unexpectedly - stale local process detected", latest.Nodes[0].Error)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Failed, persisted.Status)
		require.Equal(t, core.NodeFailed, persisted.Nodes[0].Status)
	})
	t.Run("GetLatestStatusKeepsRunAliveWithFreshRunHeartbeat", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = att.ID()
		runningStatus.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		proc, err := th.ProcStore.Acquire(ctx, dag.ProcGroup(), exec.ProcMeta{
			StartedAt:    time.Now().Unix(),
			Name:         dag.Name,
			DAGRunID:     dagRunID,
			AttemptID:    "fresh-other-attempt",
			RootName:     dag.Name,
			RootDAGRunID: dagRunID,
		})
		require.NoError(t, err)
		defer func() {
			_ = proc.Stop(ctx)
		}()

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Running, latest.Status)
		require.Equal(t, core.NodeRunning, latest.Nodes[0].Status)
		require.Empty(t, latest.Error)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Running, persisted.Status)
		require.Equal(t, core.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("GetSavedStatusRepairsStaleRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := th.DAGRunMgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, core.Failed, saved.Status)
		require.Equal(t, core.NodeFailed, saved.Nodes[0].Status)
		require.Equal(t, "process terminated unexpectedly - stale local process detected", saved.Nodes[0].Error)
	})
	t.Run("GetLatestStatusKeepsFreshRunDuringStartupGrace", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		statusTime := now.UTC()
		ctx := th.Context
		mgr := runtime.NewManager(
			th.DAGRunStore,
			th.ProcStore,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return statusTime }),
		)

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.StartedAt = exec.FormatTime(statusTime)
		runningStatus.CreatedAt = statusTime.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := mgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Running, latest.Status)
		require.Equal(t, core.NodeRunning, latest.Nodes[0].Status)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Running, persisted.Status)
		require.Equal(t, core.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("GetSavedStatusKeepsFreshRunDuringStartupGrace", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		statusTime := now.UTC()
		ctx := th.Context
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)
		mgr := runtime.NewManager(
			th.DAGRunStore,
			th.ProcStore,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return statusTime }),
		)

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.StartedAt = exec.FormatTime(statusTime)
		runningStatus.CreatedAt = statusTime.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := mgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, core.Running, saved.Status)
		require.Equal(t, core.NodeRunning, saved.Nodes[0].Status)
	})
	t.Run("GetSavedStatusDoesNotRepairDistributedRunWhenLeaseMissing", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		ref := exec.NewDAGRunRef(dag.Name, dagRunID)

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := th.DAGRunMgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, core.Running, saved.Status)
		require.Equal(t, "worker-1", saved.WorkerID)
		require.Empty(t, saved.Error)
		require.Equal(t, core.NodeRunning, saved.Nodes[0].Status)
	})
	t.Run("GetLatestStatusDoesNotReadLocalSocketForDistributedRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))
		stopSocket := startStatusSocketServer(t, ctx, dag.DAG, dagRunID, transform.NewStatusBuilder(dag.DAG).Create(
			dagRunID, core.Failed, 0, time.Now(),
		))
		defer stopSocket()

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Running, latest.Status)
		require.Equal(t, "worker-1", latest.WorkerID)
	})
	t.Run("GetCurrentStatusDoesNotReadLocalSocketForDistributedRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))
		stopSocket := startStatusSocketServer(t, ctx, dag.DAG, dagRunID, transform.NewStatusBuilder(dag.DAG).Create(
			dagRunID, core.Failed, 0, time.Now(),
		))
		defer stopSocket()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)
		require.NoError(t, err)
		require.Equal(t, core.Running, current.Status)
		require.Equal(t, "worker-1", current.WorkerID)
	})
	t.Run("GetLatestStatusDoesNotRepairDistributedRunWhenLeaseMissing", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, core.Running, latest.Status)
		require.Equal(t, "worker-1", latest.WorkerID)
		require.Empty(t, latest.Error)
		require.Equal(t, core.NodeRunning, latest.Nodes[0].Status)
	})
	t.Run("IsRunningFallsBackToFreshProcWithoutSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		attemptID := "attempt-no-socket"

		att, err := th.DAGRunStore.CreateAttempt(ctx, dag.DAG, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{
			AttemptID: attemptID,
		})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))
		runningStatus := testNewStatus(dag.DAG, dagRunID, core.Running, core.NodeRunning)
		runningStatus.AttemptID = attemptID
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		proc, err := th.ProcStore.Acquire(ctx, dag.ProcGroup(), exec.ProcMeta{
			StartedAt:    time.Now().Unix(),
			Name:         dag.Name,
			DAGRunID:     dagRunID,
			AttemptID:    attemptID,
			RootName:     dag.Name,
			RootDAGRunID: dagRunID,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = proc.Stop(ctx)
		})

		require.True(t, th.DAGRunMgr.IsRunning(ctx, dag.DAG, dagRunID))
	})
	t.Run("IsRunningIgnoresProcWithoutReadableRunningStatus", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()

		proc, err := th.ProcStore.Acquire(ctx, dag.ProcGroup(), exec.ProcMeta{
			StartedAt:    time.Now().Unix(),
			Name:         dag.Name,
			DAGRunID:     dagRunID,
			AttemptID:    "attempt-without-status",
			RootName:     dag.Name,
			RootDAGRunID: dagRunID,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = proc.Stop(ctx)
		})

		require.False(t, th.DAGRunMgr.IsRunning(ctx, dag.DAG, dagRunID))
	})
	t.Run("IsRunningWithoutProcStoreReturnsFalse", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    command: "exit 0"
`)
		mgr := runtime.NewManager(nil, nil, th.Config)

		require.False(t, mgr.IsRunning(th.Context, dag.DAG, uuid.Must(uuid.NewV7()).String()))
	})
}

func testNewStatus(dag *core.DAG, dagRunID string, dagStatus core.Status, nodeStatus core.NodeStatus) exec.DAGRunStatus {
	nodes := []runtime.NodeData{{State: runtime.NodeState{Status: nodeStatus}}}
	return transform.NewStatusBuilder(dag).Create(dagRunID, dagStatus, 0, time.Now(), transform.WithNodes(nodes))
}

func startStatusSocketServer(t *testing.T, ctx context.Context, dag *core.DAG, dagRunID string, status exec.DAGRunStatus) func() {
	t.Helper()

	socketServer, err := sock.NewServer(
		dag.SockAddr(dagRunID),
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			jsonData, marshalErr := json.Marshal(status)
			if marshalErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(jsonData)
		},
	)
	require.NoError(t, err)

	go func() {
		_ = socketServer.Serve(ctx, nil)
		_ = socketServer.Shutdown(ctx)
	}()

	return func() {
		_ = socketServer.Shutdown(ctx)
	}
}
