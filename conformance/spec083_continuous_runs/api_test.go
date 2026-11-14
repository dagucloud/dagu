// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec083_continuous_runs_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const timeout = 30 * time.Second

// beatDAG has a continuous step (a repeat mode and no limit) whose
// precondition is false, so the step never executes and the run ends.
const beatDAG = `steps:
  - name: beat
    run: "true"
    preconditions: "test 1 = 2"
    repeat_policy:
      repeat: while
      interval_sec: 1
`

const plainDAG = `steps:
  - name: once
    run: "true"
  - name: limited
    run: "true"
    repeat_policy:
      repeat: while
      condition: "false"
      limit: 3
      interval_sec: 1
`

func createDAG(t *testing.T, server test.Server, name, spec string) {
	t.Helper()
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: api.DAGName(name),
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
}

// finishedRun creates the DAG, runs it and waits for the end of the run.
func finishedRun(t *testing.T, server test.Server, name, spec string) string {
	t.Helper()
	createDAG(t, server, name, spec)
	resp := server.Client().Post("/api/v1/dags/"+name+"/start", map[string]any{}).ExpectStatus(http.StatusOK).Send(t)
	var started struct {
		DagRunID string `json:"dagRunId"`
	}
	resp.Unmarshal(t, &started)
	require.NotEmpty(t, started.DagRunID)

	require.Eventually(t, func() bool {
		resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, started.DagRunID)).Send(t)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}
		var details api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &details)
		switch details.DagRunDetails.Status {
		case api.StatusSuccess, api.StatusFailed, api.StatusAborted, api.StatusPartialSuccess, api.StatusRejected:
			return true
		case api.StatusNotStarted, api.StatusQueued, api.StatusRunning, api.StatusWaiting:
		}
		return false
	}, timeout, 200*time.Millisecond)
	return started.DagRunID
}

func stepURL(name, runID, step, endpoint string, query url.Values) string {
	u := fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/%s/%s", name, runID, step, endpoint)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func TestNonContinuousStepIsNotFound(t *testing.T) {
	server := test.SetupServer(t)
	runID := finishedRun(t, server, "plain", plainDAG)

	for _, step := range []string{"once", "limited"} {
		for _, endpoint := range []string{"samples", "events", "availability"} {
			resp := server.Client().Get(stepURL("plain", runID, step, endpoint, nil)).
				ExpectStatus(http.StatusNotFound).Send(t)
			var e api.Error
			resp.Unmarshal(t, &e)
			assert.Equal(t, api.ErrorCodeNotFound, e.Code)
			assert.Equal(t, "step "+step+" is not a continuous step", e.Message, "%s %s", step, endpoint)
		}
	}
}

func TestParamsAreValidated(t *testing.T) {
	server := test.SetupServer(t)
	runID := finishedRun(t, server, "beat", beatDAG)

	bad := []struct {
		endpoint string
		query    url.Values
	}{
		{"samples", url.Values{"points": {"9"}}},
		{"samples", url.Values{"points": {"2001"}}},
		{"samples", url.Values{"points": {"many"}}},
		{"samples", url.Values{"from": {"yesterday"}}},
		{"samples", url.Values{"from": {"2026-02-01T00:00:00Z"}, "to": {"2026-01-01T00:00:00Z"}}},
		{"events", url.Values{"limit": {"0"}}},
		{"events", url.Values{"limit": {"501"}}},
		{"events", url.Values{"from": {"2026-02-01T00:00:00Z"}, "to": {"2026-01-01T00:00:00Z"}}},
		{"events", url.Values{"before": {"bad"}}},
		{"events", url.Values{"before": {"e0"}}},
		{"events", url.Values{"before": {"e1"}}},
	}
	for _, tt := range bad {
		t.Run(tt.endpoint+"?"+tt.query.Encode(), func(t *testing.T) {
			server.Client().Get(stepURL("beat", runID, "beat", tt.endpoint, tt.query)).
				ExpectStatus(http.StatusBadRequest).Send(t)
		})
	}
}

// skipUntilStore skips a test that reads the store while the store is not
// merged: the reader of the contract answers samples.ErrNotImplemented.
func skipUntilStore(t *testing.T) {
	t.Helper()
	_, err := samples.OpenRecorder(t.Context(), t.TempDir(), samples.Options{})
	if errors.Is(err, samples.ErrNotImplemented) {
		t.Skip("the samples store is not merged yet")
	}
}

func TestEmptyStore(t *testing.T) {
	skipUntilStore(t)
	server := test.SetupServer(t)
	runID := finishedRun(t, server, "beat", beatDAG)

	var s api.StepSamples
	server.Client().Get(stepURL("beat", runID, "beat", "samples", nil)).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &s)
	assert.Empty(t, s.Buckets)

	var e api.StepEvents
	server.Client().Get(stepURL("beat", runID, "beat", "events", nil)).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &e)
	assert.Empty(t, e.Events)

	var a api.StepAvailability
	server.Client().Get(stepURL("beat", runID, "beat", "availability", nil)).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &a)
	require.Len(t, a.Windows, 4)
	assert.Equal(t, api.AvailabilityWindowWindowN24h, a.Windows[0].Window)
	assert.Equal(t, api.AvailabilityWindowWindowN365d, a.Windows[3].Window)
	for _, w := range a.Windows {
		assert.Zero(t, w.Count)
		assert.Nil(t, w.Availability)
	}
}

