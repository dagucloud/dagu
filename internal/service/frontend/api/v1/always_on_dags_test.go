// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAlwaysOnDAGAPI(helper test.Helper) *localapi.API {
	return localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)
}

const alwaysOnDAGSpec = "restart_policy: always\nrestart_wait_sec: 5\nhist_retention_runs: 10\nsteps:\n  - run: echo hi\n"

// seedRun saves a run of a DAG with the given status and trigger.
func seedRun(t *testing.T, helper test.Helper, name, id string, status ir.Status, trigger ir.TriggerType) {
	t.Helper()
	seedRunAt(t, helper, name, id, status, trigger, time.Now())
}

// seedRunAt saves a run that started at the given time.
func seedRunAt(t *testing.T, helper test.Helper, name, id string, status ir.Status, trigger ir.TriggerType, now time.Time) {
	t.Helper()
	ctx := context.Background()
	dag := &ir.DAG{Name: name}
	attempt, err := helper.DAGRunRepository.CreateAttempt(ctx, dag, now, id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	runStatus := ir.InitialStatus(dag)
	runStatus.DAGRunID = id
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = status
	runStatus.TriggerType = trigger
	runStatus.StartedAt = now.Format(time.RFC3339)
	if !status.IsActive() {
		runStatus.FinishedAt = now.Format(time.RFC3339)
	}
	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
}

// With latest_status_today, the usual lookup ignores a run of an earlier day.
// An always-on DAG that has been down for a day still has a latest run, so it is
// not pending.
func TestListAlwaysOnDAGsSeesTheLatestRunOfAnEarlierDay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence(), test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.LatestStatusToday = true
	}))
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc-old", []byte(alwaysOnDAGSpec))
	seedRunAt(t, helper, "svc-old", "run-old", ir.Succeeded, ir.TriggerTypeRestart, time.Now().Add(-48*time.Hour))

	resp, err := newAlwaysOnDAGAPI(helper).ListAlwaysOnDAGs(ctx, api.ListAlwaysOnDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListAlwaysOnDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	require.NotNil(t, list.Dags[0].LatestRun)
	assert.Equal(t, "run-old", list.Dags[0].LatestRun.DagRunId)
	assert.NotEqual(t, api.AlwaysOnDAGStatePending, list.Dags[0].State)
}

func TestListAlwaysOnDAGs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	dir := helper.Config.Paths.DAGsDir
	helper.CreateDAGFile(t, dir, "plain", []byte("steps:\n  - run: echo plain\n"))
	for _, name := range []string{"svc-idle", "svc-live", "svc-done", "svc-failing", "svc-paused"} {
		helper.CreateDAGFile(t, dir, name, []byte(alwaysOnDAGSpec))
	}
	seedRun(t, helper, "svc-live", "run-live", ir.Running, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-done", "run-done", ir.Succeeded, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-failing", "run-failed", ir.Failed, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-paused", "run-paused", ir.Succeeded, ir.TriggerTypeRestart)
	apiImpl := newAlwaysOnDAGAPI(helper)
	require.NoError(t, helper.DAGRepository.SetSuspended(ctx, "svc-paused", true))

	resp, err := apiImpl.ListAlwaysOnDAGs(ctx, api.ListAlwaysOnDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListAlwaysOnDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)

	states := make(map[string]api.AlwaysOnDAGState, len(list.Dags))
	names := make([]string, 0, len(list.Dags))
	for _, dag := range list.Dags {
		names = append(names, dag.FileName)
		states[dag.FileName] = dag.State
		assert.Equal(t, api.RestartPolicy("always"), dag.RestartPolicy)
		assert.Equal(t, 5, dag.RestartWaitSec)
	}
	assert.Equal(t, []string{"svc-done", "svc-failing", "svc-idle", "svc-live", "svc-paused"}, names)
	assert.Equal(t, map[string]api.AlwaysOnDAGState{
		"svc-idle":    "pending",
		"svc-live":    "running",
		"svc-done":    "restarting",
		"svc-failing": "failing",
		"svc-paused":  "suspended",
	}, states)
	assert.Empty(t, list.Errors)

	for _, dag := range list.Dags {
		if dag.FileName == "svc-idle" {
			assert.Nil(t, dag.LatestRun)
			continue
		}
		require.NotNil(t, dag.LatestRun, dag.FileName)
		assert.NotEmpty(t, dag.LatestRun.DagRunId)
	}

	// The SSE topic carries the same list.
	sseData, err := apiImpl.GetAlwaysOnDAGsData(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, api.AlwaysOnDAGsResponse(list), sseData)
}

