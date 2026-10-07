// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Foreach items are read from the records the executor keeps beside the
// item body logs, never from the run status, so these routes cost nothing
// for runs that are not inspected.

const (
	defaultForeachItemsPerPage = 50
	maxForeachItemsPerPage     = 500
)

// errForeachItemNotFound reports an item or body step that has no record.
var errForeachItemNotFound = errors.New("foreach item not found")

// ForeachItemsQuery selects which items of a foreach step to list.
type ForeachItemsQuery struct {
	Parent  string
	Status  string
	Page    int
	PerPage int
}

func foreachItemsQuery(parent *api.ForeachParent, status *api.ForeachItemStatusFilter, page *api.Page, perPage *api.PerPage) ForeachItemsQuery {
	query := ForeachItemsQuery{Page: valueOf(page), PerPage: valueOf(perPage)}
	if parent != nil {
		query.Parent = string(*parent)
	}
	if status != nil {
		query.Status = string(*status)
	}
	return query
}

// GetDAGRunForeachItems implements api.StrictServerInterface.
func (a *API) GetDAGRunForeachItems(ctx context.Context, request api.GetDAGRunForeachItemsRequestObject) (api.GetDAGRunForeachItemsResponseObject, error) {
	node, notFound, err := a.rootForeachNode(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetDAGRunForeachItems404JSONResponse(*notFound), nil
	}
	list, err := a.listForeachItems(node, foreachItemsQuery(request.Params.Parent, request.Params.Status, request.Params.Page, request.Params.PerPage))
	if err != nil {
		return nil, err
	}
	return api.GetDAGRunForeachItems200JSONResponse(list), nil
}

// GetSubDAGRunForeachItems implements api.StrictServerInterface.
func (a *API) GetSubDAGRunForeachItems(ctx context.Context, request api.GetSubDAGRunForeachItemsRequestObject) (api.GetSubDAGRunForeachItemsResponseObject, error) {
	node, notFound, err := a.subForeachNode(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetSubDAGRunForeachItems404JSONResponse(*notFound), nil
	}
	list, err := a.listForeachItems(node, foreachItemsQuery(request.Params.Parent, request.Params.Status, request.Params.Page, request.Params.PerPage))
	if err != nil {
		return nil, err
	}
	return api.GetSubDAGRunForeachItems200JSONResponse(list), nil
}

