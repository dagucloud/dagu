// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	defaultQueueListLimit = 100
	maxQueueListLimit     = 500
	queueCursorScanBatch  = 64
)

// ListQueues implements api.StrictServerInterface.
func (a *API) ListQueues(ctx context.Context, _ api.ListQueuesRequestObject) (api.ListQueuesResponseObject, error) {
	queueMap, err := a.collectQueues(ctx, "")
	if err != nil {
		return nil, err
	}

	queues := make([]api.Queue, 0, len(queueMap))
	queueNames := make([]string, 0, len(queueMap))
	for queueName := range queueMap {
		queueNames = append(queueNames, queueName)
	}
	sort.Strings(queueNames)
	var totalRunning, totalQueued, totalCapacity int
	for _, queueName := range queueNames {
		q := queueMap[queueName]
		queue, err := a.toQueueResource(ctx, q)
		if err != nil {
			return nil, err
		}
		totalRunning += len(q.running)
		totalQueued += q.queuedCount
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

	return api.ListQueues200JSONResponse(response), nil
}

// GetQueue implements api.StrictServerInterface.
func (a *API) GetQueue(ctx context.Context, req api.GetQueueRequestObject) (api.GetQueueResponseObject, error) {
	queueMap, err := a.collectQueues(ctx, req.Name)
	if err != nil {
		return nil, err
	}

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
func (a *API) fetchDAGRunSummary(ctx context.Context, dagRun exec.DAGRunRef) (api.DAGRunSummary, error) {
	attempt, err := a.dagRunStore.FindAttempt(ctx, dagRun)
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
		if errors.Is(err, exec.ErrInvalidCursor) {
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
}

// getOrCreateQueue returns an existing queue from the map or creates a new one.
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

func (a *API) collectQueues(ctx context.Context, onlyQueue string) (map[string]*queueInfo, error) {
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

	runningByGroup := map[string][]exec.DAGRunRef{}
	if a.procStore != nil {
		var err error
		runningByGroup, err = a.procStore.ListAllAlive(ctx)
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeInternalError,
				Message:    "Failed to list running processes",
				HTTPStatus: 500,
			}
		}
	}

	localRunningIDs := make(map[string]struct{})
	for groupName, dagRuns := range runningByGroup {
		if onlyQueue != "" && groupName != onlyQueue {
			continue
		}
		var queue *queueInfo
		for _, dagRun := range dagRuns {
			attempt, err := a.dagRunStore.FindAttempt(ctx, dagRun)
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
			if queue == nil {
				queue = getOrCreateQueue(queueMap, groupName, a.config)
			}
			queue.running = append(queue.running, toDAGRunSummary(*runStatus))
			localRunningIDs[dagRun.ID] = struct{}{}
		}
	}

	for queueName, summaries := range a.activeDistributedRunningSummaries(ctx, onlyQueue, localRunningIDs) {
		queue := getOrCreateQueue(queueMap, queueName, a.config)
		queue.running = append(queue.running, summaries...)
	}

	if a.queueStore == nil {
		return queueMap, nil
	}

	if onlyQueue != "" {
		count, err := a.countVisibleQueuedItems(ctx, onlyQueue)
		if err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeInternalError,
				Message:    "Failed to get queue length",
				HTTPStatus: 500,
			}
		}
		if count > 0 {
			queue := getOrCreateQueue(queueMap, onlyQueue, a.config)
			queue.queuedCount = count
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
			count, err := a.countVisibleQueuedItems(ctx, queueName)
			if err != nil {
				logger.Warn(ctx, "Failed to get queue length",
					tag.Queue(queueName),
					tag.Error(err))
				continue
			}
			queue := getOrCreateQueue(queueMap, queueName, a.config)
			queue.queuedCount = count
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
	for len(items) < limit {
		batchSize := min(max(limit-len(items), 1), queueCursorScanBatch)
		page, err := a.queueStore.ListCursor(ctx, queueName, currentCursor, batchSize)
		if err != nil {
			return nil, "", err
		}
		if len(page.Items) == 0 {
			return items, "", nil
		}

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
					slog.String("dagRunId", dagRunRef.ID),
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

func (a *API) countVisibleQueuedItems(ctx context.Context, queueName string) (int, error) {
	if a.queueStore == nil {
		return 0, nil
	}
	count := 0
	cursor := ""
	for {
		page, err := a.queueStore.ListCursor(ctx, queueName, cursor, queueCursorScanBatch)
		if err != nil {
			return 0, err
		}
		for _, queuedItem := range page.Items {
			dagRunRef, err := queuedItem.Data()
			if err != nil {
				continue
			}
			summary, err := a.fetchDAGRunSummary(ctx, *dagRunRef)
			if err == nil && summary.Status != api.StatusRunning {
				count++
			}
		}
		if !page.HasMore {
			return count, nil
		}
		cursor = page.NextCursor
	}
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
	return exec.DefaultStaleLeaseThreshold
}

func (a *API) activeDistributedRunningSummaries(ctx context.Context, queueName string, excludeRunIDs map[string]struct{}) map[string][]api.DAGRunSummary {
	result := make(map[string][]api.DAGRunSummary)
	if a.dagRunLeaseStore == nil {
		return result
	}

	leases, err := a.dagRunLeaseStore.ListAll(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to list distributed running leases", tag.Error(err))
		return result
	}

	now := time.Now().UTC()
	for _, lease := range leases {
		if !lease.IsFresh(now, a.effectiveLeaseStaleThreshold()) {
			continue
		}
		if _, ok := excludeRunIDs[lease.DAGRun.ID]; ok {
			continue
		}
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

	return result
}

func (a *API) runningSummaryFromLease(ctx context.Context, lease exec.DAGRunLease) (api.DAGRunSummary, bool) {
	attempt, err := a.dagRunStore.FindAttempt(ctx, lease.DAGRun)
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
	case core.Running:
		return toDAGRunSummary(*status), true
	case core.NotStarted, core.Queued:
		// A fresh lease means the worker owns the queue slot, even if the
		// persisted status has not caught up to running yet.
		summary := toDAGRunSummary(*status)
		summary.Status = api.StatusRunning
		summary.StatusLabel = api.StatusLabelRunning
		summary.Conditions = nil
		return summary, true
	case core.Failed, core.Aborted, core.Succeeded,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
		return api.DAGRunSummary{}, false
	}

	return api.DAGRunSummary{}, false
}
