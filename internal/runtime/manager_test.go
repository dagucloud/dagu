// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	procctrl "github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/testutil"
)

// TestManager exercises DAG run manager status and control behavior.
func TestManager(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	t.Run("Valid", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context

		dagRunID := uuid.Must(uuid.NewV7()).String()
		status := ir.NewStatusBuilder(dag.DAG).Create(
			dagRunID, ir.Running, 0, time.Now(),
		)
		stopSocket := startStatusSocketServer(
			t,
			ctx,
			procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
			status,
		)

		require.Eventually(t, func() bool {
			curr, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)
			if err != nil || curr == nil {
				return false
			}
			return curr.Status == ir.Running
		}, platformTestDuration(10*time.Second, 30*time.Second), 100*time.Millisecond)

		stopSocket()

		dag.AssertCurrentStatus(t, ir.NotStarted)
	})
	t.Run("GetLatestStatusUsesCanonicalSocketForQueuedSnapshot", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		queuedDAG := *dag.DAG
		queuedDAG.Location = ""
		attempt := createLiveRootAttempt(t, th, &queuedDAG, dagRunID)

		liveStatus := testNewStatus(&queuedDAG, dagRunID, ir.Waiting, ir.NodeWaiting)
		liveStatus.AttemptID = attempt.ID()
		stopSocket := startStatusSocketServer(
			t,
			ctx,
			procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
			liveStatus,
		)
		defer stopSocket()

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)

		require.NoError(t, err)
		require.Equal(t, ir.Waiting, latest.Status)
		require.Equal(t, attempt.ID(), latest.AttemptID)
	})
	t.Run("GetCurrentStatusFallsBackToLegacyRootSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		legacyStatus := testNewStatus(dag.DAG, dagRunID, ir.Waiting, ir.NodeWaiting)
		legacyStatus.Error = "legacy"
		stopSocket := startStatusSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), legacyStatus)
		defer stopSocket()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)

		require.NoError(t, err)
		require.Equal(t, ir.Waiting, current.Status)
		require.Equal(t, "legacy", current.Error)
	})
	t.Run("GetCurrentStatusSkipsStaleCanonicalSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		canonicalAddr := procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID))
		require.NoError(t, os.WriteFile(canonicalAddr, nil, 0o600))
		t.Cleanup(func() { _ = os.Remove(canonicalAddr) })

		legacyStatus := testNewStatus(dag.DAG, dagRunID, ir.Waiting, ir.NodeWaiting)
		legacyStatus.Error = "legacy"
		stopSocket := startStatusSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), legacyStatus)
		defer stopSocket()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)

		require.NoError(t, err)
		require.Equal(t, "legacy", current.Error)
	})
	t.Run("GetCurrentStatusPrefersCanonicalSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		canonicalStatus := testNewStatus(dag.DAG, dagRunID, ir.Waiting, ir.NodeWaiting)
		canonicalStatus.Error = "canonical"
		legacyStatus := canonicalStatus
		legacyStatus.Error = "legacy"

		stopCanonical := startStatusSocketServer(
			t,
			ctx,
			procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
			canonicalStatus,
		)
		defer stopCanonical()
		stopLegacy := startStatusSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), legacyStatus)
		defer stopLegacy()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)

		require.NoError(t, err)
		require.Equal(t, "canonical", current.Error)
	})
	t.Run("IsRunningDoesNotMaskCanonicalSocketFailureWithLegacyStatus", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		mgr := runtime.NewManager(nil, nil, th.Config)

		for _, tc := range []struct {
			name    string
			handler sock.HTTPHandlerFunc
		}{
			{
				name: "HTTPError",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				},
			},
			{
				name: "MalformedStatus",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("{"))
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dagRunID := uuid.Must(uuid.NewV7()).String()
				legacyStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
				stopCanonical := startSocketServer(
					t,
					ctx,
					procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
					tc.handler,
				)
				defer stopCanonical()
				stopLegacy := startStatusSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), legacyStatus)
				defer stopLegacy()

				require.False(t, mgr.IsRunning(ctx, dag.DAG, dagRunID))
			})
		}
	})
	t.Run("StopViaSocketRequestsCancel", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt := createLiveRootAttempt(t, th, dag.DAG, dagRunID)

		stopRequested := make(chan struct{}, 1)
		stopSocket := startSocketServer(
			t,
			ctx,
			procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
			func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/stop" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				select {
				case stopRequested <- struct{}{}:
				default:
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("OK"))
			},
		)
		defer stopSocket()

		require.NoError(t, th.DAGRunMgr.Stop(ctx, dag.DAG, dagRunID))

		select {
		case <-stopRequested:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "socket stop request was not received")
		}
		aborting, err := attempt.IsAborting(ctx)
		require.NoError(t, err)
		require.True(t, aborting)
	})
	t.Run("StopFallsBackToLegacyRootSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt := createLiveRootAttempt(t, th, dag.DAG, dagRunID)
		stopRequested := make(chan struct{}, 1)
		stopSocket := startSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/stop" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			stopRequested <- struct{}{}
			w.WriteHeader(http.StatusOK)
		})
		defer stopSocket()

		require.NoError(t, th.DAGRunMgr.Stop(ctx, dag.DAG, dagRunID))

		select {
		case <-stopRequested:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "legacy socket stop request was not received")
		}
		aborting, err := attempt.IsAborting(ctx)
		require.NoError(t, err)
		require.True(t, aborting)
	})
	t.Run("StopContinuesToLegacySocketAfterCanonicalHTTPError", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		createLiveRootAttempt(t, th, dag.DAG, dagRunID)
		canonicalRequested := make(chan struct{}, 1)
		legacyRequested := make(chan struct{}, 1)

		stopCanonical := startSocketServer(
			t,
			ctx,
			procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)),
			func(w http.ResponseWriter, _ *http.Request) {
				canonicalRequested <- struct{}{}
				w.WriteHeader(http.StatusInternalServerError)
			},
		)
		defer stopCanonical()
		stopLegacy := startSocketServer(t, ctx, sock.Addr(dag.Location, dagRunID), func(w http.ResponseWriter, _ *http.Request) {
			legacyRequested <- struct{}{}
			w.WriteHeader(http.StatusOK)
		})
		defer stopLegacy()

		require.NoError(t, th.DAGRunMgr.Stop(ctx, dag.DAG, dagRunID))

		select {
		case <-canonicalRequested:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "canonical socket stop request was not received")
		}
		select {
		case <-legacyRequested:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "legacy socket stop request was not received")
		}
	})
	t.Run("UpdateStatus", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		cli := th.DAGRunMgr

		// Open the Attempt data and write a status before updating it.
		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		err = att.Open(ctx)
		require.NoError(t, err)

		dagRunStatus := testNewStatus(dag.DAG, dagRunID, ir.Succeeded, ir.NodeSucceeded)

		err = att.Write(ctx, dagRunStatus)
		require.NoError(t, err)
		_ = att.Close(ctx)

		// Get the status and check if it is the same as the one we wrote.
		ref := ir.NewDAGRunRef(dag.Name, dagRunID)
		statusToCheck, err := cli.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, ir.NodeSucceeded, statusToCheck.Nodes[0].Status)

		// Update the status.
		newStatus := ir.NodeFailed
		dagRunStatus.Nodes[0].Status = newStatus

		root := ir.NewDAGRunRef(dag.Name, dagRunID)
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
    action: dag.run
    with:
      dag: tree_child
