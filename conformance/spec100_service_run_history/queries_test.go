// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec100_service_run_history_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var routes = []string{"samples", "events", "availability"}

// A DAG that is not a service DAG and has no run history gets 404 with the
// name of the DAG, on the three routes. A DAG that does not exist gets 404.
func TestNoRunHistoryIsNotFound(t *testing.T) {
	t.Parallel()
	p := newProject(t, "plain")
	p.startServer()

	for _, route := range routes {
		e := getJSON[api.Error](p, http.StatusNotFound, "/dags/plain/run-history/"+route, nil)
		assert.Equal(t, api.ErrorCodeNotFound, e.Code, route)
		assert.Equal(t, "DAG plain has no run history", e.Message, route)

		code, _ := p.get("/dags/missing/run-history/"+route, nil)
		assert.Equal(t, http.StatusNotFound, code, route)
	}
}

// A service DAG that never ran has an empty history: 200 with no bucket, no
// event and counts of 0.
func TestEmptyRunHistory(t *testing.T) {
	t.Parallel()
	p := newProject(t, "plain")
	p.startServer()

	assert.Empty(t, p.samples("service_idle", nil).Buckets)
	assert.Empty(t, p.events("service_idle", nil).Events)
	a := p.availability("service_idle")
	assert.Zero(t, a.TotalCount)
	assert.Zero(t, a.Streak)
	assert.Nil(t, a.LastFailureAt)
	for _, w := range a.Windows {
		assert.Zero(t, w.Count)
		assert.Nil(t, w.Availability)
	}
}

// A query parameter out of its range, or from after to, gets 400.
func TestQueryParamsAreValidated(t *testing.T) {
	t.Parallel()
	p := newProject(t, "plain")
	p.startServer()

	bad := []struct {
		route string
		query url.Values
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
	}
	for _, tt := range bad {
		t.Run(tt.route+"?"+tt.query.Encode(), func(t *testing.T) {
			code, body := p.get("/dags/service_idle/run-history/"+tt.route, tt.query)
			assert.Equal(t, http.StatusBadRequest, code, "%s", body)
		})
	}
}

// The activity of the DAGs that a request names gives a service DAG buckets,
// at most 60, and no run. A DAG that is not a service DAG keeps its runs.
func TestActivityInDAGList(t *testing.T) {
	t.Parallel()
	p := newProject(t, "mixed")
	p.startServer()
	p.dagu.RunWithEnv(p.env(), "start", "--run-id=plain-1", "mixed/plain.yaml").ExpectExitCode(0)
	p.startScheduler()

	// last is the page that the poll read last. A poll still in flight when
	// the wait ends can write it, so it is stored atomically.
	var last atomic.Pointer[api.DAGListActivityPage]
	list := func() map[string]*api.DAGListActivity {
		resp := getJSON[api.DAGListActivityPage](p, http.StatusOK, "/dag-list-activity", url.Values{"window": {"86400"}, "fileName": {"service_ok", "plain"}})
		last.Store(&resp)
		byName := map[string]*api.DAGListActivity{}
		for _, entry := range resp.Dags {
			byName[entry.FileName] = &entry.Activity
		}
		return byName
	}
	var activity *api.DAGListActivity
	if !assert.Eventually(t, func() bool {
		activity = list()["service_ok"]
		if activity == nil || activity.Buckets == nil {
			return false
		}
		var n int64
		for _, b := range *activity.Buckets {
			n += b.Count
		}
		return n >= 2
	}, wait, poll, "no buckets in the activity of the service DAG") {
		page, err := json.Marshal(last.Load())
		require.NoError(t, err)
		t.Logf("last activity page: %s", page)
		p.logState("service_ok")
		t.FailNow()
	}

	assert.Empty(t, activity.Runs)
	assert.False(t, activity.Truncated)
	assert.LessOrEqual(t, len(*activity.Buckets), 60)
	require.NotNil(t, activity.ResolutionSeconds)
	assert.Contains(t, []int{1, 60, 3600, 86400}, *activity.ResolutionSeconds)
	// The tier is the one of the samples query with 60 points.
	now := time.Now().UTC()
	series := p.samples("service_ok", url.Values{
		"from":   {now.Add(-24 * time.Hour).Format(time.RFC3339Nano)},
		"to":     {now.Format(time.RFC3339Nano)},
		"points": {"60"},
	})
	assert.Equal(t, series.ResolutionSeconds, *activity.ResolutionSeconds)

	plain := list()["plain"]
	require.NotNil(t, plain)
	require.Len(t, plain.Runs, 1)
	assert.Equal(t, "plain-1", plain.Runs[0].DagRunId)
	assert.Empty(t, plain.Buckets)
	assert.Nil(t, plain.ResolutionSeconds)
}

// A window of three years answers with at most `points` buckets, from the day
// tier.
func TestThreeYearWindow(t *testing.T) {
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()
	p.startScheduler()
	require.Eventually(t, func() bool {
		return p.availability("service_ok").TotalCount >= 1
	}, wait, poll, "no sample")

	const points = 600
	now := time.Now().UTC()
	series := p.samples("service_ok", url.Values{
		"from":   {now.AddDate(-3, 0, 0).Format(time.RFC3339Nano)},
		"to":     {now.Format(time.RFC3339Nano)},
		"points": {"600"},
	})
	assert.Equal(t, 86400, series.ResolutionSeconds)
	assert.NotEmpty(t, series.Buckets)
	assert.LessOrEqual(t, len(series.Buckets), points)
}
