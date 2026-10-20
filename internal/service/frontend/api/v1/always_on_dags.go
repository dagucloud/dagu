// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ListAlwaysOnDAGs returns the DAGs that set a restart policy, with their state.
func (a *API) ListAlwaysOnDAGs(ctx context.Context, request api.ListAlwaysOnDAGsRequestObject) (api.ListAlwaysOnDAGsResponseObject, error) {
	filter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	resp, err := a.readAlwaysOnDAGs(ctx, filter)
	if err != nil {
		return nil, err
	}
	return api.ListAlwaysOnDAGs200JSONResponse(resp), nil
}

// GetAlwaysOnDAGsData returns the always-on DAG list for the SSE topic. The query
// string takes the workspace parameter of ListAlwaysOnDAGs.
func (a *API) GetAlwaysOnDAGsData(ctx context.Context, queryString string) (any, error) {
	params, err := url.ParseQuery(queryString)
	if err != nil {
		return nil, fmt.Errorf("invalid query parameters: %w", err)
	}
	filter, err := a.workspaceFilterForParams(ctx, workspaceParamFromValues(params))
	if err != nil {
		return nil, err
	}
	return a.readAlwaysOnDAGs(ctx, filter)
}

// readAlwaysOnDAGs lists the always-on DAGs within the read timeout of the other
// DAG-run reads. For each DAG the list probes the process table, the socket of a
// running run and the status file, and the live stream asks for it every two
// seconds, so a stalled file system must not hold the read without a bound.
func (a *API) readAlwaysOnDAGs(ctx context.Context, filter *workspace.WorkspaceFilter) (api.AlwaysOnDAGsResponse, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/always-on-dags",
	}, func(readCtx context.Context) (api.AlwaysOnDAGsResponse, error) {
		return a.listAlwaysOnDAGs(readCtx, filter)
	})
}

func (a *API) listAlwaysOnDAGs(ctx context.Context, filter *workspace.WorkspaceFilter) (api.AlwaysOnDAGsResponse, error) {
	paginator := pagination.NewPaginator(1, int(^uint(0)>>1))
	result, errList, err := a.dagRepository.List(ctx, persis.DAGListOptions{
		Paginator:       &paginator,
		OnlyAlwaysOn:    true,
		Sort:            "name",
		Order:           "asc",
		WorkspaceFilter: filter,
	})
	if err != nil {
		return api.AlwaysOnDAGsResponse{}, fmt.Errorf("error listing always-on DAGs: %w", err)
	}

	history := a.dagRunMgr.AllHistory()
	dags := make([]api.AlwaysOnDAG, 0, len(result.Items))
	for _, item := range result.Items {
		latest, statusErr := history.GetLatestStatus(ctx, item.DAG)
		if statusErr != nil {
			errList = append(errList, statusErr.Error())
		}
		state, stateErr := scheduler.DescribeAlwaysOn(item.RestartPolicy, &latest, item.Suspended, func() (bool, error) {
			return a.stopRequested(ctx, item.DAG, latest.DAGRunID)
		})
		if stateErr != nil {
			errList = append(errList, stateErr.Error())
			state = scheduler.AlwaysOnStateFailing
		}
		dags = append(dags, api.AlwaysOnDAG{
			FileName:       item.ID,
			Name:           item.Name,
			RestartPolicy:  api.RestartPolicy(item.RestartPolicy),
			RestartWaitSec: int(item.RestartWait.Seconds()),
			State:          api.AlwaysOnDAGState(state),
			Suspended:      item.Suspended,
			LatestRun:      toAlwaysOnDAGRun(latest),
		})
	}
	if errList == nil {
		errList = []string{}
	}
	return api.AlwaysOnDAGsResponse{Dags: dags, Errors: errList}, nil
}

// stopRequested reports whether an operator asked to stop a run: a stop from
// the CLI, the API or the coordinator leaves an abort request on the attempt.
func (a *API) stopRequested(ctx context.Context, dag *ir.DAG, runID string) (bool, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, runID))
	if err != nil {
		return false, err
	}
	return attempt.IsAborting(ctx)
}

// toAlwaysOnDAGRun describes the latest run of an always-on DAG. It leaves out
// what changes while a run lives, so that a steady always-on DAG keeps one
// payload.
func toAlwaysOnDAGRun(status ir.DAGRunStatus) *api.AlwaysOnDAGRun {
	if status.DAGRunID == "" {
		return nil
	}
	return &api.AlwaysOnDAGRun{
		DagRunId:   status.DAGRunID,
		Status:     api.Status(status.Status),
		StartedAt:  parseRunTime(status.StartedAt),
		FinishedAt: parseRunTime(status.FinishedAt),
	}
}

// parseRunTime reads a time that a run status stores, in any format that the
// status accepts. It returns nil for a missing or unreadable time.
func parseRunTime(value string) *time.Time {
	parsed, err := stringutil.ParseTime(value)
	if err != nil || parsed.IsZero() {
		return nil
	}
	return &parsed
}
