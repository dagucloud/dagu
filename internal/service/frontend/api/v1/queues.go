// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	defaultQueueListLimit = 100
	maxQueueListLimit     = 500
	queueCursorScanBatch  = 64
)

// queueItemsScanCap bounds records scanned by one item-list request.
var queueItemsScanCap = 500

// queuedCountScanCap bounds how many queued items a single count may scan.
// Counting is O(items) with a status read per item, so an uncapped scan on a
// deep queue can take tens of seconds and stall both GET /queues and the queues
// SSE topic. Past the cap the count is reported as a lower bound via the
// queuedCountCapped/totalQueuedCapped flags.
//
// Declared as a var so tests can lower it instead of enqueuing thousands of runs.
var queuedCountScanCap = 500

// queuedCountRequestScanCap bounds how many queued entries a single request may
// scan across all queues combined. The per-queue cap alone does not bound a
// request: collectQueues counts every queue returned by QueueList, so the total
// would still grow with the number of queues.
//
// Declared as a var for the same reason as queuedCountScanCap.
var queuedCountRequestScanCap = 2000

// scanBudget bounds the queued entries one request may scan in total, shared
// across every queue that request counts.
type scanBudget struct {
	remaining int
}

func newScanBudget(total int) *scanBudget {
	return &scanBudget{remaining: total}
}

func (b *scanBudget) exhausted() bool {
	return b != nil && b.remaining <= 0
}

func (b *scanBudget) consume() {
	if b != nil && b.remaining > 0 {
		b.remaining--
	}
}

// limit caps n at the entries left in the budget.
func (b *scanBudget) limit(n int) int {
	if b == nil {
		return n
	}
	return min(n, b.remaining)
}

// ListQueues implements api.StrictServerInterface.
func (a *API) ListQueues(ctx context.Context, _ api.ListQueuesRequestObject) (api.ListQueuesResponseObject, error) {
	queueMap, err := a.collectQueues(ctx, "", queuesPageScan())
	if err != nil {
		return nil, err
	}
	a.addAlwaysOnQueues(ctx, queueMap, "")

	queues := make([]api.Queue, 0, len(queueMap))
	queueNames := make([]string, 0, len(queueMap))
	for queueName := range queueMap {
		queueNames = append(queueNames, queueName)
	}
	sort.Strings(queueNames)
	var totalRunning, totalQueued, totalCapacity int
	var totalQueuedCapped bool
	for _, queueName := range queueNames {
		q := queueMap[queueName]
		queue, err := a.toQueueResource(ctx, q)
		if err != nil {
			return nil, err
		}
		totalRunning += len(q.running)
		totalQueued += q.queuedCount
		totalQueuedCapped = totalQueuedCapped || q.queuedCountCapped
		if q.maxConcurrency > 0 {
			totalCapacity += q.maxConcurrency
		}
		queues = append(queues, queue)
	}

	// Calculate utilization percentage
	var utilizationPercentage float32
	if totalCapacity > 0 {
		utilizationPercentage = float32(totalRunning) / float32(totalCapacity) * 100
	}

	// Build response
	response := api.QueuesResponse{
		Queues: queues,
		Summary: api.QueuesSummary{
			TotalQueues:           len(queues),
			TotalRunning:          totalRunning,
			TotalQueued:           totalQueued,
			TotalCapacity:         totalCapacity,
			UtilizationPercentage: utilizationPercentage,
		},
	}
	if totalQueuedCapped {
		response.Summary.TotalQueuedCapped = &totalQueuedCapped
	}

	return api.ListQueues200JSONResponse(response), nil
}

// GetQueue implements api.StrictServerInterface.
func (a *API) GetQueue(ctx context.Context, req api.GetQueueRequestObject) (api.GetQueueResponseObject, error) {
	queueMap, err := a.collectQueues(ctx, req.Name, queuesPageScan())
	if err != nil {
		return nil, err
	}
	a.addAlwaysOnQueues(ctx, queueMap, req.Name)

	queueInfo, ok := queueMap[req.Name]
	if !ok {
		return nil, &Error{
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("Queue %q not found", req.Name),
			HTTPStatus: 404,
		}
	}

	queue, err := a.toQueueResource(ctx, queueInfo)
	if err != nil {
		return nil, err
	}

	return api.GetQueue200JSONResponse(queue), nil
}

