// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/samples"
	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	minActivityWindow = time.Minute
	maxActivityWindow = 7 * 24 * time.Hour
	// activityRunLimit bounds the runs that the activity of one DAG lists.
	activityRunLimit = 60
	// activityBucketLimit bounds the buckets that the activity of a service DAG lists.
	activityBucketLimit = 60
	// activityCacheSize bounds the DAGs whose activity lists keep.
	activityCacheSize = 1000
	// activityCacheMaxAge bounds how long lists reuse a kept activity.
	activityCacheMaxAge = 30 * time.Second
	// serviceActivityMaxAge bounds how long lists reuse the activity of a
	// service DAG, whose runs update the list at most once in 10 seconds.
	serviceActivityMaxAge = 10 * time.Second
)

// startedRunStatuses are the statuses of a run that can have started. A
// queued run has not, so it takes no place among the runs of an activity.
var startedRunStatuses = []ir.Status{
	ir.Running, ir.Waiting, ir.Succeeded, ir.PartiallySucceeded, ir.Failed, ir.Aborted, ir.Rejected,
}

// activityWindowFromSeconds checks the activityWindow parameter of a list.
func activityWindowFromSeconds(seconds int) (time.Duration, error) {
	window := time.Duration(seconds) * time.Second
	if window < minActivityWindow || window > maxActivityWindow {
		return 0, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("activityWindow must be from %d to %d seconds, got %d",
				int(minActivityWindow.Seconds()), int(maxActivityWindow.Seconds()), seconds),
		}
	}
	return window, nil
}

// activityWindowFromQuery reads activityWindow from the query string of an
// SSE list. It returns 0 when the parameter is absent.
func activityWindowFromQuery(params url.Values) (time.Duration, error) {
	raw := params.Get("activityWindow")
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("activityWindow must be an integer, got %q", raw),
		}
	}
	return activityWindowFromSeconds(seconds)
}

// serviceActivity gives the activity of a service DAG as buckets of its run
// history since `since`, and the uptime of the window. It does not read a run
// record, so its cost does not depend on the count of runs. `latest` is the
// latest status that the list already read: a run in progress counts in the
// buckets and in the uptime.
func (a *API) serviceActivity(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus, since time.Time) (*api.DAGListActivity, error) {
	// Buckets is always present for a service DAG, also when it has no history.
	activity := &api.DAGListActivity{Runs: []api.DAGListActivityRun{}, Buckets: &[]api.SampleBucket{}}
	reader, ok := a.runHistoryReader(dag)
	if !ok {
		return activity, nil
	}
	query := samples.SampleQuery{From: since.UTC(), To: time.Now().UTC(), MaxPoints: activityBucketLimit}
	result, err := readRunHistorySamples(ctx, reader, query, activeRunStart(latest))
	if err != nil {
		return activity, fmt.Errorf("read run history of %s: %w", dag.Name, err)
	}
	activity.Buckets = &result.Buckets
	activity.ResolutionSeconds = &result.ResolutionSeconds
	activity.Uptime = result.Uptime
	return activity, nil
}

// listActivity gives the activity of a DAG over the window that ends at now.
// It reuses the activity that an earlier list read while the latest run of
// the DAG is the same, so that a list reads the runs only of the DAGs whose
// latest run changed.
func (a *API) listActivity(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus, now time.Time, window time.Duration) (*api.DAGListActivity, error) {
	since := now.Add(-window)
	key := activityKey{dag: dag.Name, window: window, service: dag.RestartPolicy.Enabled()}
	if activity, ok := a.activities.get(key, latest, now); ok {
		return withRunsSince(activity, since), nil
	}
	activity, err := a.dagActivity(ctx, dag, latest, since)
	if err == nil {
		a.activities.put(key, latest, now, activity)
	}
	return activity, err
}

// activityCache keeps the activity of each listed DAG with the latest run
// that it was read for. A kept activity is reused for at most one bucket of
// its window, and at most activityCacheMaxAge. That bounds the changes that
// the latest run does not show, such as the retry of an older run. A service
// DAG starts a run each few seconds, so its activity is reused whatever its
// latest run, for at most serviceActivityMaxAge.
type activityCache struct {
	once    sync.Once
	entries *lru.Cache[activityKey, keptActivity]
}

type activityKey struct {
	dag     string
	window  time.Duration
	service bool
}

type keptActivity struct {
	latest   latestRunKey
	readAt   time.Time
	activity *api.DAGListActivity
}

// latestRunKey changes with each change of the latest run of a DAG.
type latestRunKey struct {
	dagRunID   string
	attemptID  string
	status     ir.Status
	startedAt  string
	finishedAt string
}

func latestRunKeyOf(status ir.DAGRunStatus) latestRunKey {
	return latestRunKey{
		dagRunID:   status.DAGRunID,
		attemptID:  status.AttemptID,
		status:     status.Status,
		startedAt:  status.StartedAt,
		finishedAt: status.FinishedAt,
	}
}

func (c *activityCache) store() *lru.Cache[activityKey, keptActivity] {
	c.once.Do(func() {
		c.entries, _ = lru.New[activityKey, keptActivity](activityCacheSize)
	})
	return c.entries
}

