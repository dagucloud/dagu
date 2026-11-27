// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubProjects serves the stores of one registered project.
type stubProjects struct {
	scope  dispatch.ProjectScope
	stores *ProjectStores
}

func (p stubProjects) Stores(_ context.Context, scope dispatch.ProjectScope) (*ProjectStores, error) {
	if scope != p.scope {
		return nil, errors.New("project is not registered")
	}
	return p.stores, nil
}

func (p stubProjects) Projects() []dispatch.ProjectScope {
	return []dispatch.ProjectScope{p.scope}
}

// A migrated project shares the default project's run IDs, so a DAG of the
// same name in both runs on workers under the same run ID. Each run is
// recorded, leased, and reported in its own project's stores, and a report
// that names no project reaches only the default project's run.
func TestHandlerKeepsProjectRunsApart(t *testing.T) {
	registerCommandExecutorCapsForCoordinatorTest()
	ctx := t.Context()
	baseDir := t.TempDir()
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
	require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-1", LastHeartbeatAt: time.Now().UnixMilli(),
	}))
	defaults, weatherRuns := newMockDAGRunStore(), newMockDAGRunStore()
	weather := dispatch.ProjectScope{ID: "weather-id"}
	h := NewHandler(HandlerConfig{
		DAGRunRepository:     defaults.repository,
		DispatchTaskStore:    newTestDispatchTaskStore(baseDir),
		DAGRunLeaseStore:     leaseStore,
		WorkerHeartbeatStore: heartbeatStore,
		Projects:             stubProjects{scope: weather, stores: &ProjectStores{DAGRunRepository: weatherRuns.repository}},
	})
	t.Cleanup(func() { h.Close(context.Background()) })

	claimed := map[dispatch.ProjectScope]*coordinatorv1.Task{}
	for scope, runs := range map[dispatch.ProjectScope]*mockDAGRunStore{{}: defaults, weather: weatherRuns} {
		// Each project queued its run as its own attempt.
		runs.addAttempt(ir.DAGRunRef{Name: "forecast", ID: "run-1"}, &ir.DAGRunStatus{
			Name: "forecast", DAGRunID: "run-1", AttemptID: "queued-" + scope.ID, Status: ir.Queued,
		}).SetDAG(&ir.DAG{Name: "forecast"})
		_, err := h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
			Operation:  coordinatorv1.Operation_OPERATION_START,
			DagRunId:   "run-1",
			Target:     "forecast",
			Definition: "steps:\n  - name: wait\n    run: sleep 2\n",
			Project:    convert.ProjectScopeToProto(scope),
		}})
		require.NoError(t, err)
		polled, err := h.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: "worker-1", PollerId: "poller-1"})
		require.NoError(t, err)
		require.Equal(t, scope, convert.ProtoToProjectScope(polled.Task.GetProject()))
		ack, err := h.AckTaskClaim(ctx, &coordinatorv1.AckTaskClaimRequest{
			WorkerId: "worker-1", ClaimToken: polled.Task.ClaimToken, AttemptKey: polled.Task.AttemptKey,
		})
		require.NoError(t, err)
		require.True(t, ack.Accepted, ack.Error)
		lease, err := leaseStore.Get(ctx, polled.Task.AttemptKey)
		require.NoError(t, err)
		assert.Equal(t, scope, lease.Project)
		claimed[scope] = polled.Task
	}
	require.NotNil(t, defaults.attempts["run-1"])
	require.NotNil(t, weatherRuns.attempts["run-1"])

	report := func(task *coordinatorv1.Task, project *coordinatorv1.ProjectScope) (*coordinatorv1.ReportStatusResponse, error) {
		protoStatus, err := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name: task.Target, DAGRunID: task.DagRunId, AttemptID: task.AttemptId, AttemptKey: task.AttemptKey,
			WorkerID: "worker-1", Status: ir.Running,
		})
		require.NoError(t, err)
		return h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status: protoStatus, WorkerId: "worker-1", Project: project,
		})
	}
	readStatus := func(runs *mockDAGRunStore) ir.Status {
		current, err := runs.attempts["run-1"].ReadStatus(ctx)
		require.NoError(t, err)
		return current.Status
	}

	// A report that names no project is refused for the weather run.
	_, err := report(claimed[weather], nil)
	require.Equal(t, codes.PermissionDenied, status.Code(err), err)
	assert.NotEqual(t, ir.Running, readStatus(weatherRuns))

	resp, err := report(claimed[weather], convert.ProjectScopeToProto(weather))
	require.NoError(t, err)
	require.True(t, resp.Accepted, resp.Error)
	assert.Equal(t, ir.Running, readStatus(weatherRuns))
	assert.NotEqual(t, ir.Running, readStatus(defaults))

	resp, err = report(claimed[dispatch.ProjectScope{}], nil)
	require.NoError(t, err)
	require.True(t, resp.Accepted, resp.Error)
	assert.Equal(t, ir.Running, readStatus(defaults))
}

// A call that names a project the coordinator does not serve is refused.
func TestHandlerRefusesUnservedProject(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	unknown := &coordinatorv1.ProjectScope{ProjectId: "unknown"}

	_, err := NewHandler(HandlerConfig{}).GetDAG(ctx, &coordinatorv1.GetDAGRequest{Name: "forecast", Project: unknown})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), err)

	h := NewHandler(HandlerConfig{Projects: stubProjects{scope: dispatch.ProjectScope{ID: "weather-id"}}})
	_, err = h.GetDAGRunStatus(ctx, &coordinatorv1.GetDAGRunStatusRequest{DagName: "forecast", DagRunId: "run-1", Project: unknown})
	assert.Equal(t, codes.NotFound, status.Code(err), err)
}