// fetchDAGRunSummary fetches the status and converts it to a summary for a given DAG-run reference.
func (a *API) fetchDAGRunSummary(ctx context.Context, dagRun ir.DAGRunRef) (api.DAGRunSummary, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, dagRun)
	if err != nil {
		return api.DAGRunSummary{}, err
	}
	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return api.DAGRunSummary{}, err
	}
	if !a.canAccessWorkspace(ctx, statusWorkspaceName(runStatus)) {
		return api.DAGRunSummary{}, workspaceResourceNotFound()
	}
	return toDAGRunSummary(*runStatus), nil
}

// ListQueueItems implements api.StrictServerInterface.
func (a *API) ListQueueItems(ctx context.Context, req api.ListQueueItemsRequestObject) (api.ListQueueItemsResponseObject, error) {
	limit := normalizeQueueListLimit(req.Params.Limit)
	cursor := valueOf(req.Params.Cursor)

	items, nextCursor, err := a.listVisibleQueuedItems(ctx, req.Name, limit, cursor)
	if err != nil {
		if errors.Is(err, pagination.ErrInvalidCursor) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Invalid queue cursor",
				HTTPStatus: 400,
			}
		}
		return nil, &Error{
			Code:       api.ErrorCodeInternalError,
			Message:    "Failed to list queued items",
			HTTPStatus: 500,
		}
	}

	response := api.ListQueueItems200JSONResponse{
		Items: items,
	}
	if nextCursor != "" {
		response.NextCursor = &nextCursor
	}
	return response, nil
}

// Helper struct to build queue information
type queueInfo struct {
	name           string
	queueType      string
	maxConcurrency int
	running        []api.DAGRunSummary
	queuedCount    int
	// queuedCountCapped reports that queuedCount hit queuedCountScanCap and is
	// therefore a lower bound rather than an exact total.
	queuedCountCapped bool
	// runningCapped reports that running runs were left unread, so running
	// is a lower bound.
	runningCapped bool
	// nextQueuedAt is when the next run in line was queued.
	nextQueuedAt time.Time
	// workerBlockReason is why a counted queued run cannot start for want
	// of a worker, or empty when none it read says so.
	workerBlockReason string
}

// getOrCreateQueue returns an existing queue from the map or creates a new one.
// addAlwaysOnQueues adds the queue of every always-on DAG that the queue map does
// not hold. A DAG-based queue exists only while a run is alive or waiting, so
// without this the queue of an always-on DAG would vanish for the wait before each
// restart, and open as not found. A failing read leaves the queues as they are.
func (a *API) addAlwaysOnQueues(ctx context.Context, queueMap map[string]*queueInfo, onlyQueue string) {
	if a.dagRepository == nil {
		return
	}
	paginator := pagination.NewPaginator(1, int(^uint(0)>>1))
	result, _, err := a.dagRepository.List(ctx, persis.DAGListOptions{
		Paginator:    &paginator,
		OnlyAlwaysOn: true,
	})
	if err != nil {
		logger.Warn(ctx, "Failed to list always-on DAGs for the queues", tag.Error(err))
		return
	}
	for _, item := range result.Items {
		if !a.canAccessWorkspace(ctx, dagWorkspaceName(item.DAG)) {
			continue
		}
		name := item.ProcGroup()
		if onlyQueue != "" && name != onlyQueue {
			continue
		}
		getOrCreateQueue(queueMap, name, a.config)
	}
}

