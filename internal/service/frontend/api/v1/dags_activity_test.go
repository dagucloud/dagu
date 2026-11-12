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
	took     time.Duration
	reused   []ir.ReusedInput
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
		server.Context, dag, start, run.id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	options := []ir.StatusOption{
		ir.WithAttemptID(attempt.ID()),
		ir.WithHierarchyRefs(ir.NewDAGRunRef(dag.Name, run.id), ir.DAGRunRef{}),
		ir.WithReusedInputs(run.reused),
	}
	if !run.status.IsActive() {
		options = append(options, ir.WithFinishedAt(start.Add(run.took)))
	}
	status := ir.NewStatusBuilder(dag).Create(run.id, run.status, 0, start, options...)
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

	activity := listActivity(t, server, "?activityWindow=86400")["activity_busy"].Activity
	require.NotNil(t, activity)
	assert.Len(t, activity.Runs, 60)
	assert.True(t, activity.Truncated)
	assert.Equal(t, "run-064", activity.Runs[0].DagRunId, "the newest run comes first")
}

func TestListDAGsActivityRejectsAWindowOutOfRange(t *testing.T) {
	server := test.SetupServer(t)
	for _, window := range []string{"10", "604801"} {
		server.Client().Get("/api/v1/dags?activityWindow=" + window).
			ExpectStatus(http.StatusBadRequest).Send(t)
	}
}
