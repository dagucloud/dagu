// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestReusedTimeMs(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	step := func(took time.Duration) ir.ReusedInput {
		return ir.ReusedInput{StartedAt: stringutil.FormatTime(at), FinishedAt: stringutil.FormatTime(at.Add(took))}
	}

	sum, ok := reusedTimeMs([]ir.ReusedInput{step(4 * time.Second), step(1500 * time.Millisecond)})
	assert.True(t, ok)
	assert.Equal(t, int64(5500), sum)

	// A step that a client supplied has no times, and a negative span is unknown.
	_, ok = reusedTimeMs([]ir.ReusedInput{{Step: "supplied"}, {StartedAt: stringutil.FormatTime(at), FinishedAt: stringutil.FormatTime(at.Add(-time.Second))}})
	assert.False(t, ok)
	_, ok = reusedTimeMs(nil)
	assert.False(t, ok)
}

func TestActivityWindowFromQuery(t *testing.T) {
	t.Parallel()
	window, err := activityWindowFromQuery(url.Values{})
	assert.NoError(t, err)
	assert.Zero(t, window)

	window, err = activityWindowFromQuery(url.Values{"activityWindow": {"86400"}})
	assert.NoError(t, err)
	assert.Equal(t, 24*time.Hour, window)

	for _, bad := range []string{"day", "59", "604801", "-1"} {
		_, err = activityWindowFromQuery(url.Values{"activityWindow": {bad}})
		var apiErr *Error
		if assert.ErrorAs(t, err, &apiErr, bad) {
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, bad)
		}
	}
}

// A list reuses the activity of a DAG while its latest run is the same, for
// at most one bucket of the window and at most activityCacheMaxAge.
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

// The activity of a plain DAG is not kept when it can change while the
// latest run stays the same.
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

// A list that reuses an activity leaves out the ended runs that the window
// passed since it was read, and keeps the run in progress. The kept activity
// stays as it was read.
func TestListActivityReuseFollowsTheWindow(t *testing.T) {
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
	moved, err := a.listActivity(t.Context(), &ir.DAG{Name: "d"}, latest, at.Add(5*time.Second), window)
	assert.NoError(t, err)
	ids := make([]string, 0, len(moved.Runs))
	for _, r := range moved.Runs {
		ids = append(ids, r.DagRunId)
	}
	assert.Equal(t, []string{"in", "long"}, ids)
	assert.Len(t, kept.Runs, 3)
}