func getOrCreateQueue(queueMap map[string]*queueInfo, queueName string, cfg *config.Config) *queueInfo {
	if queue, exists := queueMap[queueName]; exists {
		return queue
	}

	queue := &queueInfo{
		name:        queueName,
		queueType:   "dag-based",
		running:     []api.DAGRunSummary{},
		queuedCount: 0,
	}

	// Check if this is a global queue from config.
	if cfg != nil {
		if globalCfg := cfg.FindQueueConfig(queueName); globalCfg != nil {
			queue.queueType = "global"
			queue.maxConcurrency = globalCfg.MaxActiveRuns
		} else {
			// For DAG-based (local) queues, maxConcurrency is always 1 (FIFO processing).
			// DAG's maxActiveRuns is deprecated and ignored for local queues.
			queue.maxConcurrency = 1
		}
	} else {
		// For DAG-based (local) queues, maxConcurrency is always 1 (FIFO processing).
		// DAG's maxActiveRuns is deprecated and ignored for local queues.
		queue.maxConcurrency = 1
	}

	queueMap[queueName] = queue
	return queue
}

func normalizeQueueListLimit(raw *int) int {
	if raw == nil || *raw <= 0 {
		return defaultQueueListLimit
	}
	if *raw > maxQueueListLimit {
		return maxQueueListLimit
	}
	return *raw
}

// queueScan bounds and narrows the runs one queue collection reads.
type queueScan struct {
	// total bounds the queued entries read across all queues and perQueue
	// bounds them for one queue; past either, counts are lower bounds.
	total    int
	perQueue int
	// running, when set, bounds the running runs read across all queues;
	// past it, running counts are lower bounds.
	running int
	// matches, when set, counts only the runs it accepts.
	matches func(api.DAGRunSummary) bool
}

// queuesPageScan reads queues the way the Queues page shows them.
func queuesPageScan() queueScan {
	return queueScan{total: queuedCountRequestScanCap, perQueue: queuedCountScanCap}
}

func (s queueScan) accepts(summary api.DAGRunSummary) bool {
	return s.matches == nil || s.matches(summary)
}

// runningBudget returns the budget for reading running runs, or nil when
// every running run is read.
func (s queueScan) runningBudget() *scanBudget {
	if s.running == 0 {
		return nil
	}
	return newScanBudget(s.running)
}

