// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
)

func materializeLocalWorkspace(req executor.SubWorkflowRequest) (string, string, func(), error) {
	if req.Workspace == nil {
		return "", "", nil, fmt.Errorf("missing workspace for run %q", req.RunID)
	}
	desc := req.Workspace.Descriptor
	dagPath, err := workspacebundle.NormalizeRelativePath(desc.DAGPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid workspace DAG path for run %q: %w", req.RunID, err)
	}
	desc.DAGPath = dagPath

	tmp, err := os.MkdirTemp("", "dagu-workspace-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create local workspace: %w", err)
	}
	cleanup := func() {
		_ = fileutil.RemoveAll(tmp)
	}
	dest := filepath.Join(tmp, "workspace")
	if err := workspacebundle.Extract(req.Workspace.Archive, dest, desc, workspacebundle.DefaultLimits()); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("materialize workspace for run %q: %w", req.RunID, err)
	}
	target := filepath.Join(dest, filepath.FromSlash(dagPath))
	if !workspacebundle.IsPathWithin(dest, target) {
		cleanup()
		return "", "", nil, fmt.Errorf("workspace DAG path escapes workspace for run %q", req.RunID)
	}
	return dest, target, cleanup, nil
}

func localCancelSignal(intent executor.SubWorkflowCancelIntent) os.Signal {
	if intent.Mode == executor.SubWorkflowCancelModeForce {
		return os.Kill
	}
	return intent.Signal
}

// inheritedEnvForLocalRunner drops tool-managed entries from the parent run
// scope before it is inherited into a local child; the child resolves its own
// tool environment instead of reusing the parent host's manifest.
func inheritedEnvForLocalRunner(entries []cmnvalue.EnvEntry) []cmnvalue.EnvEntry {
	if !hasDAGToolsEnv(entries) {
		return entries
	}
	filtered := make([]cmnvalue.EnvEntry, 0, len(entries))
	for _, entry := range entries {
		if isDAGToolsEnvKey(entry.Key) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func toolsBasePath(rCtx runctx.Context) string {
	if rCtx.BaseEnv != nil {
		for _, env := range rCtx.BaseEnv.AsSlice() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, "PATH") {
				return value
			}
		}
	}
	return os.Getenv("PATH")
}

func hasDAGToolsEnv(entries []cmnvalue.EnvEntry) bool {
	for _, entry := range entries {
		if strings.EqualFold(entry.Key, dagutools.EnvManifest) {
			return true
		}
	}
	return false
}

func isDAGToolsEnvKey(key string) bool {
	return dagutools.IsManagedEnvKey(key)
}
