// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	// activityWindowSteps is how many equal steps a live window is aligned to.
	activityWindowSteps  = 96
	activityDefaultHours = 24
	// activityMaxLiveHours bounds a window that ends now, which live
	// updates recompute on every run status change.
	activityMaxLiveHours = 48
	// activityMaxRange bounds a fixed window read on request.
	activityMaxRange = 7 * 24 * time.Hour
	// activityPageSize is the number of runs on one page.
	activityPageSize = 100
	// activityAttentionLimit bounds the DAGs listed as needing attention.
	activityAttentionLimit = 20
	// activityLiveRunReads bounds the status files read for live runs that
	// started before the window.
	activityLiveRunReads = 50
	// activityActiveLimit bounds the runs listed as in progress.
	activityActiveLimit   = 100
	activityUpcomingLimit = 5
	activityErrorMaxBytes = 200
	// activityQueueLimit bounds the queues listed with work.
	activityQueueLimit = 20
	// activityDiskFullPercent is the data disk usage, in percent, from which
	// the activity warns that runs may soon fail to persist.
	activityDiskFullPercent = 90
)

// activityBlockedReasons maps the worker reasons a queued run records for
// not starting to the reasons the activity reports.
var activityBlockedReasons = map[string]api.DAGRunActivityQueueBlockedReason{
	ir.DAGRunReasonNoMatchingWorker:          api.DAGRunActivityQueueBlockedReasonNoMatchingWorker,
	ir.DAGRunReasonNoAvailableWorker:         api.DAGRunActivityQueueBlockedReasonNoAvailableWorker,
	ir.DAGRunReasonWorkerDispatchUnavailable: api.DAGRunActivityQueueBlockedReasonWorkerDispatchUnavailable,
}

// activityQueueScan bounds the runs read to describe queues: a few queued
// entries per queue, so the budget reaches many queues, and a fixed number of
// running runs. Deeper backlogs and busier queues are reported as lower
// bounds. Declared as a var so tests can lower it.
var activityQueueScan = queueScan{total: 200, perQueue: 5, running: 200}

// GetDAGRunActivity returns one page of the DAG-runs of a time window.
func (a *API) GetDAGRunActivity(ctx context.Context, request api.GetDAGRunActivityRequestObject) (api.GetDAGRunActivityResponseObject, error) {
	params := request.Params
	query, err := a.newActivityQuery(ctx, activityParams{
		hours:     params.Hours,
		fromDate:  params.FromDate,
		toDate:    params.ToDate,
		labels:    params.Labels,
		workspace: params.Workspace,
		cursor:    params.Cursor,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	activity, err := a.dagRunActivity(ctx, query)
	if err != nil {
		return nil, err
	}
	return api.GetDAGRunActivity200JSONResponse(activity), nil
}

// GetDAGRunActivityData returns the first page of a window that ends now for
// SSE. The identifier is the REST query string without fromDate, toDate and
// cursor.
func (a *API) GetDAGRunActivityData(ctx context.Context, queryString string) (any, error) {
	query, err := a.activityQueryFromString(ctx, queryString)
	if err != nil {
		return nil, err
	}
	return a.dagRunActivity(ctx, query)
}

// ValidateDAGRunActivityQuery returns a 400 *Error when GetDAGRunActivityData
// would reject the query string.
func (a *API) ValidateDAGRunActivityQuery(ctx context.Context, queryString string) error {
	_, err := a.activityQueryFromString(ctx, queryString)
	return err
}

func (a *API) activityQueryFromString(ctx context.Context, queryString string) (activityQuery, error) {
	values, err := url.ParseQuery(queryString)
	if err != nil {
		return activityQuery{}, activityBadRequest(fmt.Sprintf("invalid query parameters: %s", err))
	}
	if values.Has("fromDate") || values.Has("toDate") || values.Has("cursor") {
		return activityQuery{}, activityBadRequest("live activity takes hours, not fromDate, toDate or cursor")
	}
	var params activityParams
	if raw := values.Get("hours"); raw != "" {
		hours, convErr := strconv.Atoi(raw)
		if convErr != nil {
			return activityQuery{}, activityBadRequest(fmt.Sprintf("invalid hours: %q", raw))
		}
		params.hours = &hours
	}
	if raw := values.Get("labels"); raw != "" {
		params.labels = &raw
	}
	params.workspace = workspaceParamFromValues(values)
	return a.newActivityQuery(ctx, params, time.Now())
}

type activityParams struct {
	hours     *int
	fromDate  *int64
	toDate    *int64
	labels    *string
	workspace *api.Workspace
	cursor    *string
}

// activityQuery is a validated activity window, its filters, and the page
// to read.
type activityQuery struct {
	from            time.Time
	to              time.Time
	labels          []string
	workspaceFilter *workspace.WorkspaceFilter
	cursor          string
}

func (a *API) newActivityQuery(ctx context.Context, params activityParams, now time.Time) (activityQuery, error) {
	var query activityQuery
	if params.fromDate != nil {
		query.from = time.Unix(*params.fromDate, 0)
		query.to = now
		if params.toDate != nil {
			query.to = time.Unix(*params.toDate, 0)
		}
		span := query.to.Sub(query.from)
		if span <= 0 || span > activityMaxRange {
			return activityQuery{}, activityBadRequest("the window must end after it starts and span at most 7 days")
		}
	} else {
		hours := activityDefaultHours
		if params.hours != nil {
			hours = *params.hours
		}
		if hours < 1 || hours > activityMaxLiveHours {
			return activityQuery{}, activityBadRequest(fmt.Sprintf("hours must be between 1 and %d", activityMaxLiveHours))
		}
		// Aligning to a step keeps the window, and so the payload, unchanged
		// until the next step.
		window := time.Duration(hours) * time.Hour
		step := window / activityWindowSteps
		query.to = now.Truncate(step).Add(step)
		query.from = query.to.Add(-window)
	}
	query.labels = parseCommaSeparatedLabels(params.labels)
	if params.cursor != nil {
		query.cursor = *params.cursor
	}
	filter, err := a.workspaceFilterForParams(ctx, params.workspace)
	if err != nil {
		return activityQuery{}, err
	}
	query.workspaceFilter = filter
	return query, nil
}

func activityBadRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

func (a *API) dagRunActivity(ctx context.Context, query activityQuery) (api.DAGRunActivity, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-run-activity",
	}, func(readCtx context.Context) (api.DAGRunActivity, error) {
		return a.readDAGRunActivity(readCtx, query)
	})
}

