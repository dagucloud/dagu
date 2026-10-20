// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	alwaysOnHistoryDefaultBuckets = 96
	alwaysOnHistoryMaxBuckets     = 200
	alwaysOnHistoryMaxRange       = 31 * 24 * time.Hour
	// alwaysOnHistoryRunCap bounds the runs read for one DAG. An always-on DAG
	// that restarts every few seconds makes thousands of runs a day. The runs
	// are added up while they are read, so the cap bounds the time of the read
	// and not the memory.
	alwaysOnHistoryRunCap  = 150000
	alwaysOnHistoryPage    = 1000
	alwaysOnHistoryTTL     = 30 * time.Second
	alwaysOnHistoryMaxKeys = 64
)

// alwaysOnHistoryCache keeps recent answers, so that several viewers and the
// live refresh of a page do not read the same runs again. The window of a
// live view is aligned, so equal views share a key.
type alwaysOnHistoryCache struct {
	mu      sync.Mutex
	entries map[string]alwaysOnHistoryEntry
}

type alwaysOnHistoryEntry struct {
	at   time.Time
	resp api.AlwaysOnHistory
}

func (c *alwaysOnHistoryCache) get(key string, now time.Time) (api.AlwaysOnHistory, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.Sub(entry.at) > alwaysOnHistoryTTL {
		return api.AlwaysOnHistory{}, false
	}
	return entry.resp, true
}

func (c *alwaysOnHistoryCache) put(key string, now time.Time, resp api.AlwaysOnHistory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]alwaysOnHistoryEntry)
	}
	for k, entry := range c.entries {
		if now.Sub(entry.at) > alwaysOnHistoryTTL {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= alwaysOnHistoryMaxKeys {
		return
	}
	c.entries[key] = alwaysOnHistoryEntry{at: now, resp: resp}
}

// GetAlwaysOnDAGsHistory summarizes the runs of the always-on DAGs over a window.
func (a *API) GetAlwaysOnDAGsHistory(ctx context.Context, request api.GetAlwaysOnDAGsHistoryRequestObject) (api.GetAlwaysOnDAGsHistoryResponseObject, error) {
	params := request.Params
	from, to := time.Unix(params.FromDate, 0), time.Unix(params.ToDate, 0)
	span := to.Sub(from)
	if span <= 0 || span > alwaysOnHistoryMaxRange {
		return nil, alwaysOnHistoryBadRequest("the window must end after it starts and span at most 31 days")
	}
	buckets := alwaysOnHistoryDefaultBuckets
	if params.Buckets != nil {
		buckets = *params.Buckets
	}
	if buckets < 1 || buckets > alwaysOnHistoryMaxBuckets {
		return nil, alwaysOnHistoryBadRequest(fmt.Sprintf("buckets must be between 1 and %d", alwaysOnHistoryMaxBuckets))
	}
	filter, err := a.workspaceFilterForParams(ctx, params.Workspace)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	key := fmt.Sprintf("%d|%d|%d|%v|%s", params.FromDate, params.ToDate, buckets, valueOf(params.Workspace), workspaceFilterKey(filter))
	if cached, ok := a.alwaysOnHistory.get(key, now); ok {
		return api.GetAlwaysOnDAGsHistory200JSONResponse(cached), nil
	}
	resp, err := withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/always-on-dags/history",
	}, func(readCtx context.Context) (api.AlwaysOnHistory, error) {
		return a.alwaysOnDAGsHistory(readCtx, filter, from, to, buckets, now)
	})
	if err != nil {
		return nil, err
	}
	a.alwaysOnHistory.put(key, now, resp)
	return api.GetAlwaysOnDAGsHistory200JSONResponse(resp), nil
}

// workspaceFilterKey names the workspaces that the caller may see, so a cached
// response is never shared with a caller that sees other workspaces.
func workspaceFilterKey(filter *workspace.WorkspaceFilter) string {
	if filter == nil || !filter.Enabled {
		return "all"
	}
	names := slices.Clone(filter.Workspaces)
	slices.Sort(names)
	return fmt.Sprintf("%v|%t", names, filter.IncludeUnlabelled)
}

