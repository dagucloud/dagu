// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const maxCoordinatorPort = 65535

// DispatchTaskToProto converts a dispatch task to the coordinator wire shape.
func DispatchTaskToProto(task *dispatch.DispatchTask) (*coordinatorv1.Task, error) {
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
		ParallelItem:               task.ParallelItem,
		PassedEnvs:                 append([]string(nil), task.PassedEnv...),
		QueueName:                  task.QueueName,
		ProfileName:                task.ProfileName,
		DefinitionId:               task.DefinitionID,
		TriggerActor:               task.TriggerActor,
		TriggerType:                task.TriggerType.String(),
		BaseConfig:                 task.BaseConfig,
		BaseConfigWorkspace:        task.BaseConfigWorkspace,
		Labels:                     task.Labels,
		ScheduleTime:               task.ScheduleTime,
		IntervalStart:              formatWindowInstant(task.ScheduleWindow.Start),
		NextScheduledAt:            formatWindowInstant(task.ScheduleWindow.Next),
		ScheduleTimezone:           task.ScheduleWindow.Timezone,
		SourceFile:                 task.SourceFile,
		WorkerSelector:             maps.Clone(task.WorkerSelector),
		RequiredCapabilities:       append([]string(nil), task.RequiredCapabilities...),
		TargetWorkerId:             task.TargetWorkerID,
		ExternalStepRetry:          task.ExternalStepRetry,
		IncludeDownstream:          task.IncludeDownstream,
		BypassPreconditions:        task.BypassPreconditions,
		RetryPath:                  task.RetryPath,
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
func ProtoToDispatchTask(task *coordinatorv1.Task) (*dispatch.DispatchTask, error) {
	if task == nil {
		return nil, nil
	}
	if task.OwnerCoordinatorPort < 0 || task.OwnerCoordinatorPort > maxCoordinatorPort {
		return nil, fmt.Errorf("owner coordinator port out of range: %d", task.OwnerCoordinatorPort)
	}

	dispatchTask := &dispatch.DispatchTask{
		RootDAGRunName:             task.RootDagRunName,
		RootDAGRunID:               task.RootDagRunId,
		ParentDAGRunName:           task.ParentDagRunName,
		ParentDAGRunID:             task.ParentDagRunId,
		Operation:                  dispatch.DispatchOperation(task.Operation),
		DAGRunID:                   task.DagRunId,
		Target:                     task.Target,
		Definition:                 task.Definition,
		WorkerID:                   task.WorkerId,
		AttemptID:                  task.AttemptId,
		AttemptKey:                 task.AttemptKey,
		Step:                       task.Step,
		Params:                     task.Params,
		ParallelItem:               task.ParallelItem,
		PassedEnv:                  append([]string(nil), task.PassedEnvs...),
		QueueName:                  task.QueueName,
		ProfileName:                task.ProfileName,
		DefinitionID:               task.DefinitionId,
		TriggerActor:               task.TriggerActor,
		TriggerType:                ir.ParseTriggerType(task.TriggerType),
		BaseConfig:                 task.BaseConfig,
		BaseConfigWorkspace:        task.BaseConfigWorkspace,
		Labels:                     task.Labels,
		ScheduleTime:               task.ScheduleTime,
		ScheduleWindow:             ScheduleWindowFromTask(task),
		SourceFile:                 task.SourceFile,
		WorkerSelector:             maps.Clone(task.WorkerSelector),
		RequiredCapabilities:       append([]string(nil), task.RequiredCapabilities...),
		TargetWorkerID:             task.TargetWorkerId,
		ExternalStepRetry:          task.ExternalStepRetry,
		IncludeDownstream:          task.IncludeDownstream,
		BypassPreconditions:        task.BypassPreconditions,
		RetryPath:                  task.RetryPath,
		WorkspaceBundleDigest:      task.WorkspaceBundleDigest,
		WorkspaceBundleSize:        task.WorkspaceBundleSize,
		WorkspaceBundleDAGPath:     task.WorkspaceBundleDagPath,
		WorkspaceBundleOriginalRef: task.WorkspaceBundleOriginalRef,
		WorkspaceBundleResolvedRef: task.WorkspaceBundleResolvedRef,
		Owner: dispatch.CoordinatorEndpoint{
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
func WorkerStatsToProto(stats *dispatch.WorkerStats) *coordinatorv1.WorkerStats {
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
func ProtoToWorkerStats(stats *coordinatorv1.WorkerStats) *dispatch.WorkerStats {
	if stats == nil {
		return nil
	}
	runningTasks := make([]*dispatch.RunningTask, 0, len(stats.RunningTasks))
	for _, task := range stats.RunningTasks {
		runningTasks = append(runningTasks, ProtoToRunningTask(task))
	}
	return &dispatch.WorkerStats{
		TotalPollers: stats.TotalPollers,
		BusyPollers:  stats.BusyPollers,
		RunningTasks: runningTasks,
	}
}

// MachineSampleToProto converts a worker's machine sample to the coordinator
// wire shape.
func MachineSampleToProto(sample *dispatch.MachineSample) *coordinatorv1.MachineSample {
	if sample == nil {
		return nil
	}
	out := &coordinatorv1.MachineSample{
		Cpus:           int32(min(sample.CPUs, math.MaxInt32)), //nolint:gosec
		CpuLimit:       sample.CPULimit,
		CpuPercent:     sample.CPUPercent,
		CpuPeakPercent: sample.CPUPeakPercent,
		Memory:         machineUsageToProto(sample.Memory),
		Disk:           machineUsageToProto(sample.Disk),
		LogDisk:        machineUsageToProto(sample.LogDisk),
	}
	if l := sample.Load; l != nil {
		out.Load = &coordinatorv1.MachineLoad{Load1: l.Load1, Load5: l.Load5, Load15: l.Load15}
	}
	return out
}

// ProtoToMachineSample converts a worker's machine sample to the domain
// shape.
func ProtoToMachineSample(sample *coordinatorv1.MachineSample) *dispatch.MachineSample {
	if sample == nil {
		return nil
	}
	out := &dispatch.MachineSample{
		CPUs:           int(sample.Cpus),
		CPULimit:       sample.CpuLimit,
		CPUPercent:     sample.CpuPercent,
		CPUPeakPercent: sample.CpuPeakPercent,
		Memory:         protoToMachineUsage(sample.Memory),
		Disk:           protoToMachineUsage(sample.Disk),
		LogDisk:        protoToMachineUsage(sample.LogDisk),
	}
	if l := sample.Load; l != nil {
		out.Load = &dispatch.MachineLoad{Load1: l.Load1, Load5: l.Load5, Load15: l.Load15}
	}
	return out
}

func machineUsageToProto(u *dispatch.MachineUsage) *coordinatorv1.MachineUsage {
	if u == nil {
		return nil
	}
	return &coordinatorv1.MachineUsage{UsedPercent: u.UsedPercent, UsedBytes: u.UsedBytes, TotalBytes: u.TotalBytes,
		Limited: u.Limited, InodesUsedPercent: u.InodesUsedPercent}
}

func protoToMachineUsage(u *coordinatorv1.MachineUsage) *dispatch.MachineUsage {
	if u == nil {
		return nil
	}
	return &dispatch.MachineUsage{UsedPercent: u.UsedPercent, UsedBytes: u.UsedBytes, TotalBytes: u.TotalBytes,
		Limited: u.Limited, InodesUsedPercent: u.InodesUsedPercent}
}

// RunningTaskToProto converts a running task to the coordinator wire shape.
func RunningTaskToProto(task *dispatch.RunningTask) *coordinatorv1.RunningTask {
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
func ProtoToRunningTask(task *coordinatorv1.RunningTask) *dispatch.RunningTask {
	if task == nil {
		return nil
	}
	return &dispatch.RunningTask{
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

// ScheduleWindowFromTask returns the window a task's schedule slot owns.
func ScheduleWindowFromTask(task *coordinatorv1.Task) ir.ScheduleWindow {
	if task == nil {
		return ir.ScheduleWindow{}
	}
	return ir.ScheduleWindow{
		Start:    parseWindowInstant(task.IntervalStart),
		Next:     parseWindowInstant(task.NextScheduledAt),
		Timezone: task.ScheduleTimezone,
	}
}

func formatWindowInstant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseWindowInstant(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