---
name: tree_child
steps:
  - name: "1"
    run: "exit 0"
---
`)

		spec := th.SubCmdBuilder.Start(dag.DAG, launcher.StartOptions{})
		err := launcher.Start(th.Context, spec)
		require.NoError(t, err)

		var status ir.DAGRunStatus
		require.Eventually(t, func() bool {
			latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
			if err != nil {
				return false
			}
			status = latest
			t.Logf("latest status=%s errors=%v", latest.Status.String(), latest.Errors())
			return latest.Status == ir.Succeeded
		}, platformTestDuration(30*time.Second, 4*time.Minute), time.Second)

		// Get the sub dag-run status.
		dagRunID := status.DAGRunID
		subDAGRun := status.Nodes[0].SubRuns[0]

		root := ir.NewDAGRunRef(dag.Name, dagRunID)
		subDAGRunStatus, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, root, subDAGRun.DAGRunID)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded.String(), subDAGRunStatus.Status.String())

		// Update the the sub dag-run status.
		subDAGRunStatus.Nodes[0].Status = ir.NodeFailed
		err = th.DAGRunMgr.UpdateStatus(th.Context, root, *subDAGRunStatus)
		require.NoError(t, err)

		// Check if the sub dag-run status is updated.
		subDAGRunStatus, err = th.DAGRunMgr.FindSubDAGRunStatus(th.Context, root, subDAGRun.DAGRunID)
		require.NoError(t, err)
		require.Equal(t, ir.NodeFailed.String(), subDAGRunStatus.Nodes[0].Status.String())
	})
	t.Run("FindSubDAGRunStatusRepairsStaleLocalChildRun", func(t *testing.T) {
		rootDAG := th.DAG(t, `name: stale-local-root
