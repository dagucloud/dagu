// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
)

// Node runs are read from the records a step with nodes keeps beside its log
// files, never from the run status, so a step over many nodes costs a reader
// one file per step.

// GetDAGRunNodeRuns implements api.StrictServerInterface.
func (a *API) GetDAGRunNodeRuns(ctx context.Context, request api.GetDAGRunNodeRunsRequestObject) (api.GetDAGRunNodeRunsResponseObject, error) {
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId))
	if err != nil {
		return api.GetDAGRunNodeRuns404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, err
	}
	params := request.Params
	list, err := listNodeRuns(dagStatus, valueOf(params.FailedOnly), pagination.NewPaginator(valueOf(params.Page), valueOf(params.PerPage)))
	if err != nil {
		return nil, err
	}
	return api.GetDAGRunNodeRuns200JSONResponse(list), nil
}

// GetSubDAGRunNodeRuns implements api.StrictServerInterface.
func (a *API) GetSubDAGRunNodeRuns(ctx context.Context, request api.GetSubDAGRunNodeRunsRequestObject) (api.GetSubDAGRunNodeRunsResponseObject, error) {
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId), request.SubDAGRunId, "")
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return api.GetSubDAGRunNodeRuns404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
			}, nil
		}
		return nil, err
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}
	params := request.Params
	list, err := listNodeRuns(dagStatus, valueOf(params.FailedOnly), pagination.NewPaginator(valueOf(params.Page), valueOf(params.PerPage)))
	if err != nil {
		return nil, err
	}
	return api.GetSubDAGRunNodeRuns200JSONResponse(list), nil
}

// listNodeRuns returns one page of a run's node runs, one row per node.
func listNodeRuns(dagStatus *ir.DAGRunStatus, failedOnly bool, pg pagination.Paginator) (api.NodeRunList, error) {
	steps := []api.NodeRunStep{}
	rows := map[string]*api.NodeRunRow{}
	for _, node := range dagStatus.NodesInRunOrder() {
		records, err := readNodeRuns(node)
		if err != nil {
			return api.NodeRunList{}, err
		}
		if records == nil {
			continue
		}
		steps = append(steps, api.NodeRunStep{Name: node.Step.Name, Counts: toNodeRunCounts(ir.CountNodeRuns(records.Runs))})
		for _, run := range records.Runs {
			row, ok := rows[run.Node]
			if !ok {
				row = &api.NodeRunRow{Node: run.Node, Runs: []api.NodeRun{}}
				rows[run.Node] = row
			}
			row.Runs = append(row.Runs, toNodeRun(node.Step.Name, run))
		}
	}

	items := make([]api.NodeRunRow, 0, len(rows))
	for _, row := range rows {
		if !failedOnly || nodeRunRowRank(*row) == nodeRunRankFailed {
			items = append(items, *row)
		}
	}
	slices.SortFunc(items, func(x, y api.NodeRunRow) int {
		if rx, ry := nodeRunRowRank(x), nodeRunRowRank(y); rx != ry {
			return rx - ry
		}
		return strings.Compare(x.Node, y.Node)
	})
	page := pageOf(items, pg)
	return api.NodeRunList{
		Steps:      steps,
		Items:      page,
		Pagination: toPagination(pagination.NewPaginatedResult(page, len(items), pg)),
	}, nil
}

// readNodeRuns reads the records of a step with nodes, or returns nil for
// another step and for a step that recorded none.
func readNodeRuns(node *ir.Node) (*ir.NodeRuns, error) {
	dir := nodeRunsDir(node)
	if dir == "" {
		return nil, nil
	}
	var records ir.NodeRuns
	if err := readJSONFile(filepath.Join(dir, logpath.NodeRunsFile), &records); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the node runs of step %q: %w", node.Step.Name, err)
	}
	return &records, nil
}

// nodeRunCounts reads the counts of the node runs of a step with nodes, or
// returns nil for another step and for a step whose counts cannot be read.
func nodeRunCounts(node *ir.Node) *api.NodeRunCounts {
	dir := nodeRunsDir(node)
	if dir == "" {
		return nil
	}
	var counts ir.NodeRunCounts
	if err := readJSONFile(filepath.Join(dir, logpath.NodeRunsSummaryFile), &counts); err != nil {
		return nil
	}
	result := toNodeRunCounts(counts)
	return &result
}

// nodeRunsDir returns the directory of a step's node run records, or "" for
// a step without nodes or without a log file.
func nodeRunsDir(node *ir.Node) string {
	if node == nil || node.Step.Nodes == nil || node.Stdout == "" {
		return ""
	}
	return logpath.NodeRunsDir(filepath.Dir(node.Stdout), node.Step.Name)
}

// Ranks of the rows of node runs: rows with a failed run come first, then
// rows with a run in progress, then the rest.
const (
	nodeRunRankFailed = iota
	nodeRunRankActive
	nodeRunRankOther
)

func nodeRunRowRank(row api.NodeRunRow) int {
	rank := nodeRunRankOther
	for _, run := range row.Runs {
		switch run.Status {
		case api.NodeRunStatusFailed, api.NodeRunStatusAborted, api.NodeRunStatusRejected:
			return nodeRunRankFailed
		case api.NodeRunStatusRunning, api.NodeRunStatusWaiting:
			rank = nodeRunRankActive
		case api.NodeRunStatusNotStarted, api.NodeRunStatusSucceeded,
			api.NodeRunStatusPartiallySucceeded, api.NodeRunStatusDisabled:
		}
	}
	return rank
}

func toNodeRun(step string, run ir.NodeRun) api.NodeRun {
	result := api.NodeRun{
		Step:       step,
		Status:     api.NodeRunStatus(run.Status),
		DagName:    ptrOf(run.DAGName),
		DagRunId:   ptrOf(run.DAGRunID),
		StartedAt:  ptrOf(run.StartedAt),
		FinishedAt: ptrOf(run.FinishedAt),
		FailedStep: ptrOf(run.FailedStep),
	}
	if run.Steps != nil {
		result.Steps = &api.StepProgress{Done: run.Steps.Done, Total: run.Steps.Total}
	}
	return result
}

func toNodeRunCounts(counts ir.NodeRunCounts) api.NodeRunCounts {
	return api.NodeRunCounts{
		Total:      counts.Total,
		NotStarted: counts.NotStarted,
		Running:    counts.Running,
		Waiting:    counts.Waiting,
		Succeeded:  counts.Succeeded,
		Failed:     counts.Failed,
		Aborted:    counts.Aborted,
		Disabled:   counts.Disabled,
	}
}
