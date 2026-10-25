// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type activityFixture struct {
	t          *testing.T
	api        *API
	repository *persis.DAGRunRepository
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
	cfg := &config.Config{}
	return &activityFixture{
		t:          t,
		repository: repository,
		api: &API{
			dagRunRepository: repository,
			dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
			config:           cfg,
		},
	}
}

// addRun saves a run that started at start and ran for duration.
func (f *activityFixture) addRun(name, id string, status ir.Status, start time.Time, duration time.Duration, nodes ...*ir.Node) {
	f.t.Helper()
	f.addTriggeredRun(name, id, ir.TriggerTypeManual, status, start, duration, nodes...)
}

// addTriggeredRun saves a run that the trigger started.
func (f *activityFixture) addTriggeredRun(name, id string, trigger ir.TriggerType, status ir.Status, start time.Time, duration time.Duration, nodes ...*ir.Node) {
	f.t.Helper()
	ctx := f.t.Context()
	dag := &ir.DAG{Name: name}
	attempt, err := f.repository.CreateAttempt(ctx, dag, start, id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(ctx))
	runStatus := ir.InitialStatus(dag)
	runStatus.DAGRunID = id
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = status
	runStatus.TriggerType = trigger
	runStatus.StartedAt = start.Format(time.RFC3339)
	if duration > 0 {
		runStatus.FinishedAt = start.Add(duration).Format(time.RFC3339)
	}
	runStatus.Nodes = nodes
	require.NoError(f.t, attempt.Write(ctx, runStatus))
	require.NoError(f.t, attempt.Close(ctx))
}

func (f *activityFixture) activity(from, to time.Time) openapiv1.DAGRunActivity {
	f.t.Helper()
	return f.page(from, to, nil)
}

// page reads one page of a fixed window; a nil cursor reads the first page.
func (f *activityFixture) page(from, to time.Time, cursor *string) openapiv1.DAGRunActivity {
	f.t.Helper()
	fromUnix, toUnix := from.Unix(), to.Unix()
	resp, err := f.api.GetDAGRunActivity(f.t.Context(), openapiv1.GetDAGRunActivityRequestObject{
		Params: openapiv1.GetDAGRunActivityParams{FromDate: &fromUnix, ToDate: &toUnix, Cursor: cursor},
	})
	require.NoError(f.t, err)
	activity, ok := resp.(openapiv1.GetDAGRunActivity200JSONResponse)
	require.True(f.t, ok)
	return openapiv1.DAGRunActivity(activity)
}

// addRuns saves count succeeded runs of name, one a minute from start.
func (f *activityFixture) addRuns(name string, count int, start time.Time) {
	f.t.Helper()
	for i := range count {
		f.addRun(name, fmt.Sprintf("%s-%03d", name, i), ir.Succeeded, start.Add(time.Duration(i)*time.Minute), time.Second)
	}
}

func runIDs(runs []openapiv1.DAGRunActivityRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.DagRunId)
	}
	return ids
}

// Older pages list only runs; the summaries come with the first page.
func TestDAGRunActivityPagesRunsNewestFirst(t *testing.T) {
	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	f.queueRun(queueStore, "nightly", "nightly-1", day.Add(-2*time.Hour))
	f.addRuns("ping", activityPageSize+2, day.Add(time.Hour))
	f.addRun("ping", "outside", ir.Succeeded, day.Add(-time.Hour), time.Second)
	f.addRun("train", "train-1", ir.Running, day.Add(-time.Minute), 0)

	first := f.activity(day, day.Add(24*time.Hour))

	require.Len(t, first.Runs, activityPageSize)
	assert.Equal(t, "ping-101", first.Runs[0].DagRunId)
	assert.Equal(t, "ping", first.Runs[0].Name)
	assert.Equal(t, day.Add(time.Hour+101*time.Minute).Unix(), valueOf(first.Runs[0].StartedAt))
	assert.Equal(t, "ping-002", first.Runs[activityPageSize-1].DagRunId)
	require.NotNil(t, first.NextCursor)
	require.Len(t, first.Queues, 1)

	older := f.page(day, day.Add(24*time.Hour), first.NextCursor)

	assert.Equal(t, []string{"ping-001", "ping-000"}, runIDs(older.Runs))
	assert.Nil(t, older.NextCursor)
	assert.Empty(t, older.Queues)
	assert.Equal(t, first.From, older.From)
	assert.Equal(t, first.To, older.To)
}

