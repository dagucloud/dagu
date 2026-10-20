// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import "github.com/dagucloud/dagu/v2/internal/ir"

// AlwaysOnState tells what an always-on DAG is doing, as far as its latest run
// shows it. It needs no memory of the scheduler, so any process can compute it.
type AlwaysOnState string

const (
	// AlwaysOnStatePending means no run is alive yet: the DAG has no run, or its
	// latest run waits in the queue.
	AlwaysOnStatePending AlwaysOnState = "pending"
	// AlwaysOnStateRunning means a run is alive.
	AlwaysOnStateRunning AlwaysOnState = "running"
	// AlwaysOnStateRestarting means the latest run succeeded and the next run is due.
	AlwaysOnStateRestarting AlwaysOnState = "restarting"
	// AlwaysOnStateFailing means the latest run did not succeed and the next run is due.
	AlwaysOnStateFailing AlwaysOnState = "failing"
	// AlwaysOnStateSuspended means the DAG gets no new run.
	AlwaysOnStateSuspended AlwaysOnState = "suspended"
	// AlwaysOnStateStopped means an operator stopped the latest run of an
	// unless-stopped DAG, so no run follows.
	AlwaysOnStateStopped AlwaysOnState = "stopped"
)

// DescribeAlwaysOn returns the state of an always-on DAG. stopRequested tells
// whether an operator stopped the latest run. It is called only when the answer
// matters: for an aborted run of an unless-stopped DAG.
func DescribeAlwaysOn(
	policy ir.RestartPolicy,
	latest *ir.DAGRunStatus,
	suspended bool,
	stopRequested func() (bool, error),
) (AlwaysOnState, error) {
	switch {
	case latest == nil || latest.DAGRunID == "":
		return pendingOrSuspended(suspended), nil
	case latest.Status == ir.Running || latest.Status == ir.Waiting:
		// Suspending does not stop a run that is alive.
		return AlwaysOnStateRunning, nil
	case latest.Status == ir.NotStarted || latest.Status == ir.Queued:
		return pendingOrSuspended(suspended), nil
	case suspended:
		return AlwaysOnStateSuspended, nil
	case latest.Status.IsSuccess():
		return AlwaysOnStateRestarting, nil
	}

	if policy == ir.RestartPolicyUnlessStopped && latest.Status == ir.Aborted && stopRequested != nil {
		stopped, err := stopRequested()
		if err != nil {
			return "", err
		}
		if stopped {
			return AlwaysOnStateStopped, nil
		}
	}
	return AlwaysOnStateFailing, nil
}

func pendingOrSuspended(suspended bool) AlwaysOnState {
	if suspended {
		return AlwaysOnStateSuspended
	}
	return AlwaysOnStatePending
}
