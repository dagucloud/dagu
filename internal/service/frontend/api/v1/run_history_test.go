// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/samples"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	serviceSpec = "restart_policy: always\nrestart_wait_sec: 1\nsteps:\n  - run: echo hi\n"
	plainSpec   = "steps:\n  - run: echo hi\n"
)

// newRunHistoryAPI makes an API with a service DAG "svc" and a plain DAG
// "plain". The fake reader answers every run history query.
func newRunHistoryAPI(t *testing.T, fake *fakeSamplesReader) *API {
	t.Helper()
	ctx := t.Context()
	dags := testutil.NewFileDAGRepository(t.TempDir(), filedag.WithSkipExamples(true))
	require.NoError(t, dags.Create(ctx, "svc", []byte(serviceSpec)))
	require.NoError(t, dags.Create(ctx, "plain", []byte(plainSpec)))
	return &API{
		dagRepository:    dags,
		dagRunRepository: testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{}),
		config:           &config.Config{},
		samplesReader: func(dir string) samples.Reader {
			fake.dir = dir
			return fake
		},
	}
}

func historyDir(t *testing.T, a *API, dagName string) string {
	t.Helper()
	dir, ok := a.dagRunRepository.RunHistoryDir(dagName)
	require.True(t, ok)
	return dir
}

func TestRunHistoryServiceDAGWithNoHistory(t *testing.T) {
	fake := &fakeSamplesReader{}
	a := newRunHistoryAPI(t, fake)
	ctx := t.Context()

	resp, err := a.GetDAGRunHistorySamples(ctx, openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse)
	require.True(t, ok, "got %T", resp)
	assert.NotNil(t, got.Buckets)
	assert.Empty(t, got.Buckets)
	assert.Equal(t, historyDir(t, a, "svc"), fake.dir)

	ev, err := a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{FileName: "svc"})
	require.NoError(t, err)
	events, ok := ev.(openapiv1.GetDAGRunHistoryEvents200JSONResponse)
	require.True(t, ok, "got %T", ev)
	assert.NotNil(t, events.Events)
	assert.Empty(t, events.Events)

	av, err := a.GetDAGRunHistoryAvailability(ctx, openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "svc"})
	require.NoError(t, err)
	avail, ok := av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse)
	require.True(t, ok, "got %T", av)
	assert.Zero(t, avail.TotalCount)
	require.Len(t, avail.Windows, 4)
}

func TestRunHistoryNotFound(t *testing.T) {
	a := newRunHistoryAPI(t, &fakeSamplesReader{})
	ctx := t.Context()

	// A DAG that is not a service DAG and has no history, or an empty history.
	for _, prepare := range []func(){
		func() {},
		func() { require.NoError(t, os.MkdirAll(historyDir(t, a, "plain"), 0o750)) },
	} {
		prepare()
		resp, err := a.GetDAGRunHistorySamples(ctx, openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "plain"})
		require.NoError(t, err)
		nf, ok := resp.(openapiv1.GetDAGRunHistorySamples404JSONResponse)
		require.True(t, ok, "got %T", resp)
		assert.Equal(t, "DAG plain has no run history", nf.Message)

		ev, err := a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{FileName: "plain"})
		require.NoError(t, err)
		nfe, ok := ev.(openapiv1.GetDAGRunHistoryEvents404JSONResponse)
		require.True(t, ok, "got %T", ev)
		assert.Equal(t, "DAG plain has no run history", nfe.Message)

		av, err := a.GetDAGRunHistoryAvailability(ctx, openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "plain"})
		require.NoError(t, err)
		nfa, ok := av.(openapiv1.GetDAGRunHistoryAvailability404JSONResponse)
		require.True(t, ok, "got %T", av)
		assert.Equal(t, "DAG plain has no run history", nfa.Message)
	}

	resp, err := a.GetDAGRunHistorySamples(ctx, openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "missing"})
	require.NoError(t, err)
	_, ok := resp.(openapiv1.GetDAGRunHistorySamples404JSONResponse)
	assert.True(t, ok, "got %T", resp)
}

