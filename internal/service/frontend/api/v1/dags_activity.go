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
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

const (
	minActivityWindow = time.Minute
	maxActivityWindow = 7 * 24 * time.Hour
	// activityRunLimit bounds the runs that the activity of one DAG lists.
	activityRunLimit = 60
	// activityBucketLimit bounds the buckets that the activity of a service DAG lists.
	activityBucketLimit = 60
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
	now := time.Now().UTC()
	series, err := reader.Samples(ctx, samples.SampleQuery{From: since.UTC(), To: now, MaxPoints: activityBucketLimit})
	if err != nil {
		return activity, fmt.Errorf("read run history of %s: %w", dag.Name, err)
	}
	uptime, err := reader.Uptime(ctx, since.UTC(), now)
	if err != nil {
		return activity, fmt.Errorf("read uptime of %s: %w", dag.Name, err)
	}
	start := activeRunStart(latest)
	last, err := recordedEnd(ctx, reader, start, !uptime.Since.IsZero())
	if err != nil {
		return activity, fmt.Errorf("read run history of %s: %w", dag.Name, err)
	}
	result := toStepSamples(series.WithActive(start, last, now), since, now)
	activity.Buckets = &result.Buckets
	activity.ResolutionSeconds = &result.ResolutionSeconds
	if ratio, ok := uptime.WithActive(start, last).Ratio(); ok {
		activity.Uptime = &ratio
	}
	return activity, nil
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
		// The cached time comes from the reused inputs, which the run index
		// does not keep.
		FullStatus: true,
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
	if cached, ok := reusedTimeMs(status.ReusedInputs); ok {
		run.CachedMs = ptrOf(cached)
	}
	return run
}

// reusedTimeMs sums the times that the reused steps of a run took in their
// source runs, as the timeline of the workspace does. It reports false when
// no reused step has a known time.
func reusedTimeMs(inputs []ir.ReusedInput) (int64, bool) {
	var sum int64
	known := false
	for _, input := range inputs {
		started, finished := runTime(input.StartedAt), runTime(input.FinishedAt)
		if started.IsZero() || finished.IsZero() || finished.Before(started) {
			continue
		}
		sum += finished.Sub(started).Milliseconds()
		known = true
	}
	return sum, known
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