// GetDAGRunForeachItem implements api.StrictServerInterface.
func (a *API) GetDAGRunForeachItem(ctx context.Context, request api.GetDAGRunForeachItemRequestObject) (api.GetDAGRunForeachItemResponseObject, error) {
	node, notFound, err := a.rootForeachNode(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetDAGRunForeachItem404JSONResponse(*notFound), nil
	}
	item, err := a.readForeachItem(node, string(request.Item))
	if errors.Is(err, errForeachItemNotFound) {
		return api.GetDAGRunForeachItem404JSONResponse(foreachItemNotFound(request.Item, request.StepName)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetDAGRunForeachItem200JSONResponse(item), nil
}

// GetSubDAGRunForeachItem implements api.StrictServerInterface.
func (a *API) GetSubDAGRunForeachItem(ctx context.Context, request api.GetSubDAGRunForeachItemRequestObject) (api.GetSubDAGRunForeachItemResponseObject, error) {
	node, notFound, err := a.subForeachNode(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetSubDAGRunForeachItem404JSONResponse(*notFound), nil
	}
	item, err := a.readForeachItem(node, string(request.Item))
	if errors.Is(err, errForeachItemNotFound) {
		return api.GetSubDAGRunForeachItem404JSONResponse(foreachItemNotFound(request.Item, request.StepName)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetSubDAGRunForeachItem200JSONResponse(item), nil
}

// GetDAGRunForeachStepLog implements api.StrictServerInterface.
func (a *API) GetDAGRunForeachStepLog(ctx context.Context, request api.GetDAGRunForeachStepLogRequestObject) (api.GetDAGRunForeachStepLogResponseObject, error) {
	node, notFound, err := a.rootForeachNode(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetDAGRunForeachStepLog404JSONResponse(*notFound), nil
	}
	options, err := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	if err != nil {
		return nil, err
	}
	log, err := a.readForeachStepLog(node, string(request.Item), string(request.BodyStepName), request.Params.Stream, options)
	if errors.Is(err, errForeachItemNotFound) {
		return api.GetDAGRunForeachStepLog404JSONResponse(foreachLogNotFound(request.Item, request.BodyStepName)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetDAGRunForeachStepLog200JSONResponse(log), nil
}

// GetSubDAGRunForeachStepLog implements api.StrictServerInterface.
func (a *API) GetSubDAGRunForeachStepLog(ctx context.Context, request api.GetSubDAGRunForeachStepLogRequestObject) (api.GetSubDAGRunForeachStepLogResponseObject, error) {
	node, notFound, err := a.subForeachNode(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.GetSubDAGRunForeachStepLog404JSONResponse(*notFound), nil
	}
	options, err := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	if err != nil {
		return nil, err
	}
	log, err := a.readForeachStepLog(node, string(request.Item), string(request.BodyStepName), request.Params.Stream, options)
	if errors.Is(err, errForeachItemNotFound) {
		return api.GetSubDAGRunForeachStepLog404JSONResponse(foreachLogNotFound(request.Item, request.BodyStepName)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetSubDAGRunForeachStepLog200JSONResponse(log), nil
}

// DownloadDAGRunForeachStepLog implements api.StrictServerInterface.
func (a *API) DownloadDAGRunForeachStepLog(ctx context.Context, request api.DownloadDAGRunForeachStepLogRequestObject) (api.DownloadDAGRunForeachStepLogResponseObject, error) {
	node, notFound, err := a.rootForeachNode(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.DownloadDAGRunForeachStepLog404JSONResponse(*notFound), nil
	}
	response, err := a.openForeachStepLog(ctx, node, string(request.Item), string(request.BodyStepName), request.Params.Stream,
		fmt.Sprintf("%s-%s-%s", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId), sanitizeFilename(request.StepName)))
	if errors.Is(err, errForeachItemNotFound) {
		return api.DownloadDAGRunForeachStepLog404JSONResponse(foreachLogNotFound(request.Item, request.BodyStepName)), nil
	}
	return response, err
}

// DownloadSubDAGRunForeachStepLog implements api.StrictServerInterface.
func (a *API) DownloadSubDAGRunForeachStepLog(ctx context.Context, request api.DownloadSubDAGRunForeachStepLogRequestObject) (api.DownloadSubDAGRunForeachStepLogResponseObject, error) {
	node, notFound, err := a.subForeachNode(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return api.DownloadSubDAGRunForeachStepLog404JSONResponse(*notFound), nil
	}
	response, err := a.openForeachStepLog(ctx, node, string(request.Item), string(request.BodyStepName), request.Params.Stream,
		fmt.Sprintf("%s-%s-sub-%s-%s", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId), sanitizeFilename(request.SubDAGRunId), sanitizeFilename(request.StepName)))
	if errors.Is(err, errForeachItemNotFound) {
		return api.DownloadSubDAGRunForeachStepLog404JSONResponse(foreachLogNotFound(request.Item, request.BodyStepName)), nil
	}
	return response, err
}

// GetForeachItemsDataByRef lists the items of a foreach step for callers
// outside the HTTP layer, such as the MCP read tool.
func (a *API) GetForeachItemsDataByRef(ctx context.Context, ref ir.DAGRunRef, subRunID, stepName string, query ForeachItemsQuery) (any, error) {
	node, err := a.foreachNodeByRef(ctx, ref, subRunID, stepName)
	if err != nil {
		return nil, err
	}
	return a.listForeachItems(node, query)
}

// GetForeachItemDataByRef returns one item of a foreach step for callers
// outside the HTTP layer.
func (a *API) GetForeachItemDataByRef(ctx context.Context, ref ir.DAGRunRef, subRunID, stepName, item string) (any, error) {
	node, err := a.foreachNodeByRef(ctx, ref, subRunID, stepName)
	if err != nil {
		return nil, err
	}
	data, err := a.readForeachItem(node, item)
	if err != nil {
		return nil, foreachDataError(err, foreachItemNotFound(api.ForeachItemPath(item), stepName))
	}
	return data, nil
}

// GetForeachStepLogDataByRef returns the logs of one body step of a foreach
// item for callers outside the HTTP layer.
func (a *API) GetForeachStepLogDataByRef(ctx context.Context, ref ir.DAGRunRef, subRunID, stepName, item, bodyStepName string, opts StepLogReadOptions) (any, error) {
	node, err := a.foreachNodeByRef(ctx, ref, subRunID, stepName)
	if err != nil {
		return nil, err
	}
	itemDir, record, err := a.foreachItemRecord(node, item)
	if err != nil {
		return nil, foreachDataError(err, foreachLogNotFound(api.ForeachItemPath(item), api.BodyStepName(bodyStepName)))
	}
	step, err := foreachBodyStep(record, bodyStepName)
	if err != nil {
		return nil, foreachDataError(err, foreachLogNotFound(api.ForeachItemPath(item), api.BodyStepName(bodyStepName)))
	}
	stdout, stderr := foreachBodyLogPath(itemDir, step.Stdout), foreachBodyLogPath(itemDir, step.Stderr)
	return a.stepLogFromFiles(ctx, stdout, stderr, bodyStepName, opts)
}

// foreachDataError gives callers outside the HTTP layer the same not-found
// answer the handlers return for a missing record.
func foreachDataError(err error, notFound api.Error) error {
	if errors.Is(err, errForeachItemNotFound) {
		return &Error{HTTPStatus: http.StatusNotFound, Code: notFound.Code, Message: notFound.Message}
	}
	return err
}

func (a *API) foreachNodeByRef(ctx context.Context, ref ir.DAGRunRef, subRunID, stepName string) (*ir.Node, error) {
	var (
		node     *ir.Node
		notFound *api.Error
		err      error
	)
	if subRunID != "" {
		node, notFound, err = a.subForeachNode(ctx, ref.Name, ref.ID, subRunID, stepName)
	} else {
		node, notFound, err = a.rootForeachNode(ctx, ref.Name, ref.ID, stepName)
	}
	if err != nil {
		return nil, err
	}
	if notFound != nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: notFound.Code, Message: notFound.Message}
	}
	return node, nil
}

// rootForeachNode resolves a step of a root run. A missing run or step
// comes back as the 404 body to return, not as an error.
func (a *API) rootForeachNode(ctx context.Context, name, dagRunID, stepName string) (*ir.Node, *api.Error, error) {
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ir.NewDAGRunRef(name, dagRunID))
	if err != nil {
		return nil, &api.Error{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", dagRunID, name),
		}, nil
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, nil, err
	}
	return foreachNode(dagStatus, stepName, name)
}

func (a *API) subForeachNode(ctx context.Context, name, dagRunID, subRunID, stepName string) (*ir.Node, *api.Error, error) {
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, ir.NewDAGRunRef(name, dagRunID), subRunID, "")
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return nil, &api.Error{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", subRunID, name),
			}, nil
		}
		return nil, nil, err
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, nil, err
	}
	return foreachNode(dagStatus, stepName, name)
}

