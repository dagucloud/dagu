// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

// MaterializeWorkspace makes a DAG-run workspace available locally.
func (r *DAGRunRepository) MaterializeWorkspace(ctx context.Context, ref dagrun.DAGRunWorkspaceRef) (string, error) {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return "", err
	}
	return r.dagRunWorkspaces.Materialize(ctx, normalized)
}

// SnapshotWorkspace persists the current state of a DAG-run workspace.
func (r *DAGRunRepository) SnapshotWorkspace(ctx context.Context, ref dagrun.DAGRunWorkspaceRef, localDir string) error {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return err
	}
	return r.dagRunWorkspaces.Snapshot(ctx, normalized, localDir)
}

type noopDAGRunWorkspaceStore struct{}

func (noopDAGRunWorkspaceStore) Materialize(context.Context, dagrun.DAGRunWorkspaceRef) (string, error) {
	return "", nil
}

func (noopDAGRunWorkspaceStore) Snapshot(context.Context, dagrun.DAGRunWorkspaceRef, string) error {
	return nil
}

func (noopDAGRunWorkspaceStore) Remove(context.Context, dagrun.DAGRunWorkspaceRef) error {
	return nil
}

func normalizeWorkspaceRef(ref dagrun.DAGRunWorkspaceRef) (dagrun.DAGRunWorkspaceRef, error) {
	if ref.DAGRun.ID == "" {
		return dagrun.DAGRunWorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return dagrun.DAGRunWorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return dagrun.DAGRunWorkspaceRef{}, fmt.Errorf(
			"missing root dag-run name for workspace %s",
			ref.DAGRun.ID,
		)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