steps:
  - name: child
    run: echo child
`)
		childDAG := th.DAG(t, `name: stale-local-child
steps:
  - name: work
    run: echo ok
`)

		rootRunID := uuid.Must(uuid.NewV7()).String()
		childRunID := uuid.Must(uuid.NewV7()).String()
		staleAt := time.Now().Add(-3 * time.Second)
		childStatus := testNewStatus(childDAG.DAG, childRunID, ir.Running, ir.NodeRunning)
		childStatus.WorkerID = "local"
		childStatus.StartedAt = stringutil.FormatTime(staleAt)
		childStatus.CreatedAt = staleAt.UnixMilli()
		childAttempt := createRunningSubAttempt(t, th, rootDAG.DAG, childDAG.DAG, rootRunID, childRunID, childStatus)

		rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)
		status, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, rootRef, childRunID)

		require.NoError(t, err)
		require.Equal(t, ir.Failed, status.Status)
		require.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
		require.Equal(t, "process terminated unexpectedly - stale local process detected", status.Nodes[0].Error)

		persisted, err := childAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, persisted.Status)
		require.Equal(t, ir.NodeFailed, persisted.Nodes[0].Status)
	})
	t.Run("FindSubDAGRunStatusKeepsFreshLocalChildRunDuringStartupGrace", func(t *testing.T) {
		rootDAG := th.DAG(t, `name: fresh-local-root
steps:
  - name: child
    run: echo child
`)
		childDAG := th.DAG(t, `name: fresh-local-child
steps:
  - name: work
    run: echo ok
`)

		rootRunID := uuid.Must(uuid.NewV7()).String()
		childRunID := uuid.Must(uuid.NewV7()).String()
		statusTime := time.Now().UTC()
		mgr := runtime.NewManager(
			th.DAGRunRepository,
			th.ProcRepository,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return statusTime }),
		)
		childStatus := testNewStatus(childDAG.DAG, childRunID, ir.Running, ir.NodeRunning)
		childStatus.WorkerID = "local"
		childStatus.StartedAt = stringutil.FormatTime(statusTime)
		childStatus.CreatedAt = statusTime.UnixMilli()
		childAttempt := createRunningSubAttempt(t, th, rootDAG.DAG, childDAG.DAG, rootRunID, childRunID, childStatus)

		rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)
		status, err := mgr.FindSubDAGRunStatus(th.Context, rootRef, childRunID)

		require.NoError(t, err)
		require.Equal(t, ir.Running, status.Status)
		require.Equal(t, ir.NodeRunning, status.Nodes[0].Status)

		persisted, err := childAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Running, persisted.Status)
		require.Equal(t, ir.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("FindSubDAGRunStatusDoesNotRepairDistributedChildRun", func(t *testing.T) {
		rootDAG := th.DAG(t, `name: distributed-child-root
steps:
  - name: child
    run: echo child
`)
		childDAG := th.DAG(t, `name: distributed-child
steps:
  - name: work
    run: echo ok
