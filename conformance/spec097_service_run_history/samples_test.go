// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec097_service_run_history_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each run of a service DAG has one sample. After some runs, the total count
// and the sum of the counts of the buckets equal the count of the runs that
// ended.
func TestOneSampleForEachRun(t *testing.T) {
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()
	p.startScheduler()

	var ended, total, sum int64
	ok := assert.Eventually(t, func() bool {
		ended = int64(len(p.ended("service_ok")))
		total = p.availability("service_ok").TotalCount
		sum = p.countOf("service_ok")
		return ended >= 3 && total == ended && sum == ended
	}, wait, poll)
	require.True(t, ok, "ended runs %d, total count %d, sum of buckets %d", ended, total, sum)
}

// recorded returns the buckets that hold samples. A bucket with active time
// and no sample is in the series with a count of 0 (spec 097).
func recorded(series api.SampleSeries) []api.SampleBucket {
	var out []api.SampleBucket
	for _, b := range series.Buckets {
		if b.Count > 0 {
			out = append(out, b)
		}
	}
	return out
}

// With hist_retention_runs 1, the retention removes the older runs, but each
// run keeps its sample. The runs are at least 2 seconds apart, so each bucket
// of the raw tier that has a sample holds one: a double write would give a
// count of 2.
func TestRetentionKeepsSamples(t *testing.T) {
	t.Parallel()
	p := newProject(t, "retention")
	p.startServer()
	started := time.Now()
	p.startScheduler()

	query := func() url.Values {
		return url.Values{
			"from":   {started.Add(-5 * time.Second).UTC().Format(time.RFC3339Nano)},
			"to":     {time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)},
			"points": {"2000"},
		}
	}
	require.Eventually(t, func() bool {
		return len(recorded(p.samples("service_retention", query()))) >= 4
	}, wait, poll, "fewer than 4 samples")

	series := p.samples("service_retention", query())
	assert.Equal(t, 1, series.ResolutionSeconds)
	samples := recorded(series)
	for _, b := range samples {
		assert.Equal(t, int64(1), b.Count, "bucket %s", b.Time)
	}
	assert.LessOrEqual(t, len(p.runs("service_retention")), 2, "the retention keeps the latest run, and the running one")
	assert.GreaterOrEqual(t, p.availability("service_retention").TotalCount, int64(len(samples)))
}

// Runs that a user starts while no scheduler runs get their samples when the
// scheduler opens the history, and no sample is lost.
func TestImportOfRetainedRuns(t *testing.T) {
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()

	started := time.Now()
	const byHand = 3
	for i := range byHand {
		p.dagu.RunWithEnv(p.env(), "start", fmt.Sprintf("--run-id=hand-%d", i), "ok/service_ok.yaml").ExpectExitCode(0)
	}
	empty := p.samples("service_ok", nil)
	require.Empty(t, empty.Buckets, "no scheduler ran: nothing records the runs")

	// The window ends before the scheduler starts, so it holds the runs by hand
	// only. Sleep over a second so that no bucket of the raw tier is shared.
	time.Sleep(2200 * time.Millisecond)
	until := time.Now().Truncate(time.Second).Add(-time.Second)
	p.startScheduler()

	query := url.Values{
		"from":   {started.Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)},
		"to":     {until.UTC().Format(time.RFC3339Nano)},
		"points": {"2000"},
	}
	var sum int64
	ok := assert.Eventually(t, func() bool {
		sum = 0
		for _, b := range p.samples("service_ok", query).Buckets {
			sum += b.Count
		}
		return sum == byHand
	}, wait, poll)
	require.True(t, ok, "samples of the runs by hand: %d, want %d", sum, byHand)
	assert.GreaterOrEqual(t, p.availability("service_ok").TotalCount, int64(byHand))
}