func (a *API) readDAGRunActivity(ctx context.Context, query activityQuery) (api.DAGRunActivity, error) {
	page, err := a.dagRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
		From:            persis.NewUTC(query.from),
		To:              persis.NewUTC(query.to),
		Labels:          query.labels,
		WorkspaceFilter: query.workspaceFilter,
		Limit:           activityPageSize,
		Cursor:          query.cursor,
	})
	if err != nil {
		if badRequest := dagRunListBadRequest(err); badRequest != nil {
			return api.DAGRunActivity{}, badRequest
		}
		return api.DAGRunActivity{}, fmt.Errorf("error reading dag-run activity: %w", err)
	}

	result := api.DAGRunActivity{
		From:      query.from.Unix(),
		To:        query.to.Unix(),
		Runs:      make([]api.DAGRunActivityRun, 0, len(page.Items)),
		Attention: []api.DAGRunActivityRun{},
		Active:    []api.DAGRunActivityRun{},
		Upcoming:  []api.DAGRunActivityUpcoming{},
		Queues:    []api.DAGRunActivityQueue{},
	}
	if page.NextCursor != "" {
		result.NextCursor = &page.NextCursor
	}
	for _, status := range page.Items {
		result.Runs = append(result.Runs, toActivityRun(status))
	}
	if query.cursor != "" {
		return result, nil
	}

	latest, err := a.dagRunRepository.LatestStatuses(ctx, persis.DAGRunLatestStatusesQuery{
		NotBefore:       persis.NewUTC(query.from),
		Labels:          query.labels,
		WorkspaceFilter: query.workspaceFilter,
	})
	if err != nil {
		return api.DAGRunActivity{}, fmt.Errorf("error reading latest dag-runs: %w", err)
	}
	result.Attention = attentionRuns(latest)
	result.Active = a.activeRuns(ctx, query, page.Items, latest)
	result.Upcoming = a.upcomingRuns(ctx, query)
	result.Queues = a.activityQueues(ctx, query)
	result.DiskUsedPercent = a.activityDiskUsedPercent(ctx)
	return result, nil
}

