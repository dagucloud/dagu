// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A backfill submitted through the API can be listed, followed, cancelled,
// and resumed; a dry run, a repeat, and a stale plan start nothing.
func TestBackfillAPI(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	dag := server.DAG(t, `schedule: "0 2 * * *"
steps:
  - run: "true"
`)
	fileName := strings.TrimSuffix(filepath.Base(dag.Location), filepath.Ext(dag.Location))
	from, to := "2025-10-01T00:00:00Z", "2025-10-03T23:00:00Z"
	submit := func(body api.BackfillRequest, status int) api.BackfillSubmitResponse {
		t.Helper()
		var out api.BackfillSubmitResponse
		server.Client().Post("/api/v1/dags/"+fileName+"/backfills", body).ExpectStatus(status).Send(t).Unmarshal(t, &out)
		return out
	}

	dry := submit(api.BackfillRequest{From: &from, To: &to, MaxConcurrency: new(3), DryRun: new(true)}, http.StatusOK)
	require.Len(t, dry.Plan.Slots, 3)
	assert.Equal(t, 3, dry.Plan.RunCounts.None)
	assert.Equal(t, 1, dry.Plan.Concurrency, "the DAG's own queue runs one at a time")
	assert.Equal(t, 1, dry.Plan.QueueLimit)
	assert.Nil(t, dry.Plan.EstimateSeconds, "no estimate without successful runs")
	assert.Nil(t, dry.Backfill)

	submitted := submit(api.BackfillRequest{From: &from, To: &to, Revision: &dry.Plan.Revision}, http.StatusOK)
	require.NotNil(t, submitted.Backfill)
	id := submitted.Backfill.Id
	// The scheduler finds the DAG's file from the record, so the record
	// names the file, not the name in the URL.
	record, err := filebackfill.New(filebackfill.Dir(server.Config.Paths.DataDir)).Get(context.Background(), id)
	require.NoError(t, err)
	assert.FileExists(t, record.DAGFile)
	assert.Equal(t, api.BackfillStatusWaitingForScheduler, submitted.Backfill.Status)
	assert.Equal(t, 3, submitted.Backfill.SlotCount)

	again := submit(api.BackfillRequest{From: &from, To: &to}, http.StatusOK)
	require.NotNil(t, again.Backfill)
	assert.Equal(t, id, again.Backfill.Id)
	assert.True(t, *again.Existing)

	stale := submit(api.BackfillRequest{From: &from, To: &to, Rerun: new(api.BackfillRerunAll), Revision: new("stale")}, http.StatusConflict)
	assert.NotEmpty(t, stale.Plan.Revision, "a stale submit returns the new plan")

	var details api.GetDAGDetails200JSONResponse
	server.Client().Get("/api/v1/dags/"+fileName).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	require.NotNil(t, details.LatestBackfill, "the open backfill is the DAG's latest")
	assert.Equal(t, id, details.LatestBackfill.Id)

	var listed struct{ Backfills []api.Backfill }
	server.Client().Get("/api/v1/backfills?dag="+dag.Name).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &listed)
	require.Len(t, listed.Backfills, 1)
	assert.Equal(t, 3, listed.Backfills[0].Counts["pending"])
	assert.Equal(t, new(fileName), listed.Backfills[0].FileName, "the DAG API finds the DAG by its file")

	var page struct {
		Slots []api.BackfillSlot
		Total int
	}
	server.Client().Get("/api/v1/backfills/"+id+"/slots?state=pending&limit=2").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &page)
	assert.Len(t, page.Slots, 2)
	assert.Equal(t, 3, page.Total)

	var slots struct{ Slots []api.DAGSlot }
	server.Client().Get("/api/v1/dags/"+fileName+"/slots?from=2025-10-01&to=2025-10-03").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &slots)
	require.Len(t, slots.Slots, 3)
	assert.Equal(t, api.DAGSlotStatusMissing, slots.Slots[0].Status)

	var cancelled api.Backfill
	server.Client().Post("/api/v1/backfills/"+id+"/cancel", map[string]any{}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &cancelled)
	assert.Equal(t, api.BackfillStatusCancelled, cancelled.Status)
	server.Client().Post("/api/v1/backfills/"+id+"/cancel", map[string]any{}).ExpectStatus(http.StatusConflict).Send(t)
	details = api.GetDAGDetails200JSONResponse{}
	server.Client().Get("/api/v1/dags/"+fileName).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	assert.Nil(t, details.LatestBackfill, "a cancelled backfill is not shown")

	var resumed api.BackfillSubmitResponse
	server.Client().Post("/api/v1/backfills/"+id+"/resume", map[string]any{"maxConcurrency": 2}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resumed)
	require.NotNil(t, resumed.Backfill)
	assert.Equal(t, 1, *resumed.Backfill.Generation)
	assert.Equal(t, 2, resumed.Backfill.MaxConcurrency)
}

