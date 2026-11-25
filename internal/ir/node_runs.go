// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// NodeRuns records the run of each node a step with nodes matched (RFC 013),
// in node name order. It is kept beside the step's log files, outside the run
// status, so that a step over many nodes does not grow every status snapshot.
type NodeRuns struct {
	Runs []NodeRun `json:"runs"`
}

// NodeRun records the child run of one matched node.
type NodeRun struct {
	Node       string        `json:"node"`
	Status     NodeRunStatus `json:"status"`
	DAGName    string        `json:"dagName,omitempty"`
	DAGRunID   string        `json:"dagRunId,omitempty"`
	StartedAt  string        `json:"startedAt,omitempty"`
	FinishedAt string        `json:"finishedAt,omitempty"`
	// Steps is the progress the child run reached, once it finished.
	Steps *StepProgress `json:"steps,omitempty"`
	// FailedStep names the step that failed last in the child run.
	FailedStep string `json:"failedStep,omitempty"`
}

// StepProgress counts the steps of a run that succeeded.
type StepProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// NodeRunStatus is the status of a matched node's run: a child run status,
// or one of the statuses of a node that did not run.
type NodeRunStatus string

// Statuses of a node's run, as the aggregate of a step with nodes reports them.
const (
	NodeRunNotStarted         NodeRunStatus = "not_started"
	NodeRunRunning            NodeRunStatus = "running"
	NodeRunWaiting            NodeRunStatus = "waiting"
	NodeRunSucceeded          NodeRunStatus = "succeeded"
	NodeRunPartiallySucceeded NodeRunStatus = "partially_succeeded"
	NodeRunFailed             NodeRunStatus = "failed"
	NodeRunAborted            NodeRunStatus = "aborted"
	NodeRunRejected           NodeRunStatus = "rejected"
	NodeRunDisabled           NodeRunStatus = "disabled"
	// NodeRunRemoved is a node of a retried step's first attempt that has
	// left the inventory since. It does not run (RFC 013 §Retry).
	NodeRunRemoved NodeRunStatus = "removed"
)

// NodeRunStatusOf returns the status of a node whose child run returned with
// status. A child run that returned without finishing, such as a queued one
// a retry reused, failed, as the step counts it.
func NodeRunStatusOf(status Status) NodeRunStatus {
	switch status {
	case Succeeded:
		return NodeRunSucceeded
	case PartiallySucceeded:
		return NodeRunPartiallySucceeded
	case Failed:
		return NodeRunFailed
	case Aborted:
		return NodeRunAborted
	case Waiting:
		return NodeRunWaiting
	case Rejected:
		return NodeRunRejected
	case NotStarted, Running, Queued:
		return NodeRunFailed
	default:
		return NodeRunFailed
	}
}

// NodeRunCounts counts the runs of a step's matched nodes by status. A
// partially succeeded run counts as succeeded and a rejected one as failed.
type NodeRunCounts struct {
	Total      int `json:"total"`
	NotStarted int `json:"notStarted"`
	Running    int `json:"running"`
	Waiting    int `json:"waiting"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Aborted    int `json:"aborted"`
	Disabled   int `json:"disabled"`
	Removed    int `json:"removed"`
}

// CountNodeRuns counts runs by status.
func CountNodeRuns(runs []NodeRun) NodeRunCounts {
	counts := NodeRunCounts{Total: len(runs)}
	for _, run := range runs {
		switch run.Status {
		case NodeRunNotStarted:
			counts.NotStarted++
		case NodeRunRunning:
			counts.Running++
		case NodeRunWaiting:
			counts.Waiting++
		case NodeRunSucceeded, NodeRunPartiallySucceeded:
			counts.Succeeded++
		case NodeRunAborted:
			counts.Aborted++
		case NodeRunDisabled:
			counts.Disabled++
		case NodeRunRemoved:
			counts.Removed++
		case NodeRunFailed, NodeRunRejected:
			counts.Failed++
		default:
			// A status this version does not know counts as failed, as the
			// aggregate counts it.
			counts.Failed++
		}
	}
	return counts
}