// A DAG whose latest run failed needs attention even when newer runs of other
// DAGs push that run off the first page. A DAG that recovered does not.
func TestDAGRunActivityListsFailingLatestRuns(t *testing.T) {
	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	f.addRun("stale", "stale-1", ir.Failed, day.Add(-time.Hour), time.Second)
	f.addRun("report", "report-1", ir.Failed, day.Add(time.Hour), time.Second)
	f.addRun("report", "report-2", ir.Succeeded, day.Add(2*time.Hour), time.Second)
	f.addRun("etl", "etl-1", ir.Failed, day.Add(3*time.Hour), 5*time.Second,
		&ir.Node{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded},
		&ir.Node{Step: ir.Step{Name: "load"}, Status: ir.NodeFailed, Error: "\x1b[31mconnection refused\x1b[0m\nretry later"},
	)
	f.addRun("ping", "ping-1", ir.Aborted, day.Add(4*time.Hour), time.Second)
	f.addRuns("tick", activityPageSize, day.Add(5*time.Hour))

	activity := f.activity(day, day.Add(24*time.Hour))

	assert.NotContains(t, runIDs(activity.Runs), "etl-1")
	assert.Equal(t, []string{"ping-1", "etl-1"}, runIDs(activity.Attention))
	etl := activity.Attention[1]
	assert.Equal(t, "etl", etl.Name)
	assert.Equal(t, "load", valueOf(etl.FailedStep))
	assert.Equal(t, "connection refused", valueOf(etl.Error))
}

// Runs in progress come from the first page and from each DAG's latest run, so
// a waiting run, which has no process, stays listed after newer runs.
func TestDAGRunActivityListsActiveRuns(t *testing.T) {
	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	f.addRun("approve", "approve-1", ir.Waiting, day.Add(time.Hour), 0)
	f.addRuns("tick", activityPageSize, day.Add(2*time.Hour))
	f.addRun("train", "train-1", ir.Running, day.Add(20*time.Hour), 0)

	activity := f.activity(day, day.Add(24*time.Hour))

	assert.Equal(t, []string{"approve-1", "train-1"}, runIDs(activity.Active))
	assert.Equal(t, "approve", activity.Active[0].Name)
}

// aliveProcs reports the given runs as having live processes.
type aliveProcs struct {
	processRepository
	refs []ir.DAGRunRef
}

func (p aliveProcs) ListAllAlive(context.Context) (map[string][]ir.DAGRunRef, error) {
	return map[string][]ir.DAGRunRef{"default": p.refs}, nil
}

// Runs that started before the window appear while their process lives, up
// to a bound on the status files read for them.
func TestDAGRunActivityListsLiveRunsFromBeforeWindow(t *testing.T) {
	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	var refs []ir.DAGRunRef
	for i := range activityLiveRunReads + 1 {
		id := fmt.Sprintf("train-%02d", i)
		f.addRun("train", id, ir.Running, day.Add(-time.Hour).Add(time.Duration(i)*time.Second), 0)
		refs = append(refs, ir.NewDAGRunRef("train", id))
	}
	f.api.procRepository = aliveProcs{refs: refs}

	activity := f.activity(day, day.Add(24*time.Hour))

	require.Len(t, activity.Active, activityLiveRunReads)
	assert.Equal(t, "train-00", activity.Active[0].DagRunId)
	assert.Equal(t, "train", activity.Active[0].Name)
	assert.Empty(t, activity.Runs)
}

func TestDAGRunActivityRejectsInvalidWindows(t *testing.T) {
	f := newActivityFixture(t)
	now := time.Now()
	tests := []struct {
		name   string
		params openapiv1.GetDAGRunActivityParams
	}{
		{name: "EndsBeforeStart", params: openapiv1.GetDAGRunActivityParams{
			FromDate: new(now.Unix()), ToDate: new(now.Add(-time.Hour).Unix()),
		}},
		{name: "LongerThanAWeek", params: openapiv1.GetDAGRunActivityParams{
			FromDate: new(now.Add(-8 * 24 * time.Hour).Unix()), ToDate: new(now.Unix()),
		}},
		{name: "TooManyHours", params: openapiv1.GetDAGRunActivityParams{Hours: new(49)}},
		{name: "NoHours", params: openapiv1.GetDAGRunActivityParams{Hours: new(0)}},
		{name: "UnknownCursor", params: openapiv1.GetDAGRunActivityParams{Cursor: new("bogus")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.api.GetDAGRunActivity(t.Context(), openapiv1.GetDAGRunActivityRequestObject{Params: tt.params})

			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		})
	}
}

