// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

// MaterializeWorkspace makes a DAG-run workspace available locally.
func (r *DAGRunRepository) MaterializeWorkspace(ctx context.Context, ref dagrun.WorkspaceRef) (string, error) {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return "", err
	}
	return r.workspaces.Materialize(ctx, normalized)
}

// SnapshotWorkspace persists the current state of a DAG-run workspace.
func (r *DAGRunRepository) SnapshotWorkspace(ctx context.Context, ref dagrun.WorkspaceRef, localDir string) error {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return err
	}
	return r.workspaces.Snapshot(ctx, normalized, localDir)
}

type noopWorkspaceStore struct{}

func (noopWorkspaceStore) Materialize(context.Context, dagrun.WorkspaceRef) (string, error) {
	return "", nil
}

func (noopWorkspaceStore) Snapshot(context.Context, dagrun.WorkspaceRef, string) error {
	return nil
}

func (noopWorkspaceStore) Remove(context.Context, dagrun.WorkspaceRef) error {
	return nil
}

func normalizeWorkspaceRef(ref dagrun.WorkspaceRef) (dagrun.WorkspaceRef, error) {
	if ref.DAGRun.ID == "" {
		return dagrun.WorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return dagrun.WorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return dagrun.WorkspaceRef{}, fmt.Errorf(
			"missing root dag-run name for workspace %s",
			ref.DAGRun.ID,
		)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
