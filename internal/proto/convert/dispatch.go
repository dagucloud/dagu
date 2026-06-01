// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

const maxCoordinatorPort = 65535

// DispatchTaskToProto converts a dispatch task to the coordinator wire shape.
func DispatchTaskToProto(task *exec.DispatchTask) (*coordinatorv1.Task, error) {
	if task == nil {
		return nil, nil
	}
	if task.Owner.Port < 0 || task.Owner.Port > maxCoordinatorPort {
		return nil, fmt.Errorf("owner coordinator port out of range: %d", task.Owner.Port)
	}

	protoTask := &coordinatorv1.Task{
		RootDagRunName:             task.RootDAGRunName,
		RootDagRunId:               task.RootDAGRunID,
		ParentDagRunName:           task.ParentDAGRunName,
		ParentDagRunId:             task.ParentDAGRunID,
		Operation:                  coordinatorv1.Operation(task.Operation),
		DagRunId:                   task.DAGRunID,
		Target:                     task.Target,
		Definition:                 task.Definition,
		WorkerId:                   task.WorkerID,
		AttemptId:                  task.AttemptID,
		AttemptKey:                 task.AttemptKey,
		Step:                       task.Step,
		Params:                     task.Params,
		QueueName:                  task.QueueName,
		ProfileName:                task.ProfileName,
		BaseConfig:                 task.BaseConfig,
		Labels:                     task.Labels,
		ScheduleTime:               task.ScheduleTime,
		SourceFile:                 task.SourceFile,
		WorkerSelector:             maps.Clone(task.WorkerSelector),
		AgentSnapshot:              append([]byte(nil), task.AgentSnapshot...),
		ExternalStepRetry:          task.ExternalStepRetry,
		WorkspaceBundleDigest:      task.WorkspaceBundleDigest,
		WorkspaceBundleSize:        task.WorkspaceBundleSize,
		WorkspaceBundleDagPath:     task.WorkspaceBundleDAGPath,
		WorkspaceBundleOriginalRef: task.WorkspaceBundleOriginalRef,
		WorkspaceBundleResolvedRef: task.WorkspaceBundleResolvedRef,
		OwnerCoordinatorId:         task.Owner.ID,
		OwnerCoordinatorHost:       task.Owner.Host,
		OwnerCoordinatorPort:       int32(task.Owner.Port), //nolint:gosec // Port is range-checked above before narrowing to proto int32.
		ClaimToken:                 task.ClaimToken,
	}
	if task.PreviousStatus != nil {
		protoStatus, err := DAGRunStatusToProto(task.PreviousStatus)
		if err != nil {
			return nil, fmt.Errorf("convert previous status to proto: %w", err)
		}
		protoTask.PreviousStatus = protoStatus
	}
	return protoTask, nil
}

