// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

type actionWorkspace struct {
	dir     string
	dagFile string
	desc    workspacebundle.Descriptor
}

func actionWorkspaceDir(workDir string) string {
	return filepath.Join(workDir, "workspace")
}

func taskWorkspaceDescriptor(task *coordinatorv1.Task) (workspacebundle.Descriptor, bool, error) {
	if task == nil || strings.TrimSpace(task.WorkspaceBundleDigest) == "" {
		return workspacebundle.Descriptor{}, false, nil
	}
	if !workspacebundle.ValidDigest(task.WorkspaceBundleDigest) {
		return workspacebundle.Descriptor{}, false, fmt.Errorf("invalid workspace bundle digest %q", task.WorkspaceBundleDigest)
	}
	dagPath, err := workspacebundle.NormalizeRelativePath(task.WorkspaceBundleDagPath)
	if err != nil {
		return workspacebundle.Descriptor{}, false, fmt.Errorf("invalid workspace bundle DAG path: %w", err)
	}
	if task.WorkspaceBundleSize <= 0 {
		return workspacebundle.Descriptor{}, false, fmt.Errorf("workspace bundle size is required")
	}
	return workspacebundle.Descriptor{
		Digest:      task.WorkspaceBundleDigest,
		Size:        task.WorkspaceBundleSize,
		DAGPath:     dagPath,
		OriginalRef: task.WorkspaceBundleOriginalRef,
		ResolvedRef: task.WorkspaceBundleResolvedRef,
	}, true, nil
}

func materializeTaskWorkspace(
	ctx context.Context,
	task *coordinatorv1.Task,
	client workspacebundle.Client,
	workDir string,
) (*actionWorkspace, error) {
	desc, ok, err := taskWorkspaceDescriptor(task)
	if err != nil || !ok {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("coordinator client does not support workspace bundles")
	}
	data, err := client.GetWorkspaceBundle(ctx, desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("download workspace bundle %s: %w", desc.Digest, err)
	}
	if int64(len(data)) != desc.Size {
		return nil, fmt.Errorf("workspace bundle size mismatch: got %d, want %d", len(data), desc.Size)
	}
	if err := workspacebundle.Extract(data, workDir, desc, workspacebundle.DefaultLimits()); err != nil {
		extractErr := fmt.Errorf("extract workspace bundle %s: %w", desc.Digest, err)
		if cleanupErr := os.RemoveAll(workDir); cleanupErr != nil {
			return nil, errors.Join(extractErr, fmt.Errorf("cleanup workspace %q: %w", workDir, cleanupErr))
		}
		return nil, extractErr
	}
	return &actionWorkspace{
		dir:     workDir,
		dagFile: filepath.Join(workDir, filepath.FromSlash(desc.DAGPath)),
		desc:    desc,
	}, nil
}

func sharedNothingActionWorkDir(task *coordinatorv1.Task) string {
	return filepath.Join(
		os.TempDir(),
		fmt.Sprintf("dagu_%s_%s", fileutil.SafeName(task.Target), task.DagRunId),
	)
}
