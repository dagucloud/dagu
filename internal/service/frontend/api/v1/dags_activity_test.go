// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
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
	backfill  string
	// retried keeps the times of the previous attempt on a queued run, as a
	// retry that waits in the queue does.
	retried bool
	// unstarted leaves the run without a start, as a run that is canceled in
	// the queue ends.
	unstarted bool
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
		ir.WithBackfillID(run.backfill),
	}
	if !run.status.IsActive() || run.retried {
		options = append(options, ir.WithFinishedAt(start.Add(run.took)))
	}
	startedAt := start
	if run.status == ir.Queued && !run.retried || run.unstarted {
		startedAt = time.Time{}
	}
	status := ir.NewStatusBuilder(dag).Create(run.id, run.status, 0, startedAt, options...)
	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, status))
	require.NoError(t, attempt.Close(server.Context))
}

// listActivity reads the activity of the DAG list page of query, by fileName.
func listActivity(t *testing.T, server test.Server, query string) map[string]*api.DAGListActivity {
	t.Helper()
	page := readActivityPage(t, server, query)
	byName := make(map[string]*api.DAGListActivity, len(page.Dags))
	for _, entry := range page.Dags {
		byName[entry.FileName] = &entry.Activity
	}
	return byName
}

func readActivityPage(t *testing.T, server test.Server, query string) api.DAGListActivityPage {
	t.Helper()
	var page api.DAGListActivityPage
	server.Client().Get("/api/v1/dag-list-activity"+query).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &page)
	return page
}

func activityRunIDs(activity *api.DAGListActivity) []string {
	ids := make([]string, 0, len(activity.Runs))
	for _, run := range activity.Runs {
		ids = append(ids, run.DagRunId)
	}
	return ids
}

func TestDAGListActivityLimitedToTheWindow(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_window")
	seedActivityRun(t, server, dag, activityRun{id: "older", status: ir.Succeeded, startAgo: 72 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "first", status: ir.Succeeded, startAgo: 3 * time.Hour, took: 5 * time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "second", status: ir.Failed, startAgo: 30 * time.Minute, took: time.Minute})

	dags := listActivity(t, server, "?window=86400")
	activity := dags["activity_window"]
	require.NotNil(t, activity)
	assert.Equal(t, []string{"second", "first"}, activityRunIDs(activity))
	assert.False(t, activity.Truncated)

	second := activity.Runs[0]
	assert.Equal(t, api.Status(ir.Failed), second.Status)
	assert.NotNil(t, second.FinishedAt)

}

func TestDAGListActivityOfAnIdleDAGIsEmpty(t *testing.T) {
	server := test.SetupServer(t)
	idle := createActivityDAG(t, server, "activity_idle")
	createActivityDAG(t, server, "activity_never")
	seedActivityRun(t, server, idle, activityRun{id: "ancient", status: ir.Succeeded, startAgo: 2 * 365 * 24 * time.Hour, took: time.Minute})

	dags := listActivity(t, server, "?window=86400")
	for _, name := range []string{"activity_idle", "activity_never"} {
		require.NotNil(t, dags[name], name)
		assert.Empty(t, dags[name].Runs, name)
		assert.False(t, dags[name].Truncated, name)
	}
}

func TestDAGListActivityIsBounded(t *testing.T) {
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

	dags := listActivity(t, server, "?window=86400")
	activity := dags["activity_busy"]
	require.NotNil(t, activity)
	assert.Len(t, activity.Runs, 60)
	assert.True(t, activity.Truncated)
	assert.Equal(t, "run-064", activity.Runs[0].DagRunId, "the newest run comes first")

	activity = dags["activity_full"]
	require.NotNil(t, activity)
	assert.Len(t, activity.Runs, 60)
	assert.False(t, activity.Truncated)
}

// A queued run has not started: it is not listed and does not hide the runs
// before it. A retry in the queue is not listed either, though it keeps the
// start of its previous attempt. A run that started before the window and is
// still active, here waiting on a person, is listed.
func TestDAGListActivityAroundQueuedAndLongRuns(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_queue")
	seedActivityRun(t, server, dag, activityRun{id: "done", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "going", status: ir.Running, startAgo: 10 * time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "queued", status: ir.Queued})
	retry := createActivityDAG(t, server, "activity_retry")
	seedActivityRun(t, server, retry, activityRun{id: "retry", status: ir.Queued, startAgo: 30 * time.Hour, took: time.Minute, retried: true})
	long := createActivityDAG(t, server, "activity_long")
	seedActivityRun(t, server, long, activityRun{id: "long", status: ir.Waiting, startAgo: 30 * time.Hour})

	for _, query := range []string{"?window=86400", "?window=86400&sort=lastRun"} {
		dags := listActivity(t, server, query)
		require.NotNil(t, dags["activity_queue"], query)
		assert.Equal(t, []string{"going", "done"}, activityRunIDs(dags["activity_queue"]), query)
		require.NotNil(t, dags["activity_retry"], query)
		assert.Empty(t, dags["activity_retry"].Runs, query)
		require.NotNil(t, dags["activity_long"], query)
		assert.Equal(t, []string{"long"}, activityRunIDs(dags["activity_long"]), query)
	}
}

