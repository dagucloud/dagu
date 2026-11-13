// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec097_service_run_history_test

import (
	"net/url"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uptimeOf returns the uptime of the 24 hours window, when the API gives it.
func (p *project) uptimeOf(dag string) (float64, bool) {
	p.t.Helper()
	for _, w := range p.availability(dag).Windows {
		if w.Window == api.AvailabilityWindowWindowN24h && w.Uptime != nil {
			return *w.Uptime, true
		}
	}
	return 0, false
}

// A service DAG whose run lasts longer than the restart wait has one long run.
// No run has ended, so no sample exists, but the run in progress counts: the
// uptime is near 1, and the success rate has no value. The uptime starts at the
// next full minute, because the window is made of whole minutes.
func TestUptimeOfRunInProgress(t *testing.T) {
	t.Parallel()
	p := newProject(t, "uptime")
	p.startServer()
	p.startScheduler()

	var uptime float64
	ok := assert.Eventually(t, func() bool {
		var known bool
		uptime, known = p.uptimeOf("service_long")
		return known
	}, 2*wait, poll)
	require.True(t, ok, "the availability gave no uptime")
	assert.GreaterOrEqual(t, uptime, 0.99)
	assert.LessOrEqual(t, uptime, 1.0)

	// The fields of the success rate do not change: no run ended.
	av := p.availability("service_long")
	assert.Zero(t, av.TotalCount)
	assert.Zero(t, av.Streak)
	assert.Nil(t, av.LastFailureAt)
	require.Len(t, av.Windows, 4)
	for _, w := range av.Windows {
		assert.Zero(t, w.Count)
		assert.Nil(t, w.Availability, "window %s", w.Window)
	}

	// The samples of the history give the uptime of their window. No run
	// ended, so no bucket has a run, but the run in progress gives its up time
	// to the buckets that it covers.
	series := p.samples("service_long", url.Values{"points": {"2000"}})
	require.NotNil(t, series.Uptime)
	assert.GreaterOrEqual(t, *series.Uptime, 0.99)
	require.NotEmpty(t, series.Buckets)
	for _, b := range series.Buckets {
		assert.Zero(t, b.Count)
		assert.Zero(t, b.Failures)
		require.NotNil(t, b.UpMs)
		assert.Positive(t, *b.UpMs)
		assert.LessOrEqual(t, *b.UpMs, int64(series.ResolutionSeconds)*1000)
	}

	// The DAG list gives it in the activity of the DAG.
	resp := getJSON[api.ListDAGs200JSONResponse](p, 200, "/dags", url.Values{"activityWindow": {"3600"}})
	var activity *api.DAGListActivity
	for _, d := range resp.Dags {
		if d.FileName == "service_long" {
			activity = d.Activity
		}
	}
	require.NotNil(t, activity)
	require.NotNil(t, activity.Uptime)
	assert.GreaterOrEqual(t, *activity.Uptime, 0.99)

	// The activity has the same buckets with up time: the strip is green
	// where the service is up, and the run in progress shows.
	require.NotNil(t, activity.Buckets)
	require.NotEmpty(t, *activity.Buckets)
	for _, b := range *activity.Buckets {
		assert.Zero(t, b.Count)
		require.NotNil(t, b.UpMs)
		assert.Positive(t, *b.UpMs)
	}
}

// A service DAG that restarts as designed is active all the time: a run of a
// few milliseconds, one second of restart wait and the delay of the queue make
// a gap of 3 to 6 seconds between two runs, and this gap is not a stop. The
// uptime is near 1, and not near the share of the run in the period.
//
// The uptime is on whole minutes, so the test waits for the first full minute.
func TestUptimeOfFastRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a full minute")
	}
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()
	p.startScheduler()

	var uptime float64
	ok := assert.Eventually(t, func() bool {
		known := false
		uptime, known = p.uptimeOf("service_ok")
		return known && p.availability("service_ok").TotalCount >= 10 && uptime >= 0.9
	}, 3*wait, time.Second)
	require.True(t, ok, "no uptime, fewer than 10 runs, or an uptime under 0.9 (last value %v)", uptime)
	assert.LessOrEqual(t, uptime, 1.0)

	// The minute buckets of the samples give the up time of the run history.
	// A bucket never has more than its own width.
	var up int64
	for _, b := range p.samples("service_ok", url.Values{"points": {"2000"}}).Buckets {
		if b.UpMs != nil {
			assert.LessOrEqual(t, *b.UpMs, int64(60_000))
			up += *b.UpMs
		}
	}
	assert.Positive(t, up)
}

// A stop of the scheduler that is longer than the restart wait plus the
// tolerance is not active time, so the uptime is below 1 while the other runs
// restart on time. The runs succeed, so the success rate is 1, except for a run
// that the stop kills.
//
// This test waits for a stop of 20 seconds and for a full minute.
func TestUptimeBelowOneWithGaps(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a stop of 20 seconds and for a full minute")
	}
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()
	scheduler := p.startScheduler()

	require.Eventually(t, func() bool {
		return p.availability("service_ok").TotalCount >= 3
	}, wait, poll, "fewer than 3 samples")
	// Stop between two runs, so that the stop does not abort a run.
	require.Eventually(t, func() bool {
		runs := p.runs("service_ok")
		return len(runs) > 0 && len(p.ended("service_ok")) == len(runs) && runs[0].Status == api.StatusSuccess
	}, wait, 100*time.Millisecond, "no moment between two runs")
	scheduler.Stop()
	// restart_wait_sec is 1: the tolerance is 11 seconds.
	time.Sleep(20 * time.Second)
	p.startScheduler()

	// The window is made of whole minutes: wait until the minute of the restart
	// is closed and its active time is written.
	resumed := time.Now()
	time.Sleep(time.Until(resumed.Truncate(time.Minute).Add(time.Minute + 10*time.Second)))

	var uptime float64
	ok := assert.Eventually(t, func() bool {
		known := false
		uptime, known = p.uptimeOf("service_ok")
		return known
	}, wait, time.Second)
	require.True(t, ok, "the availability gave no uptime")
	assert.Greater(t, uptime, 0.0)
	assert.Less(t, uptime, 0.97, "the stop is not active time")

	av := p.availability("service_ok")
	first := av.Windows[0]
	assert.GreaterOrEqual(t, first.Count, int64(3))
	// The scheduler can start a run between the check and the stop, and the
	// stop kills it: that run is the only one that can fail.
	assert.LessOrEqual(t, first.Failures, int64(1))
	require.NotNil(t, first.Availability)
	assert.InDelta(t, float64(first.Count-first.Failures)/float64(first.Count), *first.Availability, 1e-9)
}
