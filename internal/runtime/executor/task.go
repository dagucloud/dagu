// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"log/slog"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
)

// CreateTask creates a dispatch task from this DAG for distributed execution.
// It constructs a task with the given operation and run ID, setting the DAG's name
// as both the root DAG and target, and includes the DAG's YAML definition.
func CreateTask(
	dagName string,
	yamlDefinition string,
	op dispatch.DispatchOperation,
	runID string,
	opts ...TaskOption,
) *dispatch.DispatchTask {
	task := &dispatch.DispatchTask{
		RootDAGRunName: dagName,
		RootDAGRunID:   runID,
		Operation:      op,
		DAGRunID:       runID,
		Target:         dagName,
		Definition:     yamlDefinition,
	}

	for _, opt := range opts {
		opt(task)
	}

	return task
}

// TaskOption is a function that modifies a dispatch task.
type TaskOption func(*dispatch.DispatchTask)

// WithRootDagRun sets the root DAG run name and ID in the task.
func WithRootDagRun(ref ir.DAGRunRef) TaskOption {
	return func(task *dispatch.DispatchTask) {
		if ref.Name == "" || ref.ID == "" {
			return // No root DAG run reference provided
		}
		task.RootDAGRunName = ref.Name
		task.RootDAGRunID = ref.ID
	}
}

// WithParentDagRun sets the parent DAG run name and ID in the task.
func WithParentDagRun(ref ir.DAGRunRef) TaskOption {
	return func(task *dispatch.DispatchTask) {
		if ref.Name == "" || ref.ID == "" {
			return // No parent DAG run reference provided
		}
		task.ParentDAGRunName = ref.Name
		task.ParentDAGRunID = ref.ID
	}
}

// WithTaskParams sets the parameters for the task.
func WithTaskParams(params string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.Params = params
	}
}

// WithParallelItem sets the value bound to ITEM for a parallel child run.
func WithParallelItem(item string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.ParallelItem = item
	}
}

// WithSourceFile sets the original DAG source file path for provenance-aware flows.
func WithSourceFile(sourceFile string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.SourceFile = sourceFile
	}
}

// WithSourceWorkDir sets the source workspace for source-less DAG definitions.
func WithSourceWorkDir(dir string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.SourceWorkDir = dir
	}
}

// WithWorkerSelector sets the worker selector labels for the task.
func WithWorkerSelector(selector map[string]string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.WorkerSelector = selector
	}
}

// WithTargetWorkerID pins the task to one execution host.
func WithTargetWorkerID(workerID string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.TargetWorkerID = workerID
	}
}

// WithStep sets the step name for retry operations.
func WithStep(step string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.Step = step
	}
}

// WithIncludeDownstream requests that a step retry also reset reachable descendants.
func WithIncludeDownstream(enabled bool) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.IncludeDownstream = enabled
	}
}

// WithLabels sets additional labels (comma-separated) for the task.
func WithLabels(labels string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.Labels = labels
	}
}

// WithTags sets additional labels (comma-separated) for the task.
// Deprecated: use WithLabels.
func WithTags(tags string) TaskOption {
	return WithLabels(tags)
}

// WithScheduleTime sets the RFC 3339 timestamp of when the task was scheduled.
func WithScheduleTime(scheduleTime string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.ScheduleTime = scheduleTime
	}
}

// WithProfileName sets the runtime profile name for a dispatched task.
func WithProfileName(profileName string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.ProfileName = profileName
	}
}

// WithDefinitionID sets the stable DAG definition identity on a dispatched task.
func WithDefinitionID(id string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.DefinitionID = id
	}
}

// WithTriggerActor sets the attributable trigger actor for a dispatched task.
func WithTriggerActor(actor string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.TriggerActor = actor
	}
}

// WithBaseConfig sets the base config YAML content on the task.
// This allows workers to apply base config without needing local base config files.
func WithBaseConfig(content string) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.BaseConfig = content
	}
}

// WithWorkspaceBundle sets workspace bundle metadata for worker dispatch.
func WithWorkspaceBundle(desc workspacebundle.Descriptor) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.WorkspaceBundleDigest = desc.Digest
		task.WorkspaceBundleSize = desc.Size
		task.WorkspaceBundleDAGPath = desc.DAGPath
		task.WorkspaceBundleOriginalRef = desc.OriginalRef
		task.WorkspaceBundleResolvedRef = desc.ResolvedRef
	}
}

// WithExternalStepRetry enables parent-managed step retries for the dispatched task.
func WithExternalStepRetry(enabled bool) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.ExternalStepRetry = enabled
	}
}

// WithRetryPath sets the persisted child DAG path for a retry task.
func WithRetryPath(path dagrun.RetryPath) TaskOption {
	return func(task *dispatch.DispatchTask) {
		task.RetryPath = path.Encode()
	}
}

// ResolveBaseConfig returns the base config content for a DAG task.
// It prefers embedded BaseConfigData from the DAG, falling back to reading the file at fallbackPath.
func ResolveBaseConfig(baseConfigData []byte, fallbackPath string) string {
	if len(baseConfigData) > 0 {
		return string(baseConfigData)
	}
	if fallbackPath == "" {
		return ""
	}
	data, err := fileutil.ReadFile(fallbackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("failed to read base config file", "path", fallbackPath, "error", err)
		}
		return ""
	}
	return string(data)
}

// WithPreviousStatus sets the previous status for retry operations.
// When set, workers can retry without querying local DAG-run persistence.
func WithPreviousStatus(status *ir.DAGRunStatus) TaskOption {
	return func(task *dispatch.DispatchTask) {
		if status != nil {
			if task.QueueName == "" && status.ProcGroup != "" {
				task.QueueName = status.ProcGroup
			}
			task.PreviousStatus = status
		}
	}
}