// A DAG that is no longer a service DAG keeps its history and the API gives it.
// The scheduler no longer records its runs, so its run in progress adds no up
// time.
func TestRunHistoryOfFormerServiceDAG(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	// The history starts 3 hours ago and has no up time in the window.
	recorded := samples.Uptime{Start: now.Add(-24 * time.Hour), To: now, Since: now.Add(-3 * time.Hour)}
	fake := &fakeSamplesReader{uptime: recorded, availability: samples.Availability{
		Windows: []samples.WindowStat{{Window: samples.Window24h, Uptime: recorded}},
	}}
	a := newRunHistoryAPI(t, fake)
	dir := historyDir(t, a, "plain")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "raw.log"), []byte("x"), 0o600))
	fixture := &activityFixture{t: t, repository: a.dagRunRepository, api: a}
	a.dagRunMgr = runtimepkg.NewManager(a.dagRunRepository, nil, a.config)
	fixture.addRun("plain", "run-1", ir.Running, now.Add(-90*time.Minute), 0)

	resp, err := a.GetDAGRunHistorySamples(t.Context(), openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "plain"})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse)
	require.True(t, ok, "got %T", resp)
	assert.Equal(t, dir, fake.dir)
	require.NotNil(t, got.Uptime)
	assert.Zero(t, *got.Uptime)
	assert.Empty(t, got.Buckets)

	av, err := a.GetDAGRunHistoryAvailability(t.Context(), openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "plain"})
	require.NoError(t, err)
	window := av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse).Windows[0]
	require.NotNil(t, window.Uptime)
	assert.Zero(t, *window.Uptime)
}

func TestRunHistoryQueryRanges(t *testing.T) {
	fake := &fakeSamplesReader{}
	a := newRunHistoryAPI(t, fake)
	ctx := t.Context()

	_, err := a.GetDAGRunHistorySamples(ctx, openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
	require.NoError(t, err)
	assert.Equal(t, 600, fake.sampleQuery.MaxPoints)
	assert.Equal(t, 24*time.Hour, fake.sampleQuery.To.Sub(fake.sampleQuery.From))
	_, err = a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{FileName: "svc"})
	require.NoError(t, err)
	assert.Equal(t, 100, fake.eventQuery.Limit)

	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Hour)
	for name, params := range map[string]openapiv1.GetDAGRunHistorySamplesParams{
		"points low":  {Points: ptrOf(9)},
		"points high": {Points: ptrOf(2001)},
		"from > to":   {From: &from, To: &to},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.GetDAGRunHistorySamples(ctx, openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc", Params: params})
			requireBadRequest(t, err)
		})
	}
	for _, n := range []int{0, 501} {
		_, err := a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{
			FileName: "svc", Params: openapiv1.GetDAGRunHistoryEventsParams{Limit: &n},
		})
		requireBadRequest(t, err)
	}
	_, err = a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{
		FileName: "svc", Params: openapiv1.GetDAGRunHistoryEventsParams{From: &from, To: &to},
	})
	requireBadRequest(t, err)

	fake.eventsErr = fmt.Errorf("decode: %w", samples.ErrInvalidCursor)
	_, err = a.GetDAGRunHistoryEvents(ctx, openapiv1.GetDAGRunHistoryEventsRequestObject{
		FileName: "svc", Params: openapiv1.GetDAGRunHistoryEventsParams{Before: ptrOf("x")},
	})
	requireBadRequest(t, err)
}

func TestRunHistoryEventsMapping(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeSamplesReader{page: samples.EventPage{
		NextCursor: "c2",
		Events: []samples.Event{
			{Time: t0.Add(time.Hour), Kind: samples.EventGap, RunID: "r4", Gap: 20 * time.Minute},
			{Time: t0.Add(3 * time.Minute), Kind: samples.EventRecovered, RunID: "r3", FailedCount: 120, Outage: 2 * time.Minute},
			{Time: t0.Add(time.Minute), Kind: samples.EventFailed, RunID: "r2", FailedCount: 3600},
			{Time: t0, Kind: samples.EventFailed, RunID: "r1", Message: "boom", ExitCode: 1},
			// A store that has a lifecycle event does not show it.
			{Time: t0.Add(-time.Hour), Kind: samples.EventStarted},
			{Time: t0.Add(-2 * time.Hour), Kind: samples.EventStopped},
		},
	}}
	a := newRunHistoryAPI(t, fake)

	resp, err := a.GetDAGRunHistoryEvents(t.Context(), openapiv1.GetDAGRunHistoryEventsRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunHistoryEvents200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, got.NextCursor)
	assert.Equal(t, "c2", *got.NextCursor)
	require.Len(t, got.Events, 4)

	gap, rec, repeated, failed := got.Events[0], got.Events[1], got.Events[2], got.Events[3]
	assert.Equal(t, openapiv1.RunHistoryEventKind("gap"), gap.Kind)
	assert.Equal(t, "r4", gap.RunId)
	require.NotNil(t, gap.GapSeconds)
	assert.Equal(t, int64(1200), *gap.GapSeconds)
	assert.Nil(t, gap.OutageSeconds)
	assert.Nil(t, gap.Message)

	assert.Equal(t, openapiv1.RunHistoryEventKind("recovered"), rec.Kind)
	assert.Equal(t, "r3", rec.RunId)
	require.NotNil(t, rec.OutageSeconds)
	assert.Equal(t, int64(120), *rec.OutageSeconds)
	require.NotNil(t, rec.FailedCount)
	assert.Equal(t, int64(120), *rec.FailedCount)
	assert.Nil(t, rec.GapSeconds)

	assert.Equal(t, openapiv1.RunHistoryEventKind("failed"), repeated.Kind)
	require.NotNil(t, repeated.FailedCount)
	assert.Equal(t, int64(3600), *repeated.FailedCount)
	assert.Nil(t, repeated.Message)

	assert.Equal(t, "r1", failed.RunId)
	require.NotNil(t, failed.Message)
	assert.Equal(t, "boom", *failed.Message)
	assert.Nil(t, failed.FailedCount)
	assert.Nil(t, failed.OutageSeconds)
}

