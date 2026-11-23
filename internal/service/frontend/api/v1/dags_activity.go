// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
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
	// maxActivityPageSize is the most DAGs whose activity one request reads,
	// the size of the largest DAG list page.
	maxActivityPageSize = 200
)

// startedRunStatuses are the statuses of a run that can have started. A
// queued run has not, so it takes no place among the runs of an activity.
var startedRunStatuses = []ir.Status{
	ir.Running, ir.Waiting, ir.Succeeded, ir.PartiallySucceeded, ir.Failed, ir.Aborted, ir.Rejected,
}

// activityWindowFromSeconds checks the window of an activity request.
func activityWindowFromSeconds(seconds int) (time.Duration, error) {
	window := time.Duration(seconds) * time.Second
	if window < minActivityWindow || window > maxActivityWindow {
		return 0, activityBadRequest(fmt.Sprintf("window must be from %d to %d seconds, got %d",
			int(minActivityWindow.Seconds()), int(maxActivityWindow.Seconds()), seconds))
	}
	return window, nil
}

// activityWindowFromQuery reads the window from the query string of an
// activity topic.
func activityWindowFromQuery(params url.Values) (time.Duration, error) {
	raw := params.Get("window")
	if raw == "" {
		return 0, activityBadRequest("window is required")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, activityBadRequest(fmt.Sprintf("window must be an integer, got %q", raw))
	}
	return activityWindowFromSeconds(seconds)
}

// GetDAGListActivity returns the activity of the DAGs of one DAG list page.
func (a *API) GetDAGListActivity(ctx context.Context, request api.GetDAGListActivityRequestObject) (api.GetDAGListActivityResponseObject, error) {
	p := request.Params
	window, err := activityWindowFromSeconds(p.Window)
	if err != nil {
		return nil, err
	}
	listOpts, err := a.dagListOptions(ctx, dagListParams{
		page: p.Page, perPage: p.PerPage, workspace: p.Workspace,
		name: p.Name, labels: p.Labels, tags: p.Tags, active: p.Active,
		sort: (*string)(p.Sort), order: (*string)(p.Order),
	})
	if err != nil {
		return nil, err
	}
	if err := checkActivityPage(listOpts); err != nil {
		return nil, err
	}
	page, err := a.dagListActivity(ctx, listOpts, window)
	if err != nil {
		return nil, err
	}
	return api.GetDAGListActivity200JSONResponse(page), nil
}

// GetDAGListActivityData returns the activity of a DAG list page for SSE. The
// identifier is the query string of the list topic of the page, with window.
func (a *API) GetDAGListActivityData(ctx context.Context, queryString string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-list-activity",
	}, func(readCtx context.Context) (any, error) {
		listOpts, window, err := a.dagListActivityQuery(readCtx, queryString)
		if err != nil {
			return nil, err
		}
		return a.dagListActivity(readCtx, listOpts, window)
	})
}

// ValidateDAGListActivityQuery returns a 400 *Error when
// GetDAGListActivityData would reject the query string.
func (a *API) ValidateDAGListActivityQuery(ctx context.Context, queryString string) error {
	_, _, err := a.dagListActivityQuery(ctx, queryString)
	return err
}

// dagListActivityQuery reads the list options and the window of an activity
// topic.
func (a *API) dagListActivityQuery(ctx context.Context, queryString string) (persis.DAGListOptions, time.Duration, error) {
	params, err := url.ParseQuery(queryString)
	if err != nil {
		return persis.DAGListOptions{}, 0, activityBadRequest(fmt.Sprintf("invalid query parameters: %s", err))
	}
	window, err := activityWindowFromQuery(params)
	if err != nil {
		return persis.DAGListOptions{}, 0, err
	}
	if sort := params.Get("sort"); sort != "" && !api.GetDAGListActivityParamsSort(sort).Valid() {
		return persis.DAGListOptions{}, 0, activityBadRequest(fmt.Sprintf("sort must be name, nextRun, status, or lastRun, got %q", sort))
	}
	if order := params.Get("order"); order != "" && !api.GetDAGListActivityParamsOrder(order).Valid() {
		return persis.DAGListOptions{}, 0, activityBadRequest(fmt.Sprintf("order must be asc or desc, got %q", order))
	}
	listOpts, err := a.buildDAGListOptions(ctx, queryString)
	if err != nil {
		return persis.DAGListOptions{}, 0, err
	}
	if err := checkActivityPage(listOpts); err != nil {
		return persis.DAGListOptions{}, 0, err
	}
	return listOpts, window, nil
}

