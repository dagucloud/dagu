// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var historyT0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return historyT0.Add(time.Duration(seconds) * time.Second) }

func ended(start, end int, failed bool) alwaysOnRun {
	return alwaysOnRun{start: at(start), end: at(end), ended: true, failed: failed}
}

func alive(start int) alwaysOnRun { return alwaysOnRun{start: at(start)} }

func TestBucketRunsCountsRunsFailuresUptimeAndWait(t *testing.T) {
	t.Parallel()

	// Four buckets of a minute. The last run is still alive at now.
	runs := []alwaysOnRun{
		ended(10, 70, false),
		ended(80, 100, true),
		alive(130),
	}

	got := bucketRuns(runs, at(0), at(240), 60, at(200))

	assert.Equal(t, []api.AlwaysOnBucket{
		{Runs: 1, RunningSec: 50},
		{Runs: 1, Failed: 1, RunningSec: 30, WaitSec: 40, MaxWaitSec: 30},
		{Runs: 1, RunningSec: 50},
		{RunningSec: 20},
	}, got)
}

// A run that started before the window still counts inside it, but is not
// one of the runs that started there.
func TestBucketRunsCountsTheRunThatStartedBeforeTheWindow(t *testing.T) {
	t.Parallel()

	runs := []alwaysOnRun{
		ended(-30, 20, false),
		ended(40, 50, false),
	}

	got := bucketRuns(runs, at(0), at(120), 60, at(200))

	assert.Equal(t, []api.AlwaysOnBucket{
		{Runs: 1, RunningSec: 30, WaitSec: 20, MaxWaitSec: 20},
		{RunningSec: 0},
	}, got)
}

// Runs are summed in the order of their start, whatever order they come in.
func TestBucketRunsDoesNotDependOnTheOrderOfRuns(t *testing.T) {
	t.Parallel()

	newestFirst := []alwaysOnRun{alive(130), ended(80, 100, true), ended(10, 70, false)}
	oldestFirst := []alwaysOnRun{ended(10, 70, false), ended(80, 100, true), alive(130)}

	assert.Equal(t,
		bucketRuns(oldestFirst, at(0), at(240), 60, at(200)),
		bucketRuns(newestFirst, at(0), at(240), 60, at(200)),
	)
}

func TestBucketRunsWithoutRunsIsEmptyBuckets(t *testing.T) {
	t.Parallel()

	got := bucketRuns(nil, at(0), at(180), 60, at(200))

	assert.Equal(t, make([]api.AlwaysOnBucket, 3), got)
}

// A restricted caller must not get the cached response of a caller that sees
// every workspace, whatever the request says.
func TestWorkspaceFilterKeySeparatesWhatCallersSee(t *testing.T) {
	t.Parallel()

	all := workspaceFilterKey(nil)
	assert.Equal(t, all, workspaceFilterKey(&workspace.WorkspaceFilter{}))

	ops := workspaceFilterKey(&workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}})
	dev := workspaceFilterKey(&workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"dev"}})
	assert.NotEqual(t, all, ops)
	assert.NotEqual(t, ops, dev)
	assert.Equal(t, workspaceFilterKey(&workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"a", "b"}}),
		workspaceFilterKey(&workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"b", "a"}}))
}

// A run shorter than a second must still count: a DAG that restarts every
// second with a 400 ms step is up a third of the time, not 0 %.
func TestBucketRunsAddsUpRunsShorterThanASecond(t *testing.T) {
	t.Parallel()

	from := historyT0
	var runs []alwaysOnRun
	for i := range 20 {
		start := from.Add(time.Duration(i) * 1400 * time.Millisecond)
		runs = append(runs, alwaysOnRun{start: start, end: start.Add(400 * time.Millisecond), ended: true})
	}

	buckets := bucketRuns(runs, from, from.Add(time.Minute), 60, from.Add(time.Minute))

	assert.Equal(t, 20, buckets[0].Runs)
	assert.Equal(t, 8, buckets[0].RunningSec)
	assert.Equal(t, 19, buckets[0].WaitSec, "19 waits of one second")
	assert.Equal(t, 1, buckets[0].MaxWaitSec)
}

// The accumulator takes the runs newest first, as the store lists them, and
// gives the result of the runs taken in any order.
func TestBucketAccumulatorTakesRunsNewestFirst(t *testing.T) {
	t.Parallel()

	runs := []alwaysOnRun{ended(10, 70, false), ended(80, 100, true), alive(130)}
	want := bucketRuns(append([]alwaysOnRun(nil), runs...), at(0), at(240), 60, at(200))

	acc := newBucketAccumulator(at(0), at(240), 60, at(200))
	for _, run := range []alwaysOnRun{runs[2], runs[1], runs[0]} {
		acc.add(run)
	}

	assert.Equal(t, want, acc.buckets())
}