// A live window ends at the next 96th of its length, so it stays the same,
// and so does its payload, until that step passes.
func TestDAGRunActivityAlignsLiveWindow(t *testing.T) {
	f := newActivityFixture(t)
	now := time.Date(2026, 3, 2, 10, 7, 30, 0, time.UTC)

	query, err := f.api.newActivityQuery(t.Context(), activityParams{}, now)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 2, 10, 15, 0, 0, time.UTC), query.to.UTC())
	assert.Equal(t, query.to.Add(-24*time.Hour), query.from)
}

func TestDAGRunActivityStreamTakesOnlyLiveWindows(t *testing.T) {
	f := newActivityFixture(t)

	require.NoError(t, f.api.ValidateDAGRunActivityQuery(t.Context(), "hours=6"))

	for _, query := range []string{"fromDate=1", "cursor=abc"} {
		err := f.api.ValidateDAGRunActivityQuery(t.Context(), query)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr, query)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, query)
	}
}

// queueRun saves a queued run of name and puts it in the DAG's own queue. A
// zero queuedAt saves the run without a queue time.
func (f *activityFixture) queueRun(queueStore queue.QueueStore, name, id string, queuedAt time.Time, labels ...string) {
	f.t.Helper()
	f.queueTriggeredRun(queueStore, name, id, ir.TriggerTypeUnknown, queuedAt, labels...)
}

// queueTriggeredRun is queueRun for a run that the trigger started.
func (f *activityFixture) queueTriggeredRun(queueStore queue.QueueStore, name, id string, trigger ir.TriggerType, queuedAt time.Time, labels ...string) {
	f.t.Helper()
	ctx := f.t.Context()
	dag := &ir.DAG{Name: name}
	createdAt := queuedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	attempt, err := f.repository.CreateAttempt(ctx, dag, createdAt, id, persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(ctx))
	runStatus := ir.InitialStatus(dag)
	runStatus.DAGRunID = id
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = ir.Queued
	runStatus.ProcGroup = name
	runStatus.TriggerType = trigger
	runStatus.Labels = labels
	if !queuedAt.IsZero() {
		runStatus.QueuedAt = queuedAt.UTC().Format(time.RFC3339)
	}
	require.NoError(f.t, attempt.Write(ctx, runStatus))
	require.NoError(f.t, attempt.Close(ctx))
	require.NoError(f.t, queueStore.Enqueue(ctx, name, queue.QueuePriorityLow, ir.NewDAGRunRef(name, id)))
}

// Queues with work are listed on the first page, the one whose next run has
// waited longest first.
func TestDAGRunActivityListsQueuesLongestWaitFirst(t *testing.T) {
	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	f.queueRun(queueStore, "report", "report-1", now.Add(-2*time.Minute))
	f.queueRun(queueStore, "etl", "etl-1", now.Add(-10*time.Minute))
	f.queueRun(queueStore, "etl", "etl-2", now.Add(-time.Minute))

	first := f.activity(now.Add(-time.Hour), now.Add(time.Hour))

	require.Len(t, first.Queues, 2)
	etl := first.Queues[0]
	assert.Equal(t, "etl", etl.Name)
	assert.Equal(t, 2, etl.Queued)
	assert.Equal(t, 0, etl.Running)
	assert.Equal(t, 1, valueOf(etl.MaxConcurrency))
	assert.Equal(t, now.Add(-10*time.Minute).Unix(), valueOf(etl.NextQueuedAt))
	assert.Equal(t, "report", first.Queues[1].Name)

	// Runs in progress name their queue, so a client can tell which ones a
	// queue line already covers.
	require.NotEmpty(t, first.Active)
	for _, run := range first.Active {
		assert.Equal(t, run.Name, valueOf(run.Queue))
	}
}

