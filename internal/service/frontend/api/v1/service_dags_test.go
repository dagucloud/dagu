// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/proc"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newServiceDAGAPI(helper test.Helper) *localapi.API {
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

const serviceDAGSpec = "restart_policy: always\nrestart_wait_sec: 5\nhist_retention_runs: 10\nsteps:\n  - run: echo hi\n"

// seedRun saves a run of a DAG with the given status and trigger.
func seedRun(t *testing.T, helper test.Helper, name, id string, status ir.Status, trigger ir.TriggerType) {
	t.Helper()
	seedRunAt(t, helper, name, id, status, trigger, time.Now())
}

// seedRunAt saves a run that started at the given time, with the labels the
// DAG had then. A running run gets a live process heartbeat for the rest of
// the test; without one, a status read after the startup grace window would
// repair it as a stale process.
func seedRunAt(t *testing.T, helper test.Helper, name, id string, status ir.Status, trigger ir.TriggerType, now time.Time, labels ...string) {
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
	runStatus.Labels = labels
	runStatus.StartedAt = now.Format(time.RFC3339)
	if !status.IsActive() {
		runStatus.FinishedAt = now.Format(time.RFC3339)
	}
	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
	if status != ir.Running {
		return
	}
	handle, err := helper.ProcRepository.Acquire(ctx, dag.ProcGroup(), proc.ProcMeta{
		StartedAt:    now.Unix(),
		Name:         name,
		DAGRunID:     id,
		AttemptID:    attempt.ID(),
		RootName:     name,
		RootDAGRunID: id,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
}

// With latest_status_today, the usual lookup ignores a run of an earlier day.
// A service DAG that has been down for a day still has a latest run, so it is
// not pending.
func TestListServiceDAGsSeesTheLatestRunOfAnEarlierDay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence(), test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.LatestStatusToday = true
	}))
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc-old", []byte(serviceDAGSpec))
	seedRunAt(t, helper, "svc-old", "run-old", ir.Succeeded, ir.TriggerTypeRestart, time.Now().Add(-48*time.Hour))

	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	require.NotNil(t, list.Dags[0].LatestRun)
	assert.Equal(t, "run-old", list.Dags[0].LatestRun.DagRunId)
	assert.NotEqual(t, api.ServiceDAGStatePending, list.Dags[0].State)
}

// A run keeps the workspace its DAG had when it started. After the DAG moves
// to another workspace, a viewer of that workspace does not see the runs it
// made in the previous one.
func TestListServiceDAGsHidesRunsOfAnotherWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc-moved", []byte("labels:\n  - workspace=dev\n"+serviceDAGSpec))
	seedRunAt(t, helper, "svc-moved", "run-ops", ir.Running, ir.TriggerTypeRestart, time.Now(), "workspace=ops")

	dev := api.Workspace("dev")
	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{
		Params: api.ListServiceDAGsParams{Workspace: &dev},
	})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	assert.Nil(t, list.Dags[0].LatestRun)
	assert.Equal(t, api.ServiceDAGStatePending, list.Dags[0].State)
}

func TestListServiceDAGs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	dir := helper.Config.Paths.DAGsDir
	helper.CreateDAGFile(t, dir, "plain", []byte("steps:\n  - run: echo plain\n"))
	for _, name := range []string{"svc-idle", "svc-live", "svc-done", "svc-failing", "svc-paused"} {
		helper.CreateDAGFile(t, dir, name, []byte(serviceDAGSpec))
	}
	seedRun(t, helper, "svc-live", "run-live", ir.Running, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-done", "run-done", ir.Succeeded, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-failing", "run-failed", ir.Failed, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc-paused", "run-paused", ir.Succeeded, ir.TriggerTypeRestart)
	apiImpl := newServiceDAGAPI(helper)
	require.NoError(t, helper.DAGRepository.SetSuspended(ctx, "svc-paused", true))

	resp, err := apiImpl.ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)

	states := make(map[string]api.ServiceDAGState, len(list.Dags))
	names := make([]string, 0, len(list.Dags))
	for _, dag := range list.Dags {
		names = append(names, dag.FileName)
		states[dag.FileName] = dag.State
		assert.Equal(t, api.RestartPolicy("always"), dag.RestartPolicy)
		assert.Equal(t, 5, dag.RestartWaitSec)
	}
	assert.Equal(t, []string{"svc-done", "svc-failing", "svc-idle", "svc-live", "svc-paused"}, names)
	assert.Equal(t, map[string]api.ServiceDAGState{
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
	sseData, err := apiImpl.GetServiceDAGsData(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, api.ServiceDAGsResponse(list), sseData)
}

// A DAG that sets no queue runs in a queue named after it, so a client can
// tell which queue belongs to the DAG.
func TestListServiceDAGsReportsTheQueueOfEachDAG(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	dir := helper.Config.Paths.DAGsDir
	helper.CreateDAGFile(t, dir, "svc-own", []byte(serviceDAGSpec))
	helper.CreateDAGFile(t, dir, "svc-shared", []byte("queue: services\n"+serviceDAGSpec))

	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)

	queues := make(map[string]string, len(list.Dags))
	for _, dag := range list.Dags {
		queues[dag.FileName] = dag.Queue
	}
	assert.Equal(t, map[string]string{"svc-own": "svc-own", "svc-shared": "services"}, queues)
}

