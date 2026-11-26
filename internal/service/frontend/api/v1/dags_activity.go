// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
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
	// maxActivityFileNames is the most DAGs that one activity request names,
	// so that the request line stays under the 8 KiB that common proxies
	// accept.
	maxActivityFileNames = 100
	// activityScanLimit bounds the runs that the activity of one DAG reads to
	// find the runs that started last: the runs created last since one window
	// before the window.
	activityScanLimit = 1000
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

// GetDAGListActivity returns the activity of the DAGs that fileName names, in
// request order, each once. A name that names no DAG of the list that the
// caller can see is left out.
func (a *API) GetDAGListActivity(ctx context.Context, request api.GetDAGListActivityRequestObject) (api.GetDAGListActivityResponseObject, error) {
	p := request.Params
	window, err := activityWindowFromSeconds(p.Window)
	if err != nil {
		return nil, err
	}
	names, err := activityFileNames(p.FileName)
	if err != nil {
		return nil, err
	}
	page, err := a.dagActivities(ctx, names, window)
	if err != nil {
		return nil, err
	}
	return api.GetDAGListActivity200JSONResponse(page), nil
}

// activityFileNames checks the count of the names of an activity request and
// drops repeats, keeping the first.
func activityFileNames(names []string) ([]string, error) {
	if len(names) == 0 || len(names) > maxActivityFileNames {
		return nil, activityBadRequest(fmt.Sprintf("fileName must be given from 1 to %d times, got %d",
			maxActivityFileNames, len(names)))
	}
	unique := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	return unique, nil
}

// dagActivities gives the activity over the window that ends now of each DAG
// that a name names and the caller can see, in the order of names.
func (a *API) dagActivities(ctx context.Context, names []string, window time.Duration) (api.DAGListActivityPage, error) {
	// The time is taken before any read, so that a change after it has a
	// later time in the change feed than readAt.
	now := time.Now()
	page := api.DAGListActivityPage{
		Dags:   []api.DAGListActivityEntry{},
		Errors: []string{},
		ReadAt: now.UTC().Truncate(time.Millisecond),
	}
	dags, err := a.visibleDAGsByFile(ctx, names)
	if err != nil {
		return api.DAGListActivityPage{}, err
	}
	for _, name := range names {
		dag, ok := dags[name]
		if !ok {
			continue
		}
		activity, err := a.listedDAGActivity(ctx, dag, now, window)
		if err != nil {
			page.Errors = append(page.Errors, err.Error())
		}
		page.Dags = append(page.Dags, api.DAGListActivityEntry{FileName: name, Activity: *activity})
	}
	return page, nil
}

// visibleDAGsByFile returns the DAGs that a name names and the caller can
// see, by file name. It reads the named DAGs only, so a request costs the
// count of its names, not of all DAGs. A name that is not a file name of the
// list, such as a path, names no DAG.
func (a *API) visibleDAGsByFile(ctx context.Context, names []string) (map[string]*ir.DAG, error) {
	fileNames := slices.DeleteFunc(slices.Clone(names), func(name string) bool {
		return name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`)
	})
	dags, err := a.dagRepository.MetadataByIDs(ctx, fileNames)
	if err != nil {
		return nil, fmt.Errorf("error reading DAGs: %w", err)
	}
	filter := a.workspaceFilterForContext(ctx)
	maps.DeleteFunc(dags, func(_ string, dag *ir.DAG) bool {
		return !filter.MatchesLabels(dag.Labels)
	})
	return dags, nil
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

// dagActivity lists the runs of a DAG that started since `since`, the last
// started first, at most activityRunLimit, and the runs still in progress. It
// looks for them among the runs created since one window before `since`, at
// most activityScanLimit created last, so a run that waited in a queue for
// longer shows only while it is the latest run.
// Backfill runs are left out, and so are runs that never started. A service
// DAG gets the buckets of its run history instead. `latest` is the latest
// status that the list already read; it may be empty when the server keeps
// only the runs of today.
func (a *API) dagActivity(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus, since time.Time, window time.Duration) (*api.DAGListActivity, error) {
	if dag.RestartPolicy.Enabled() {
		return a.serviceActivity(ctx, dag, latest, since)
	}
	activity := &api.DAGListActivity{Runs: []api.DAGListActivityRun{}}
	from := since.Add(-window)
	// The latest run is the newest created, and a run starts after it is
	// created, so no run was created since `from` when the latest run started
	// before it and ended.
	if latestStart := runTime(latest.StartedAt); !latestStart.IsZero() && latestStart.Before(from) && !latest.Status.IsActive() {
		return activity, nil
	}
	page, err := a.dagRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
		ExactName: dag.Name,
		From:      persis.NewUTC(from),
		Statuses:  startedRunStatuses,
		// A run can start after runs created after it, as a run started outside
		// the queue does, so the runs created since `from` are read to find the
		// runs that started last.
		Limit: activityScanLimit,
		// The activity cache keeps what the list needs, and these reads would
		// push out of the shared status cache what other pages read.
		SkipStatusCache: true,
		// The list shows a backfill on its own.
		ExcludeBackfill: true,
	})
	if err != nil {
		return activity, fmt.Errorf("list recent runs of %s: %w", dag.Name, err)
	}
	runs := make([]ir.DAGRunStatus, 0, len(page.Items))
	for _, status := range page.Items {
		if inActivity(*status, since) {
			runs = append(runs, *status)
		}
	}
	// A latest run created before `from`, as a retried run can be, shows when
	// it started in the window or is still active. A retry in the queue keeps
	// the start of its previous attempt, but it has not started again.
	listed := slices.ContainsFunc(runs, func(run ir.DAGRunStatus) bool {
		return run.DAGRunID == latest.DAGRunID
	})
	if !listed && latest.Status != ir.Queued && latest.BackfillID == "" && inActivity(latest, since) {
		runs = append(runs, latest)
	}
	slices.SortStableFunc(runs, func(x, y ir.DAGRunStatus) int {
		return runTime(y.StartedAt).Compare(runTime(x.StartedAt))
	})
	// With more runs created than it reads, the activity may miss runs.
	activity.Truncated = len(runs) > activityRunLimit || page.NextCursor != ""
	for _, run := range runs[:min(len(runs), activityRunLimit)] {
		activity.Runs = append(activity.Runs, toListActivityRun(run))
	}
	return activity, nil
}

// inActivity reports whether a run shows in an activity since `since`: it
// started then or later, or it is still in progress. A run canceled in the
// queue ends without a start.
func inActivity(status ir.DAGRunStatus, since time.Time) bool {
	started := runTime(status.StartedAt)
	return !started.IsZero() && (!started.Before(since) || status.Status.IsActive())
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