// A run that a restart policy started stays out of the list on request, and a
// run that someone started by hand stays in.
func TestListDAGRunsExcludesAlwaysOnRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	seedRun(t, helper, "svc", "restarted", ir.Succeeded, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc", "manual", ir.Succeeded, ir.TriggerTypeManual)
	apiImpl := newAlwaysOnDAGAPI(helper)

	runIDs := func(runs []api.DAGRunSummary) []string {
		ids := make([]string, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.DagRunId)
		}
		return ids
	}
	list := func(exclude *bool) []string {
		resp, err := apiImpl.ListDAGRuns(ctx, api.ListDAGRunsRequestObject{
			Params: api.ListDAGRunsParams{ExcludeAlwaysOnRuns: exclude},
		})
		require.NoError(t, err)
		page, ok := resp.(api.ListDAGRuns200JSONResponse)
		require.True(t, ok, "expected 200 response, got %T", resp)
		return runIDs(page.DagRuns)
	}
	yes, no := true, false

	assert.ElementsMatch(t, []string{"restarted", "manual"}, list(nil))
	assert.ElementsMatch(t, []string{"restarted", "manual"}, list(&no))
	assert.Equal(t, []string{"manual"}, list(&yes))

	sseData, err := apiImpl.GetDAGRunsListData(ctx, "excludeAlwaysOnRuns=true")
	require.NoError(t, err)
	page, ok := sseData.(api.DAGRunsPageResponse)
	require.True(t, ok, "expected a page, got %T", sseData)
	assert.Equal(t, []string{"manual"}, runIDs(page.DagRuns))

	err = apiImpl.ValidateDAGRunsListQuery(ctx, "excludeAlwaysOnRuns=maybe")
	requireAPIErrorStatus(t, err, http.StatusBadRequest)
}

