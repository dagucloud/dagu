// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ListServiceDAGs returns the DAGs that set a restart policy, with their state.
func (a *API) ListServiceDAGs(ctx context.Context, request api.ListServiceDAGsRequestObject) (api.ListServiceDAGsResponseObject, error) {
	filter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	resp, err := a.readServiceDAGs(ctx, filter)
	if err != nil {
		return nil, err
	}
	return api.ListServiceDAGs200JSONResponse(resp), nil
}

// GetServiceDAGsData returns the service DAG list for the SSE topic. The query
// string takes the workspace parameter of ListServiceDAGs.
func (a *API) GetServiceDAGsData(ctx context.Context, queryString string) (any, error) {
	params, err := url.ParseQuery(queryString)
	if err != nil {
		return nil, fmt.Errorf("invalid query parameters: %w", err)
	}
	filter, err := a.workspaceFilterForParams(ctx, workspaceParamFromValues(params))
	if err != nil {
		return nil, err
	}
	return a.readServiceDAGs(ctx, filter)
}

// readServiceDAGs lists the service DAGs within the read timeout of the other
// DAG-run reads. For each DAG the list probes the process table, the socket of a
// running run and the status file, and the live stream asks for it every two
// seconds, so a stalled file system must not hold the read without a bound.
func (a *API) readServiceDAGs(ctx context.Context, filter *workspace.WorkspaceFilter) (api.ServiceDAGsResponse, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/service-dags",
	}, func(readCtx context.Context) (api.ServiceDAGsResponse, error) {
		return a.listServiceDAGs(readCtx, filter)
	})
}

func (a *API) listServiceDAGs(ctx context.Context, filter *workspace.WorkspaceFilter) (api.ServiceDAGsResponse, error) {
	paginator := pagination.NewPaginator(1, int(^uint(0)>>1))
	result, errList, err := a.dagRepository.List(ctx, persis.DAGListOptions{
		Paginator:       &paginator,
		OnlyServiceDAGs: true,
		Sort:            "name",
		Order:           "asc",
		WorkspaceFilter: filter,
	})
	if err != nil {
		return api.ServiceDAGsResponse{}, fmt.Errorf("error listing service DAGs: %w", err)
	}

	history := a.dagRunMgr.AllHistory()
	dags := make([]api.ServiceDAG, 0, len(result.Items))
	for _, item := range result.Items {
		latest, statusErr := history.ReadLatestStatus(ctx, item.DAG)
		// A run keeps the workspace its DAG had when it started, so after a move
		// the latest run may belong to a workspace the caller cannot see.
		if !filter.MatchesLabels(ir.NewLabels(latest.Labels)) {
			latest = ir.InitialStatus(item.DAG)
		}
		state, stateErr := scheduler.DescribeServiceDAG(item.RestartPolicy, &latest, item.Suspended, func() (bool, error) {
			return scheduler.IsStopRequested(ctx, a.dagRunRepository, item.DAG, latest.DAGRunID)
		})
		var readError *string
		if readErr := errors.Join(statusErr, stateErr); readErr != nil {
			state = scheduler.ServiceDAGStateFailing
			message := readErr.Error()
			readError = &message
		}
		runs := a.serviceDAGHistory(ctx, item.DAG, latest)
		var nextRestartAt *time.Time
		if readError == nil && !runs.nextRestart.IsZero() &&
			(state == scheduler.ServiceDAGStateRestarting || state == scheduler.ServiceDAGStateFailing) {
			nextRestartAt = &runs.nextRestart
		}
		dags = append(dags, api.ServiceDAG{
			FileName:       item.ID,
			Name:           item.Name,
			RestartPolicy:  api.RestartPolicy(item.RestartPolicy),
			RestartWaitSec: int(item.RestartWait.Seconds()),
			Queue:          item.ProcGroup(),
			State:          api.ServiceDAGState(state),
			Suspended:      item.Suspended,
			LatestRun:      toServiceDAGRun(latest),
			ReadError:      readError,
			NextRestartAt:  nextRestartAt,

			ConsecutiveFailures: runs.failures,
		})
	}
	if errList == nil {
		errList = []string{}
	}
	return api.ServiceDAGsResponse{Dags: dags, Errors: errList}, nil
}

// serviceDAGFailureStreakLimit caps the failed runs in a row that the
// service DAG list counts, so that it reads a bounded part of the history.
const serviceDAGFailureStreakLimit = 10

// serviceDAGHistory is what the service DAG list reads from the recent runs of
// a DAG.
type serviceDAGHistory struct {
	// failures counts the newest ended runs in a row that did not succeed, up to
	// serviceDAGFailureStreakLimit.
	failures int
	// nextRestart is when the scheduler starts the next run. It is zero while
	// the latest run has not ended.
	nextRestart time.Time
}