// A latest run that cannot be read is not a DAG without a run: the scheduler
// starts nothing after it, so the list reports the error and does not say that a
// first run is coming.
func TestListServiceDAGsReportsUnreadableLatestRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc-broken", []byte(serviceDAGSpec))
	seedRun(t, helper, "svc-broken", "run-broken", ir.Succeeded, ir.TriggerTypeRestart)
	corruptStatusFiles(t, helper.Config.Paths.DAGRunsDir)

	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	assert.Equal(t, api.ServiceDAGStateFailing, list.Dags[0].State)
	require.NotNil(t, list.Dags[0].ReadError)
	assert.NotEmpty(t, *list.Dags[0].ReadError)
	assert.Empty(t, list.Errors)
}

// A run that failed in a step names the step and its error, which the run's
// own error field leaves empty.
func TestListServiceDAGsReportsWhyTheLatestRunFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc", []byte(serviceDAGSpec))
	dag := &ir.DAG{Name: "svc"}
	attempt, err := helper.DAGRunRepository.CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = "run-1"
	status.AttemptID = attempt.ID()
	status.Status = ir.Failed
	status.StartedAt = time.Now().Format(time.RFC3339)
	status.FinishedAt = status.StartedAt
	status.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "prepare"}, Status: ir.NodeSucceeded},
		{Step: ir.Step{Name: "serve"}, Status: ir.NodeFailed, Error: "exit status 1"},
	}
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	require.NotNil(t, list.Dags[0].LatestRun)
	require.NotNil(t, list.Dags[0].LatestRun.Error)
	assert.Equal(t, "serve: exit status 1", *list.Dags[0].LatestRun.Error)
}

// corruptStatusFiles replaces every status file below dir with a complete line
// that is not a status.
func corruptStatusFiles(t *testing.T, dir string) {
	t.Helper()
	found := false
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != filedagrun.JSONLStatusFile {
			return err
		}
		found = true
		return os.WriteFile(path, []byte("not a status\n"), 0o600)
	}))
	require.True(t, found, "no status file below %s", dir)
}

// A stop leaves a request on the attempt, whatever status the run ends with.
func TestListServiceDAGsSeesTheStopOfARunThatFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	spec := "restart_policy: unless-stopped\nrestart_wait_sec: 5\nsteps:\n  - run: echo hi\n"
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc-stopped", []byte(spec))
	seedRun(t, helper, "svc-stopped", "run-stopped", ir.Failed, ir.TriggerTypeRestart)
	attempt, err := helper.DAGRunRepository.FindAttempt(ctx, ir.NewDAGRunRef("svc-stopped", "run-stopped"))
	require.NoError(t, err)
	require.NoError(t, attempt.Abort(ctx))

	resp, err := newServiceDAGAPI(helper).ListServiceDAGs(ctx, api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	assert.Equal(t, api.ServiceDAGStateStopped, list.Dags[0].State)
}

// On request a run list leaves out every run of a service DAG, whoever
// started it. A restart run of a DAG that is no longer a service DAG stays in.
func TestListDAGRunsExcludesServiceDAGRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc", []byte(serviceDAGSpec))
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "former", []byte("steps:\n  - run: echo hi\n"))
	seedRun(t, helper, "svc", "restarted", ir.Succeeded, ir.TriggerTypeRestart)
	seedRun(t, helper, "svc", "manual", ir.Succeeded, ir.TriggerTypeManual)
	seedRun(t, helper, "former", "former-restart", ir.Succeeded, ir.TriggerTypeRestart)
	apiImpl := newServiceDAGAPI(helper)

	runIDs := func(runs []api.DAGRunSummary) []string {
		ids := make([]string, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.DagRunId)
		}
		return ids
	}
	list := func(exclude *bool) []string {
		resp, err := apiImpl.ListDAGRuns(ctx, api.ListDAGRunsRequestObject{
			Params: api.ListDAGRunsParams{ExcludeServiceDAGRuns: exclude},
		})
		require.NoError(t, err)
		page, ok := resp.(api.ListDAGRuns200JSONResponse)
		require.True(t, ok, "expected 200 response, got %T", resp)
		return runIDs(page.DagRuns)
	}
	yes, no := true, false

	assert.ElementsMatch(t, []string{"restarted", "manual", "former-restart"}, list(nil))
	assert.ElementsMatch(t, []string{"restarted", "manual", "former-restart"}, list(&no))
	assert.Equal(t, []string{"former-restart"}, list(&yes))

	sseData, err := apiImpl.GetDAGRunsListData(ctx, "excludeServiceDAGRuns=true")
	require.NoError(t, err)
	page, ok := sseData.(api.DAGRunsPageResponse)
	require.True(t, ok, "expected a page, got %T", sseData)
	assert.Equal(t, []string{"former-restart"}, runIDs(page.DagRuns))

	err = apiImpl.ValidateDAGRunsListQuery(ctx, "excludeServiceDAGRuns=maybe")
	requireAPIErrorStatus(t, err, http.StatusBadRequest)
}

