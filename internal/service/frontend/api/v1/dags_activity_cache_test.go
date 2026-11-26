// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// While the run events are not followed, a list reuses the activity of a DAG
// while its latest run is the same, for at most one bucket of the window and
// at most activityCacheMaxAge.
func TestActivityCacheReusesWhileTheLatestRunIsTheSame(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	latest := ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Succeeded}
	activity := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{}}
	day := activityKey{dag: "d", window: 24 * time.Hour}
	short := activityKey{dag: "d", window: 10 * time.Minute}

	var cache activityCache
	cache.put(day, latest, at, activity)
	cache.put(short, latest, at, activity)

	kept, ok := cache.get(day, latest, at.Add(activityCacheMaxAge-time.Second))
	assert.True(t, ok)
	assert.Same(t, activity, kept)
	_, ok = cache.get(day, latest, at.Add(activityCacheMaxAge))
	assert.False(t, ok, "older than the max age")
	_, ok = cache.get(short, latest, at.Add(10*time.Second))
	assert.False(t, ok, "older than one bucket of the window")

	for name, changed := range map[string]ir.DAGRunStatus{
		"new run":    {DAGRunID: "r2", AttemptID: "a2", Status: ir.Running},
		"new status": {DAGRunID: "r1", AttemptID: "a1", Status: ir.Failed},
		"retried":    {DAGRunID: "r1", AttemptID: "a2", Status: ir.Succeeded},
	} {
		_, ok = cache.get(day, changed, at)
		assert.False(t, ok, name)
	}
}

// A service DAG starts a run each few seconds, so a list reuses its activity
// whatever its latest run, for at most serviceActivityMaxAge.
func TestActivityCacheReusesServiceActivityByAge(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	service := activityKey{dag: "s", window: 24 * time.Hour, service: true}
	activity := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{}}
	restarted := ir.DAGRunStatus{DAGRunID: "r2", AttemptID: "a2", Status: ir.Running}

	var cache activityCache
	cache.put(service, ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Running}, at, activity)
	kept, ok := cache.get(service, restarted, at.Add(serviceActivityMaxAge-time.Second))
	assert.True(t, ok, "a new run of the service")
	assert.Same(t, activity, kept)
	_, ok = cache.get(service, restarted, at.Add(serviceActivityMaxAge))
	assert.False(t, ok, "older than the max age of a service DAG")
}

// While the run events are not followed, the activity of a plain DAG is not
// reused when it can change while the latest run stays the same.
func TestActivityCacheSkipsActivitiesThatChangeUnseen(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	plain := activityKey{dag: "d", window: 24 * time.Hour}
	service := activityKey{dag: "s", window: 24 * time.Hour, service: true}
	queued := ir.DAGRunStatus{DAGRunID: "r3", AttemptID: "a3", Status: ir.Queued}
	running := ir.DAGRunStatus{DAGRunID: "r2", AttemptID: "a2", Status: ir.Running}
	olderRunning := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{
		{DagRunId: "r2", Status: openapiv1.Status(ir.Running)},
		{DagRunId: "r1", Status: openapiv1.Status(ir.Running)},
	}}
	empty := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{}}

	var cache activityCache
	cache.put(plain, queued, at, empty)
	_, ok := cache.get(plain, queued, at)
	assert.False(t, ok, "the latest run is queued")
	cache.put(plain, running, at, olderRunning)
	_, ok = cache.get(plain, running, at)
	assert.False(t, ok, "an older run is in progress")
	cache.put(service, queued, at, empty)
	_, ok = cache.get(service, queued, at)
	assert.True(t, ok, "a service DAG keeps its buckets")
}

// While the run events are followed, a list reuses the activity of a plain
// DAG for one bucket of its window, also when an older run is in progress,
// until an event tells that the runs of the DAG changed. An activity whose read
// began before that change is not reused. A change drops the activity of a
// service DAG too, so that a list that reads after the change sees it.
func TestActivityCacheFollowsRunChanges(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	window := 24 * time.Hour
	running := ir.DAGRunStatus{DAGRunID: "r2", AttemptID: "a2", Status: ir.Running}
	olderRunning := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{
		{DagRunId: "r2", Status: openapiv1.Status(ir.Running)},
		{DagRunId: "r1", Status: openapiv1.Status(ir.Running)},
	}}
	plain := activityKey{dag: "d", window: window}
	other := activityKey{dag: "o", window: window}
	service := activityKey{dag: "s", window: window, service: true}

	var cache activityCache
	cache.changed(nil, at)
	for _, key := range []activityKey{plain, other, service} {
		cache.put(key, running, at, olderRunning)
	}

	later := at.Add(activityCacheMaxAge + time.Minute)
	cache.changed(nil, later)
	kept, ok := cache.get(plain, running, later)
	assert.True(t, ok, "no run of the DAG changed")
	assert.Same(t, olderRunning, kept)

	cache.changed([]string{"d", "s"}, later)
	_, ok = cache.get(plain, running, later)
	assert.False(t, ok, "a run of the DAG changed")
	_, ok = cache.get(other, running, later)
	assert.True(t, ok, "the runs of another DAG did not change")
	_, ok = cache.get(service, running, later)
	assert.False(t, ok, "a run of the service DAG changed")

	cache.put(plain, running, later.Add(-time.Second), olderRunning)
	_, ok = cache.get(plain, running, later)
	assert.False(t, ok, "the read began before the change")

	end := at.Add(window / activityBucketLimit)
	cache.changed(nil, end)
	_, ok = cache.get(other, running, end)
	assert.False(t, ok, "older than one bucket of the window")
}