func foreachNode(dagStatus *ir.DAGRunStatus, stepName, dagName string) (*ir.Node, *api.Error, error) {
	node, err := dagStatus.NodeByName(stepName)
	if err != nil {
		return nil, &api.Error{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", stepName, dagName),
		}, nil
	}
	if node.Step.ExecutorConfig.Type != ir.ExecutorTypeForeach {
		return nil, &api.Error{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s in DAG %s is not a foreach step", stepName, dagName),
		}, nil
	}
	return node, nil, nil
}

func foreachItemNotFound(item api.ForeachItemPath, stepName string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeNotFound,
		Message: fmt.Sprintf("item %s of step %s has no record", item, stepName),
	}
}

func foreachLogNotFound(item api.ForeachItemPath, bodyStepName api.BodyStepName) api.Error {
	return api.Error{
		Code:    api.ErrorCodeNotFound,
		Message: fmt.Sprintf("log file not found for body step %s of item %s", bodyStepName, item),
	}
}

func invalidForeachPath(err error) error {
	return &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: err.Error()}
}

// foreachStepDir is the directory holding the step's item records and body
// logs. A step that never prepared a log file has none.
func foreachStepDir(node *ir.Node) string {
	if node.Stdout == "" {
		return ""
	}
	return logpath.ForeachStepDir(node.Stdout, node.Step.Name)
}