// consecutiveFailures reads the service DAG list and returns the count of
// failed runs in a row that it reports for the only DAG.
func consecutiveFailures(t *testing.T, apiImpl *localapi.API) int {
	t.Helper()
	resp, err := apiImpl.ListServiceDAGs(context.Background(), api.ListServiceDAGsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(api.ListServiceDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	require.Len(t, list.Dags, 1)
	return list.Dags[0].ConsecutiveFailures
}

// A DAG that keeps crashing shows as running most of the time, so the list
// counts the failed runs before the one that has not ended. A success, a stop
// by an operator or a run from another workspace ends the count. The runs that
// have not ended are queued here: a running run without a process would be
// repaired into a failed one.
func TestListServiceDAGsCountsFailuresInARow(t *testing.T) {
	t.Parallel()
	start := time.Now().Add(-time.Hour)
	at := func(i int) time.Time { return start.Add(time.Duration(i) * time.Minute) }
	setup := func(t *testing.T) (test.Helper, *localapi.API) {
		helper := test.Setup(t, test.WithStatusPersistence())
		helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "svc", []byte(serviceDAGSpec))
		return helper, newServiceDAGAPI(helper)
	}

	t.Run("CrashLoop", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		seedRunAt(t, helper, "svc", "ok", ir.Succeeded, ir.TriggerTypeRestart, at(0))
		for i := 1; i <= 3; i++ {
			seedRunAt(t, helper, "svc", fmt.Sprintf("crash-%d", i), ir.Failed, ir.TriggerTypeRestart, at(i))
		}
		seedRunAt(t, helper, "svc", "next", ir.Queued, ir.TriggerTypeRestart, at(4))
		assert.Equal(t, 3, consecutiveFailures(t, apiImpl))
	})

	t.Run("SuccessEndsCount", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		seedRunAt(t, helper, "svc", "crash", ir.Failed, ir.TriggerTypeRestart, at(0))
		seedRunAt(t, helper, "svc", "ok", ir.Succeeded, ir.TriggerTypeRestart, at(1))
		assert.Equal(t, 0, consecutiveFailures(t, apiImpl))
	})

	t.Run("StopEndsCount", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		seedRunAt(t, helper, "svc", "crash-1", ir.Failed, ir.TriggerTypeRestart, at(0))
		seedRunAt(t, helper, "svc", "stopped", ir.Aborted, ir.TriggerTypeRestart, at(1))
		attempt, err := helper.DAGRunRepository.FindAttempt(context.Background(), ir.NewDAGRunRef("svc", "stopped"))
		require.NoError(t, err)
		require.NoError(t, attempt.Abort(context.Background()))
		seedRunAt(t, helper, "svc", "crash-2", ir.Failed, ir.TriggerTypeRestart, at(2))
		assert.Equal(t, 1, consecutiveFailures(t, apiImpl))
	})

	t.Run("OtherWorkspaceEndsCount", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		seedRunAt(t, helper, "svc", "crash-old", ir.Failed, ir.TriggerTypeRestart, at(0), "workspace=dev")
		seedRunAt(t, helper, "svc", "crash-new", ir.Failed, ir.TriggerTypeRestart, at(1))
		assert.Equal(t, 1, consecutiveFailures(t, apiImpl))
	})

	t.Run("Capped", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		for i := range 12 {
			seedRunAt(t, helper, "svc", fmt.Sprintf("crash-%02d", i), ir.Failed, ir.TriggerTypeRestart, at(i))
		}
		assert.Equal(t, 10, consecutiveFailures(t, apiImpl))
		// A run that has not ended takes no place among the ten.
		seedRunAt(t, helper, "svc", "next", ir.Queued, ir.TriggerTypeRestart, at(12))
		assert.Equal(t, 10, consecutiveFailures(t, apiImpl))
	})

	// The count is kept for the latest run, so the history is read again only
	// when the latest run changes.
	t.Run("ReadOncePerLatestRun", func(t *testing.T) {
		t.Parallel()
		helper, apiImpl := setup(t)
		for i := 1; i <= 3; i++ {
			seedRunAt(t, helper, "svc", fmt.Sprintf("crash-%d", i), ir.Failed, ir.TriggerTypeRestart, at(i))
		}
		seedRunAt(t, helper, "svc", "next", ir.Queued, ir.TriggerTypeRestart, at(4))
		require.Equal(t, 3, consecutiveFailures(t, apiImpl))

		seedRunAt(t, helper, "svc", "crash-0", ir.Failed, ir.TriggerTypeRestart, at(0))
		assert.Equal(t, 3, consecutiveFailures(t, apiImpl))

		seedRunAt(t, helper, "svc", "next-2", ir.Queued, ir.TriggerTypeRestart, at(5))
		assert.Equal(t, 4, consecutiveFailures(t, apiImpl))
	})
}
