// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform

import (
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// WithNodes converts runtime node data into persisted DAG-run nodes.
func WithNodes(nodes []runtime.NodeData) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		convertedNodes := make([]*dagrun.Node, len(nodes))
		for i, node := range nodes {
			convertedNodes[i] = newNode(node)
		}
		status.Nodes = convertedNodes
	}
}

func convertNodeIfPresent(node *runtime.Node) *dagrun.Node {
	if node == nil {
		return nil
	}
	return newNode(node.NodeData())
}

// WithOnInitNode converts and sets the initialization handler node.
func WithOnInitNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnInit = convertNodeIfPresent(node)
	}
}

// WithOnExitNode converts and sets the exit handler node.
func WithOnExitNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnExit = convertNodeIfPresent(node)
	}
}

// WithOnSuccessNode converts and sets the success handler node.
func WithOnSuccessNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnSuccess = convertNodeIfPresent(node)
	}
}

// WithOnFailureNode converts and sets the failure handler node.
func WithOnFailureNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnFailure = convertNodeIfPresent(node)
	}
}

// WithOnAbortNode converts and sets the abort handler node.
func WithOnAbortNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnAbort = convertNodeIfPresent(node)
	}
}

// WithOnWaitNode converts and sets the wait handler node.
func WithOnWaitNode(node *runtime.Node) dagrun.StatusOption {
	return func(status *dagrun.DAGRunStatus) {
		status.OnWait = convertNodeIfPresent(node)
	}
}