func (a *API) listForeachItems(node *ir.Node, query ForeachItemsQuery) (api.ForeachItemList, error) {
	list := api.ForeachItemList{Items: []api.ForeachItemSummary{}}
	stepDir := foreachStepDir(node)
	if stepDir == "" {
		return list, nil
	}
	dir, err := logpath.ForeachParentDir(stepDir, query.Parent)
	if err != nil {
		return list, invalidForeachPath(err)
	}

	var items ir.ForeachItems
	if err := readJSONFile(filepath.Join(dir, logpath.ForeachItemsFile), &items); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return list, nil
		}
		return list, err
	}
	list.Total = items.Total

	summaries := make([]api.ForeachItemSummary, 0, len(items.Items))
	for _, ref := range items.Items {
		record := ir.ForeachItemStatus{Index: ref.Index, Key: ref.Key, Status: ir.NodeNotStarted}
		recordPath := filepath.Join(dir, fmt.Sprint(ref.Index), logpath.ForeachItemStatusFile)
		if err := readJSONFile(recordPath, &record); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return list, err
		}
		list.Counts = countForeachItem(list.Counts, record.Status)
		if query.Status != "" && string(foreachItemBucket(record.Status)) != query.Status {
			continue
		}
		summaries = append(summaries, api.ForeachItemSummary{
			Item:        logpath.ForeachItemPath(query.Parent, ref.Index),
			Index:       ref.Index,
			Key:         ref.Key,
			Status:      api.NodeStatus(record.Status),
			StatusLabel: api.NodeStatusLabel(record.Status.String()),
			Error:       ptrOf(record.Error),
			StartedAt:   ptrOf(record.StartedAt),
			FinishedAt:  ptrOf(record.FinishedAt),
		})
	}

	// Failed items first, then running ones, so the items worth a look
	// lead on every page; index order within a group keeps paging stable.
	sort.SliceStable(summaries, func(i, j int) bool {
		return foreachItemRank(summaries[i].Status) < foreachItemRank(summaries[j].Status)
	})
	list.Items = pageOfForeachItems(summaries, query.Page, query.PerPage)
	return list, nil
}

// foreachItemBucket files an item under one of the five states the status
// filter and counts use. An item record never holds the remaining node
// states, which fall back to not started.
func foreachItemBucket(status ir.NodeStatus) api.ForeachItemStatusFilter {
	switch status {
	case ir.NodeSucceeded:
		return api.ForeachItemStatusFilterSucceeded
	case ir.NodeFailed:
		return api.ForeachItemStatusFilterFailed
	case ir.NodeAborted:
		return api.ForeachItemStatusFilterAborted
	case ir.NodeRunning, ir.NodeRetrying:
		return api.ForeachItemStatusFilterRunning
	case ir.NodeNotStarted, ir.NodeSkipped, ir.NodePartiallySucceeded, ir.NodeWaiting, ir.NodeRejected:
		return api.ForeachItemStatusFilterNotStarted
	}
	return api.ForeachItemStatusFilterNotStarted
}

func countForeachItem(counts api.ForeachItemCounts, status ir.NodeStatus) api.ForeachItemCounts {
	switch foreachItemBucket(status) {
	case api.ForeachItemStatusFilterSucceeded:
		counts.Succeeded++
	case api.ForeachItemStatusFilterFailed:
		counts.Failed++
	case api.ForeachItemStatusFilterAborted:
		counts.Aborted++
	case api.ForeachItemStatusFilterRunning:
		counts.Running++
	case api.ForeachItemStatusFilterNotStarted:
		counts.NotStarted++
	}
	return counts
}

func foreachItemRank(status api.NodeStatus) int {
	switch ir.NodeStatus(status) {
	case ir.NodeFailed, ir.NodeAborted:
		return 0
	case ir.NodeRunning, ir.NodeRetrying:
		return 1
	case ir.NodeNotStarted, ir.NodeSucceeded, ir.NodeSkipped, ir.NodePartiallySucceeded, ir.NodeWaiting, ir.NodeRejected:
		return 2
	}
	return 2
}

func pageOfForeachItems(items []api.ForeachItemSummary, page, perPage int) []api.ForeachItemSummary {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultForeachItemsPerPage
	}
	if perPage > maxForeachItemsPerPage {
		perPage = maxForeachItemsPerPage
	}
	start := (page - 1) * perPage
	if start >= len(items) {
		return []api.ForeachItemSummary{}
	}
	return items[start:min(start+perPage, len(items))]
}