// A store of three years answers in bounded size and time.
func TestThreeYearsOfSamples(t *testing.T) {
	skipUntilStore(t)
	server := test.SetupServer(t)
	runID := finishedRun(t, server, "beat", beatDAG)

	ctx := server.Context
	attempt, err := server.DAGRunRepository.FindAttempt(ctx, ir.NewDAGRunRef("beat", runID))
	require.NoError(t, err)
	dirOf, ok := attempt.(interface{ Dir() string })
	require.True(t, ok, "attempt has no Dir")

	// The server reads the tiers by the wall clock, so the data ends now.
	now := time.Now().UTC().Truncate(time.Hour)
	clock := now.AddDate(-3, 0, 0)
	rec, err := samples.OpenRecorder(ctx, samples.Dir(dirOf.Dir(), "beat"), samples.Options{Now: func() time.Time { return clock }})
	require.NoError(t, err)

	// One sample each hour for three years, then one each 30 seconds for the
	// last two days. Every 1000th sample fails.
	var total, failures uint64
	record := func(at time.Time) {
		clock = at
		code := 0
		if total%1000 == 999 {
			code, failures = 1, failures+1
		}
		require.NoError(t, rec.Record(ctx, samples.Sample{
			Time: at, Duration: time.Duration(10+total%50) * time.Millisecond, ExitCode: code,
		}))
		total++
	}
	denseFrom := now.Add(-48 * time.Hour)
	var first time.Time
	for at := now.AddDate(-3, 0, 0); at.Before(denseFrom); at = at.Add(time.Hour) {
		if first.IsZero() {
			first = at
		}
		record(at)
	}
	for at := denseFrom; at.Before(now); at = at.Add(30 * time.Second) {
		record(at)
	}
	clock = now
	require.NoError(t, rec.Close(ctx))

	const points = 600
	q := url.Values{
		"from":   {first.Add(-24 * time.Hour).Format(time.RFC3339)},
		"to":     {now.Format(time.RFC3339)},
		"points": {fmt.Sprint(points)},
	}
	started := time.Now()
	resp := server.Client().Get(stepURL("beat", runID, "beat", "samples", q)).ExpectStatus(http.StatusOK).Send(t)
	elapsed := time.Since(started)
	var s api.StepSamples
	resp.Unmarshal(t, &s)

	// The spec bound is 500 ms on its reference machine; shared CI runners,
	// some with the race detector, get more room.
	assert.Less(t, elapsed, 2*time.Second)
	assert.LessOrEqual(t, len(s.Buckets), points)
	assert.NotEmpty(t, s.Buckets)
	assert.Contains(t, []int{1, 60, 3600, 86400}, s.ResolutionSeconds)
	var count, failed int64
	for i, b := range s.Buckets {
		count += b.Count
		failed += b.Failures
		assert.LessOrEqual(t, b.MinMs, b.P50Ms)
		assert.LessOrEqual(t, b.P50Ms, b.P95Ms)
		assert.LessOrEqual(t, b.P95Ms, b.MaxMs)
		if i > 0 {
			assert.True(t, s.Buckets[i-1].Time.Before(b.Time), "buckets are in time order")
		}
	}
	// The window is wider than the points allow at the day tier: the response
	// cuts it to the last `points` days, so it counts the recent samples only.
	assert.Positive(t, count)
	assert.LessOrEqual(t, uint64(count), total)
	assert.LessOrEqual(t, uint64(failed), failures)

	// A window of one day answers from a finer tier.
	q = url.Values{"from": {now.Add(-24 * time.Hour).Format(time.RFC3339)}, "to": {now.Format(time.RFC3339)}}
	var day api.StepSamples
	server.Client().Get(stepURL("beat", runID, "beat", "samples", q)).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &day)
	assert.LessOrEqual(t, len(day.Buckets), 600)
	assert.Less(t, day.ResolutionSeconds, s.ResolutionSeconds)

	// Events: newest first, in pages.
	var page api.StepEvents
	server.Client().Get(stepURL("beat", runID, "beat", "events", url.Values{"limit": {"2"}})).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &page)
	require.Len(t, page.Events, 2)
	assert.False(t, page.Events[0].Time.Before(page.Events[1].Time))
	require.NotNil(t, page.NextCursor)
	var next api.StepEvents
	server.Client().Get(stepURL("beat", runID, "beat", "events", url.Values{"limit": {"2"}, "before": {*page.NextCursor}})).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &next)
	require.NotEmpty(t, next.Events)
	assert.False(t, next.Events[0].Time.After(page.Events[1].Time))

	// Availability: the counts agree with the failure rate of the data.
	var av api.StepAvailability
	server.Client().Get(stepURL("beat", runID, "beat", "availability", nil)).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &av)
	require.Len(t, av.Windows, 4)
	assert.Equal(t, int64(total), av.TotalCount)
	for _, w := range av.Windows {
		if w.Count > 0 {
			require.NotNil(t, w.Availability)
			assert.InDelta(t, float64(w.Count-w.Failures)/float64(w.Count), *w.Availability, 1e-9)
		}
	}
}
