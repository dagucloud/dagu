// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

// StatusBuilder creates DAG-run statuses for a DAG definition.
type StatusBuilder struct {
	dag *DAG
}

// NewStatusBuilder returns a status builder for dag.
func NewStatusBuilder(dag *DAG) *StatusBuilder {
	return &StatusBuilder{dag: dag}
}

// StatusOption configures a DAG-run status during construction.
type StatusOption func(*DAGRunStatus)

// WithHierarchyRefs sets the root and parent DAG-run references.
func WithHierarchyRefs(root, parent DAGRunRef) StatusOption {
	return func(status *DAGRunStatus) {
		status.Root = root
		status.Parent = parent
	}
}

// WithAttemptID sets the attempt identifier.
func WithAttemptID(attemptID string) StatusOption {
	return func(status *DAGRunStatus) {
		status.AttemptID = attemptID
	}
}

// WithQueuedAt sets the time at which the run was queued.
func WithQueuedAt(formattedTime string) StatusOption {
	return func(status *DAGRunStatus) {
		status.QueuedAt = formattedTime
	}
}

// WithCreatedAt sets the creation time, defaulting to the current time when zero.
func WithCreatedAt(createdAt int64) StatusOption {
	return func(status *DAGRunStatus) {
		if createdAt == 0 {
			createdAt = time.Now().UnixMilli()
		}
		status.CreatedAt = createdAt
	}
}

// WithScheduleTime sets the scheduled execution time.
func WithScheduleTime(formattedTime string) StatusOption {
	return func(status *DAGRunStatus) {
		status.ScheduleTime = formattedTime
	}
}

// WithFinishedAt sets the completion time.
func WithFinishedAt(finishedAt time.Time) StatusOption {
	return func(status *DAGRunStatus) {
		status.FinishedAt = stringutil.FormatTime(finishedAt)
	}
}

// WithLogFilePath sets the DAG-run log path.
func WithLogFilePath(logFilePath string) StatusOption {
	return func(status *DAGRunStatus) {
		status.Log = logFilePath
	}
}

// WithWorkingDir sets the effective DAG-run working directory.
func WithWorkingDir(workingDir string) StatusOption {
	return func(status *DAGRunStatus) {
		status.WorkingDir = workingDir
	}
}

// WithArchiveDir sets the artifact archive directory.
func WithArchiveDir(archiveDir string) StatusOption {
	return func(status *DAGRunStatus) {
		status.ArchiveDir = archiveDir
	}
}

// WithError sets the top-level error message.
func WithError(err string) StatusOption {
	return func(status *DAGRunStatus) {
		status.Error = err
	}
}

// WithPreconditions initializes DAG-level precondition results.
func WithPreconditions(conditions []*Condition) StatusOption {
	return func(status *DAGRunStatus) {
		status.Preconditions = conditionResults(conditions)
	}
}

// WithPreconditionResults sets evaluated DAG-level preconditions.
func WithPreconditionResults(results []ConditionResult) StatusOption {
	return func(status *DAGRunStatus) {
		if results == nil {
			return
		}
		status.Preconditions = slices.Clone(results)
	}
}

// WithWorkerID sets the worker identifier.
func WithWorkerID(workerID string) StatusOption {
	return func(status *DAGRunStatus) {
		status.WorkerID = workerID
	}
}

// WithPIDStartedAt sets the operating-system process start time.
func WithPIDStartedAt(startedAt int64) StatusOption {
	return func(status *DAGRunStatus) {
		status.PIDStartedAt = startedAt
	}
}

// WithTriggerType sets the run trigger type.
func WithTriggerType(triggerType TriggerType) StatusOption {
	return func(status *DAGRunStatus) {
		status.TriggerType = triggerType
	}
}

// WithTriggerActor sets the attributable trigger actor.
func WithTriggerActor(actor string) StatusOption {
	return func(status *DAGRunStatus) {
		status.TriggerActor = actor
	}
}

// WithParallelItem sets the value bound to ITEM for a parallel child run.
func WithParallelItem(item string) StatusOption {
	return func(status *DAGRunStatus) {
		status.ParallelItem = item
	}
}

// WithAutoRetryCount sets the current automatic retry count.
func WithAutoRetryCount(autoRetryCount int) StatusOption {
	return func(status *DAGRunStatus) {
		status.AutoRetryCount = autoRetryCount
	}
}

// WithPendingStepRetries sets parent-managed step retries awaiting scheduling.
func WithPendingStepRetries(retries []PendingStepRetry) StatusOption {
	return func(status *DAGRunStatus) {
		status.PendingStepRetries = retries
	}
}

// WithConditions sets the current DAG-run conditions.
func WithConditions(conditions []DAGRunCondition) StatusOption {
	return func(status *DAGRunStatus) {
		status.Conditions = MergeDAGRunConditions(nil, conditions...)
	}
}

// WithRuntimeProfile sets the selected runtime profile metadata.
func WithRuntimeProfile(name, resolvedAt string, entries []RuntimeProfileEntry) StatusOption {
	return func(status *DAGRunStatus) {
		status.ProfileName = name
		status.ProfileResolvedAt = resolvedAt
		status.ProfileEntries = append([]RuntimeProfileEntry(nil), entries...)
	}
}

// WithDAGDefinitionID records the stable identity of the persisted DAG definition.
func WithDAGDefinitionID(id string) StatusOption {
	return func(status *DAGRunStatus) {
		if id != "" {
			status.DefinitionID = id
		}
	}
}

// WithNoReuse records whether manifest reuse is disabled.
func WithNoReuse(disabled bool) StatusOption {
	return func(status *DAGRunStatus) {
		status.NoReuse = disabled
	}
}

// WithAgentSessions carries Dagu-owned provider sessions across DAG-run attempts.
func WithAgentSessions(resources []AgentSessionResource) StatusOption {
	return func(status *DAGRunStatus) {
		status.AgentSessions = append([]AgentSessionResource(nil), resources...)
	}
}

// Create builds a DAG-run status.
func (builder *StatusBuilder) Create(
	dagRunID string,
	status Status,
	pid int,
	startedAt time.Time,
	opts ...StatusOption,
) DAGRunStatus {
	result := InitialStatus(builder.dag)
	result.DAGRunID = dagRunID
	result.Status = status
	result.PID = PID(pid)
	result.StartedAt = stringutil.FormatTime(startedAt)
	result.CreatedAt = time.Now().UnixMilli()

	for _, opt := range opts {
		opt(&result)
	}
	result.AgentSessions = MergeAgentSessionResources(result.AgentSessions, result.Nodes)
	NormalizeDAGRunConditions(&result)

	if result.PendingStepRetries == nil {
		result.PendingStepRetries = PendingStepRetriesFromStatus(&result)
	}

	if result.AttemptKey == "" && result.AttemptID != "" {
		rootName := result.Root.Name
		rootID := result.Root.ID
		if rootName == "" {
			rootName = result.Name
			rootID = result.DAGRunID
		}
		result.AttemptKey = GenerateAttemptKey(
			rootName,
			rootID,
			result.Name,
			result.DAGRunID,
			result.AttemptID,
		)
	}

	return result
}