// serviceDAGHistories keeps the history of each service DAG with the latest
// run it was read for, so that a DAG whose latest run has not changed costs no
// read of its runs.
type serviceDAGHistories struct {
	mu      sync.Mutex
	entries map[string]serviceDAGHistoryEntry
}

type serviceDAGHistoryEntry struct {
	runID   string
	status  ir.Status
	history serviceDAGHistory
}

func (s *serviceDAGHistories) get(name string, latest ir.DAGRunStatus) (serviceDAGHistory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[name]
	if !ok || entry.runID != latest.DAGRunID || entry.status != latest.Status {
		return serviceDAGHistory{}, false
	}
	return entry.history, true
}

func (s *serviceDAGHistories) put(name string, latest ir.DAGRunStatus, history serviceDAGHistory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]serviceDAGHistoryEntry)
	}
	s.entries[name] = serviceDAGHistoryEntry{runID: latest.DAGRunID, status: latest.Status, history: history}
}

// serviceDAGHistory reads the history of a service DAG from its recent runs. A
// run that an operator stopped, or one from another workspace than the latest
// run, ends the count of failures.
func (a *API) serviceDAGHistory(ctx context.Context, dag *ir.DAG, latest ir.DAGRunStatus) serviceDAGHistory {
	if latest.DAGRunID == "" {
		return serviceDAGHistory{}
	}
	if latest.Status.IsSuccess() {
		// A success ends both the count and the backoff, so the latest run
		// tells the rest.
		next, _ := scheduler.NextRestart(dag.RestartWait, []ir.DAGRunStatus{latest})
		return serviceDAGHistory{nextRestart: next}
	}
	if history, ok := a.serviceDAGHistories.get(dag.Name, latest); ok {
		return history
	}
	// One more than the limit, since a run that is alive takes a place.
	statuses, err := a.dagRunRepository.RecentStatuses(ctx, dag.Name, serviceDAGFailureStreakLimit+1)
	if err != nil {
		logger.Warn(ctx, "Failed to read the recent runs of a service DAG", tag.DAG(dag.Name), tag.Error(err))
		return serviceDAGHistory{}
	}
	ended := make([]ir.DAGRunStatus, 0, len(statuses))
	for _, status := range statuses {
		if runEnded(status.Status) {
			ended = append(ended, status)
		}
	}

	latestWorkspace, _ := workspace.WorkspaceNameFromLabels(ir.NewLabels(latest.Labels))
	var history serviceDAGHistory
	for _, status := range ended {
		if status.Status.IsSuccess() || history.failures == serviceDAGFailureStreakLimit {
			break
		}
		if name, _ := workspace.WorkspaceNameFromLabels(ir.NewLabels(status.Labels)); name != latestWorkspace {
			break
		}
		if stopped, err := scheduler.IsStopRequested(ctx, a.dagRunRepository, dag, status.DAGRunID); err == nil && stopped {
			break
		}
		history.failures++
	}
	if runEnded(latest.Status) {
		history.nextRestart, _ = scheduler.NextRestart(dag.RestartWait, ended)
	}
	// A cancelled read skips runs, so its history is not kept.
	if ctx.Err() == nil {
		a.serviceDAGHistories.put(dag.Name, latest, history)
	}
	return history
}

// excludedDAGNames returns the DAGs whose runs a list leaves out: the service
// DAGs when exclude is set, and none otherwise. The result is never nil when
// exclude is set, even if the names cannot be read, so a page cursor keeps
// its filter; the list then keeps those runs rather than fail.
func (a *API) excludedDAGNames(ctx context.Context, exclude bool) []string {
	if !exclude {
		return nil
	}
	names, err := a.dagRepository.ServiceDAGNames(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read the service DAGs; their runs stay in the list", tag.Error(err))
		return []string{}
	}
	if names == nil {
		return []string{}
	}
	return names
}

// toServiceDAGRun describes the latest run of a service DAG. It leaves out
// what changes while a run lives, so that a steady service DAG keeps one
// payload.
func toServiceDAGRun(status ir.DAGRunStatus) *api.ServiceDAGRun {
	if status.DAGRunID == "" {
		return nil
	}
	run := &api.ServiceDAGRun{
		DagRunId:   status.DAGRunID,
		Status:     api.Status(status.Status),
		StartedAt:  parseRunTime(status.StartedAt),
		FinishedAt: parseRunTime(status.FinishedAt),
	}
	if runEnded(status.Status) && !status.Status.IsSuccess() {
		step, message := firstFailure(&status)
		if step != "" {
			message = step + ": " + message
		}
		if message != "" {
			run.Error = &message
		}
	}
	return run
}

// runEnded reports whether a run with the status has ended.
func runEnded(status ir.Status) bool {
	return !status.IsActive() && status != ir.NotStarted
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
