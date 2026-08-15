// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform

import (
	"errors"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// ToNode converts a persistence Node back to a runtime Node
func ToNode(n *ir.Node) *runtime.Node {
	return ToNodeWithStep(n, n.Step)
}

// ToNodeWithStep converts a persistence Node back to a runtime Node using the
// supplied step definition.
func ToNodeWithStep(n *ir.Node, step ir.Step) *runtime.Node {
	startedAt, _ := stringutil.ParseTime(n.StartedAt)
	finishedAt, _ := stringutil.ParseTime(n.FinishedAt)
	retriedAt, _ := stringutil.ParseTime(n.RetriedAt)
	children := make([]runtime.SubDAGRun, len(n.SubRuns))
	for i, r := range n.SubRuns {
		children[i] = runtime.SubDAGRun(r)
	}
	childrenRepeated := make([]runtime.SubDAGRun, len(n.SubRunsRepeated))
	for i, r := range n.SubRunsRepeated {
		childrenRepeated[i] = runtime.SubDAGRun(r)
	}
	var err error
	if n.Error != "" {
		err = errors.New(n.Error)
	}
	return runtime.NewNode(step, runtime.NodeState{
		Status:                 n.Status,
		Stdout:                 n.Stdout,
		Stderr:                 n.Stderr,
		WorkingDir:             n.WorkingDir,
		StartedAt:              startedAt,
		FinishedAt:             finishedAt,
		RetriedAt:              retriedAt,
		RetryCount:             n.RetryCount,
		DoneCount:              n.DoneCount,
		Repeated:               n.Repeated,
		SkippedByRetry:         n.SkippedByRetry,
		Error:                  err,
		PreconditionResults:    slices.Clone(n.PreconditionResults),
		StatusDetails:          append([]ir.NodeStatusDetail(nil), n.StatusDetails...),
		Build:                  n.Build,
		SubRuns:                children,
		SubRunsRepeated:        childrenRepeated,
		OutputVariables:        n.OutputVariables,
		OutputValue:            n.OutputValue,
		OutputsValue:           n.OutputsValue,
		StepOutputsValue:       n.StepOutputsValue,
		HumanTaskInput:         append([]byte(nil), n.HumanTaskInput...),
		AgentState:             append([]byte(nil), n.AgentState...),
		HumanTaskCompletedBy:   n.HumanTaskCompletedBy,
		HumanTaskCompletedByID: n.HumanTaskCompletedByID,
		ChatMessages:           n.ChatMessages,
		ToolDefinitions:        n.ToolDefinitions,
		ApprovalInputs:         n.ApprovalInputs,
		ApprovedAt:             n.ApprovedAt,
		ApprovedBy:             n.ApprovedBy,
		ApprovedByID:           n.ApprovedByID,
		RejectedAt:             n.RejectedAt,
		RejectedBy:             n.RejectedBy,
		RejectedByID:           n.RejectedByID,
		RejectionReason:        n.RejectionReason,
		ApprovalIteration:      n.ApprovalIteration,
		PushBackInputs:         n.PushBackInputs,
		PushBackHistory:        dagrun.ClonePushBackHistory(n.PushBackHistory),
		PushBackPreviousStdout: n.PushBackPreviousStdout,
	})
}

// newNode converts a single runtime NodeData to a persistence Node
func newNode(node runtime.NodeData) *ir.Node {
	children := make([]ir.SubDAGRun, len(node.State.SubRuns))
	for i, child := range node.State.SubRuns {
		children[i] = ir.SubDAGRun(child)
	}
	var errText string
	if node.State.Error != nil {
		errText = node.State.Error.Error()
	}
	childrenRepeated := make([]ir.SubDAGRun, len(node.State.SubRunsRepeated))
	for i, child := range node.State.SubRunsRepeated {
		childrenRepeated[i] = ir.SubDAGRun(child)
	}
	return &ir.Node{
		Step:                   node.Step,
		PreconditionResults:    slices.Clone(node.State.PreconditionResults),
		Stdout:                 node.State.Stdout,
		Stderr:                 node.State.Stderr,
		WorkingDir:             node.State.WorkingDir,
		StartedAt:              stringutil.FormatTime(node.State.StartedAt),
		FinishedAt:             stringutil.FormatTime(node.State.FinishedAt),
		Status:                 node.State.Status,
		RetriedAt:              stringutil.FormatTime(node.State.RetriedAt),
		RetryCount:             node.State.RetryCount,
		DoneCount:              node.State.DoneCount,
		Repeated:               node.State.Repeated,
		SkippedByRetry:         node.State.SkippedByRetry,
		Error:                  errText,
		StatusDetails:          append([]ir.NodeStatusDetail(nil), node.State.StatusDetails...),
		Build:                  node.State.Build,
		SubRuns:                children,
		SubRunsRepeated:        childrenRepeated,
		OutputVariables:        node.State.OutputVariables,
		OutputValue:            node.State.OutputValue,
		OutputsValue:           node.State.OutputsValue,
		StepOutputsValue:       node.State.StepOutputsValue,
		HumanTaskInput:         append([]byte(nil), node.State.HumanTaskInput...),
		AgentState:             append([]byte(nil), node.State.AgentState...),
		HumanTaskCompletedBy:   node.State.HumanTaskCompletedBy,
		HumanTaskCompletedByID: node.State.HumanTaskCompletedByID,
		ChatMessages:           node.State.ChatMessages,
		ToolDefinitions:        node.State.ToolDefinitions,
		ApprovalInputs:         node.State.ApprovalInputs,
		ApprovedAt:             node.State.ApprovedAt,
		ApprovedBy:             node.State.ApprovedBy,
		ApprovedByID:           node.State.ApprovedByID,
		RejectedAt:             node.State.RejectedAt,
		RejectedBy:             node.State.RejectedBy,
		RejectedByID:           node.State.RejectedByID,
		RejectionReason:        node.State.RejectionReason,
		ApprovalIteration:      node.State.ApprovalIteration,
		PushBackInputs:         node.State.PushBackInputs,
		PushBackHistory:        dagrun.ClonePushBackHistory(node.State.PushBackHistory),
		PushBackPreviousStdout: node.State.PushBackPreviousStdout,
	}
}