// ProtoToDispatchTask converts a coordinator wire task to a dispatch task.
func ProtoToDispatchTask(task *coordinatorv1.Task) (*exec.DispatchTask, error) {
	if task == nil {
		return nil, nil
	}
	if task.OwnerCoordinatorPort < 0 || task.OwnerCoordinatorPort > maxCoordinatorPort {
		return nil, fmt.Errorf("owner coordinator port out of range: %d", task.OwnerCoordinatorPort)
	}

	dispatchTask := &exec.DispatchTask{
		RootDAGRunName:             task.RootDagRunName,
		RootDAGRunID:               task.RootDagRunId,
		ParentDAGRunName:           task.ParentDagRunName,
		ParentDAGRunID:             task.ParentDagRunId,
		Operation:                  exec.DispatchOperation(task.Operation),
		DAGRunID:                   task.DagRunId,
		Target:                     task.Target,
		Definition:                 task.Definition,
		WorkerID:                   task.WorkerId,
		AttemptID:                  task.AttemptId,
		AttemptKey:                 task.AttemptKey,
		Step:                       task.Step,
		Params:                     task.Params,
		QueueName:                  task.QueueName,
		ProfileName:                task.ProfileName,
		BaseConfig:                 task.BaseConfig,
		Labels:                     task.Labels,
		ScheduleTime:               task.ScheduleTime,
		SourceFile:                 task.SourceFile,
		WorkerSelector:             maps.Clone(task.WorkerSelector),
		AgentSnapshot:              append([]byte(nil), task.AgentSnapshot...),
		ExternalStepRetry:          task.ExternalStepRetry,
		WorkspaceBundleDigest:      task.WorkspaceBundleDigest,
		WorkspaceBundleSize:        task.WorkspaceBundleSize,
		WorkspaceBundleDAGPath:     task.WorkspaceBundleDagPath,
		WorkspaceBundleOriginalRef: task.WorkspaceBundleOriginalRef,
		WorkspaceBundleResolvedRef: task.WorkspaceBundleResolvedRef,
		Owner: exec.CoordinatorEndpoint{
			ID:   task.OwnerCoordinatorId,
			Host: task.OwnerCoordinatorHost,
			Port: int(task.OwnerCoordinatorPort),
		},
		ClaimToken: task.ClaimToken,
	}
	if task.PreviousStatus != nil {
		status, err := ProtoToDAGRunStatus(task.PreviousStatus)
		if err != nil {
			return nil, fmt.Errorf("convert previous status from proto: %w", err)
		}
		dispatchTask.PreviousStatus = status
	}
	return dispatchTask, nil
}

// WorkerStatsToProto converts worker stats to the coordinator wire shape.
func WorkerStatsToProto(stats *exec.WorkerStats) *coordinatorv1.WorkerStats {
	if stats == nil {
		return nil
	}
	runningTasks := make([]*coordinatorv1.RunningTask, 0, len(stats.RunningTasks))
	for _, task := range stats.RunningTasks {
		runningTasks = append(runningTasks, RunningTaskToProto(task))
	}
	return &coordinatorv1.WorkerStats{
		TotalPollers: stats.TotalPollers,
		BusyPollers:  stats.BusyPollers,
		RunningTasks: runningTasks,
	}
}

// ProtoToWorkerStats converts coordinator worker stats to the domain shape.
func ProtoToWorkerStats(stats *coordinatorv1.WorkerStats) *exec.WorkerStats {
	if stats == nil {
		return nil
	}
	runningTasks := make([]*exec.RunningTask, 0, len(stats.RunningTasks))
	for _, task := range stats.RunningTasks {
		runningTasks = append(runningTasks, ProtoToRunningTask(task))
	}
	return &exec.WorkerStats{
		TotalPollers: stats.TotalPollers,
		BusyPollers:  stats.BusyPollers,
		RunningTasks: runningTasks,
	}
}

// RunningTaskToProto converts a running task to the coordinator wire shape.
func RunningTaskToProto(task *exec.RunningTask) *coordinatorv1.RunningTask {
	if task == nil {
		return nil
	}
	return &coordinatorv1.RunningTask{
		DagRunId:         task.DAGRunID,
		DagName:          task.DAGName,
		StartedAt:        task.StartedAt,
		RootDagRunName:   task.RootDAGRunName,
		RootDagRunId:     task.RootDAGRunID,
		ParentDagRunName: task.ParentDAGRunName,
		ParentDagRunId:   task.ParentDAGRunID,
		AttemptKey:       task.AttemptKey,
	}
}

// ProtoToRunningTask converts a coordinator running task to the domain shape.
func ProtoToRunningTask(task *coordinatorv1.RunningTask) *exec.RunningTask {
	if task == nil {
		return nil
	}
	return &exec.RunningTask{
		DAGRunID:         task.DagRunId,
		DAGName:          task.DagName,
		StartedAt:        task.StartedAt,
		RootDAGRunName:   task.RootDagRunName,
		RootDAGRunID:     task.RootDagRunId,
		ParentDAGRunName: task.ParentDagRunName,
		ParentDAGRunID:   task.ParentDagRunId,
		AttemptKey:       task.AttemptKey,
	}
}