func (a *API) readForeachItem(node *ir.Node, item string) (api.ForeachItem, error) {
	_, record, err := a.foreachItemRecord(node, item)
	if err != nil {
		return api.ForeachItem{}, err
	}
	steps := make([]api.ForeachBodyStep, 0, len(record.Steps))
	for _, step := range record.Steps {
		entry := api.ForeachBodyStep{
			Name:        step.Name,
			Id:          ptrOf(step.ID),
			Status:      api.NodeStatus(step.Status),
			StatusLabel: api.NodeStatusLabel(step.Status.String()),
			Error:       ptrOf(step.Error),
			StartedAt:   ptrOf(step.StartedAt),
			FinishedAt:  ptrOf(step.FinishedAt),
			RetryCount:  ptrOf(step.RetryCount),
			HasStdout:   step.Stdout != "",
			HasStderr:   step.Stderr != "",
		}
		if step.Foreach {
			entry.ForeachParent = ptrOf(logpath.ForeachNestedParent(item, step.Name))
		}
		steps = append(steps, entry)
	}
	return api.ForeachItem{
		Item:        item,
		Index:       record.Index,
		Key:         record.Key,
		Status:      api.NodeStatus(record.Status),
		StatusLabel: api.NodeStatusLabel(record.Status.String()),
		Error:       ptrOf(record.Error),
		StartedAt:   ptrOf(record.StartedAt),
		FinishedAt:  ptrOf(record.FinishedAt),
		Steps:       steps,
	}, nil
}

// foreachItemRecord reads an item's record and returns the item directory
// its body logs live in.
func (a *API) foreachItemRecord(node *ir.Node, item string) (string, ir.ForeachItemStatus, error) {
	var record ir.ForeachItemStatus
	stepDir := foreachStepDir(node)
	if stepDir == "" {
		return "", record, errForeachItemNotFound
	}
	itemDir, err := logpath.ForeachItemDir(stepDir, item)
	if err != nil {
		return "", record, invalidForeachPath(err)
	}
	if err := readJSONFile(filepath.Join(itemDir, logpath.ForeachItemStatusFile), &record); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", record, errForeachItemNotFound
		}
		return "", record, err
	}
	return itemDir, record, nil
}

// foreachBodyStep finds a body step by name, or by ID for convenience.
func foreachBodyStep(record ir.ForeachItemStatus, bodyStepName string) (ir.ForeachBodyStepStatus, error) {
	for _, step := range record.Steps {
		if step.Name == bodyStepName {
			return step, nil
		}
	}
	for _, step := range record.Steps {
		if step.ID != "" && step.ID == bodyStepName {
			return step, nil
		}
	}
	return ir.ForeachBodyStepStatus{}, errForeachItemNotFound
}

// foreachBodyLogPath places a recorded log file name inside the item
// directory. The record is data from disk, so only its base name is trusted.
func foreachBodyLogPath(itemDir, name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join(itemDir, filepath.Base(name))
}

func (a *API) foreachBodyLogFile(node *ir.Node, item, bodyStepName string, stream *api.Stream) (string, error) {
	itemDir, record, err := a.foreachItemRecord(node, item)
	if err != nil {
		return "", err
	}
	step, err := foreachBodyStep(record, bodyStepName)
	if err != nil {
		return "", err
	}
	name := step.Stdout
	if stream != nil && *stream == api.StreamStderr {
		name = step.Stderr
	}
	if name == "" {
		return "", errForeachItemNotFound
	}
	return foreachBodyLogPath(itemDir, name), nil
}

func (a *API) readForeachStepLog(node *ir.Node, item, bodyStepName string, stream *api.Stream, options fileutil.LogReadOptions) (api.Log, error) {
	logFile, err := a.foreachBodyLogFile(node, item, bodyStepName, stream)
	if err != nil {
		return api.Log{}, err
	}
	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(logFile, options)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "file not found") {
			return api.Log{}, errForeachItemNotFound
		}
		return api.Log{}, fmt.Errorf("error reading %s: %w", logFile, err)
	}
	return api.Log{
		Content:    content,
		LineCount:  ptrOf(lineCount),
		TotalLines: ptrOf(totalLines),
		HasMore:    ptrOf(hasMore),
		IsEstimate: ptrOf(isEstimate),
	}, nil
}

func (a *API) openForeachStepLog(ctx context.Context, node *ir.Node, item, bodyStepName string, stream *api.Stream, prefix string) (*logFileResponse, error) {
	logFile, err := a.foreachBodyLogFile(node, item, bodyStepName, stream)
	if err != nil {
		return nil, err
	}
	reader, err := a.dagRunRepository.OpenLog(ctx, logFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errForeachItemNotFound
		}
		return nil, fmt.Errorf("error reading %s: %w", logFile, err)
	}
	streamName := "stdout"
	if stream != nil && *stream == api.StreamStderr {
		streamName = "stderr"
	}
	return &logFileResponse{
		ctx:      ctx,
		reader:   reader,
		filename: fmt.Sprintf("%s-%s-%s-%s.log", prefix, sanitizeFilename(item), sanitizeFilename(bodyStepName), streamName),
	}, nil
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // paths derive from recorded log locations and validated segments
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
