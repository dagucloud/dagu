// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/samples"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSamplesReader answers from fixed values and records the queries.
type fakeSamplesReader struct {
	series       samples.Series
	page         samples.EventPage
	availability samples.Availability

	sampleQuery samples.SampleQuery
	eventQuery  samples.EventQuery
	dir         string
	eventsErr   error // returned by Events when set
}

func (f *fakeSamplesReader) Samples(_ context.Context, q samples.SampleQuery) (samples.Series, error) {
	f.sampleQuery = q
	return f.series, nil
}

func (f *fakeSamplesReader) Events(_ context.Context, q samples.EventQuery) (samples.EventPage, error) {
	f.eventQuery = q
	return f.page, f.eventsErr
}

func (f *fakeSamplesReader) Availability(_ context.Context, _ time.Time) (samples.Availability, error) {
	return f.availability, nil
}

func continuousNode(name string) *ir.Node {
	return &ir.Node{
		Step:   ir.Step{Name: name, RepeatPolicy: ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Interval: time.Second}},
		Status: ir.NodeRunning,
	}
}

func newSamplesAPI(t *testing.T, fake *fakeSamplesReader, labels []string, nodes ...*ir.Node) (*API, string) {
	t.Helper()
	repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
	ctx := t.Context()
	dag := &ir.DAG{Name: "demo", Labels: ir.NewLabels(labels)}
	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().UTC(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = "run-1"
	status.AttemptID = attempt.ID()
	status.Status = ir.Running
	status.Nodes = nodes
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	cfg := &config.Config{}
	a := &API{
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
		config:           cfg,
		samplesReader: func(dir string) samples.Reader {
			fake.dir = dir
			return fake
		},
	}
	return a, attempt.(interface{ Dir() string }).Dir()
}

func getSamples(t *testing.T, a *API, step string, params openapiv1.GetDAGRunStepSamplesParams) openapiv1.GetDAGRunStepSamplesResponseObject {
	t.Helper()
	resp, err := a.GetDAGRunStepSamples(t.Context(), openapiv1.GetDAGRunStepSamplesRequestObject{
		Name: "demo", DagRunId: "latest", StepName: step, Params: params,
	})
	require.NoError(t, err)
	return resp
}

func requireBadRequest(t *testing.T, err error) {
	t.Helper()
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	assert.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
}

func TestSamplesNotContinuous(t *testing.T) {
	limited := continuousNode("limited")
	limited.Step.RepeatPolicy.Limit = 5
	plain := &ir.Node{Step: ir.Step{Name: "plain"}}
	a, _ := newSamplesAPI(t, &fakeSamplesReader{}, nil, limited, plain, continuousNode("beat"))
	ctx := t.Context()

	for _, step := range []string{"limited", "plain"} {
		want := "step " + step + " is not a continuous step"
		resp, err := a.GetDAGRunStepSamples(ctx, openapiv1.GetDAGRunStepSamplesRequestObject{Name: "demo", DagRunId: "run-1", StepName: step})
		require.NoError(t, err)
		nf, ok := resp.(openapiv1.GetDAGRunStepSamples404JSONResponse)
		require.True(t, ok, "got %T", resp)
		assert.Equal(t, want, nf.Message)

		ev, err := a.GetDAGRunStepEvents(ctx, openapiv1.GetDAGRunStepEventsRequestObject{Name: "demo", DagRunId: "run-1", StepName: step})
		require.NoError(t, err)
		nfe, ok := ev.(openapiv1.GetDAGRunStepEvents404JSONResponse)
		require.True(t, ok, "got %T", ev)
		assert.Equal(t, want, nfe.Message)

		av, err := a.GetDAGRunStepAvailability(ctx, openapiv1.GetDAGRunStepAvailabilityRequestObject{Name: "demo", DagRunId: "run-1", StepName: step})
		require.NoError(t, err)
		nfa, ok := av.(openapiv1.GetDAGRunStepAvailability404JSONResponse)
		require.True(t, ok, "got %T", av)
		assert.Equal(t, want, nfa.Message)
	}

	resp := getSamples(t, a, "missing", openapiv1.GetDAGRunStepSamplesParams{})
	_, ok := resp.(openapiv1.GetDAGRunStepSamples404JSONResponse)
	assert.True(t, ok, "got %T", resp)
}

func TestSamplesDefaults(t *testing.T) {
	fake := &fakeSamplesReader{}
	a, attemptDir := newSamplesAPI(t, fake, nil, continuousNode("beat"))

	before := time.Now().UTC()
	resp := getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{})
	after := time.Now().UTC()

	_, ok := resp.(openapiv1.GetDAGRunStepSamples200JSONResponse)
	require.True(t, ok, "got %T", resp)
	assert.Equal(t, samples.Dir(attemptDir, "beat"), fake.dir)
	q := fake.sampleQuery
	assert.Equal(t, 600, q.MaxPoints)
	assert.False(t, q.To.Before(before) || q.To.After(after))
	assert.Equal(t, 24*time.Hour, q.To.Sub(q.From))
}

