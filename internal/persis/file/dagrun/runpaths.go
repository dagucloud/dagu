// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
)

// withRunPaths sets how the attempt's record stores the paths of its log,
// step output, and artifact directory.
func withRunPaths(paths runpath.Mapper) AttemptOption {
	return func(att *Attempt) {
		att.paths = paths
	}
}

// storedPaths returns status with the paths its record stores. A node whose
// paths change is copied, so status and its nodes are left as they are.
func storedPaths(paths runpath.Mapper, status ir.DAGRunStatus) ir.DAGRunStatus {
	if !paths.StoresRelative() {
		return status
	}
	status.Log = paths.StoreLog(status.Log)
	status.ArchiveDir = paths.StoreArtifact(status.ArchiveDir)
	store := func(node *ir.Node) *ir.Node {
		if node == nil {
			return nil
		}
		stored := *node
		stored.Stdout = paths.StoreLog(node.Stdout)
		stored.Stderr = paths.StoreLog(node.Stderr)
		stored.PushBackPreviousStdout = paths.StoreLog(node.PushBackPreviousStdout)
		stored.RetryNodeRunsDir = paths.StoreLog(node.RetryNodeRunsDir)
		return &stored
	}
	if status.Nodes != nil {
		nodes := make([]*ir.Node, len(status.Nodes))
		for i, node := range status.Nodes {
			nodes[i] = store(node)
		}
		status.Nodes = nodes
	}
	status.OnInit = store(status.OnInit)
	status.OnExit = store(status.OnExit)
	status.OnSuccess = store(status.OnSuccess)
	status.OnFailure = store(status.OnFailure)
	status.OnAbort = store(status.OnAbort)
	status.OnWait = store(status.OnWait)
	return status
}

// resolvePaths rewrites the stored paths of a status read from its record to
// the paths they name.
func resolvePaths(paths runpath.Mapper, status *ir.DAGRunStatus) {
	status.Log = paths.ResolveLog(status.Log)
	status.ArchiveDir = paths.ResolveArtifact(status.ArchiveDir)
	for _, node := range status.NodesInRunOrder() {
		if node == nil {
			continue
		}
		node.Stdout = paths.ResolveLog(node.Stdout)
		node.Stderr = paths.ResolveLog(node.Stderr)
		node.PushBackPreviousStdout = paths.ResolveLog(node.PushBackPreviousStdout)
		node.RetryNodeRunsDir = paths.ResolveLog(node.RetryNodeRunsDir)
	}
}