// collectQueues gathers running runs and queued counts per queue.
func (a *API) collectQueues(ctx context.Context, onlyQueue string, scan queueScan) (map[string]*queueInfo, error) {
	queueMap := make(map[string]*queueInfo)

	if a.config != nil && a.config.Queues.Enabled && a.config.Queues.Config != nil {
		for _, queueCfg := range a.config.Queues.Config {
			if onlyQueue != "" && queueCfg.Name != onlyQueue {
				continue
			}
			queueMap[queueCfg.Name] = &queueInfo{
				name:           queueCfg.Name,
				queueType:      "global",
				maxConcurrency: queueCfg.MaxActiveRuns,
				running:        []api.DAGRunSummary{},
				queuedCount:    0,
			}
		}
	}

	runningByGroup := map[string][]ir.DAGRunRef{}
	if a.procRepository != nil {
		var err error
		runningByGroup, err = a.procRepository.ListAllAlive(ctx)
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeInternalError,
				Message:    "Failed to list running processes",
				HTTPStatus: 500,
			}
		}
	}

	runningReads := scan.runningBudget()
	localRunningIDs := make(map[string]struct{})
	for _, groupName := range slices.Sorted(maps.Keys(runningByGroup)) {
		if onlyQueue != "" && groupName != onlyQueue {
			continue
		}
		var queue *queueInfo
		for _, dagRun := range runningByGroup[groupName] {
			if runningReads.exhausted() {
				// Left unread, the run still counts as local, so no lease
				// counts it again.
				localRunningIDs[dagRun.ID] = struct{}{}
				getOrCreateQueue(queueMap, groupName, a.config).runningCapped = true
				continue
			}
			runningReads.consume()
			attempt, err := a.dagRunRepository.FindAttempt(ctx, dagRun)
			if err != nil {
				continue
			}
			runStatus, err := attempt.ReadStatus(ctx)
			if err != nil {
				continue
			}
			if !a.canAccessWorkspace(ctx, statusWorkspaceName(runStatus)) {
				continue
			}
			localRunningIDs[dagRun.ID] = struct{}{}
			summary := toDAGRunSummary(*runStatus)
			if !scan.accepts(summary) {
				continue
			}
			if queue == nil {
				queue = getOrCreateQueue(queueMap, groupName, a.config)
			}
			queue.running = append(queue.running, summary)
		}
	}

	distributed, unread := a.activeDistributedRunningSummaries(ctx, onlyQueue, localRunningIDs, runningReads)
	for queueName := range unread {
		getOrCreateQueue(queueMap, queueName, a.config).runningCapped = true
	}
	for queueName, summaries := range distributed {
		var accepted []api.DAGRunSummary
		for _, summary := range summaries {
			if scan.accepts(summary) {
				accepted = append(accepted, summary)
			}
		}
		if len(accepted) == 0 {
			continue
		}
		queue := getOrCreateQueue(queueMap, queueName, a.config)
		queue.running = append(queue.running, accepted...)
	}

	if a.queueStore == nil {
		return queueMap, nil
	}

	// One budget shared by every queue counted in this request.
	budget := newScanBudget(scan.total)

	if onlyQueue != "" {
		queued, err := a.countVisibleQueuedItems(ctx, onlyQueue, budget, scan)
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeInternalError,
				Message:    "Failed to get queue length",
				HTTPStatus: 500,
			}
		}
		// A capped scan proves entries exist even when none seen so far were
		// visible, so the queue must still surface rather than read as absent.
		if queued.count > 0 || queued.capped {
			queued.applyTo(getOrCreateQueue(queueMap, onlyQueue, a.config))
		}
	} else {
		queueNames, err := a.queueStore.QueueList(ctx)
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeInternalError,
				Message:    "Failed to list queue names",
				HTTPStatus: 500,
			}
		}
		for _, queueName := range queueNames {
			queued, err := a.countVisibleQueuedItems(ctx, queueName, budget, scan)
			if err != nil {
				logger.Warn(ctx, "Failed to get queue length",
					tag.Queue(queueName),
					tag.Error(err))
				continue
			}
			queued.applyTo(getOrCreateQueue(queueMap, queueName, a.config))
		}
	}

	return queueMap, nil
}

func (a *API) toQueueResource(_ context.Context, q *queueInfo) (api.Queue, error) {
	if q == nil {
		return api.Queue{}, nil
	}

	queue := api.Queue{
		Name:         q.name,
		Type:         api.QueueType(q.queueType),
		Running:      q.running,
		RunningCount: len(q.running),
		QueuedCount:  q.queuedCount,
	}
	if q.queuedCountCapped {
		capped := true
		queue.QueuedCountCapped = &capped
	}
	if q.maxConcurrency > 0 {
		queue.MaxConcurrency = &q.maxConcurrency
	}
	return queue, nil
}

func (a *API) listVisibleQueuedItems(ctx context.Context, queueName string, limit int, cursor string) ([]api.DAGRunSummary, string, error) {
	if limit <= 0 || a.queueStore == nil {
		return []api.DAGRunSummary{}, "", nil
	}

	items := make([]api.DAGRunSummary, 0, limit)
	currentCursor := cursor
	scanned := 0
	for len(items) < limit && scanned < queueItemsScanCap {
		batchSize := min(max(limit-len(items), 1), queueCursorScanBatch, queueItemsScanCap-scanned)
		page, err := a.queueStore.ListCursor(ctx, queueName, currentCursor, batchSize)
		if err != nil {
			return nil, "", err
		}
		if len(page.Items) == 0 {
			return items, "", nil
		}
		scanned += len(page.Items)

		for _, queuedItem := range page.Items {
			dagRunRef, err := queuedItem.Data()
			if err != nil {
				logger.Warn(ctx, "Failed to parse queued item data",
					tag.Queue(queueName),
					tag.Error(err),
					slog.String("queueItemID", queuedItem.ID()),
				)
				continue
			}

			summary, err := a.fetchDAGRunSummary(ctx, *dagRunRef)
			if err != nil {
				var apiErr *Error
				if errors.As(err, &apiErr) && apiErr.HTTPStatus == 404 {
					continue
				}
				logger.Warn(ctx, "Failed to fetch queued DAG run summary",
					tag.Queue(queueName),
					tag.Error(err),
					tag.RunID(dagRunRef.ID),
				)
				continue
			}
			if summary.Status == api.StatusRunning {
				continue
			}
			items = append(items, summary)
			if len(items) == limit {
				break
			}
		}

		currentCursor = page.NextCursor
		if !page.HasMore {
			return items, "", nil
		}
	}

	return items, currentCursor, nil
}