func TestSamplesParams(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	points := 2000

	fake := &fakeSamplesReader{}
	a, _ := newSamplesAPI(t, fake, nil, continuousNode("beat"))
	getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{From: &from, To: &to, Points: &points})
	assert.Equal(t, samples.SampleQuery{From: from, To: to, MaxPoints: 2000}, fake.sampleQuery)

	// Only from: to is now.
	getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{From: &from})
	assert.Equal(t, from, fake.sampleQuery.From)
	assert.WithinDuration(t, time.Now(), fake.sampleQuery.To, time.Minute)
}

func TestSamplesBadParams(t *testing.T) {
	a, _ := newSamplesAPI(t, &fakeSamplesReader{}, nil, continuousNode("beat"))
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Hour)
	for name, params := range map[string]openapiv1.GetDAGRunStepSamplesParams{
		"points low":  {Points: ptrOf(9)},
		"points high": {Points: ptrOf(2001)},
		"from > to":   {From: &from, To: &to},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.GetDAGRunStepSamples(t.Context(), openapiv1.GetDAGRunStepSamplesRequestObject{
				Name: "demo", DagRunId: "run-1", StepName: "beat", Params: params,
			})
			requireBadRequest(t, err)
		})
	}
	for _, n := range []int{10, 2000} {
		resp := getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{Points: &n})
		_, ok := resp.(openapiv1.GetDAGRunStepSamples200JSONResponse)
		assert.True(t, ok, "points %d: got %T", n, resp)
	}
}

func TestSamplesMapping(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	fake := &fakeSamplesReader{series: samples.Series{
		Tier: samples.TierHour,
		From: t0, To: t0.Add(2 * time.Hour),
		Buckets: []samples.Bucket{{
			Time: t0, Count: 3600, Failures: 12,
			Min: 1500 * time.Microsecond, Max: 2 * time.Second, P50: 40 * time.Millisecond, P95: 900 * time.Millisecond,
		}},
	}}
	a, _ := newSamplesAPI(t, fake, nil, continuousNode("beat"))

	got, ok := getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{}).(openapiv1.GetDAGRunStepSamples200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 3600, got.ResolutionSeconds)
	assert.Equal(t, time.UTC, got.From.Location())
	assert.True(t, t0.Equal(got.From))
	assert.True(t, t0.Add(2*time.Hour).Equal(got.To))
	require.Len(t, got.Buckets, 1)
	b := got.Buckets[0]
	assert.Equal(t, time.UTC, b.Time.Location())
	assert.True(t, t0.Equal(b.Time))
	assert.Equal(t, int64(3600), b.Count)
	assert.Equal(t, int64(12), b.Failures)
	assert.Equal(t, int64(1), b.MinMs)
	assert.Equal(t, int64(2000), b.MaxMs)
	assert.Equal(t, int64(40), b.P50Ms)
	assert.Equal(t, int64(900), b.P95Ms)
}

func TestSamplesEmptyStore(t *testing.T) {
	a, _ := newSamplesAPI(t, &fakeSamplesReader{}, nil, continuousNode("beat"))

	got, ok := getSamples(t, a, "beat", openapiv1.GetDAGRunStepSamplesParams{}).(openapiv1.GetDAGRunStepSamples200JSONResponse)
	require.True(t, ok)
	assert.NotNil(t, got.Buckets)
	assert.Empty(t, got.Buckets)
	assert.False(t, got.From.IsZero())
	assert.Equal(t, 1, got.ResolutionSeconds)

	ev, err := a.GetDAGRunStepEvents(t.Context(), openapiv1.GetDAGRunStepEventsRequestObject{Name: "demo", DagRunId: "run-1", StepName: "beat"})
	require.NoError(t, err)
	events, ok := ev.(openapiv1.GetDAGRunStepEvents200JSONResponse)
	require.True(t, ok)
	assert.NotNil(t, events.Events)
	assert.Empty(t, events.Events)
	assert.Nil(t, events.NextCursor)

	av, err := a.GetDAGRunStepAvailability(t.Context(), openapiv1.GetDAGRunStepAvailabilityRequestObject{Name: "demo", DagRunId: "run-1", StepName: "beat"})
	require.NoError(t, err)
	avail, ok := av.(openapiv1.GetDAGRunStepAvailability200JSONResponse)
	require.True(t, ok)
	require.Len(t, avail.Windows, 4)
	for _, w := range avail.Windows {
		assert.Zero(t, w.Count)
		assert.Nil(t, w.Availability)
	}
	assert.Nil(t, avail.LastFailureAt)
}