// A run in line without a queue time does not hide the wait of the runs
// behind it.
func TestDAGRunActivityQueueWaitSkipsRunsWithoutQueueTime(t *testing.T) {
	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	f.queueRun(queueStore, "etl", "etl-1", time.Time{})
	f.queueRun(queueStore, "etl", "etl-2", now.Add(-5*time.Minute))

	activity := f.activity(now.Add(-time.Hour), now.Add(time.Hour))

	require.Len(t, activity.Queues, 1)
	assert.Equal(t, 2, activity.Queues[0].Queued)
	assert.Equal(t, now.Add(-5*time.Minute).Unix(), valueOf(activity.Queues[0].NextQueuedAt))
}

// Queue lines count only the runs the Overview's filters select.
func TestDAGRunActivityQueuesFollowTheFilters(t *testing.T) {
	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	f.queueRun(queueStore, "etl", "etl-1", now.Add(-time.Minute), "team=ops")
	f.queueRun(queueStore, "report", "report-1", now.Add(-time.Minute))

	from, to := now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()
	resp, err := f.api.GetDAGRunActivity(t.Context(), openapiv1.GetDAGRunActivityRequestObject{
		Params: openapiv1.GetDAGRunActivityParams{FromDate: &from, ToDate: &to, Labels: new("team=ops")},
	})
	require.NoError(t, err)
	activity, ok := resp.(openapiv1.GetDAGRunActivity200JSONResponse)
	require.True(t, ok)

	require.Len(t, activity.Queues, 1)
	assert.Equal(t, "etl", activity.Queues[0].Name)
}

// setConditions replaces the runtime conditions a saved run records.
func (f *activityFixture) setConditions(name, id string, conditions ...ir.DAGRunCondition) {
	f.t.Helper()
	ctx := f.t.Context()
	attempt, err := f.repository.FindAttempt(ctx, ir.NewDAGRunRef(name, id))
	require.NoError(f.t, err)
	runStatus, err := attempt.ReadStatus(ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(ctx))
	runStatus.Conditions = conditions
	require.NoError(f.t, attempt.Write(ctx, *runStatus))
	require.NoError(f.t, attempt.Close(ctx))
}

func notRunnable(status openapiv1.DAGRunConditionStatus, reason string) ir.DAGRunCondition {
	return ir.NewDAGRunCondition(ir.DAGRunConditionRunnable, string(status), reason, "", time.Now())
}

// A queue says why its runs cannot start when a run in line records that no
// worker can take it, even behind a run that only waits for room.
func TestDAGRunActivityReportsQueuesBlockedByWorkers(t *testing.T) {
	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	waitsForRoom := notRunnable(openapiv1.DAGRunConditionStatusFalse, "MaxConcurrencyReached")
	f.queueRun(queueStore, "etl", "etl-1", now.Add(-3*time.Minute))
	f.setConditions("etl", "etl-1", waitsForRoom)
	f.queueRun(queueStore, "etl", "etl-2", now.Add(-2*time.Minute))
	f.setConditions("etl", "etl-2", notRunnable(openapiv1.DAGRunConditionStatusFalse, ir.DAGRunReasonNoMatchingWorker))
	f.queueRun(queueStore, "report", "report-1", now.Add(-time.Minute))
	f.setConditions("report", "report-1", notRunnable(openapiv1.DAGRunConditionStatusUnknown, ir.DAGRunReasonWorkerDispatchUnavailable))
	f.queueRun(queueStore, "sync", "sync-1", now.Add(-time.Minute))
	f.setConditions("sync", "sync-1", waitsForRoom)

	activity := f.activity(now.Add(-time.Hour), now.Add(time.Hour))

	require.Len(t, activity.Queues, 3)
	blocked := map[string]openapiv1.DAGRunActivityQueueBlockedReason{}
	for _, queue := range activity.Queues {
		if queue.BlockedReason != nil {
			blocked[queue.Name] = *queue.BlockedReason
		}
	}
	assert.Equal(t, map[string]openapiv1.DAGRunActivityQueueBlockedReason{
		"etl":    openapiv1.DAGRunActivityQueueBlockedReasonNoMatchingWorker,
		"report": openapiv1.DAGRunActivityQueueBlockedReasonWorkerDispatchUnavailable,
	}, blocked)
}