// The runs of a backfill are listed by its ID, also those created before
// today or in the second the backfill was submitted, and each run names it.
func TestListDAGRunsOfABackfill(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	dag := server.DAG(t, "steps:\n  - run: \"true\"\n")
	ctx := context.Background()
	now := time.Now()
	// Run directories name their creation to the second.
	submitted := now.Add(-48 * time.Hour).Truncate(time.Second).Add(500 * time.Millisecond)
	store := filebackfill.New(filebackfill.Dir(server.Config.Paths.DataDir))
	require.NoError(t, store.Create(ctx, &backfill.Record{
		ID: "bf123456", DAGName: dag.Name, DAGFile: dag.Location, CreatedAt: submitted,
		Options: backfill.Options{Rerun: backfill.RerunNone, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop},
	}))
	for runID, run := range map[string]struct {
		backfillID string
		created    time.Time
	}{
		"bf-run":       {"bf123456", now.Add(-24 * time.Hour)},
		"first-bf-run": {"bf123456", submitted.Add(200 * time.Millisecond)},
		"other-bf-run": {"bf654321", now},
		"live-run":     {"", now},
	} {
		backfillID := run.backfillID
		attempt, err := server.DAGRunRepository.CreateAttempt(ctx, dag.DAG, run.created, runID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
			Name: dag.Name, DAGRunID: runID, AttemptID: attempt.ID(), Status: ir.Succeeded, BackfillID: backfillID,
		}))
		require.NoError(t, attempt.Close(ctx))
	}

	var page api.DAGRunsPageResponse
	server.Client().Get("/api/v1/dag-runs?backfillId=bf123456").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &page)
	require.Len(t, page.DagRuns, 2)
	assert.Equal(t, "bf-run", page.DagRuns[0].DagRunId)
	assert.Equal(t, "first-bf-run", page.DagRuns[1].DagRunId)
	assert.Equal(t, new("bf123456"), page.DagRuns[0].BackfillId)

	live, err := newDAGPinAPIWithStore(server.Helper, nil, localapi.WithBackfillStore(store)).GetDAGRunsListData(ctx, "backfillId=bf123456")
	require.NoError(t, err)
	liveRuns := live.(api.DAGRunsPageResponse).DagRuns
	require.Len(t, liveRuns, 2, "the live run list filters alike")
	assert.Equal(t, "bf-run", liveRuns[0].DagRunId)

	var details api.GetDAGRunDetails200JSONResponse
	server.Client().Get("/api/v1/dag-runs/"+dag.Name+"/bf-run").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	assert.Equal(t, new("bf123456"), details.DagRunDetails.BackfillId)
}

func TestBackfillAPIRejects(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	dag := server.DAG(t, `schedule: "0 2 * * *"
steps:
  - run: "true"
`)
	fileName := strings.TrimSuffix(filepath.Base(dag.Location), filepath.Ext(dag.Location))

	server.Client().Post("/api/v1/dags/"+fileName+"/backfills", api.BackfillRequest{From: new("2099-01-01")}).
		ExpectStatus(http.StatusBadRequest).Send(t)
	server.Client().Post("/api/v1/dags/missing/backfills", api.BackfillRequest{From: new("2025-10-01")}).
		ExpectStatus(http.StatusNotFound).Send(t)
	server.Client().Get("/api/v1/backfills/aaaaaaaa").ExpectStatus(http.StatusNotFound).Send(t)
}

// A backfill belongs to the workspace of its DAG's labels, which its record
// keeps, so access holds after the DAG is deleted.
func TestBackfillWorkspaceAccess(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	dagFile := helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "ops-etl", []byte("labels: [workspace=ops]\nschedule: \"0 2 * * *\"\nsteps:\n  - run: echo ops\n"))
	store := filebackfill.New(t.TempDir())
	slot := time.Date(2025, 10, 1, 2, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(context.Background(), &backfill.Record{
		ID: "ops12345", DAGName: "ops-etl", DAGFile: dagFile, Labels: ir.NewLabels([]string{"workspace=ops"}),
		Options:   backfill.Options{From: slot, Rerun: backfill.RerunNone, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop},
		CreatedAt: slot, PlannedAt: slot, Slots: []backfill.RecordSlot{{At: slot}},
	}))
	apiImpl := newDAGPinAPIWithStore(helper, nil, localapi.WithBackfillStore(store), localapi.WithAuthService(stubAuthService{}))
	user := func(name string, role auth.Role) context.Context {
		return auth.WithUser(context.Background(), &auth.User{
			Username:        name,
			Role:            role,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: name, Role: role}}},
		})
	}
	other, viewer, developer := user("other", auth.RoleDeveloper), user("ops", auth.RoleViewer), user("ops", auth.RoleDeveloper)

	listed, err := apiImpl.ListBackfills(other, api.ListBackfillsRequestObject{})
	require.NoError(t, err)
	assert.Empty(t, listed.(api.ListBackfills200JSONResponse).Backfills, "another workspace's backfill is not listed")
	_, err = apiImpl.GetBackfill(other, api.GetBackfillRequestObject{BackfillId: "ops12345"})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
	_, err = apiImpl.GetBackfill(viewer, api.GetBackfillRequestObject{BackfillId: "ops12345"})
	require.NoError(t, err)
	_, err = apiImpl.GetBackfillData(other, "ops12345")
	requireAPIErrorStatus(t, err, http.StatusNotFound)
	live, err := apiImpl.GetBackfillData(viewer, "ops12345")
	require.NoError(t, err)
	assert.Equal(t, "ops12345", live.(api.Backfill).Id, "the live feed reports the backfill")

	require.NoError(t, os.Remove(dagFile))
	cancel := api.CancelBackfillRequestObject{BackfillId: "ops12345", Body: &api.CancelBackfillJSONRequestBody{}}
	_, err = apiImpl.CancelBackfill(other, cancel)
	requireAPIErrorStatus(t, err, http.StatusNotFound)
	_, err = apiImpl.CancelBackfill(viewer, cancel)
	requireAPIErrorStatus(t, err, http.StatusForbidden)
	cancelled, err := apiImpl.CancelBackfill(developer, cancel)
	require.NoError(t, err)
	assert.Equal(t, api.BackfillStatusCancelled, cancelled.(api.CancelBackfill200JSONResponse).Status)
}
