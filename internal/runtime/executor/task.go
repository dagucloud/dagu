// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"log/slog"
	"os"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
)

// CreateTask creates a dispatch task from this DAG for distributed execution.
// It constructs a task with the given operation and run ID, setting the DAG's name
// as both the root DAG and target, and includes the DAG's YAML definition.
func CreateTask(
	dagName string,
	yamlDefinition string,
	op exec.DispatchOperation,
	runID string,
	opts ...TaskOption,
) *exec.DispatchTask {
	task := &exec.DispatchTask{
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
type TaskOption func(*exec.DispatchTask)

// WithRootDagRun sets the root DAG run name and ID in the task.
func WithRootDagRun(ref exec.DAGRunRef) TaskOption {
	return func(task *exec.DispatchTask) {
		if ref.Name == "" || ref.ID == "" {
			return // No root DAG run reference provided
		}
		task.RootDAGRunName = ref.Name
		task.RootDAGRunID = ref.ID
	}
}

// WithParentDagRun sets the parent DAG run name and ID in the task.
func WithParentDagRun(ref exec.DAGRunRef) TaskOption {
	return func(task *exec.DispatchTask) {
		if ref.Name == "" || ref.ID == "" {
			return // No parent DAG run reference provided
		}
		task.ParentDAGRunName = ref.Name
		task.ParentDAGRunID = ref.ID
	}
}

// WithTaskParams sets the parameters for the task.
func WithTaskParams(params string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.Params = params
	}
}

// WithSourceFile sets the original DAG source file path for provenance-aware flows.
func WithSourceFile(sourceFile string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.SourceFile = sourceFile
	}
}

// WithWorkerSelector sets the worker selector labels for the task.
func WithWorkerSelector(selector map[string]string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.WorkerSelector = selector
	}
}

// WithStep sets the step name for retry operations.
func WithStep(step string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.Step = step
	}
}

// WithLabels sets additional labels (comma-separated) for the task.
func WithLabels(labels string) TaskOption {
	return func(task *exec.DispatchTask) {
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
	return func(task *exec.DispatchTask) {
		task.ScheduleTime = scheduleTime
	}
}

// WithProfileName sets the runtime profile name for a dispatched task.
func WithProfileName(profileName string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.ProfileName = profileName
	}
}

// WithBaseConfig sets the base config YAML content on the task.
// This allows workers to apply base config without needing local base config files.
func WithBaseConfig(content string) TaskOption {
	return func(task *exec.DispatchTask) {
		task.BaseConfig = content
	}
}

// WithAgentSnapshot sets the opaque worker agent snapshot on the task.
func WithAgentSnapshot(snapshot []byte) TaskOption {
	return func(task *exec.DispatchTask) {
		if len(snapshot) == 0 {
			task.AgentSnapshot = nil
			return
		}
		task.AgentSnapshot = append([]byte(nil), snapshot...)
	}
}

// WithWorkspaceBundle sets workspace bundle metadata for shared-nothing dispatch.
func WithWorkspaceBundle(desc workspacebundle.Descriptor) TaskOption {
	return func(task *exec.DispatchTask) {
		task.WorkspaceBundleDigest = desc.Digest
		task.WorkspaceBundleSize = desc.Size
		task.WorkspaceBundleDAGPath = desc.DAGPath
		task.WorkspaceBundleOriginalRef = desc.OriginalRef
		task.WorkspaceBundleResolvedRef = desc.ResolvedRef
	}
}

// WithExternalStepRetry enables parent-managed step retries for the dispatched task.
func WithExternalStepRetry(enabled bool) TaskOption {
	return func(task *exec.DispatchTask) {
		task.ExternalStepRetry = enabled
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

// WithPreviousStatus sets the previous status for retry operations in shared-nothing mode.
// When set, workers can retry without needing local DAGRunStore access.
func WithPreviousStatus(status *exec.DAGRunStatus) TaskOption {
	return func(task *exec.DispatchTask) {
		if status != nil {
			if task.QueueName == "" && status.ProcGroup != "" {
				task.QueueName = status.ProcGroup
			}
			task.PreviousStatus = status
		}
	}
}