// Once the scan budget runs out, later queues are not listed as if they
// held no work.
func TestDAGRunActivityLeavesOutQueuesItCouldNotRead(t *testing.T) {
	// Not parallel: this test lowers the package-level scan bounds.
	original := activityQueueScan
	activityQueueScan = queueScan{total: 3, perQueue: 3}
	t.Cleanup(func() { activityQueueScan = original })

	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	for i := range 4 {
		f.queueRun(queueStore, "a-busy", fmt.Sprintf("busy-%d", i), now.Add(-time.Duration(10-i)*time.Minute))
	}
	f.queueRun(queueStore, "b-unread", "unread-1", now.Add(-time.Minute))

	activity := f.activity(now.Add(-time.Hour), now.Add(time.Hour))

	require.Len(t, activity.Queues, 1)
	busy := activity.Queues[0]
	assert.Equal(t, "a-busy", busy.Name)
	assert.Equal(t, 3, busy.Queued)
	assert.True(t, valueOf(busy.QueuedCapped))
}

// Queue lines read a bounded number of running runs, local and distributed.
// Past the bound, a queue's running count is a lower bound.
func TestDAGRunActivityBoundsRunningReads(t *testing.T) {
	// Not parallel: this test lowers the package-level scan bounds.
	original := activityQueueScan
	activityQueueScan = queueScan{total: 200, perQueue: 5, running: 3}
	t.Cleanup(func() { activityQueueScan = original })

	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	var refs []ir.DAGRunRef
	for i := range 2 {
		id := fmt.Sprintf("train-%d", i)
		f.addRun("train", id, ir.Running, day.Add(-time.Hour), 0)
		refs = append(refs, ir.NewDAGRunRef("train", id))
	}
	f.api.procRepository = aliveProcs{refs: refs}
	leaseStore := newTestDAGRunLeaseStore(t.TempDir())
	f.api.dagRunLeaseStore = leaseStore
	for i := range 2 {
		createDistributedQueueRun(t, t.Context(), f.repository, leaseStore, "train", fmt.Sprintf("remote-%d", i), "default", time.Now())
	}

	activity := f.activity(day, day.Add(24*time.Hour))

	require.Len(t, activity.Queues, 1)
	assert.Equal(t, 3, activity.Queues[0].Running)
	assert.True(t, valueOf(activity.Queues[0].RunningCapped))
}

// countingQueueStore counts the queued records its cursor pages load.
type countingQueueStore struct {
	queue.QueueStore
	loaded int
}

func (s *countingQueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (pagination.CursorResult[queue.QueuedItemData], error) {
	page, err := s.QueueStore.ListCursor(ctx, name, cursor, limit)
	s.loaded += len(page.Items)
	return page, err
}

// Counting backlogs loads only the queued records the scan may count.
func TestDAGRunActivityLoadsOnlyTheQueuedRecordsItCounts(t *testing.T) {
	// Not parallel: this test lowers the package-level scan bounds.
	original := activityQueueScan
	activityQueueScan = queueScan{total: 7, perQueue: 5}
	t.Cleanup(func() { activityQueueScan = original })

	f := newActivityFixture(t)
	queueStore := &countingQueueStore{QueueStore: store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))}
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	for i := range 10 {
		f.queueRun(queueStore, "a", fmt.Sprintf("a-%d", i), now.Add(-time.Duration(20-i)*time.Minute))
		f.queueRun(queueStore, "b", fmt.Sprintf("b-%d", i), now.Add(-time.Duration(20-i)*time.Minute))
	}
	queueStore.loaded = 0

	activity := f.activity(now.Add(-time.Hour), now.Add(time.Hour))

	assert.Equal(t, 7, queueStore.loaded)
	require.Len(t, activity.Queues, 2)
	assert.Equal(t, 5, activity.Queues[0].Queued)
	assert.Equal(t, 2, activity.Queues[1].Queued)
}

// diskSample reports a fixed disk usage.
type diskSample float64

func (diskSample) GetHistory(time.Duration) *resource.ResourceHistory {
	return &resource.ResourceHistory{}
}

func (d diskSample) DiskUsedPercent() (float64, bool) {
	return float64(d), true
}

