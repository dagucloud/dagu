// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// ServiceDAGState tells what a service DAG is doing, as far as its latest run
// shows it. It needs no memory of the scheduler, so any process can compute it.
type ServiceDAGState string

const (
	// ServiceDAGStatePending means no run is alive yet: the DAG has no run, or
	// its first run waits in the queue.
	ServiceDAGStatePending ServiceDAGState = "pending"
	// ServiceDAGStateRunning means a run is alive.
	ServiceDAGStateRunning ServiceDAGState = "running"
	// ServiceDAGStateWaiting means a run is alive but waits for a person, such
	// as an approval, and does nothing until someone acts on it.
	ServiceDAGStateWaiting ServiceDAGState = "waiting"
	// ServiceDAGStateRestarting means the latest run succeeded, or an operator
	// stopped the latest run of an always DAG, and the next run is due or waits
	// in the queue.
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
	// Suspending does not stop a run that is alive.
	case latest.Status == ir.Waiting:
		return ServiceDAGStateWaiting, nil
	case latest.Status == ir.Running:
		return ServiceDAGStateRunning, nil
	case latest.Status == ir.NotStarted || latest.Status == ir.Queued:
		// The restart of a DAG that ran waits in the queue for a short time.
		if latest.TriggerType == ir.TriggerTypeRestart && !suspended {
			return ServiceDAGStateRestarting, nil
		}
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

// NextRestart returns when the scheduler starts the next run of a service DAG
// after its latest run ended, given the backoff that the scheduler last shared
// for the DAG. A run may have ended since that save, so it is counted here as the
// scheduler counts it. ok is false when the end of the run is unknown.
func NextRestart(restartWait time.Duration, latest *ir.DAGRunStatus, backoff schedulerstate.RestartBackoff) (time.Time, bool) {
	endedAt, ok := retryReferenceTime(latest)
	if !ok {
		return time.Time{}, false
	}
	return restartDue(endedAt, backoffWait(restartWait, countRun(backoff, latest).Failures)), true
}

// IsStopRequested reports whether an operator asked to stop a run: a stop from
// the CLI, the API or the coordinator leaves an abort request on the attempt,
// which other aborted runs lack.
func IsStopRequested(ctx context.Context, runs *persis.DAGRunRepository, dag *ir.DAG, runID string) (bool, error) {
	attempt, err := runs.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, runID))
	if err != nil {
		return false, err
	}
	return attempt.IsAborting(ctx)
}

func pendingOrSuspended(suspended bool) ServiceDAGState {
	if suspended {
		return ServiceDAGStateSuspended
	}
	return ServiceDAGStatePending
}
