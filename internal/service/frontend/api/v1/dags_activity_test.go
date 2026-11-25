// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
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

// listActivity reads the activity over the last day of the DAGs that names
// name, by fileName.
func listActivity(t *testing.T, server test.Server, names ...string) map[string]*api.DAGListActivity {
	t.Helper()
	page := readActivityPage(t, server, names...)
	byName := make(map[string]*api.DAGListActivity, len(page.Dags))
	for _, entry := range page.Dags {
		byName[entry.FileName] = &entry.Activity
	}
	return byName
}

func readActivityPage(t *testing.T, server test.Server, names ...string) api.DAGListActivityPage {
	t.Helper()
	var page api.DAGListActivityPage
	server.Client().Get("/api/v1/dag-list-activity?"+activityQuery("86400", names...)).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &page)
	return page
}

func activityQuery(window string, names ...string) string {
	return url.Values{"window": {window}, "fileName": names}.Encode()
}

func activityFileNames(page api.DAGListActivityPage) []string {
	names := make([]string, 0, len(page.Dags))
	for _, entry := range page.Dags {
		names = append(names, entry.FileName)
	}
	return names
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

	dags := listActivity(t, server, "activity_window")
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

	dags := listActivity(t, server, "activity_idle", "activity_never")
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

	dags := listActivity(t, server, "activity_busy", "activity_full")
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

	dags := listActivity(t, server, "activity_queue", "activity_retry", "activity_long")
	require.NotNil(t, dags["activity_queue"])
	assert.Equal(t, []string{"going", "done"}, activityRunIDs(dags["activity_queue"]))
	require.NotNil(t, dags["activity_retry"])
	assert.Empty(t, dags["activity_retry"].Runs)
	require.NotNil(t, dags["activity_long"])
	assert.Equal(t, []string{"long"}, activityRunIDs(dags["activity_long"]))
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

	dags := listActivity(t, server, "activity_late_running", "activity_late_ended", "activity_late_order")
	assert.Equal(t, []string{"running"}, activityRunIDs(dags["activity_late_running"]))
	assert.Equal(t, []string{"ended"}, activityRunIDs(dags["activity_late_ended"]))
	assert.Equal(t, []string{"waited", "direct"}, activityRunIDs(dags["activity_late_order"]))
}

// A backfill run is not listed, as it is not the latest run of its DAG
// either: the list shows a backfill on its own.
// A run that waited in the queue from before the window and started in it
// shows, also when a newer run is the latest. A run that started before the
// window does not.
func TestDAGListActivityOfRunsThatWaitedFromBeforeTheWindow(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_waited")
	seedActivityRun(t, server, dag, activityRun{id: "before", status: ir.Succeeded, startAgo: 40 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "waited", status: ir.Succeeded, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour, took: time.Minute})
	seedActivityRun(t, server, dag, activityRun{id: "newer", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})

	assert.Equal(t, []string{"newer", "waited"}, activityRunIDs(listActivity(t, server, "activity_waited")["activity_waited"]))
}

// A run can start after a run created after it, as a run started outside the
// queue does. Such a run shows when it started in the window, whether the
// newer run is the latest run or a run in the window follows it.
func TestDAGListActivityDoesNotAssumeRunsStartInOrder(t *testing.T) {
	server := test.SetupServer(t)
	seed := func(name string, recent bool) {
		dag := createActivityDAG(t, server, name)
		seedActivityRun(t, server, dag, activityRun{id: "waited", status: ir.Succeeded, startAgo: 2 * time.Hour, queuedFor: 28 * time.Hour, took: time.Minute})
		seedActivityRun(t, server, dag, activityRun{id: "direct", status: ir.Succeeded, startAgo: 26 * time.Hour, queuedFor: time.Hour, took: time.Minute})
		if recent {
			seedActivityRun(t, server, dag, activityRun{id: "recent", status: ir.Succeeded, startAgo: time.Hour, took: time.Minute})
		}
	}
	seed("activity_order_latest", false)
	seed("activity_order_recent", true)

	dags := listActivity(t, server, "activity_order_latest", "activity_order_recent")
	assert.Equal(t, []string{"waited"}, activityRunIDs(dags["activity_order_latest"]))
	assert.Equal(t, []string{"recent", "waited"}, activityRunIDs(dags["activity_order_recent"]))
}