// checkActivityPage rejects a page that is larger than a DAG list page.
func checkActivityPage(listOpts persis.DAGListOptions) error {
	if listOpts.Paginator.Limit() > maxActivityPageSize {
		return activityBadRequest(fmt.Sprintf("perPage must be at most %d", maxActivityPageSize))
	}
	return nil
}

// dagListActivity gives the activity of each DAG of the DAG list page that
// listOpts select, in page order. The page, its order, and its errors are
// those of the DAG list with the same options.
func (a *API) dagListActivity(ctx context.Context, listOpts persis.DAGListOptions, window time.Duration) (api.DAGListActivityPage, error) {
	now := time.Now()
	page := api.DAGListActivityPage{Dags: []api.DAGListActivityEntry{}}
	var activityErrs []string
	list, err := a.listDAGsDataWithSearchPaths(ctx, listOpts, dagListSource{
		pinnedFirst:   true,
		skipBackfills: true,
		visit: func(item persis.DAGListItem, latest ir.DAGRunStatus) {
			activity, err := a.cachedDAGActivity(ctx, item.DAG, latest, now, window)
			if err != nil {
				activityErrs = append(activityErrs, err.Error())
			}
			page.Dags = append(page.Dags, api.DAGListActivityEntry{FileName: item.ID, Activity: *activity})
		},
	})
	if err != nil {
		return api.DAGListActivityPage{}, err
	}
	page.Errors = append(list.Errors, activityErrs...)
	return page, nil
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
	runs, more, err := a.startedRuns(ctx, dag.Name, persis.NewUTC(since), persis.TimeInUTC{}, activityRunLimit, nil)
	if err != nil {
		return activity, fmt.Errorf("list recent runs of %s: %w", dag.Name, err)
	}
	if !more {
		// A run created before the window starts in it after a wait in the
		// queue. Runs leave a queue in the order they entered it, so the first
		// older run that started before the window and ended ends the search.
		// A retry of an older run enters the queue again, so it shows only
		// while it is the latest run.
		older, olderMore, err := a.startedRuns(ctx, dag.Name, persis.NewUTC(since.Add(-maxActivityWindow)), persis.NewUTC(since),
			activityRunLimit-len(runs), func(status *ir.DAGRunStatus) bool {
				return runTime(status.StartedAt).Before(since) && !status.Status.IsActive()
			})
		if err != nil {
			return activity, fmt.Errorf("list waited runs of %s: %w", dag.Name, err)
		}
		runs, more = append(runs, older...), olderMore
	}
	for _, run := range runs {
		activity.Runs = append(activity.Runs, toListActivityRun(run))
	}
	activity.Truncated = more
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

// startedRuns pages through the runs of a DAG created from `from` and before
// `to`, newest created first, and keeps the runs that started, at most limit.
// It ends at the first started run that stop reports true for. It reports
// whether more started runs remain.
func (a *API) startedRuns(ctx context.Context, dagName string, from, to persis.TimeInUTC, limit int, stop func(*ir.DAGRunStatus) bool) ([]ir.DAGRunStatus, bool, error) {
	opts := persis.DAGRunListOptions{
		ExactName: dagName,
		From:      from,
		To:        to,
		Statuses:  startedRunStatuses,
		Limit:     activityRunLimit,
		// The activity cache keeps what the list needs, and these reads would
		// push out of the shared status cache what other pages read.
		SkipStatusCache: true,
		// The list shows a backfill on its own.
		ExcludeBackfill: true,
	}
	var runs []ir.DAGRunStatus
	for {
		page, err := a.dagRunRepository.ListStatusesPage(ctx, opts)
		if err != nil {
			return runs, false, err
		}
		for _, status := range page.Items {
			if runTime(status.StartedAt).IsZero() {
				// A run canceled in the queue ends without a start.
				continue
			}
			if stop != nil && stop(status) {
				return runs, false, nil
			}
			if len(runs) == limit {
				return runs, true, nil
			}
			runs = append(runs, *status)
		}
		if page.NextCursor == "" {
			return runs, false, nil
		}
		opts.Cursor = page.NextCursor
	}
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
