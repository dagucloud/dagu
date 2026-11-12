// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
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