// The first page warns of a nearly full data disk, and only to callers who
// may view system status.
func TestDAGRunActivityWarnsOfANearlyFullDisk(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	from, to := now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()
	read := func(f *activityFixture, ctx context.Context) *int {
		resp, err := f.api.GetDAGRunActivity(ctx, openapiv1.GetDAGRunActivityRequestObject{
			Params: openapiv1.GetDAGRunActivityParams{FromDate: &from, ToDate: &to},
		})
		require.NoError(t, err)
		activity, ok := resp.(openapiv1.GetDAGRunActivity200JSONResponse)
		require.True(t, ok)
		return activity.DiskUsedPercent
	}

	f := newActivityFixture(t)
	f.api.resourceService = diskSample(93.7)
	assert.Equal(t, new(93), read(f, t.Context()))

	f.api.resourceService = diskSample(89.9)
	assert.Nil(t, read(f, t.Context()), "room left")

	f.api.resourceService = diskSample(93.7)
	f.api.authService = struct{ AuthService }{}
	viewer := auth.WithUser(t.Context(), &auth.User{Role: auth.RoleViewer, WorkspaceAccess: auth.AllWorkspaceAccess()})
	assert.Nil(t, read(f, viewer), "a viewer cannot view system status")
}

// An always-on DAG restarts without end, so on request its runs stay out of the
// timeline, the failing DAGs and the runs in progress.
func TestDAGRunActivityLeavesOutAlwaysOnRunsOnRequest(t *testing.T) {
	f := newActivityFixture(t)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	f.addRun("job", "job-1", ir.Succeeded, day.Add(time.Hour), time.Second)
	f.addTriggeredRun("svc-a", "svc-a-1", ir.TriggerTypeRestart, ir.Failed, day.Add(2*time.Hour), time.Second)
	f.addTriggeredRun("svc-b", "svc-b-1", ir.TriggerTypeRestart, ir.Running, day.Add(3*time.Hour), 0)

	read := func(exclude bool) openapiv1.DAGRunActivity {
		from, to := day.Unix(), day.Add(24*time.Hour).Unix()
		resp, err := f.api.GetDAGRunActivity(t.Context(), openapiv1.GetDAGRunActivityRequestObject{
			Params: openapiv1.GetDAGRunActivityParams{FromDate: &from, ToDate: &to, ExcludeAlwaysOnRuns: &exclude},
		})
		require.NoError(t, err)
		activity, ok := resp.(openapiv1.GetDAGRunActivity200JSONResponse)
		require.True(t, ok)
		return openapiv1.DAGRunActivity(activity)
	}

	all := read(false)
	assert.ElementsMatch(t, []string{"job-1", "svc-a-1", "svc-b-1"}, runIDs(all.Runs))
	assert.Equal(t, []string{"svc-a-1"}, runIDs(all.Attention))
	assert.Equal(t, []string{"svc-b-1"}, runIDs(all.Active))

	without := read(true)
	assert.Equal(t, []string{"job-1"}, runIDs(without.Runs))
	assert.Empty(t, without.Attention)
	assert.Empty(t, without.Active)

	// The live stream takes the same switch.
	data, err := f.api.GetDAGRunActivityData(t.Context(), "excludeAlwaysOnRuns=true")
	require.NoError(t, err)
	assert.IsType(t, openapiv1.DAGRunActivity{}, data)
	_, err = f.api.GetDAGRunActivityData(t.Context(), "excludeAlwaysOnRuns=maybe")
	require.Error(t, err)
}

// The queue of an always-on DAG flips between a run and a wait, so on request
// the activity leaves it out of the queues that it lists.
func TestDAGRunActivityLeavesOutTheQueuesOfAlwaysOnDAGsOnRequest(t *testing.T) {
	f := newActivityFixture(t)
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(t.TempDir(), "queue")))
	f.api.queueStore = queueStore
	now := time.Now().Truncate(time.Second)
	f.queueRun(queueStore, "etl", "etl-1", now.Add(-time.Minute))
	f.queueTriggeredRun(queueStore, "svc", "svc-1", ir.TriggerTypeRestart, now.Add(-10*time.Second))

	read := func(exclude bool) []string {
		from, to := now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()
		resp, err := f.api.GetDAGRunActivity(t.Context(), openapiv1.GetDAGRunActivityRequestObject{
			Params: openapiv1.GetDAGRunActivityParams{FromDate: &from, ToDate: &to, ExcludeAlwaysOnRuns: &exclude},
		})
		require.NoError(t, err)
		activity, ok := resp.(openapiv1.GetDAGRunActivity200JSONResponse)
		require.True(t, ok)
		names := make([]string, 0, len(activity.Queues))
		for _, queue := range activity.Queues {
			names = append(names, queue.Name)
		}
		return names
	}

	assert.ElementsMatch(t, []string{"etl", "svc"}, read(false))
	assert.Equal(t, []string{"etl"}, read(true))
}
