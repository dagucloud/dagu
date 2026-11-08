// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import "github.com/dagucloud/dagu/v2/internal/ir"

// ServiceDAGState tells what a service DAG is doing, as far as its latest run
// shows it. It needs no memory of the scheduler, so any process can compute it.
type ServiceDAGState string

const (
	// ServiceDAGStatePending means no run is alive yet: the DAG has no run, or
	// its latest run waits in the queue.
	ServiceDAGStatePending ServiceDAGState = "pending"
	// ServiceDAGStateRunning means a run is alive.
	ServiceDAGStateRunning ServiceDAGState = "running"
	// ServiceDAGStateRestarting means the latest run succeeded, or an operator
	// stopped the latest run of an always DAG, and the next run is due.
	ServiceDAGStateRestarting ServiceDAGState = "restarting"
	// ServiceDAGStateFailing means the latest run did not succeed, with no stop
	// request, and the next run is due.
	ServiceDAGStateFailing ServiceDAGState = "failing"
	// ServiceDAGStateSuspended means the DAG gets no new run.
	ServiceDAGStateSuspended ServiceDAGState = "suspended"
	// ServiceDAGStateStopped means an operator stopped the latest run of an
	// unless-stopped DAG, so no run follows.
	ServiceDAGStateStopped ServiceDAGState = "stopped"
)

// DescribeServiceDAG returns the state of a service DAG. stopRequested tells
// whether an operator stopped the latest run. It is called only when the answer
// matters: for a run that has ended, unless an always DAG's run succeeded.
func DescribeServiceDAG(
	policy ir.RestartPolicy,
	latest *ir.DAGRunStatus,
	suspended bool,
	stopRequested func() (bool, error),
) (ServiceDAGState, error) {
	switch {
	case latest == nil || latest.DAGRunID == "":
		return pendingOrSuspended(suspended), nil
	case latest.Status == ir.Running || latest.Status == ir.Waiting:
		// Suspending does not stop a run that is alive.
		return ServiceDAGStateRunning, nil
	case latest.Status == ir.NotStarted || latest.Status == ir.Queued:
		return pendingOrSuspended(suspended), nil
	case suspended:
		return ServiceDAGStateSuspended, nil
	}

	if latest.Status.IsSuccess() && policy != ir.RestartPolicyUnlessStopped {
		return ServiceDAGStateRestarting, nil
	}
	if stopRequested != nil {
		stopped, err := stopRequested()
		if err != nil {
			return "", err
		}
		if stopped {
			// A stop keeps an unless-stopped DAG down; an always DAG runs again.
			if policy == ir.RestartPolicyUnlessStopped {
				return ServiceDAGStateStopped, nil
			}
			return ServiceDAGStateRestarting, nil
		}
	}
	if latest.Status.IsSuccess() {
		return ServiceDAGStateRestarting, nil
	}
	return ServiceDAGStateFailing, nil
}

func pendingOrSuspended(suspended bool) ServiceDAGState {
	if suspended {
		return ServiceDAGStateSuspended
	}
	return ServiceDAGStatePending
}