// While the run events are followed, a kept activity is reused without the
// latest run of its DAG, until an event tells that the runs of the DAG
// changed.
func TestActivityCacheReusesFollowedActivity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	key := activityKey{dag: "d", window: 24 * time.Hour}
	kept := &openapiv1.DAGListActivity{}
	var cache activityCache
	cache.put(key, ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Succeeded}, at, kept)

	_, ok := cache.getFollowed(key, at)
	assert.False(t, ok, "the run events are not followed yet")

	cache.changed(nil, at)
	got, ok := cache.getFollowed(key, at.Add(time.Second))
	assert.True(t, ok)
	assert.Same(t, kept, got)

	cache.changed([]string{"d"}, at.Add(2*time.Second))
	_, ok = cache.getFollowed(key, at.Add(3*time.Second))
	assert.False(t, ok, "a run of the DAG changed")
}

// A change of a DAG drops its kept activity also when the changes of more DAGs
// than the cache holds come after it.
func TestActivityCacheKeepsAChangePastManyOthers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	latest := ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Succeeded}
	activity := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{}}
	key := activityKey{dag: "d", window: 24 * time.Hour}

	var cache activityCache
	cache.changed(nil, at)
	cache.put(key, latest, at, activity)
	cache.changed([]string{"d"}, at.Add(time.Second))
	for i := range activityCacheSize {
		cache.changed([]string{fmt.Sprintf("other-%d", i)}, at.Add(2*time.Second))
	}

	_, ok := cache.get(key, latest, at.Add(3*time.Second))
	assert.False(t, ok)
}

// When the run events are no longer read, a list reuses the activity of a
// plain DAG for at most activityCacheMaxAge again.
func TestActivityCacheFallsBackWhenTheEventsStop(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	latest := ir.DAGRunStatus{DAGRunID: "r1", AttemptID: "a1", Status: ir.Succeeded}
	activity := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{}}
	key := activityKey{dag: "d", window: 24 * time.Hour}

	var cache activityCache
	cache.changed(nil, at)
	cache.put(key, latest, at, activity)
	last := at.Add(activityCacheMaxAge)
	cache.changed(nil, last)
	_, ok := cache.get(key, latest, last)
	assert.True(t, ok, "the events are read")
	_, ok = cache.get(key, latest, last.Add(activityFollowGrace))
	assert.False(t, ok, "the events stopped and the activity is older than the max age")
}

// A list that reuses an activity leaves out the ended runs that the window
// passed since it was read, and keeps the run in progress. The kept activity
// stays as it was read.
func TestCachedDAGActivityFollowsTheWindow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	window := 24 * time.Hour
	since := at.Add(-window)
	run := func(id string, status ir.Status, started time.Time) openapiv1.DAGListActivityRun {
		return openapiv1.DAGListActivityRun{DagRunId: id, Status: openapiv1.Status(status), StartedAt: stringutil.FormatTime(started)}
	}
	latest := ir.DAGRunStatus{DAGRunID: "long", AttemptID: "a", Status: ir.Running}
	kept := &openapiv1.DAGListActivity{Runs: []openapiv1.DAGListActivityRun{
		run("in", ir.Succeeded, since.Add(10*time.Second)),
		run("out", ir.Failed, since.Add(time.Second)),
		run("long", ir.Running, since.Add(-time.Hour)),
	}}
	a := &API{}
	a.activities.put(activityKey{dag: "d", window: window}, latest, at, kept)

	// No run repository: the activity can only come from the cache.
	moved, err := a.cachedDAGActivity(t.Context(), &ir.DAG{Name: "d"}, latest, at.Add(5*time.Second), window)
	assert.NoError(t, err)
	ids := make([]string, 0, len(moved.Runs))
	for _, r := range moved.Runs {
		ids = append(ids, r.DagRunId)
	}
	assert.Equal(t, []string{"in", "long"}, ids)
	assert.Len(t, kept.Runs, 3)
}