// A DAG-based queue exists only while a run is alive or waiting, so the queue
// of a dag must be added for the wait before each restart.
func TestQueuesKeepTheQueueOfAnIdleAlwaysOnDAG(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	dir := helper.Config.Paths.DAGsDir
	helper.CreateDAGFile(t, dir, "plain", []byte("steps:\n  - run: echo plain\n"))
	helper.CreateDAGFile(t, dir, "svc-idle", []byte(alwaysOnDAGSpec))
	seedRun(t, helper, "svc-idle", "run-done", ir.Succeeded, ir.TriggerTypeRestart)
	apiImpl := newAlwaysOnDAGAPI(helper)

	resp, err := apiImpl.ListQueues(ctx, api.ListQueuesRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListQueues200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	names := make([]string, 0, len(list.Queues))
	for _, queue := range list.Queues {
		names = append(names, queue.Name)
	}
	assert.Equal(t, []string{"svc-idle"}, names)
	assert.Equal(t, api.QueueTypeDagBased, list.Queues[0].Type)
	assert.Zero(t, list.Queues[0].RunningCount)
	assert.Zero(t, list.Queues[0].QueuedCount)

	one, err := apiImpl.GetQueue(ctx, api.GetQueueRequestObject{Name: "svc-idle"})
	require.NoError(t, err)
	assert.Equal(t, "svc-idle", one.(api.GetQueue200JSONResponse).Name)

	_, err = apiImpl.GetQueue(ctx, api.GetQueueRequestObject{Name: "plain"})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
}

// seedRunBetween saves a run that started and ended at the given times.
func seedRunBetween(t *testing.T, helper test.Helper, name, id string, status ir.Status, start, end time.Time) {
	t.Helper()
	ctx := context.Background()
	dag := &ir.DAG{Name: name}
	attempt, err := helper.DAGRunRepository.CreateAttempt(ctx, dag, start, id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	runStatus := ir.InitialStatus(dag)
	runStatus.DAGRunID = id
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = status
	runStatus.TriggerType = ir.TriggerTypeRestart
	runStatus.StartedAt = start.Format(time.RFC3339)
	runStatus.FinishedAt = end.Format(time.RFC3339)
	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
}

func TestAlwaysOnDAGsHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	dir := helper.Config.Paths.DAGsDir
	helper.CreateDAGFile(t, dir, "plain", []byte("steps:\n  - run: echo plain\n"))
	helper.CreateDAGFile(t, dir, "svc", []byte(alwaysOnDAGSpec))
	apiImpl := newAlwaysOnDAGAPI(helper)

	// Two runs, 10 seconds apart, one a minute before the window ends.
	now := time.Now().Truncate(time.Second)
	seedRunBetween(t, helper, "svc", "run-1", ir.Succeeded, now.Add(-300*time.Second), now.Add(-200*time.Second))
	seedRunBetween(t, helper, "svc", "run-2", ir.Failed, now.Add(-190*time.Second), now.Add(-100*time.Second))

	buckets := 4
	resp, err := apiImpl.GetAlwaysOnDAGsHistory(ctx, api.GetAlwaysOnDAGsHistoryRequestObject{
		Params: api.GetAlwaysOnDAGsHistoryParams{
			FromDate: now.Add(-400 * time.Second).Unix(),
			ToDate:   now.Unix(),
			Buckets:  &buckets,
		},
	})
	require.NoError(t, err)
	history, ok := resp.(api.GetAlwaysOnDAGsHistory200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)

	assert.Equal(t, 100, history.BucketSec)
	require.Len(t, history.Dags, 1)
	dag := history.Dags[0]
	assert.Equal(t, "svc", dag.Name)
	assert.False(t, dag.Capped)

	var runs, failed, running, wait, maxWait int
	for _, bucket := range dag.Buckets {
		runs += bucket.Runs
		failed += bucket.Failed
		running += bucket.RunningSec
		wait += bucket.WaitSec
		maxWait = max(maxWait, bucket.MaxWaitSec)
	}
	assert.Equal(t, 2, runs)
	assert.Equal(t, 1, failed)
	assert.Equal(t, 190, running)
	assert.Equal(t, 10, wait)
	assert.Equal(t, 10, maxWait)
}

func TestAlwaysOnDAGsHistoryRejectsBadWindows(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t, test.WithStatusPersistence())
	apiImpl := newAlwaysOnDAGAPI(helper)
	now := time.Now()
	tooMany, none := 201, 0

	for name, params := range map[string]api.GetAlwaysOnDAGsHistoryParams{
		"EmptyWindow": {FromDate: now.Unix(), ToDate: now.Unix()},
		"Reversed":    {FromDate: now.Unix(), ToDate: now.Add(-time.Hour).Unix()},
		"TooLong":     {FromDate: now.Add(-32 * 24 * time.Hour).Unix(), ToDate: now.Unix()},
		"TooMany":     {FromDate: now.Add(-time.Hour).Unix(), ToDate: now.Unix(), Buckets: &tooMany},
		"None":        {FromDate: now.Add(-time.Hour).Unix(), ToDate: now.Unix(), Buckets: &none},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := apiImpl.GetAlwaysOnDAGsHistory(context.Background(), api.GetAlwaysOnDAGsHistoryRequestObject{Params: params})
			requireAPIErrorStatus(t, err, http.StatusBadRequest)
		})
	}
}
