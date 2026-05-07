// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform

import (
	"errors"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

// ToNode converts a persistence Node back to a runtime Node
func ToNode(n *exec.Node) *runtime.Node {
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
	return runtime.NewNode(n.Step, runtime.NodeState{
		Status:                 n.Status,
		Stdout:                 n.Stdout,
		Stderr:                 n.Stderr,
		StartedAt:              startedAt,
		FinishedAt:             finishedAt,
		RetriedAt:              retriedAt,
		RetryCount:             n.RetryCount,
		DoneCount:              n.DoneCount,
		Repeated:               n.Repeated,
		SkippedByRetry:         n.SkippedByRetry,
		Error:                  err,
		SubRuns:                children,
		SubRunsRepeated:        childrenRepeated,
		OutputVariables:        n.OutputVariables,
		OutputValue:            n.OutputValue,
		ChatMessages:           n.ChatMessages,
		ToolDefinitions:        n.ToolDefinitions,
		ApprovalInputs:         n.ApprovalInputs,
		ApprovedAt:             n.ApprovedAt,
		ApprovedBy:             n.ApprovedBy,
		RejectedAt:             n.RejectedAt,
		RejectedBy:             n.RejectedBy,
		RejectionReason:        n.RejectionReason,
		ApprovalIteration:      n.ApprovalIteration,
		PushBackInputs:         n.PushBackInputs,
		PushBackHistory:        exec.ClonePushBackHistory(n.PushBackHistory),
		PushBackPreviousStdout: n.PushBackPreviousStdout,
	})
}

// newNode converts a single runtime NodeData to a persistence Node
func newNode(node runtime.NodeData) *exec.Node {
	children := make([]exec.SubDAGRun, len(node.State.SubRuns))
	for i, child := range node.State.SubRuns {
		children[i] = exec.SubDAGRun(child)
	}
	var errText string
	if node.State.Error != nil {
		errText = node.State.Error.Error()
	}
	childrenRepeated := make([]exec.SubDAGRun, len(node.State.SubRunsRepeated))
	for i, child := range node.State.SubRunsRepeated {
		childrenRepeated[i] = exec.SubDAGRun(child)
	}
	return &exec.Node{
		Step:                   node.Step,
		Stdout:                 node.State.Stdout,
		Stderr:                 node.State.Stderr,
		StartedAt:              stringutil.FormatTime(node.State.StartedAt),
		FinishedAt:             stringutil.FormatTime(node.State.FinishedAt),
		Status:                 node.State.Status,
		RetriedAt:              stringutil.FormatTime(node.State.RetriedAt),
		RetryCount:             node.State.RetryCount,
		DoneCount:              node.State.DoneCount,
		Repeated:               node.State.Repeated,
		SkippedByRetry:         node.State.SkippedByRetry,
		Error:                  errText,
		SubRuns:                children,
		SubRunsRepeated:        childrenRepeated,
		OutputVariables:        node.State.OutputVariables,
		OutputValue:            node.State.OutputValue,
		ChatMessages:           node.State.ChatMessages,
		ToolDefinitions:        node.State.ToolDefinitions,
		ApprovalInputs:         node.State.ApprovalInputs,
		ApprovedAt:             node.State.ApprovedAt,
		ApprovedBy:             node.State.ApprovedBy,
		RejectedAt:             node.State.RejectedAt,
		RejectedBy:             node.State.RejectedBy,
		RejectionReason:        node.State.RejectionReason,
		ApprovalIteration:      node.State.ApprovalIteration,
		PushBackInputs:         node.State.PushBackInputs,
		PushBackHistory:        exec.ClonePushBackHistory(node.State.PushBackHistory),
		PushBackPreviousStdout: node.State.PushBackPreviousStdout,
	}
}