// visibleQueued is the queued backlog of one queue as far as its scan reached.
type visibleQueued struct {
	count int
	// capped reports that the scan stopped early, so count is a lower bound.
	capped bool
	// nextQueuedAt is when the first visible entry with a queue time, which
	// is normally the next to run, was queued.
	nextQueuedAt time.Time
	// workerBlockReason is the first worker reason a visible entry records
	// for not starting.
	workerBlockReason string
}

func (v visibleQueued) applyTo(queue *queueInfo) {
	queue.queuedCount = v.count
	queue.queuedCountCapped = v.capped
	queue.nextQueuedAt = v.nextQueuedAt
	queue.workerBlockReason = v.workerBlockReason
}

// countVisibleQueuedItems counts queued items that are not already running.
//
// The scan is bounded twice: by the scan's per-queue bound, and by the
// caller's shared budget for the request as a whole. Counting requires a status
// read per entry to exclude entries whose run has already started, so without
// both bounds the cost grows with queue depth and with the number of queues.
// When either bound is reached, counting stops and the result is marked as a
// lower bound.
func (a *API) countVisibleQueuedItems(ctx context.Context, queueName string, budget *scanBudget, scan queueScan) (visibleQueued, error) {
	var result visibleQueued
	if a.queueStore == nil {
		return result, nil
	}
	if budget.exhausted() {
		// Earlier queues already consumed the request budget. Report zero as a
		// lower bound rather than spending more reads on this one.
		result.capped = true
		return result, nil
	}
	scanned := 0
	cursor := ""
	for {
		// Load only the entries this queue and the request may still scan.
		limit := budget.limit(min(queueCursorScanBatch, scan.perQueue-scanned))
		page, err := a.queueStore.ListCursor(ctx, queueName, cursor, limit)
		if err != nil {
			return visibleQueued{}, err
		}
		for _, queuedItem := range page.Items {
			budget.consume()

			// Every entry costs a status read, whether or not it ends up visible,
			// so the cap tracks entries processed rather than entries counted.
			// Capping on the visible count alone would leave a queue made up of
			// running or unreadable entries scanning without bound.
			scanned++

			dagRunRef, err := queuedItem.Data()
			if err == nil {
				summary, err := a.fetchDAGRunSummary(ctx, *dagRunRef)
				if err == nil && summary.Status != api.StatusRunning && scan.accepts(summary) {
					if result.nextQueuedAt.IsZero() {
						result.nextQueuedAt = parseQueuedAt(summary.QueuedAt)
					}
					if result.workerBlockReason == "" {
						result.workerBlockReason = workerBlockReason(summary)
					}
					result.count++
				}
			}

			if scanned >= scan.perQueue {
				// Only a lower bound if entries actually remain unscanned.
				result.capped = page.HasMore
				return result, nil
			}
		}
		if !page.HasMore {
			return result, nil
		}
		if budget.exhausted() {
			// The entries left stay unscanned.
			result.capped = true
			return result, nil
		}
		cursor = page.NextCursor
	}
}

