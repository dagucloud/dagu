// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform

import (
	"maps"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// SeedNodes returns the initial node states for a new run of dag. Steps named
// in skipped are recorded as skipped by retry, so their dependents still run,
// and carry the outputs and completion details of the matching Reusable node
// in source. Every other step starts as not started. source may be nil.
func SeedNodes(dag *ir.DAG, source *ir.DAGRunStatus, skipped []string) []runtime.NodeData {
	sourceNodes := make(map[string]*ir.Node)
	if source != nil {
		for _, node := range source.Nodes {
			if Reusable(node) {
				sourceNodes[node.Step.Name] = node
			}
		}
	}
	skipSet := make(map[string]struct{}, len(skipped))
	for _, stepName := range skipped {
		skipSet[stepName] = struct{}{}
	}

	nodes := make([]runtime.NodeData, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		data := runtime.NodeData{
			Step: step,
			State: runtime.NodeState{
				Status: ir.NodeNotStarted,
			},
		}
		if _, ok := skipSet[step.Name]; ok {
			data.State = skippedNodeState(sourceNodes[step.Name])
		}
		nodes = append(nodes, data)
	}
	return nodes
}

// Reusable reports whether a node's recorded state may stand in for a skipped
// step of another run: the node succeeded, or it was itself skipped by retry.
func Reusable(node *ir.Node) bool {
	if node == nil {
		return false
	}
	return node.Status.IsSuccess() || (node.Status == ir.NodeSkipped && node.SkippedByRetry)
}

func skippedNodeState(source *ir.Node) runtime.NodeState {
	state := runtime.NodeState{
		Status:         ir.NodeSkipped,
		SkippedByRetry: true,
	}
	if source == nil {
		return state
	}

	startedAt, _ := stringutil.ParseTime(source.StartedAt)
	finishedAt, _ := stringutil.ParseTime(source.FinishedAt)
	retriedAt, _ := stringutil.ParseTime(source.RetriedAt)
	state.Stdout = source.Stdout
	state.Stderr = source.Stderr
	state.StartedAt = startedAt
	state.FinishedAt = finishedAt
	state.RetriedAt = retriedAt
	state.RetryCount = source.RetryCount
	state.DoneCount = source.DoneCount
	state.Repeated = source.Repeated
	state.OutputVariables = cloneSyncMap(source.OutputVariables)
	state.OutputValue = cloneString(source.OutputValue)
	state.OutputsValue = cloneString(source.OutputsValue)
	state.ChatMessages = append([]ir.LLMMessage(nil), source.ChatMessages...)
	state.AgentSession = ir.CloneAgentSession(source.AgentSession)
	state.ToolDefinitions = append([]ir.ToolDefinition(nil), source.ToolDefinitions...)
	state.HumanTaskInput = append(state.HumanTaskInput, source.HumanTaskInput...)
	state.StepOutputsValue = cloneString(source.StepOutputsValue)
	state.HumanTaskCompletedBy = source.HumanTaskCompletedBy
	state.HumanTaskCompletedByID = source.HumanTaskCompletedByID
	state.ApprovalInputs = cloneStringMap(source.ApprovalInputs)
	state.ApprovedAt = source.ApprovedAt
	state.ApprovedBy = source.ApprovedBy
	state.ApprovedByID = source.ApprovedByID
	state.RejectedAt = source.RejectedAt
	state.RejectedBy = source.RejectedBy
	state.RejectedByID = source.RejectedByID
	state.RejectionReason = source.RejectionReason
	state.ApprovalIteration = source.ApprovalIteration
	state.PushBackInputs = cloneStringMap(source.PushBackInputs)
	state.PushBackHistory = dagrun.ClonePushBackHistory(source.PushBackHistory)
	return state
}

func cloneSyncMap(src *collections.SyncMap) *collections.SyncMap {
	if src == nil {
		return nil
	}
	dst := &collections.SyncMap{}
	src.Range(func(key, value any) bool {
		dst.Store(key, value)
		return true
	})
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func cloneString(src *string) *string {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}
