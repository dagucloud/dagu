// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ServiceDAGSources are what the state of the service DAGs is read from:
// the DAGs, the history of their runs, what the scheduler shares of its
// restart backoff, and whether it holds restarts now. Filter limits the DAGs
// to some workspaces; nil reads them all.
type ServiceDAGSources struct {
	DAGs    *persis.DAGRepository
	Runs    *persis.DAGRunRepository
	History runtime.Manager
	Backoff map[string]schedulerstate.RestartBackoff
	// Held is set while the scheduler starts no run of a service DAG: an
	// operator paused it, or no scheduler runs.
	Held   bool
	Filter *workspace.WorkspaceFilter
	// Saved takes the DAGs from the listing that the DAG store saved last,
	// when it has one, and reads no DAG file. It can miss a definition changed
	// outside the store since that listing, and it reports no file errors.
	Saved bool
}

// ServiceDAGView is a service DAG with its latest run and state. ReadError
// is why the latest run or the state could not be read, which makes the
// state failing. NextRestartAt is when the scheduler starts the next run of
// a DAG that waits to restart, unless restarts are held.
type ServiceDAGView struct {
	Item          persis.DAGListItem
	Latest        ir.DAGRunStatus
	State         ServiceDAGState
	ReadError     error
	NextRestartAt *time.Time
}

// ListServiceDAGs reads the service DAGs by name, each with its latest run
// and state, and the errors of the DAG files that could not be read.
func ListServiceDAGs(ctx context.Context, src ServiceDAGSources) ([]ServiceDAGView, []string, error) {
	paginator := pagination.NewPaginator(1, int(^uint(0)>>1))
	result, errList, err := src.DAGs.List(ctx, persis.DAGListOptions{
		Paginator:       &paginator,
		OnlyServiceDAGs: true,
		Saved:           src.Saved,
		Sort:            "name",
		Order:           "asc",
		WorkspaceFilter: src.Filter,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error listing service DAGs: %w", err)
	}
	history := src.History.AllHistory()
	views := make([]ServiceDAGView, 0, len(result.Items))
	for _, item := range result.Items {
		latest, statusErr := history.ReadLatestStatus(ctx, item.DAG)
		// A run keeps the workspace its DAG had when it started, so after a move
		// the latest run may belong to a workspace the caller cannot see.
		if !src.Filter.MatchesLabels(ir.NewLabels(latest.Labels)) {
			latest = ir.InitialStatus(item.DAG)
		}
		state, stateErr := DescribeServiceDAG(item.RestartPolicy, &latest, item.Suspended, func() (bool, error) {
			return IsStopRequested(ctx, src.Runs, item.DAG, latest.DAGRunID)
		})
		view := ServiceDAGView{Item: item, Latest: latest, State: state, ReadError: errors.Join(statusErr, stateErr)}
		if view.ReadError != nil {
			view.State = ServiceDAGStateFailing
		}
		if view.ReadError == nil && !src.Held && (state == ServiceDAGStateRestarting || state == ServiceDAGStateFailing) {
			if at, ok := NextRestart(item.RestartWait, &latest, src.Backoff[item.Name]); ok {
				view.NextRestartAt = &at
			}
		}
		views = append(views, view)
	}
	return views, errList, nil
}