`)

		rootRunID := uuid.Must(uuid.NewV7()).String()
		childRunID := uuid.Must(uuid.NewV7()).String()
		staleAt := time.Now().Add(-3 * time.Second)
		childStatus := testNewStatus(childDAG.DAG, childRunID, ir.Running, ir.NodeRunning)
		childStatus.WorkerID = "worker-1"
		childStatus.StartedAt = stringutil.FormatTime(staleAt)
		childStatus.CreatedAt = staleAt.UnixMilli()
		childAttempt := createRunningSubAttempt(t, th, rootDAG.DAG, childDAG.DAG, rootRunID, childRunID, childStatus)

		rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)
		status, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, rootRef, childRunID)

		require.NoError(t, err)
		require.Equal(t, ir.Running, status.Status)
		require.Equal(t, "worker-1", status.WorkerID)
		require.Equal(t, ir.NodeRunning, status.Nodes[0].Status)

		persisted, err := childAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Running, persisted.Status)
		require.Equal(t, ir.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("FindSubDAGRunStatusReturnsNoStatusDataForNilChildStatus", func(t *testing.T) {
		ctx := th.Context
		attempt := new(testutil.MockAttempt)
		attempt.On("ReadStatus", ctx).Return(nil, nil).Once()
		store := &managerDAGRunStore{subAttempt: attempt}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		mgr := runtime.NewManager(repository, th.ProcRepository, th.Config)

		status, err := mgr.FindSubDAGRunStatus(ctx, ir.NewDAGRunRef("root", "root-run"), "child-run")

		require.Nil(t, status)
		require.ErrorIs(t, err, dagrun.ErrNoStatusData)
		attempt.AssertExpectations(t)
	})
	t.Run("InvalidUpdateStatusWithInvalidDAGRunID", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		cli := th.DAGRunMgr

		// update with invalid dag-run ID.
		status := testNewStatus(dag.DAG, "unknown-req-id", ir.Failed, ir.NodeFailed)

		// Check if the update fails.
		root := ir.NewDAGRunRef(dag.Name, "unknown-req-id")
		err := cli.UpdateStatus(ctx, root, status)
		require.Error(t, err)
	})
	t.Run("GetLatestStatusRepairsStaleRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, latest.Status)
		require.Equal(t, ir.NodeFailed, latest.Nodes[0].Status)
		require.Equal(t, "process terminated unexpectedly - stale local process detected", latest.Nodes[0].Error)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, persisted.Status)
		require.Equal(t, ir.NodeFailed, persisted.Nodes[0].Status)
	})
	t.Run("GetCurrentStatusWithoutRunIDUsesLatestRunSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		stopSocket := startStatusSocketServer(t, ctx, procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)), ir.NewStatusBuilder(dag.DAG).Create(
			dagRunID, ir.Running, 0, time.Now(),
		))
		defer stopSocket()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, "")
		require.NoError(t, err)
		require.Equal(t, dagRunID, current.DAGRunID)
		require.Equal(t, ir.Running, current.Status)
	})
	t.Run("GetLatestStatusKeepsRunAliveWithFreshRunHeartbeat", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = att.ID()
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		proc, err := th.ProcRepository.Acquire(ctx, dag.ProcGroup(), procctrl.ProcMeta{
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
		require.Equal(t, ir.Running, latest.Status)
		require.Equal(t, ir.NodeRunning, latest.Nodes[0].Status)
		require.Empty(t, latest.Error)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Running, persisted.Status)
		require.Equal(t, ir.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("GetLatestStatusKeepsRunAliveWithStaleHeartbeatAndAlivePID", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = att.ID()
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "local"
		runningStatus.PID = ir.PID(os.Getpid())
		pidStartedAt, ok := procutil.StartTime(os.Getpid())
		require.True(t, ok)
		runningStatus.PIDStartedAt = pidStartedAt
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Running, latest.Status)
		require.Equal(t, ir.NodeRunning, latest.Nodes[0].Status)
		require.Empty(t, latest.Error)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Running, persisted.Status)
		require.Equal(t, ir.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("GetSavedStatusRepairsStaleRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		ref := ir.NewDAGRunRef(dag.Name, dagRunID)

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		staleAt := time.Now().Add(-3 * time.Second)
		runningStatus.StartedAt = staleAt.UTC().Format(time.RFC3339)
		runningStatus.CreatedAt = staleAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := th.DAGRunMgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, saved.Status)
		require.Equal(t, ir.NodeFailed, saved.Nodes[0].Status)
		require.Equal(t, "process terminated unexpectedly - stale local process detected", saved.Nodes[0].Error)
	})
	t.Run("GetSavedStatusUsesPersistedProcGroupWhenDAGReadFails", func(t *testing.T) {
		ctx := th.Context
		ref := ir.NewDAGRunRef("unreadable-dag", uuid.Must(uuid.NewV7()).String())
		status := testNewStatus(&ir.DAG{Name: ref.Name}, ref.ID, ir.Running, ir.NodeRunning)
		status.AttemptID = "attempt-1"
		status.ProcGroup = "persisted-group"
		staleAt := time.Now().Add(-3 * time.Second)
		status.StartedAt = stringutil.FormatTime(staleAt)
		status.CreatedAt = staleAt.UnixMilli()

		attempt := new(testutil.MockAttempt)
		attempt.On("ReadStatus", ctx).Return(&status, nil).Once()
		attempt.On("ReadDAG", ctx).Return(nil, errors.New("dag unavailable")).Once()
		store := &managerDAGRunStore{rootAttempt: attempt}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		processes := &managerProcessRepository{attemptAlive: true}
		mgr := runtime.NewManager(repository, processes, th.Config)

		saved, err := mgr.GetSavedStatus(ctx, ref)

		require.NoError(t, err)
		require.Equal(t, ir.Running, saved.Status)
		require.Equal(t, "persisted-group", processes.attemptGroup)
		attempt.AssertExpectations(t)
	})
	t.Run("GetSavedStatusUsesPersistedProcGroupAfterDAGQueueChange", func(t *testing.T) {
		ctx := th.Context
		ref := ir.NewDAGRunRef("changed-dag", uuid.Must(uuid.NewV7()).String())
		status := testNewStatus(&ir.DAG{Name: ref.Name}, ref.ID, ir.Running, ir.NodeRunning)
		status.AttemptID = "attempt-1"
		status.ProcGroup = "original-group"
		staleAt := time.Now().Add(-3 * time.Second)
		status.StartedAt = stringutil.FormatTime(staleAt)
		status.CreatedAt = staleAt.UnixMilli()

		attempt := new(testutil.MockAttempt)
		attempt.On("ReadStatus", ctx).Return(&status, nil).Once()
		attempt.On("ReadDAG", ctx).Return(&ir.DAG{Name: ref.Name, Queue: "updated-group"}, nil).Once()
		store := &managerDAGRunStore{rootAttempt: attempt}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		processes := &managerProcessRepository{attemptAlive: true}
		mgr := runtime.NewManager(repository, processes, th.Config)

		saved, err := mgr.GetSavedStatus(ctx, ref)

		require.NoError(t, err)
		require.Equal(t, ir.Running, saved.Status)
		require.Equal(t, "original-group", processes.attemptGroup)
		attempt.AssertExpectations(t)
	})
	t.Run("GetLatestStatusKeepsFreshRunDuringStartupGrace", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		statusTime := now.UTC()
		ctx := th.Context
		mgr := runtime.NewManager(
			th.DAGRunRepository,
			th.ProcRepository,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return statusTime }),
		)

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.StartedAt = stringutil.FormatTime(statusTime)
		runningStatus.CreatedAt = statusTime.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := mgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Running, latest.Status)
		require.Equal(t, ir.NodeRunning, latest.Nodes[0].Status)

		persisted, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Running, persisted.Status)
		require.Equal(t, ir.NodeRunning, persisted.Nodes[0].Status)
	})
	t.Run("GetSavedStatusKeepsFreshRunDuringStartupGrace", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		statusTime := now.UTC()
		ctx := th.Context
		ref := ir.NewDAGRunRef(dag.Name, dagRunID)
		mgr := runtime.NewManager(
			th.DAGRunRepository,
			th.ProcRepository,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return statusTime }),
		)

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.StartedAt = stringutil.FormatTime(statusTime)
		runningStatus.CreatedAt = statusTime.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := mgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, ir.Running, saved.Status)
		require.Equal(t, ir.NodeRunning, saved.Nodes[0].Status)
	})
	t.Run("GetSavedStatusDoesNotRepairDistributedRunWhenLeaseMissing", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context
		ref := ir.NewDAGRunRef(dag.Name, dagRunID)

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		saved, err := th.DAGRunMgr.GetSavedStatus(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, ir.Running, saved.Status)
		require.Equal(t, "worker-1", saved.WorkerID)
		require.Empty(t, saved.Error)
		require.Equal(t, ir.NodeRunning, saved.Nodes[0].Status)
	})
	t.Run("GetLatestStatusDoesNotReadLocalSocketForDistributedRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))
		stopSocket := startStatusSocketServer(t, ctx, procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)), ir.NewStatusBuilder(dag.DAG).Create(
			dagRunID, ir.Failed, 0, time.Now(),
		))
		defer stopSocket()

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Running, latest.Status)
		require.Equal(t, "worker-1", latest.WorkerID)
	})
	t.Run("GetCurrentStatusDoesNotReadLocalSocketForDistributedRun", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))
		stopSocket := startStatusSocketServer(t, ctx, procctrl.DAGSocketAddr(ir.NewDAGRunRef(dag.Name, dagRunID)), ir.NewStatusBuilder(dag.DAG).Create(
			dagRunID, ir.Failed, 0, time.Now(),
		))
		defer stopSocket()

		current, err := th.DAGRunMgr.GetCurrentStatus(ctx, dag.DAG, dagRunID)
		require.NoError(t, err)
		require.Equal(t, ir.Running, current.Status)
		require.Equal(t, "worker-1", current.WorkerID)
	})
	t.Run("GetLatestStatusDoesNotRepairDistributedRunWhenLeaseMissing", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		now := time.Now()
		ctx := th.Context

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, now, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = "attempt-1"
		runningStatus.AttemptKey = ir.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, runningStatus.AttemptID)
		runningStatus.WorkerID = "worker-1"
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		latest, err := th.DAGRunMgr.GetLatestStatus(ctx, dag.DAG)
		require.NoError(t, err)
		require.Equal(t, ir.Running, latest.Status)
		require.Equal(t, "worker-1", latest.WorkerID)
		require.Empty(t, latest.Error)
		require.Equal(t, ir.NodeRunning, latest.Nodes[0].Status)
	})
	t.Run("IsRunningFallsBackToFreshProcWithoutSocket", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		attemptID := "attempt-no-socket"

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{
			AttemptID: attemptID,
		})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))
		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.AttemptID = attemptID
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		proc, err := th.ProcRepository.Acquire(ctx, dag.ProcGroup(), procctrl.ProcMeta{
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
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()

		proc, err := th.ProcRepository.Acquire(ctx, dag.ProcGroup(), procctrl.ProcMeta{
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
	t.Run("IsRunningWithoutProcRepositoryReturnsFalse", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		mgr := runtime.NewManager(nil, nil, th.Config)

		require.False(t, mgr.IsRunning(th.Context, dag.DAG, uuid.Must(uuid.NewV7()).String()))
	})
	t.Run("GetCurrentStatusWithoutStoresReturnsInitial", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		mgr := runtime.NewManager(nil, nil, th.Config)

		status, err := mgr.GetCurrentStatus(th.Context, dag.DAG, "")
		require.NoError(t, err)
		require.Equal(t, ir.NotStarted, status.Status)
	})
	t.Run("GetCurrentStatusWithoutRunIDSkipsRepairWithoutProcRepository", func(t *testing.T) {
		dag := th.DAG(t, `steps:
  - name: "1"
    run: "exit 0"