func alwaysOnHistoryBadRequest(message string) *Error {
	return &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: message}
}

func (a *API) alwaysOnDAGsHistory(
	ctx context.Context,
	filter *workspace.WorkspaceFilter,
	from, to time.Time,
	buckets int,
	now time.Time,
) (api.AlwaysOnHistory, error) {
	paginator := pagination.NewPaginator(1, int(^uint(0)>>1))
	result, _, err := a.dagRepository.List(ctx, persis.DAGListOptions{
		Paginator:       &paginator,
		OnlyAlwaysOn:    true,
		Sort:            "name",
		Order:           "asc",
		WorkspaceFilter: filter,
	})
	if err != nil {
		return api.AlwaysOnHistory{}, fmt.Errorf("error listing always-on DAGs: %w", err)
	}

	bucketSec := int((to.Sub(from).Seconds() + float64(buckets) - 1) / float64(buckets))
	resp := api.AlwaysOnHistory{
		From:      from.Unix(),
		To:        to.Unix(),
		BucketSec: bucketSec,
		Dags:      make([]api.AlwaysOnDAGHistory, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		acc := newBucketAccumulator(from, to, bucketSec, now)
		coveredFrom, capped, err := a.readAlwaysOnRuns(ctx, item.Name, from, to, acc.add)
		if err != nil {
			return api.AlwaysOnHistory{}, err
		}
		resp.Dags = append(resp.Dags, api.AlwaysOnDAGHistory{
			FileName:    item.ID,
			Name:        item.Name,
			Buckets:     acc.buckets(),
			Capped:      capped,
			CoveredFrom: coveredFrom.Unix(),
		})
	}
	return resp, nil
}

// alwaysOnRun is a run reduced to what the summary needs.
type alwaysOnRun struct {
	start, end time.Time
	// ended is false while the run is alive.
	ended  bool
	failed bool
}

// readAlwaysOnRuns passes the runs of a DAG that started in the window to add,
// newest first, and then the newest run before the window, which may still be
// alive inside it. Nothing is kept: the caller adds the runs up as they come.
// The read stops at the run cap. It then returns capped and the start of the
// oldest run that it passed, and the window before that instant is not covered.
// When the read is complete, coveredFrom is the start of the window.
func (a *API) readAlwaysOnRuns(
	ctx context.Context,
	name string,
	from, to time.Time,
	add func(alwaysOnRun),
) (coveredFrom time.Time, capped bool, err error) {
	var (
		cursor string
		count  int
		oldest time.Time
	)
	for {
		page, err := a.dagRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
			ExactName: name,
			From:      persis.NewUTC(from),
			To:        persis.NewUTC(to),
			Limit:     alwaysOnHistoryPage,
			Cursor:    cursor,
		})
		if err != nil {
			return time.Time{}, false, fmt.Errorf("error reading runs of %s: %w", name, err)
		}
		for _, status := range page.Items {
			if run, ok := toAlwaysOnRun(status); ok {
				add(run)
				oldest = run.start
				count++
			}
		}
		if page.NextCursor == "" {
			break
		}
		if count >= alwaysOnHistoryRunCap {
			return oldest, true, nil
		}
		cursor = page.NextCursor
	}

	// Without a lower bound the listing would stop at the start of today.
	before, err := a.dagRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
		ExactName:  name,
		To:         persis.NewUTC(from),
		AllHistory: true,
		Limit:      1,
	})
	if err != nil {
		return time.Time{}, false, fmt.Errorf("error reading runs of %s: %w", name, err)
	}
	for _, status := range before.Items {
		if run, ok := toAlwaysOnRun(status); ok {
			add(run)
		}
	}
	return from, false, nil
}

func toAlwaysOnRun(status *ir.DAGRunStatus) (alwaysOnRun, bool) {
	start := parseRunTime(status.StartedAt)
	if start == nil {
		return alwaysOnRun{}, false
	}
	run := alwaysOnRun{
		start:  *start,
		failed: status.Status == ir.Failed || status.Status == ir.Rejected,
	}
	if end := parseRunTime(status.FinishedAt); end != nil && !isActiveRunStatus(status.Status) {
		run.end, run.ended = *end, true
	}
	return run, true
}

