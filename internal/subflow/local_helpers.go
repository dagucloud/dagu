// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	dagutools "github.com/dagucloud/dagu/internal/tools"
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

	tmp, err := os.MkdirTemp("", "dagu-action-workspace-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create local action workspace: %w", err)
	}
	cleanup := func() {
		_ = fileutil.RemoveAll(tmp)
	}
	dest := filepath.Join(tmp, "workspace")
	if err := workspacebundle.Extract(req.Workspace.Archive, dest, desc, workspacebundle.DefaultLimits()); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("materialize action workspace for run %q: %w", req.RunID, err)
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

func inheritedEnvForLocalRunner(envs []string) []string {
	if !hasDAGToolsEnv(envs) {
		return envs
	}
	filtered := make([]string, 0, len(envs))
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && isDAGToolsEnvKey(key) {
			continue
		}
		filtered = append(filtered, env)
	}
	return filtered
}

func hasDAGToolsEnv(envs []string) bool {
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.EqualFold(key, dagutools.EnvManifest) {
			return true
		}
	}
	return false
}

func isDAGToolsEnvKey(key string) bool {
	for _, candidate := range []string{
		"PATH",
		"AQUA_ROOT_DIR",
		"AQUA_CONFIG",
		"AQUA_DISABLE_LAZY_INSTALL",
		"AQUA_CHECKSUM",
		"AQUA_REQUIRE_CHECKSUM",
		"AQUA_ENFORCE_CHECKSUM",
		"AQUA_ENFORCE_REQUIRE_CHECKSUM",
		dagutools.EnvManifest,
	} {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	return false
}