func TestEvents(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeSamplesReader{page: samples.EventPage{
		NextCursor: "c2",
		Events: []samples.Event{
			{Time: t0.Add(3 * time.Minute), Kind: samples.EventRecovered, FailedCount: 120, Outage: 2 * time.Minute},
			{Time: t0, Kind: samples.EventFailed, ExitCode: 7, Message: "boom"},
			{Time: t0.Add(-time.Hour), Kind: samples.EventStarted},
			// A stop during an outage keeps its count for the next start only.
			{Time: t0.Add(-2 * time.Hour), Kind: samples.EventStopped, FailedCount: 3},
			// An outage shorter than a second still has a length.
			{Time: t0.Add(-3 * time.Hour), Kind: samples.EventRecovered, FailedCount: 1, Outage: 500 * time.Millisecond},
		},
	}}
	a, _ := newSamplesAPI(t, fake, nil, continuousNode("beat"))
	ctx := t.Context()
	limit, before := 500, "c1"
	resp, err := a.GetDAGRunStepEvents(ctx, openapiv1.GetDAGRunStepEventsRequestObject{
		Name: "demo", DagRunId: "run-1", StepName: "beat",
		Params: openapiv1.GetDAGRunStepEventsParams{Limit: &limit, Before: &before},
	})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunStepEvents200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, samples.EventQuery{Limit: 500, Before: "c1"}, fake.eventQuery)
	require.NotNil(t, got.NextCursor)
	assert.Equal(t, "c2", *got.NextCursor)
	require.Len(t, got.Events, 5)

	rec, failed, started := got.Events[0], got.Events[1], got.Events[2]
	assert.Equal(t, openapiv1.StepEventKindRecovered, rec.Kind)
	require.NotNil(t, rec.OutageSeconds)
	assert.Equal(t, int64(120), *rec.OutageSeconds)
	require.NotNil(t, rec.FailedCount)
	assert.Equal(t, int64(120), *rec.FailedCount)
	assert.Nil(t, rec.ExitCode)

	assert.Equal(t, openapiv1.StepEventKindFailed, failed.Kind)
	require.NotNil(t, failed.ExitCode)
	assert.Equal(t, 7, *failed.ExitCode)
	require.NotNil(t, failed.Message)
	assert.Equal(t, "boom", *failed.Message)
	assert.Nil(t, failed.OutageSeconds)

	assert.Equal(t, openapiv1.StepEventKindStarted, started.Kind)
	assert.Nil(t, started.ExitCode)
	assert.Nil(t, started.Message)
	assert.Nil(t, started.FailedCount)

	assert.Nil(t, got.Events[3].FailedCount)
	require.NotNil(t, got.Events[4].OutageSeconds)
	assert.Equal(t, int64(0), *got.Events[4].OutageSeconds)
}

func TestEventsDefaultsAndBadParams(t *testing.T) {
	fake := &fakeSamplesReader{}
	a, _ := newSamplesAPI(t, fake, nil, continuousNode("beat"))
	ctx := t.Context()
	call := func(p openapiv1.GetDAGRunStepEventsParams) (openapiv1.GetDAGRunStepEventsResponseObject, error) {
		return a.GetDAGRunStepEvents(ctx, openapiv1.GetDAGRunStepEventsRequestObject{
			Name: "demo", DagRunId: "run-1", StepName: "beat", Params: p,
		})
	}

	_, err := call(openapiv1.GetDAGRunStepEventsParams{})
	require.NoError(t, err)
	assert.Equal(t, 100, fake.eventQuery.Limit)

	for _, n := range []int{0, 501} {
		_, err := call(openapiv1.GetDAGRunStepEventsParams{Limit: &n})
		requireBadRequest(t, err)
	}
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Hour)
	_, err = call(openapiv1.GetDAGRunStepEventsParams{From: &from, To: &to})
	requireBadRequest(t, err)

	fake.eventsErr = fmt.Errorf("decode: %w", samples.ErrInvalidCursor)
	bad := "x"
	_, err = call(openapiv1.GetDAGRunStepEventsParams{Before: &bad})
	requireBadRequest(t, err)
}