// bucketRuns splits the window into buckets of bucketSec seconds and counts,
// for each, the runs that started in it, how many of them failed, the seconds
// in which a run was alive, and the wait between a run that ended in the
// bucket and the next run. A run that is still alive counts up to now.
func bucketRuns(runs []alwaysOnRun, from, to time.Time, bucketSec int, now time.Time) []api.AlwaysOnBucket {
	slices.SortFunc(runs, func(left, right alwaysOnRun) int { return right.start.Compare(left.start) })
	acc := newBucketAccumulator(from, to, bucketSec, now)
	for _, run := range runs {
		acc.add(run)
	}
	return acc.buckets()
}

// bucketAccumulator adds up the runs of a DAG into the buckets of a window. It
// takes the runs newest first, and keeps no run: the wait that follows a run
// needs only the start of the run that came before it in the input. Times are
// kept as durations and rounded once, so that runs shorter than a second add up.
type bucketAccumulator struct {
	from, to, now time.Time
	step          time.Duration
	out           []api.AlwaysOnBucket
	running       []time.Duration
	wait          []time.Duration
	maxWait       []time.Duration
	newerStart    time.Time
	hasNewer      bool
}

func newBucketAccumulator(from, to time.Time, bucketSec int, now time.Time) *bucketAccumulator {
	step := time.Duration(bucketSec) * time.Second
	count := int((to.Sub(from) + step - 1) / step)
	return &bucketAccumulator{
		from: from, to: to, now: now, step: step,
		out:     make([]api.AlwaysOnBucket, count),
		running: make([]time.Duration, count),
		wait:    make([]time.Duration, count),
		maxWait: make([]time.Duration, count),
	}
}

func (b *bucketAccumulator) indexOf(t time.Time) int {
	return int(t.Sub(b.from) / b.step)
}

// add takes the next run, which started before the run that add took before.
func (b *bucketAccumulator) add(run alwaysOnRun) {
	count := len(b.out)
	end := run.end
	if !run.ended {
		end = b.now
	}
	end = laterOf(end, run.start)

	if !run.start.Before(b.from) && run.start.Before(b.to) {
		bucket := &b.out[min(count-1, b.indexOf(run.start))]
		bucket.Runs++
		if run.failed {
			bucket.Failed++
		}
	}

	// The time of the run inside each bucket it touches.
	for i := max(0, b.indexOf(laterOf(run.start, b.from))); i < count; i++ {
		bucketStart := b.from.Add(time.Duration(i) * b.step)
		bucketEnd := earlierOf(bucketStart.Add(b.step), b.to)
		if !end.After(bucketStart) {
			break
		}
		if overlap := earlierOf(end, bucketEnd).Sub(laterOf(run.start, bucketStart)); overlap > 0 {
			b.running[i] += overlap
		}
	}

	// The wait between the end of a run that ended inside the window and the
	// start of the next run.
	if run.ended && b.hasNewer && !end.Before(b.from) && end.Before(b.to) {
		if gap := b.newerStart.Sub(end); gap > 0 {
			i := min(count-1, b.indexOf(end))
			b.wait[i] += gap
			b.maxWait[i] = max(b.maxWait[i], gap)
		}
	}
	b.newerStart, b.hasNewer = run.start, true
}

// buckets returns the sums, rounded to whole seconds.
func (b *bucketAccumulator) buckets() []api.AlwaysOnBucket {
	for i := range b.out {
		b.out[i].RunningSec = roundSeconds(b.running[i])
		b.out[i].WaitSec = roundSeconds(b.wait[i])
		b.out[i].MaxWaitSec = roundSeconds(b.maxWait[i])
	}
	return b.out
}

func roundSeconds(d time.Duration) int {
	return int(d.Round(time.Second) / time.Second)
}

func laterOf(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func earlierOf(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