// get returns the activity kept for key when it is still recent at now and,
// for a plain DAG, was read for the same latest run.
func (c *activityCache) get(key activityKey, latest ir.DAGRunStatus, now time.Time) (*api.DAGListActivity, bool) {
	kept, ok := c.store().Get(key)
	if !ok || (!key.service && kept.latest != latestRunKeyOf(latest)) {
		return nil, false
	}
	if age := now.Sub(kept.readAt); age < 0 || age >= key.maxAge() {
		return nil, false
	}
	return kept.activity, true
}

// maxAge is how long lists reuse the activity of the key: one bucket of its
// window, and at most activityCacheMaxAge, or serviceActivityMaxAge for a
// service DAG.
func (key activityKey) maxAge() time.Duration {
	limit := activityCacheMaxAge
	if key.service {
		limit = serviceActivityMaxAge
	}
	return min(limit, key.window/activityBucketLimit)
}

// put keeps an activity read for the latest run at now, unless it is the
// activity of a plain DAG that can change while the latest run stays the same.
func (c *activityCache) put(key activityKey, latest ir.DAGRunStatus, now time.Time, activity *api.DAGListActivity) {
	if !key.service && changesUnseen(latest, activity) {
		c.store().Remove(key)
		return
	}
	c.store().Add(key, keptActivity{latest: latestRunKeyOf(latest), readAt: now, activity: activity})
}

// changesUnseen reports whether the activity of a plain DAG can change while
// its latest run stays the same: when an older run is in progress, or when the
// latest run is queued, since the runs queued before it can start.
func changesUnseen(latest ir.DAGRunStatus, activity *api.DAGListActivity) bool {
	if latest.Status == ir.Queued {
		return true
	}
	return slices.ContainsFunc(activity.Runs, func(run api.DAGListActivityRun) bool {
		return run.DagRunId != latest.DAGRunID && ir.Status(run.Status).IsActive()
	})
}

// withRunsSince leaves out the runs that ended and started before since, as
// the window moved on since the activity was read. It does not change the
// kept activity.
func withRunsSince(activity *api.DAGListActivity, since time.Time) *api.DAGListActivity {
	before := func(run api.DAGListActivityRun) bool {
		return runTime(run.StartedAt).Before(since) && !ir.Status(run.Status).IsActive()
	}
	if !slices.ContainsFunc(activity.Runs, before) {
		return activity
	}
	moved := *activity
	moved.Runs = slices.DeleteFunc(slices.Clone(activity.Runs), before)
	return &moved
}

// dagActivity lists the runs of a DAG that started since `since`, newest
// first, and its latest run while it is active. Backfill runs are left out.
// A service DAG gets the buckets of its run history instead. It reads only
// the runs created in the window, and none when the latest run ended before
// it. `latest` is the latest status that the list already read; it may be
// empty when the server keeps only the runs of today.
func (a *API) dagActivity(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus, since time.Time) (*api.DAGListActivity, error) {
	if dag.RestartPolicy.Enabled() {
		return a.serviceActivity(ctx, dag, latest, since)
	}
	activity := &api.DAGListActivity{Runs: []api.DAGListActivityRun{}}
	latestStart := runTime(latest.StartedAt)
	before := !latestStart.IsZero() && latestStart.Before(since)
	if before && !latest.Status.IsActive() {
		return activity, nil
	}
	page, err := a.dagRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
		ExactName: dag.Name,
		From:      persis.NewUTC(since),
		Statuses:  startedRunStatuses,
		Limit:     activityRunLimit,
		// The activity cache keeps what the list needs, and these reads would
		// push out of the shared status cache what other pages read.
		SkipStatusCache: true,
		// A backfill run is not the latest run of its DAG either.
		ExcludeBackfill: true,
	})
	if err != nil {
		return activity, fmt.Errorf("list recent runs of %s: %w", dag.Name, err)
	}
	for _, status := range page.Items {
		if runTime(status.StartedAt).IsZero() {
			// A queued run that is aborted ends without a start.
			continue
		}
		activity.Runs = append(activity.Runs, toListActivityRun(*status))
	}
	activity.Truncated = page.NextCursor != ""
	// The list holds the runs created in the window. A latest run created
	// before it, as a queued or retried run can be, shows when it started in
	// the window or is still active. A retry in the queue keeps the start of
	// its previous attempt, but it has not started again, so it does not show.
	listed := slices.ContainsFunc(activity.Runs, func(run api.DAGListActivityRun) bool {
		return run.DagRunId == latest.DAGRunID
	})
	if !latestStart.IsZero() && latest.Status != ir.Queued && latest.BackfillID == "" && !listed &&
		len(activity.Runs) < activityRunLimit {
		activity.Runs = append(activity.Runs, toListActivityRun(latest))
	}
	slices.SortStableFunc(activity.Runs, func(x, y api.DAGListActivityRun) int {
		return runTime(y.StartedAt).Compare(runTime(x.StartedAt))
	})
	return activity, nil
}

func toListActivityRun(status ir.DAGRunStatus) api.DAGListActivityRun {
	run := api.DAGListActivityRun{
		DagRunId:  status.DAGRunID,
		Status:    api.Status(status.Status),
		StartedAt: status.StartedAt,
	}
	if !runTime(status.FinishedAt).IsZero() {
		run.FinishedAt = ptrOf(status.FinishedAt)
	}
	return run
}

// runTime parses a run timestamp; the zero time stands for an empty or
// unreadable value.
func runTime(value string) time.Time {
	t, err := stringutil.ParseTime(value)
	if err != nil {
		return time.Time{}
	}
	return t
}
