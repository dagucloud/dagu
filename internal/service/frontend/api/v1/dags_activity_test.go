// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activityRun is a run that seedActivityRun stores.
type activityRun struct {
	id       string
	status   ir.Status
	startAgo time.Duration
	// queuedFor is the wait between the creation of the run and its start.
	queuedFor time.Duration
	took      time.Duration
	reused    []ir.ReusedInput
	backfill  string
}

func createActivityDAG(t *testing.T, server test.Server, name string) *ir.DAG {
	t.Helper()
	spec := "steps:\n  - name: s\n    run: echo s\n"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: name, Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
	return &ir.DAG{Name: name, Steps: []ir.Step{{Name: "s"}}}
}

func seedActivityRun(t *testing.T, server test.Server, dag *ir.DAG, run activityRun) {
	t.Helper()
	start := time.Now().Add(-run.startAgo)
	attempt, err := server.DAGRunRepository.CreateAttempt(
		server.Context, dag, start.Add(-run.queuedFor), run.id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	options := []ir.StatusOption{
		ir.WithAttemptID(attempt.ID()),
		ir.WithHierarchyRefs(ir.NewDAGRunRef(dag.Name, run.id), ir.DAGRunRef{}),
		ir.WithReusedInputs(run.reused),
		ir.WithBackfillID(run.backfill),
	}
	if !run.status.IsActive() {
		options = append(options, ir.WithFinishedAt(start.Add(run.took)))
	}
	startedAt := start
	if run.status == ir.Queued {
		startedAt = time.Time{}
	}
	status := ir.NewStatusBuilder(dag).Create(run.id, run.status, 0, startedAt, options...)
	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, status))
	require.NoError(t, attempt.Close(server.Context))
}

func listActivity(t *testing.T, server test.Server, query string) map[string]api.DAGFile {
	t.Helper()
	var list api.ListDAGs200JSONResponse
	server.Client().Get("/api/v1/dags"+query).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &list)
	byName := make(map[string]api.DAGFile, len(list.Dags))
	for _, dag := range list.Dags {
		byName[dag.FileName] = dag
	}
	return byName
}

func activityRunIDs(activity *api.DAGListActivity) []string {
	ids := make([]string, 0, len(activity.Runs))
	for _, run := range activity.Runs {
		ids = append(ids, run.DagRunId)
	}
	return ids
}