// activityQueues lists the queues with runs running or waiting that match the
// query's filters, the longest wait first. A queue read failure leaves the
// list empty rather than failing the whole summary.
func (a *API) activityQueues(ctx context.Context, query activityQuery) []api.DAGRunActivityQueue {
	queues := []api.DAGRunActivityQueue{}
	scan := activityQueueScan
	labelFilters := parseLabelFilters(query.labels)
	scan.matches = func(summary api.DAGRunSummary) bool {
		labels := ir.NewLabels(valueOf(summary.Labels))
		return query.workspaceFilter.MatchesLabels(labels) && labels.MatchesFilters(labelFilters)
	}
	queueMap, err := a.collectQueues(ctx, "", scan)
	if err != nil {
		logger.Warn(ctx, "Failed to read queues for activity", tag.Error(err))
		return queues
	}
	for _, info := range queueMap {
		// Only measured work earns a line. A queue the budget did not reach,
		// or whose runs the filters left out, has none.
		if len(info.running) == 0 && info.queuedCount == 0 {
			continue
		}
		item := api.DAGRunActivityQueue{
			Name:    info.name,
			Running: len(info.running),
			Queued:  info.queuedCount,
		}
		if info.maxConcurrency > 0 {
			item.MaxConcurrency = new(info.maxConcurrency)
		}
		if info.queuedCountCapped {
			item.QueuedCapped = new(true)
		}
		if info.runningCapped {
			item.RunningCapped = new(true)
		}
		if !info.nextQueuedAt.IsZero() {
			item.NextQueuedAt = new(info.nextQueuedAt.Unix())
		}
		if reason, ok := activityBlockedReasons[info.workerBlockReason]; ok {
			item.BlockedReason = &reason
		}
		queues = append(queues, item)
	}
	slices.SortFunc(queues, func(left, right api.DAGRunActivityQueue) int {
		// A queue with a waiting run sorts by how long it has waited.
		leftWait, rightWait := left.NextQueuedAt != nil, right.NextQueuedAt != nil
		if leftWait != rightWait {
			if leftWait {
				return -1
			}
			return 1
		}
		return cmp.Or(
			cmp.Compare(valueOf(left.NextQueuedAt), valueOf(right.NextQueuedAt)),
			cmp.Compare(right.Running, left.Running),
			cmp.Compare(left.Name, right.Name),
		)
	})
	return queues[:min(len(queues), activityQueueLimit)]
}

// activityDiskUsedPercent returns how full the data directory's disk is when
// it is nearly full and the caller may view system status. Whole percents
// keep the live payload unchanged while usage creeps.
func (a *API) activityDiskUsedPercent(ctx context.Context) *int {
	if a.resourceService == nil || a.requireDeveloperOrAbove(ctx) != nil {
		return nil
	}
	used, ok := a.resourceService.DiskUsedPercent()
	if !ok || used < activityDiskFullPercent {
		return nil
	}
	return new(int(used))
}

// toActivityRun summarizes a run. A run that has not started reports its
// queue time, or its creation time, as startedAt.
func toActivityRun(status *ir.DAGRunStatus) api.DAGRunActivityRun {
	result := api.DAGRunActivityRun{
		Name:     status.Name,
		DagRunId: status.DAGRunID,
		Status:   api.Status(status.Status),
	}
	if started, err := stringutil.ParseTime(status.StartedAt); err == nil && !started.IsZero() {
		result.StartedAt = new(started.Unix())
	} else if queued, err := stringutil.ParseTime(status.QueuedAt); err == nil && !queued.IsZero() {
		result.StartedAt = new(queued.Unix())
	} else if status.CreatedAt > 0 {
		result.StartedAt = new(time.UnixMilli(status.CreatedAt).Unix())
	}
	if finished, err := stringutil.ParseTime(status.FinishedAt); err == nil && !finished.IsZero() {
		result.FinishedAt = new(finished.Unix())
	}
	return result
}

func compareActivityRunsNewestFirst(left, right api.DAGRunActivityRun) int {
	if c := cmp.Compare(valueOf(right.StartedAt), valueOf(left.StartedAt)); c != 0 {
		return c
	}
	if c := cmp.Compare(left.Name, right.Name); c != 0 {
		return c
	}
	return cmp.Compare(left.DagRunId, right.DagRunId)
}

func isUnsuccessfulStatus(status ir.Status) bool {
	switch status {
	case ir.Failed, ir.Aborted, ir.Rejected, ir.PartiallySucceeded:
		return true
	case ir.NotStarted, ir.Running, ir.Succeeded, ir.Queued, ir.Waiting:
		return false
	}
	return false
}

// attentionRuns returns the latest runs that did not succeed, newest first,
// with the step that failed.
func attentionRuns(latest []*ir.DAGRunStatus) []api.DAGRunActivityRun {
	runs := make([]api.DAGRunActivityRun, 0)
	for _, status := range latest {
		if !isUnsuccessfulStatus(status.Status) {
			continue
		}
		run := toActivityRun(status)
		step, message := firstFailure(status)
		if step != "" {
			run.FailedStep = &step
		}
		if message != "" {
			run.Error = &message
		}
		runs = append(runs, run)
	}
	slices.SortFunc(runs, compareActivityRunsNewestFirst)
	return runs[:min(len(runs), activityAttentionLimit)]
}

