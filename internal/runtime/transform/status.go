// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform

import (
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

// StatusBuilder creates Status objects for a specific DAG
type StatusBuilder struct {
	dag *core.DAG // The DAG for which to create status objects
}

// NewStatusBuilder creates a new StatusFactory for the specified DAG
func NewStatusBuilder(dag *core.DAG) *StatusBuilder {
	return &StatusBuilder{dag: dag}
}

// StatusOption is a functional option pattern for configuring Status objects
type StatusOption func(*exec.DAGRunStatus)

// WithHierarchyRefs returns a StatusOption that sets the root DAG information
func WithHierarchyRefs(root exec.DAGRunRef, parent exec.DAGRunRef) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.Root = root
		s.Parent = parent
	}
}

// WithNodes returns a StatusOption that sets the node data for the status
func WithNodes(nodes []runtime.NodeData) StatusOption {
	return func(s *exec.DAGRunStatus) {
		convertedNodes := make([]*exec.Node, len(nodes))
		for i, n := range nodes {
			convertedNodes[i] = newNode(n)
		}
		s.Nodes = convertedNodes
	}
}

// WithAttemptID returns a StatusOption that sets the attempt ID
func WithAttemptID(attemptID string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.AttemptID = attemptID
	}
}

// WithAttemptKey returns a StatusOption that sets the attempt key
func WithAttemptKey(attemptKey string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.AttemptKey = attemptKey
	}
}

// WithQueuedAt returns a StatusOption that sets the queued time
func WithQueuedAt(formattedTime string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.QueuedAt = formattedTime
	}
}

// WithCreatedAt returns a StatusOption that sets the created time
func WithCreatedAt(t int64) StatusOption {
	return func(s *exec.DAGRunStatus) {
		if t == 0 {
			t = time.Now().UnixMilli()
		}
		s.CreatedAt = t
	}
}

// WithScheduleTime returns a StatusOption that sets the schedule time
func WithScheduleTime(formattedTime string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.ScheduleTime = formattedTime
	}
}

// WithFinishedAt returns a StatusOption that sets the finished time
func WithFinishedAt(t time.Time) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.FinishedAt = exec.FormatTime(t)
	}
}

// convertNodeIfPresent converts a runtime.Node to exec.Node if non-nil
func convertNodeIfPresent(node *runtime.Node) *exec.Node {
	if node == nil {
		return nil
	}
	return newNode(node.NodeData())
}

// WithOnInitNode returns a StatusOption that sets the init handler node
func WithOnInitNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnInit = convertNodeIfPresent(node)
	}
}

// WithOnExitNode returns a StatusOption that sets the exit handler node
func WithOnExitNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnExit = convertNodeIfPresent(node)
	}
}

// WithOnSuccessNode returns a StatusOption that sets the success handler node
func WithOnSuccessNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnSuccess = convertNodeIfPresent(node)
	}
}

// WithOnFailureNode returns a StatusOption that sets the failure handler node
func WithOnFailureNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnFailure = convertNodeIfPresent(node)
	}
}

// WithOnAbortNode returns a StatusOption that sets the abort handler node
func WithOnAbortNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnAbort = convertNodeIfPresent(node)
	}
}

// WithOnWaitNode returns a StatusOption that sets the wait handler node
func WithOnWaitNode(node *runtime.Node) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.OnWait = convertNodeIfPresent(node)
	}
}

// WithLogFilePath returns a StatusOption that sets the log file path
func WithLogFilePath(logFilePath string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.Log = logFilePath
	}
}

// WithWorkingDir returns a StatusOption that sets the effective dag-run
// working directory path.
func WithWorkingDir(workingDir string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.WorkingDir = workingDir
	}
}

// WithArchiveDir returns a StatusOption that sets the artifact/archive directory path.
func WithArchiveDir(archiveDir string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.ArchiveDir = archiveDir
	}
}

// WithError returns a StatusOption that sets the top-level error message
func WithError(err string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.Error = err
	}
}

// WithPreconditions returns a StatusOption that sets the preconditions
func WithPreconditions(conditions []*core.Condition) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.Preconditions = conditions
	}
}

// WithWorkerID returns a StatusOption that sets the worker ID
func WithWorkerID(workerID string) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.WorkerID = workerID
	}
}

// WithTriggerType returns a StatusOption that sets the trigger type
func WithTriggerType(triggerType core.TriggerType) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.TriggerType = triggerType
	}
}

// WithAutoRetryCount returns a StatusOption that sets the DAG-run auto-retry count.
func WithAutoRetryCount(autoRetryCount int) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.AutoRetryCount = autoRetryCount
	}
}

// WithPendingStepRetries returns a StatusOption that sets any parent-managed
// step retries that are waiting to be scheduled.
func WithPendingStepRetries(retries []exec.PendingStepRetry) StatusOption {
	return func(s *exec.DAGRunStatus) {
		s.PendingStepRetries = retries
	}
}

// Create builds a Status object for a dag-run with the specified parameters
func (f *StatusBuilder) Create(
	dagRunID string,
	status core.Status,
	pid int,
	startedAt time.Time,
	opts ...StatusOption,
) exec.DAGRunStatus {
	statusObj := exec.InitialStatus(f.dag)
	statusObj.DAGRunID = dagRunID
	statusObj.Status = status
	statusObj.PID = exec.PID(pid)
	statusObj.StartedAt = exec.FormatTime(startedAt)
	statusObj.CreatedAt = time.Now().UnixMilli()

	for _, opt := range opts {
		opt(&statusObj)
	}

	if statusObj.PendingStepRetries == nil {
		statusObj.PendingStepRetries = exec.PendingStepRetriesFromStatus(&statusObj)
	}

	// Generate AttemptKey if not already set and we have all required fields
	if statusObj.AttemptKey == "" && statusObj.AttemptID != "" {
		rootName := statusObj.Root.Name
		rootID := statusObj.Root.ID
		if rootName == "" {
			rootName = statusObj.Name // Self-referential for root runs
			rootID = statusObj.DAGRunID
		}
		statusObj.AttemptKey = exec.GenerateAttemptKey(
			rootName, rootID,
			statusObj.Name, statusObj.DAGRunID,
			statusObj.AttemptID,
		)
	}

	return statusObj
}