`)
		ctx := th.Context
		dagRunID := uuid.Must(uuid.NewV7()).String()
		startedAt := time.Now().Add(-time.Minute)
		mgr := runtime.NewManager(
			th.DAGRunRepository,
			nil,
			th.Config,
			runtime.WithManagerClock(func() time.Time { return time.Now() }),
		)

		att, err := th.DAGRunRepository.CreateAttempt(ctx, dag.DAG, startedAt, dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))

		runningStatus := testNewStatus(dag.DAG, dagRunID, ir.Running, ir.NodeRunning)
		runningStatus.StartedAt = stringutil.FormatTime(startedAt)
		runningStatus.CreatedAt = startedAt.UnixMilli()
		require.NoError(t, att.Write(ctx, runningStatus))
		require.NoError(t, att.Close(ctx))

		status, err := mgr.GetCurrentStatus(ctx, dag.DAG, "")
		require.NoError(t, err)
		require.Equal(t, ir.Running, status.Status)
		require.Equal(t, dagRunID, status.DAGRunID)
	})
}

// testNewStatus builds a minimal persisted DAG run status for manager tests.
func testNewStatus(dag *ir.DAG, dagRunID string, dagStatus ir.Status, nodeStatus ir.NodeStatus) ir.DAGRunStatus {
	nodes := []runtime.NodeData{{State: runtime.NodeState{Status: nodeStatus}}}
	return ir.NewStatusBuilder(dag).Create(dagRunID, dagStatus, 0, time.Now(), transform.WithNodes(nodes))
}

func createRunningSubAttempt(
	t *testing.T,
	th test.Helper,
	rootDAG *ir.DAG,
	childDAG *ir.DAG,
	rootRunID string,
	childRunID string,
	status ir.DAGRunStatus,
) dagrun.Attempt {
	t.Helper()

	ctx := th.Context
	rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)

	rootAttempt, err := th.DAGRunRepository.CreateAttempt(ctx, rootDAG, time.Now(), rootRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, rootAttempt.Open(ctx))
	rootStatus := testNewStatus(rootDAG, rootRunID, ir.Running, ir.NodeRunning)
	rootStatus.AttemptID = rootAttempt.ID()
	rootStatus.AttemptKey = ir.GenerateAttemptKey(rootDAG.Name, rootRunID, rootDAG.Name, rootRunID, rootStatus.AttemptID)
	require.NoError(t, rootAttempt.Write(ctx, rootStatus))
	require.NoError(t, rootAttempt.Close(ctx))

	childAttempt, err := th.DAGRunRepository.CreateAttempt(ctx, childDAG, time.Now(), childRunID, persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))
	status.AttemptID = childAttempt.ID()
	status.AttemptKey = ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, childDAG.Name, childRunID, status.AttemptID)
	status.Root = rootRef
	status.Parent = rootRef
	status.DAGRunID = childRunID
	require.NoError(t, childAttempt.Write(ctx, status))
	require.NoError(t, childAttempt.Close(ctx))
	return childAttempt
}

type managerDAGRunStore struct {
	testutil.DAGRunStoreStub
	rootAttempt dagrun.Attempt
	subAttempt  dagrun.Attempt
}

func (s *managerDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	if s.rootAttempt == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return s.rootAttempt, nil
}

func (s *managerDAGRunStore) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	if s.subAttempt == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func createLiveRootAttempt(t *testing.T, th test.Helper, dag *ir.DAG, dagRunID string) dagrun.Attempt {
	t.Helper()

	ctx := th.Context
	attempt, err := th.DAGRunRepository.CreateAttempt(ctx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := testNewStatus(dag, dagRunID, ir.Running, ir.NodeRunning)
	status.AttemptID = attempt.ID()
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	process, err := th.ProcRepository.Acquire(ctx, dag.ProcGroup(), procctrl.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         dag.Name,
		DAGRunID:     dagRunID,
		AttemptID:    attempt.ID(),
		RootName:     dag.Name,
		RootDAGRunID: dagRunID,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = process.Stop(ctx)
	})

	return attempt
}

// startStatusSocketServer serves a fixed status over the requested socket.
func startStatusSocketServer(t *testing.T, ctx context.Context, addr string, status ir.DAGRunStatus) func() {
	t.Helper()

	jsonData, err := json.Marshal(status)
	require.NoError(t, err)
	return startSocketServer(t, ctx, addr, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonData)
	})
}

func startSocketServer(t *testing.T, ctx context.Context, addr string, handler sock.HTTPHandlerFunc) func() {
	t.Helper()

	socketServer, err := sock.NewServer(addr, handler)
	require.NoError(t, err)

	listen := make(chan error, 1)
	go func() {
		_ = socketServer.Serve(ctx, listen)
		_ = socketServer.Shutdown(ctx)
	}()
	require.NoError(t, <-listen)

	return func() {
		_ = socketServer.Shutdown(ctx)
	}
}

type managerProcessRepository struct {
	attemptAlive bool
	attemptGroup string
}

func (*managerProcessRepository) ListAlive(context.Context, string) ([]ir.DAGRunRef, error) {
	return nil, nil
}

func (*managerProcessRepository) IsRunAlive(context.Context, string, ir.DAGRunRef) (bool, error) {
	return false, nil
}

func (r *managerProcessRepository) IsAttemptAlive(_ context.Context, group string, _ ir.DAGRunRef, _ string) (bool, error) {
	r.attemptGroup = group
	return r.attemptAlive, nil
}

func (*managerProcessRepository) LatestFreshEntryByDAGName(context.Context, string, string) (*procctrl.ProcEntry, error) {
	return nil, nil
}