func TestListDAGsActivityLimitedToTheWindow(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_window")
	reused := func(step string, took time.Duration) ir.ReusedInput {
		finished := time.Now().Add(-time.Hour)
		return ir.ReusedInput{
			Step:       step,
			RunID:      "source",
			StartedAt:  stringutil.FormatTime(finished.Add(-took)),
			FinishedAt: stringutil.FormatTime(finished),
			Via:        ir.ReusedInputViaRun,
		}
	}
	seedActivityRun(t, server, dag, activityRun{id: "older", status: ir.Succeeded, startAgo: 72 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{
		id: "first", status: ir.Succeeded, startAgo: 3 * time.Hour, took: 5 * time.Minute,
		reused: []ir.ReusedInput{reused("a", 4*time.Second), reused("b", 6*time.Second)},
	})
	seedActivityRun(t, server, dag, activityRun{id: "second", status: ir.Failed, startAgo: 30 * time.Minute, took: time.Minute})

	dags := listActivity(t, server, "?activityWindow=86400")
	activity := dags["activity_window"].Activity
	require.NotNil(t, activity)
	assert.Equal(t, []string{"second", "first"}, activityRunIDs(activity))
	assert.False(t, activity.Truncated)

	second, first := activity.Runs[0], activity.Runs[1]
	assert.Equal(t, api.Status(ir.Failed), second.Status)
	assert.NotNil(t, second.FinishedAt)
	assert.Nil(t, second.CachedMs, "a run that reused nothing has no cached time")
	require.NotNil(t, first.CachedMs)
	assert.Equal(t, int64(10_000), *first.CachedMs, "the cached time sums the reused steps")

	assert.Nil(t, listActivity(t, server, "")["activity_window"].Activity,
		"a list without a window carries no activity")
}

func TestListDAGsActivityOfAnIdleDAGIsEmpty(t *testing.T) {
	server := test.SetupServer(t)
	idle := createActivityDAG(t, server, "activity_idle")
	createActivityDAG(t, server, "activity_never")
	seedActivityRun(t, server, idle, activityRun{id: "ancient", status: ir.Succeeded, startAgo: 2 * 365 * 24 * time.Hour, took: time.Minute})

	dags := listActivity(t, server, "?activityWindow=86400")
	for _, name := range []string{"activity_idle", "activity_never"} {
		require.NotNil(t, dags[name].Activity, name)
		assert.Empty(t, dags[name].Activity.Runs, name)
		assert.False(t, dags[name].Activity.Truncated, name)
	}
}

func TestListDAGsActivityIsBounded(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_busy")
	const total = 65
	for i := range total {
		seedActivityRun(t, server, dag, activityRun{
			id: fmt.Sprintf("run-%03d", i), status: ir.Succeeded,
			startAgo: time.Duration(total-i) * time.Minute, took: time.Second,
		})
	}

	// A queued run takes no place among the 60 runs: a DAG with 60 runs and
	// a queued one lists them all.
	full := createActivityDAG(t, server, "activity_full")
	for i := range 60 {
		seedActivityRun(t, server, full, activityRun{
			id: fmt.Sprintf("run-%03d", i), status: ir.Succeeded,
			startAgo: time.Duration(60-i) * time.Minute, took: time.Second,
		})
	}
	seedActivityRun(t, server, full, activityRun{id: "queued", status: ir.Queued})

	dags := listActivity(t, server, "?activityWindow=86400")
	activity := dags["activity_busy"].Activity
	require.NotNil(t, activity)
	assert.Len(t, activity.Runs, 60)
	assert.True(t, activity.Truncated)
	assert.Equal(t, "run-064", activity.Runs[0].DagRunId, "the newest run comes first")

	activity = dags["activity_full"].Activity
	require.NotNil(t, activity)
	assert.Len(t, activity.Runs, 60)
	assert.False(t, activity.Truncated)
}

// A queued run has not started: it is not listed and does not hide the runs
// before it. A run that started before the window and is still active, here
// waiting on a person, is listed.
func TestListDAGsActivityAroundQueuedAndLongRuns(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_queue")
	seedActivityRun(t, server, dag, activityRun{id: "done", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "going", status: ir.Running, startAgo: 10 * time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "queued", status: ir.Queued})
	long := createActivityDAG(t, server, "activity_long")
	seedActivityRun(t, server, long, activityRun{id: "long", status: ir.Waiting, startAgo: 30 * time.Hour})

	for _, query := range []string{"?activityWindow=86400", "?activityWindow=86400&sort=lastRun"} {
		dags := listActivity(t, server, query)
		require.NotNil(t, dags["activity_queue"].Activity, query)
		assert.Equal(t, []string{"going", "done"}, activityRunIDs(dags["activity_queue"].Activity), query)
		require.NotNil(t, dags["activity_long"].Activity, query)
		assert.Equal(t, []string{"long"}, activityRunIDs(dags["activity_long"].Activity), query)
	}
}

// A run keeps the time it was created, when it was queued, which can be before
// the window it starts in. Such a latest run is listed whether it still runs
// or has ended, and the runs are in the order they started.
func TestListDAGsActivityOfRunsQueuedBeforeTheyStarted(t *testing.T) {
	server := test.SetupServer(t)
	running := createActivityDAG(t, server, "activity_late_running")
	seedActivityRun(t, server, running, activityRun{id: "running", status: ir.Running, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour})
	ended := createActivityDAG(t, server, "activity_late_ended")
	seedActivityRun(t, server, ended, activityRun{id: "ended", status: ir.Succeeded, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour, took: time.Minute})
	order := createActivityDAG(t, server, "activity_late_order")
	seedActivityRun(t, server, order, activityRun{id: "waited", status: ir.Succeeded, startAgo: time.Hour, queuedFor: 4 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, order, activityRun{id: "direct", status: ir.Succeeded, startAgo: 3 * time.Hour, took: time.Minute})

	for _, query := range []string{"?activityWindow=86400", "?activityWindow=86400&sort=lastRun"} {
		dags := listActivity(t, server, query)
		assert.Equal(t, []string{"running"}, activityRunIDs(dags["activity_late_running"].Activity), query)
		assert.Equal(t, []string{"ended"}, activityRunIDs(dags["activity_late_ended"].Activity), query)
		assert.Equal(t, []string{"waited", "direct"}, activityRunIDs(dags["activity_late_order"].Activity), query)
	}
}

// A backfill run is not listed, as it is not the latest run of its DAG
// either: the list shows a backfill on its own.
func TestListDAGsActivityLeavesOutBackfillRuns(t *testing.T) {
	server := test.SetupServer(t)
	idle := createActivityDAG(t, server, "activity_backfill_idle")
	seedActivityRun(t, server, idle, activityRun{id: "live", status: ir.Succeeded, startAgo: 72 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, idle, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})
	recent := createActivityDAG(t, server, "activity_backfill_recent")
	seedActivityRun(t, server, recent, activityRun{id: "live", status: ir.Succeeded, startAgo: 2 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, recent, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})
	only := createActivityDAG(t, server, "activity_backfill_only")
	seedActivityRun(t, server, only, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})

	dags := listActivity(t, server, "?activityWindow=86400")
	for _, name := range []string{"activity_backfill_idle", "activity_backfill_only"} {
		require.NotNil(t, dags[name].Activity, name)
		assert.Empty(t, dags[name].Activity.Runs, name)
	}
	assert.Equal(t, []string{"live"}, activityRunIDs(dags["activity_backfill_recent"].Activity))
}

func TestListDAGsActivityRejectsAWindowOutOfRange(t *testing.T) {
	server := test.SetupServer(t)
	for _, window := range []string{"10", "604801"} {
		server.Client().Get("/api/v1/dags?activityWindow=" + window).
			ExpectStatus(http.StatusBadRequest).Send(t)
	}
}