func TestRunHistorySamplesAndAvailabilityMapping(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeSamplesReader{
		series: samples.Series{
			Tier: samples.TierMinute, From: t0, To: t0.Add(time.Hour),
			Buckets: []samples.Bucket{{Time: t0, Count: 20, Failures: 2, Min: time.Second, Max: 3 * time.Second, P50: 2 * time.Second, P95: 3 * time.Second}},
		},
		availability: samples.Availability{
			Windows: []samples.WindowStat{{Window: samples.Window24h, Count: 10, Failures: 1}},
			Streak:  4, TotalCount: 77,
		},
	}
	a := newRunHistoryAPI(t, fake)

	resp, err := a.GetDAGRunHistorySamples(t.Context(), openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 60, got.ResolutionSeconds)
	require.Len(t, got.Buckets, 1)
	assert.Equal(t, int64(20), got.Buckets[0].Count)
	assert.Equal(t, int64(2), got.Buckets[0].Failures)
	assert.Equal(t, int64(3000), got.Buckets[0].MaxMs)

	av, err := a.GetDAGRunHistoryAvailability(t.Context(), openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "svc"})
	require.NoError(t, err)
	avail, ok := av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, int64(77), avail.TotalCount)
	assert.Equal(t, int64(4), avail.Streak)
	require.NotNil(t, avail.Windows[0].Availability)
	assert.InDelta(t, 0.9, *avail.Windows[0].Availability, 1e-9)
}

// The availability gives the uptime of a window next to the success rate, and
// leaves it out when the run history has no record of the window.
func TestRunHistoryAvailabilityUptime(t *testing.T) {
	now := time.Now().UTC()
	known := samples.Uptime{
		Start: now.Add(-4 * time.Hour), To: now, Up: 3 * time.Hour,
		Since: now.Add(-5 * time.Hour),
	}
	fake := &fakeSamplesReader{newestEnd: now.Add(-time.Hour), availability: samples.Availability{
		Windows: []samples.WindowStat{
			{Window: samples.Window24h, Count: 10, Failures: 5, Uptime: known},
			{Window: samples.Window7d, Count: 10, Failures: 5},
		},
	}}
	a := newRunHistoryAPI(t, fake)
	av, err := a.GetDAGRunHistoryAvailability(t.Context(), openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "svc"})
	require.NoError(t, err)
	avail := av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse)
	require.NotNil(t, avail.Windows[0].Uptime)
	assert.InDelta(t, 0.75, *avail.Windows[0].Uptime, 1e-9)
	require.NotNil(t, avail.Windows[0].Availability)
	assert.InDelta(t, 0.5, *avail.Windows[0].Availability, 1e-9)
	assert.Nil(t, avail.Windows[1].Uptime)
	assert.Nil(t, avail.Windows[3].Uptime)
}