func TestAvailability(t *testing.T) {
	last := time.Date(2026, 3, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	fake := &fakeSamplesReader{availability: samples.Availability{
		// The reader may list the windows in any order, and omit some.
		Windows: []samples.WindowStat{
			{Window: samples.Window30d, Count: 1000, Failures: 10},
			{Window: samples.Window24h, Count: 100, Failures: 0},
			{Window: samples.Window7d, Count: 0},
			// Every sample failed: the availability is zero, not absent.
			{Window: samples.Window365d, Count: 5, Failures: 5},
		},
		Streak: 42, LastFailureAt: last, TotalCount: 5_000_000_000,
	}}
	a, _ := newSamplesAPI(t, fake, nil, continuousNode("beat"))

	resp, err := a.GetDAGRunStepAvailability(t.Context(), openapiv1.GetDAGRunStepAvailabilityRequestObject{Name: "demo", DagRunId: "run-1", StepName: "beat"})
	require.NoError(t, err)
	got, ok := resp.(openapiv1.GetDAGRunStepAvailability200JSONResponse)
	require.True(t, ok)
	require.Len(t, got.Windows, 4)
	want := []openapiv1.AvailabilityWindowWindow{
		openapiv1.AvailabilityWindowWindowN24h, openapiv1.AvailabilityWindowWindowN7d,
		openapiv1.AvailabilityWindowWindowN30d, openapiv1.AvailabilityWindowWindowN365d,
	}
	for i, w := range got.Windows {
		assert.Equal(t, want[i], w.Window)
	}
	require.NotNil(t, got.Windows[0].Availability)
	assert.InDelta(t, 1.0, *got.Windows[0].Availability, 1e-9)
	assert.Nil(t, got.Windows[1].Availability)
	require.NotNil(t, got.Windows[2].Availability)
	assert.InDelta(t, 0.99, *got.Windows[2].Availability, 1e-9)
	assert.Equal(t, int64(1000), got.Windows[2].Count)
	assert.Equal(t, int64(10), got.Windows[2].Failures)
	require.NotNil(t, got.Windows[3].Availability)
	assert.Zero(t, *got.Windows[3].Availability)
	assert.Equal(t, int64(42), got.Streak)
	assert.Equal(t, int64(5_000_000_000), got.TotalCount)
	require.NotNil(t, got.LastFailureAt)
	assert.Equal(t, time.UTC, got.LastFailureAt.Location())
	assert.True(t, last.Equal(*got.LastFailureAt))
}

// The handlers follow the visibility rule of the other step handlers: a user
// with no access to the workspace of the run gets 404.
func TestSamplesWorkspaceVisibility(t *testing.T) {
	a, _ := newSamplesAPI(t, &fakeSamplesReader{}, []string{"workspace=ops"}, continuousNode("beat"))
	a.authService = struct{ AuthService }{}
	user := func(workspace string) *auth.User {
		return &auth.User{Role: auth.RoleViewer, WorkspaceAccess: &auth.WorkspaceAccess{
			Grants: []auth.WorkspaceGrant{{Workspace: workspace, Role: auth.RoleViewer}},
		}}
	}

	_, err := a.GetDAGRunStepSamples(auth.WithUser(t.Context(), user("secret")), openapiv1.GetDAGRunStepSamplesRequestObject{
		Name: "demo", DagRunId: "run-1", StepName: "beat",
	})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.HTTPStatus)

	resp, err := a.GetDAGRunStepSamples(auth.WithUser(t.Context(), user("ops")), openapiv1.GetDAGRunStepSamplesRequestObject{
		Name: "demo", DagRunId: "run-1", StepName: "beat",
	})
	require.NoError(t, err)
	_, ok := resp.(openapiv1.GetDAGRunStepSamples200JSONResponse)
	assert.True(t, ok, "got %T", resp)
}

func TestSamplesStoreDir(t *testing.T) {
	fake := &fakeSamplesReader{}
	a, attemptDir := newSamplesAPI(t, fake, nil, continuousNode("my beat"))
	getSamples(t, a, "my beat", openapiv1.GetDAGRunStepSamplesParams{})
	assert.Equal(t, samples.Dir(attemptDir, "my beat"), fake.dir)
}