// With more runs in the window than the activity lists, it lists the runs
// that started last, also when one of them was created before the others.
func TestDAGListActivityTruncatesByStart(t *testing.T) {
	server := test.SetupServer(t)
	dag := createActivityDAG(t, server, "activity_order_truncated")
	seedActivityRun(t, server, dag, activityRun{id: "failed-late", status: ir.Failed, startAgo: 30 * time.Minute, queuedFor: 19 * time.Hour, took: time.Minute})
	for i := range 60 {
		seedActivityRun(t, server, dag, activityRun{
			id: fmt.Sprintf("ok-%02d", i), status: ir.Succeeded, startAgo: 3*time.Hour + time.Duration(i)*time.Minute, took: time.Second,
		})
	}

	activity := listActivity(t, server, "activity_order_truncated")["activity_order_truncated"]
	require.NotNil(t, activity)
	require.Len(t, activity.Runs, 60)
	assert.Equal(t, "failed-late", activity.Runs[0].DagRunId)
	assert.True(t, activity.Truncated)
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

	activity := listActivity(t, server, "activity_canceled")["activity_canceled"]
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

	dags := listActivity(t, server, "activity_backfill_idle", "activity_backfill_recent", "activity_backfill_only")
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
	assert.Equal(t, []string{"first"}, activityRunIDs(listActivity(t, server, "activity_new")["activity_new"]))

	seedActivityRun(t, server, dag, activityRun{id: "second", status: ir.Running, startAgo: time.Minute})
	assert.Equal(t, []string{"second", "first"}, activityRunIDs(listActivity(t, server, "activity_new")["activity_new"]))
}

func TestDAGListActivityRejectsAWindowOutOfRange(t *testing.T) {
	server := test.SetupServer(t)
	for _, window := range []string{"10", "604801", ""} {
		server.Client().Get("/api/v1/dag-list-activity?" + activityQuery(window, "x")).
			ExpectStatus(http.StatusBadRequest).Send(t)
	}
}

// A request names from 1 to 100 DAGs.
func TestDAGListActivityLimitsNames(t *testing.T) {
	server := test.SetupServer(t)
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("dag_%03d", i)
	}
	for _, names := range [][]string{nil, tooMany} {
		server.Client().Get("/api/v1/dag-list-activity?" + activityQuery("86400", names...)).
			ExpectStatus(http.StatusBadRequest).Send(t)
	}
}

// The activity comes in the order of the request, each DAG once.
func TestDAGListActivityKeepsRequestOrder(t *testing.T) {
	server := test.SetupServer(t)
	for _, name := range []string{"order_a", "order_b", "order_c"} {
		createActivityDAG(t, server, name)
	}

	page := readActivityPage(t, server, "order_c", "order_a", "order_c", "order_b")
	assert.Equal(t, []string{"order_c", "order_a", "order_b"}, activityFileNames(page))
}

// A name that names no DAG of the list is left out, with no error.
func TestDAGListActivityLeavesOutUnknownNames(t *testing.T) {
	server := test.SetupServer(t)
	createActivityDAG(t, server, "known")

	page := readActivityPage(t, server, "known", "missing", "../known", "")
	assert.Equal(t, []string{"known"}, activityFileNames(page))
	assert.Empty(t, page.Errors)
}

// readAt is the server time when the activity was read, to the millisecond.
func TestDAGListActivityTellsWhenItRead(t *testing.T) {
	server := test.SetupServer(t)
	createActivityDAG(t, server, "read_at")

	before := time.Now().Truncate(time.Millisecond)
	page := readActivityPage(t, server, "read_at")
	after := time.Now()
	assert.False(t, page.ReadAt.Before(before), "readAt %s before %s", page.ReadAt, before)
	assert.False(t, page.ReadAt.After(after), "readAt %s after %s", page.ReadAt, after)
}

// A caller gets the activity of the DAGs of the workspaces it can see, and of
// DAGs in no workspace; a DAG it cannot see is left out with no error that
// names it.
func TestDAGListActivityLeavesOutOtherWorkspaces(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "ops_dag", []byte("labels: [workspace=ops]\nsteps:\n  - run: echo ops\n"))
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "dev_dag", []byte("labels: [workspace=dev]\nsteps:\n  - run: echo dev\n"))
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "plain_dag", []byte("steps:\n  - run: echo plain\n"))
	apiImpl := newDAGPinAPIWithStore(helper, nil, localapi.WithAuthService(stubAuthService{}))
	activityOf := func(workspace string) api.DAGListActivityPage {
		t.Helper()
		ctx := auth.WithUser(context.Background(), &auth.User{
			Username: "viewer", Role: auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: workspace, Role: auth.RoleViewer}}},
		})
		resp, err := apiImpl.GetDAGListActivity(ctx, api.GetDAGListActivityRequestObject{Params: api.GetDAGListActivityParams{
			FileName: []string{"ops_dag", "dev_dag", "plain_dag"}, Window: 86400,
		}})
		require.NoError(t, err)
		return api.DAGListActivityPage(resp.(api.GetDAGListActivity200JSONResponse))
	}

	ops := activityOf("ops")
	assert.Equal(t, []string{"ops_dag", "plain_dag"}, activityFileNames(ops))
	assert.Empty(t, ops.Errors)
	assert.Equal(t, []string{"dev_dag", "plain_dag"}, activityFileNames(activityOf("dev")))
}