// The run in progress counts in the uptime, from the latest status.
func TestRunHistoryAvailabilityActiveRun(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakeSamplesReader{availability: samples.Availability{
		Windows: []samples.WindowStat{{
			Window: samples.Window24h, Count: 1,
			// The history starts 3 hours ago and has no up time in the window.
			Uptime: samples.Uptime{Start: now.Add(-24 * time.Hour), To: now, Since: now.Add(-3 * time.Hour)},
		}},
	}}
	a := newRunHistoryAPI(t, fake)
	fixture := &activityFixture{t: t, repository: a.dagRunRepository, api: a}
	a.dagRunMgr = runtimepkg.NewManager(a.dagRunRepository, nil, a.config)
	fixture.addRun("svc", "run-1", ir.Running, now.Add(-90*time.Minute), 0)

	av, err := a.GetDAGRunHistoryAvailability(t.Context(), openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got := av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse).Windows[0]
	require.NotNil(t, got.Uptime)
	// 90 minutes of the 3 hours that the history covers.
	assert.InDelta(t, 0.5, *got.Uptime, 0.01)

	// A recorded run that ended 60 minutes ago overlaps the run in progress.
	// The history holds the time up to that end, so only the 60 minutes after
	// it are added to the 30 recorded minutes.
	fake.newestEnd = now.Add(-time.Hour)
	fake.availability.Windows[0].Uptime = samples.Uptime{
		Start: now.Add(-24 * time.Hour), To: now, Up: 30 * time.Minute, Since: now.Add(-3 * time.Hour),
	}
	av, err = a.GetDAGRunHistoryAvailability(t.Context(), openapiv1.GetDAGRunHistoryAvailabilityRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got = av.(openapiv1.GetDAGRunHistoryAvailability200JSONResponse).Windows[0]
	require.NotNil(t, got.Uptime)
	assert.InDelta(t, 0.5, *got.Uptime, 0.01)
}

func TestActiveRunStart(t *testing.T) {
	started := "2026-03-01T12:00:00Z"
	for status, want := range map[ir.Status]bool{
		ir.Running: true, ir.Waiting: true, ir.Queued: false, ir.Succeeded: false, ir.Failed: false, ir.NotStarted: false,
	} {
		got := activeRunStart(ir.DAGRunStatus{Status: status, StartedAt: started})
		assert.Equal(t, want, !got.IsZero(), status.String())
	}
	assert.True(t, activeRunStart(ir.DAGRunStatus{Status: ir.Running}).IsZero())
}

// The samples of the run history give the uptime of their window.
func TestRunHistorySamplesUptime(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeSamplesReader{newestEnd: now, uptime: samples.Uptime{
		Start: now.Add(-time.Hour), To: now, Up: 45 * time.Minute, Since: now.Add(-2 * time.Hour),
	}}
	a := newRunHistoryAPI(t, fake)
	resp, err := a.GetDAGRunHistorySamples(t.Context(), openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
	require.NoError(t, err)
	got := resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse)
	require.NotNil(t, got.Uptime)
	assert.InDelta(t, 0.75, *got.Uptime, 1e-9)

	fake.uptime = samples.Uptime{}
	resp, err = a.GetDAGRunHistorySamples(t.Context(), openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
	require.NoError(t, err)
	assert.Nil(t, resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse).Uptime)
}

// The run in progress gives its up time to the buckets from the end of the
// newest recorded run, which the history already counts. A history with no
// recorded uptime counts the run from its start, as its uptime does.
func TestRunHistorySamplesActiveRun(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakeSamplesReader{
		newestEnd: now.Add(-30 * time.Minute),
		series:    samples.Series{Tier: samples.TierHour, From: now.Add(-24 * time.Hour), To: now},
	}
	a := newRunHistoryAPI(t, fake)
	fixture := &activityFixture{t: t, repository: a.dagRunRepository, api: a}
	a.dagRunMgr = runtimepkg.NewManager(a.dagRunRepository, nil, a.config)
	fixture.addRun("svc", "run-1", ir.Running, now.Add(-90*time.Minute), 0)

	upMinutes := func() float64 {
		resp, err := a.GetDAGRunHistorySamples(t.Context(), openapiv1.GetDAGRunHistorySamplesRequestObject{FileName: "svc"})
		require.NoError(t, err)
		var up int64
		for _, b := range resp.(openapiv1.GetDAGRunHistorySamples200JSONResponse).Buckets {
			require.NotNil(t, b.UpMs)
			up += *b.UpMs
		}
		return (time.Duration(up) * time.Millisecond).Minutes()
	}

	fake.uptime = samples.Uptime{Start: now.Add(-24 * time.Hour), To: now, Since: now.Add(-3 * time.Hour)}
	assert.InDelta(t, 30, upMinutes(), 1)

	fake.uptime = samples.Uptime{Start: now.Add(-24 * time.Hour), To: now}
	assert.InDelta(t, 90, upMinutes(), 1)
}