// A run keeps the time it was created, when it was queued, which can be before
// the window it starts in. Such a latest run is listed whether it still runs
// or has ended, and the runs are in the order they started.
func TestDAGListActivityOfRunsQueuedBeforeTheyStarted(t *testing.T) {
	server := test.SetupServer(t)
	running := createActivityDAG(t, server, "activity_late_running")
	seedActivityRun(t, server, running, activityRun{id: "running", status: ir.Running, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour})
	ended := createActivityDAG(t, server, "activity_late_ended")
	seedActivityRun(t, server, ended, activityRun{id: "ended", status: ir.Succeeded, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour, took: time.Minute})
	order := createActivityDAG(t, server, "activity_late_order")
	seedActivityRun(t, server, order, activityRun{id: "waited", status: ir.Succeeded, startAgo: time.Hour, queuedFor: 4 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, order, activityRun{id: "direct", status: ir.Succeeded, startAgo: 3 * time.Hour, took: time.Minute})

	for _, query := range []string{"?window=86400", "?window=86400&sort=lastRun"} {
		dags := listActivity(t, server, query)
		assert.Equal(t, []string{"running"}, activityRunIDs(dags["activity_late_running"]), query)
		assert.Equal(t, []string{"ended"}, activityRunIDs(dags["activity_late_ended"]), query)
		assert.Equal(t, []string{"waited", "direct"}, activityRunIDs(dags["activity_late_order"]), query)
	}
}

// A backfill run is not listed, as it is not the latest run of its DAG
// either: the list shows a backfill on its own.
// A run that waited in the queue from before the window and started in it
// shows, also when a newer run is the latest. The search for such runs ends at
// the first run that started before the window.
func TestDAGListActivityOfRunsThatWaitedFromBeforeTheWindow(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_waited")
	seedActivityRun(t, server, dag, activityRun{id: "before", status: ir.Succeeded, startAgo: 40 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "waited", status: ir.Succeeded, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "newer", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})

	for _, query := range []string{"?window=86400", "?window=86400&sort=lastRun"} {
		assert.Equal(t, []string{"newer", "waited"}, activityRunIDs(listActivity(t, server, query)["activity_waited"]), query)
	}
}

// Runs canceled in the queue take none of the 60 places, so they do not hide
// the runs before them.
func TestDAGListActivityLeavesOutRunsCanceledInTheQueue(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_canceled")
	seedActivityRun(t, server, dag, activityRun{id: "ran", status: ir.Succeeded, startAgo: 3 * time.Hour, took: time.Minute})
	for i := range 60 {
		seedActivityRun(t, server, dag, activityRun{
			id: fmt.Sprintf("canceled-%02d", i), status: ir.Aborted, startAgo: time.Duration(i+1) * time.Minute, unstarted: true,
		})
	}

	activity := listActivity(t, server, "?window=86400")["activity_canceled"]
	require.NotNil(t, activity)
	assert.Equal(t, []string{"ran"}, activityRunIDs(activity))
	assert.False(t, activity.Truncated)
}

func TestDAGListActivityLeavesOutBackfillRuns(t *testing.T) {
	server := test.SetupServer(t)
	idle := createActivityDAG(t, server, "activity_backfill_idle")
	seedActivityRun(t, server, idle, activityRun{id: "live", status: ir.Succeeded, startAgo: 72 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, idle, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})
	recent := createActivityDAG(t, server, "activity_backfill_recent")
	seedActivityRun(t, server, recent, activityRun{id: "live", status: ir.Succeeded, startAgo: 2 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, recent, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})
	only := createActivityDAG(t, server, "activity_backfill_only")
	seedActivityRun(t, server, only, activityRun{id: "slot", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute, backfill: "fill"})

	dags := listActivity(t, server, "?window=86400")
	for _, name := range []string{"activity_backfill_idle", "activity_backfill_only"} {
		require.NotNil(t, dags[name], name)
		assert.Empty(t, dags[name].Runs, name)
	}
	assert.Equal(t, []string{"live"}, activityRunIDs(dags["activity_backfill_recent"]))
}

// A list keeps the activity of a DAG, and the next list shows a new run of
// the DAG at once.
func TestDAGListActivityShowsANewRunAtOnce(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_new")
	seedActivityRun(t, server, dag, activityRun{id: "first", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})
	assert.Equal(t, []string{"first"}, activityRunIDs(listActivity(t, server, "?window=86400")["activity_new"]))

	seedActivityRun(t, server, dag, activityRun{id: "second", status: ir.Running, startAgo: time.Minute})
	assert.Equal(t, []string{"second", "first"}, activityRunIDs(listActivity(t, server, "?window=86400")["activity_new"]))
}

func TestDAGListActivityRejectsAWindowOutOfRange(t *testing.T) {
	server := test.SetupServer(t)
	for _, query := range []string{"?window=10", "?window=604801", ""} {
		server.Client().Get("/api/v1/dag-list-activity" + query).
			ExpectStatus(http.StatusBadRequest).Send(t)
	}
}

// The activity covers the DAGs of the list page with the same paging and
// sort, in the order of the list.
func TestDAGListActivityFollowsTheListPage(t *testing.T) {
	server := test.SetupServer(t)
	for _, name := range []string{"page_a", "page_b", "page_c"} {
		createActivityDAG(t, server, name)
	}

	for _, query := range []string{"perPage=2&page=2", "perPage=2&page=1&order=desc"} {
		var list api.ListDAGs200JSONResponse
		server.Client().Get("/api/v1/dags?"+query).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &list)
		want := make([]string, 0, len(list.Dags))
		for _, dag := range list.Dags {
			want = append(want, dag.FileName)
		}
		require.NotEmpty(t, want, query)

		page := readActivityPage(t, server, "?window=86400&"+query)
		got := make([]string, 0, len(page.Dags))
		for _, entry := range page.Dags {
			got = append(got, entry.FileName)
		}
		assert.Equal(t, want, got, query)
	}
}