// firstFailure returns the first failed step in run order and its error, or
// the run-level error when no step failed.
func firstFailure(status *ir.DAGRunStatus) (string, string) {
	for _, node := range status.NodesInRunOrder() {
		if node != nil && node.Status == ir.NodeFailed {
			return node.Step.Name, shortError(node.Error)
		}
	}
	return "", shortError(status.Error)
}

func shortError(message string) string {
	message = strings.TrimSpace(stringutil.StripANSI(message))
	if first, _, found := strings.Cut(message, "\n"); found {
		message = first
	}
	return stringutil.TruncUTF8Bytes(message, activityErrorMaxBytes)
}

// activeRuns lists runs that are still in progress, oldest first: those on
// the first page, the latest run of each DAG, and live processes of runs
// created before the window. Waiting runs have no process, so the latest runs
// are what keep them listed.
func (a *API) activeRuns(ctx context.Context, query activityQuery, pageRuns, latest []*ir.DAGRunStatus) []api.DAGRunActivityRun {
	active := make([]api.DAGRunActivityRun, 0)
	seen := make(map[ir.DAGRunRef]struct{})
	add := func(status *ir.DAGRunStatus) {
		ref := ir.NewDAGRunRef(status.Name, status.DAGRunID)
		if _, ok := seen[ref]; ok || !isActiveRunStatus(status.Status) {
			return
		}
		seen[ref] = struct{}{}
		run := toActivityRun(status)
		if status.ProcGroup != "" {
			run.Queue = &status.ProcGroup
		}
		active = append(active, run)
	}
	for _, status := range pageRuns {
		add(status)
	}
	for _, status := range latest {
		add(status)
	}

	if a.procRepository != nil {
		alive, err := a.procRepository.ListAllAlive(ctx)
		if err == nil {
			labelFilters := parseLabelFilters(query.labels)
			reads := 0
		groups:
			for _, group := range slices.Sorted(maps.Keys(alive)) {
				for _, ref := range alive[group] {
					if _, ok := seen[ref]; ok {
						continue
					}
					if reads == activityLiveRunReads {
						break groups
					}
					reads++
					status, err := a.readLiveRunStatus(ctx, ref)
					if err != nil {
						continue
					}
					labels := ir.NewLabels(status.Labels)
					if !query.workspaceFilter.MatchesLabels(labels) || !labels.MatchesFilters(labelFilters) {
						continue
					}
					add(status)
				}
			}
		}
	}

	slices.SortFunc(active, func(left, right api.DAGRunActivityRun) int {
		return compareActivityRunsNewestFirst(right, left)
	})
	return active[:min(len(active), activityActiveLimit)]
}

// readLiveRunStatus reads the saved status of a run whose process heartbeat
// is fresh, without querying the process itself.
func (a *API) readLiveRunStatus(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(ctx)
}

func isActiveRunStatus(status ir.Status) bool {
	switch status {
	case ir.Running, ir.Queued, ir.Waiting:
		return true
	case ir.NotStarted, ir.Failed, ir.Aborted, ir.Succeeded, ir.PartiallySucceeded, ir.Rejected:
		return false
	}
	return false
}

func parseLabelFilters(labels []string) []ir.LabelFilter {
	filters := make([]ir.LabelFilter, 0, len(labels))
	for _, label := range labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			filters = append(filters, ir.ParseLabelFilter(trimmed))
		}
	}
	return filters
}

// upcomingRuns returns the next scheduled runs of unsuspended DAGs.
func (a *API) upcomingRuns(ctx context.Context, query activityQuery) []api.DAGRunActivityUpcoming {
	upcoming := make([]api.DAGRunActivityUpcoming, 0, activityUpcomingLimit)
	if a.dagRepository == nil {
		return upcoming
	}
	now := time.Now()
	projection := a.nextRunProjection(ctx)
	page := pagination.NewPaginator(1, activityUpcomingLimit)
	result, _, err := a.dagRepository.List(ctx, persis.DAGListOptions{
		Paginator:         &page,
		Labels:            query.labels,
		ActiveOnly:        true,
		Sort:              "nextRun",
		Order:             "asc",
		Time:              &now,
		NextRunProjection: projection,
		WorkspaceFilter:   query.workspaceFilter,
	})
	if err != nil {
		return upcoming
	}
	for _, item := range result.Items {
		nextRun := projection(item.DAG, now)
		if nextRun.IsZero() {
			continue
		}
		upcoming = append(upcoming, api.DAGRunActivityUpcoming{
			Name:     item.Name,
			FileName: item.ID,
			NextRun:  nextRun.Unix(),
		})
	}
	return upcoming
}