// workerBlockReason returns the reason a queued run records for not starting
// when no worker can take it, or empty when it records none.
func workerBlockReason(summary api.DAGRunSummary) string {
	for _, condition := range valueOf(summary.Conditions) {
		if condition.Type != ir.DAGRunConditionRunnable || condition.Status == api.DAGRunConditionStatusTrue {
			continue
		}
		switch condition.Reason {
		case ir.DAGRunReasonNoMatchingWorker,
			ir.DAGRunReasonNoAvailableWorker,
			ir.DAGRunReasonWorkerDispatchUnavailable:
			return condition.Reason
		}
	}
	return ""
}

func parseQueuedAt(value *string) time.Time {
	if value == nil {
		return time.Time{}
	}
	queuedAt, err := stringutil.ParseTime(*value)
	if err != nil {
		return time.Time{}
	}
	return queuedAt
}

// GetQueuesListData returns queue list for SSE.
// Identifier format: URL query string (ignored for now)
func (a *API) GetQueuesListData(ctx context.Context, _ string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/queues",
	}, func(readCtx context.Context) (api.ListQueuesResponseObject, error) {
		response, err := a.ListQueues(readCtx, api.ListQueuesRequestObject{})
		if err != nil {
			return nil, fmt.Errorf("error listing queues: %w", err)
		}
		return response, nil
	})
}

func (a *API) effectiveLeaseStaleThreshold() time.Duration {
	if a.leaseStaleThreshold > 0 {
		return a.leaseStaleThreshold
	}
	return dagrun.DefaultStaleLeaseThreshold
}

// activeDistributedRunningSummaries returns the runs held by fresh leases, by
// queue. Once reads is exhausted, leases are left unread, and the queues they
// belong to are returned as unread.
func (a *API) activeDistributedRunningSummaries(ctx context.Context, queueName string, excludeRunIDs map[string]struct{}, reads *scanBudget) (map[string][]api.DAGRunSummary, map[string]struct{}) {
	result := make(map[string][]api.DAGRunSummary)
	unread := make(map[string]struct{})
	if a.dagRunLeaseStore == nil {
		return result, unread
	}

	leases, err := a.dagRunLeaseStore.ListAll(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to list distributed running leases", tag.Error(err))
		return result, unread
	}

	now := time.Now().UTC()
	for _, lease := range leases {
		if !lease.IsFresh(now, a.effectiveLeaseStaleThreshold()) {
			continue
		}
		if _, ok := excludeRunIDs[lease.DAGRun.ID]; ok {
			continue
		}
		if reads.exhausted() {
			unread[cmp.Or(lease.QueueName, lease.DAGRun.Name)] = struct{}{}
			continue
		}
		reads.consume()
		summary, ok := a.runningSummaryFromLease(ctx, lease)
		if !ok {
			continue
		}
		groupName := lease.QueueName
		if groupName == "" {
			groupName = summary.Name
		}
		if queueName != "" && groupName != queueName {
			continue
		}
		result[groupName] = append(result[groupName], summary)
	}

	return result, unread
}

func (a *API) runningSummaryFromLease(ctx context.Context, lease dispatch.DAGRunLease) (api.DAGRunSummary, bool) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, lease.DAGRun)
	if err != nil {
		return api.DAGRunSummary{}, false
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return api.DAGRunSummary{}, false
	}
	if status.AttemptID != lease.AttemptID {
		return api.DAGRunSummary{}, false
	}
	if !a.canAccessWorkspace(ctx, statusWorkspaceName(status)) {
		return api.DAGRunSummary{}, false
	}
	switch status.Status {
	case ir.Running:
		return toDAGRunSummary(*status), true
	case ir.NotStarted, ir.Queued:
		// A fresh lease means the worker owns the queue slot, even if the
		// persisted status has not caught up to running yet.
		summary := toDAGRunSummary(*status)
		summary.Status = api.StatusRunning
		summary.StatusLabel = api.StatusLabelRunning
		summary.Conditions = nil
		return summary, true
	case ir.Failed, ir.Aborted, ir.Succeeded,
		ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
		return api.DAGRunSummary{}, false
	}

	return api.DAGRunSummary{}, false
}
