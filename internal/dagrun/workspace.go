// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// WorkspaceRef identifies the execution workspace belonging to a DAG run.
type WorkspaceRef struct {
	RootDAGRun ir.DAGRunRef
	DAGRun     ir.DAGRunRef
}

// WorkspaceStore manages the durable workspace snapshot for a DAG run.
type WorkspaceStore interface {
	Materialize(ctx context.Context, ref WorkspaceRef) (string, error)
	Snapshot(ctx context.Context, ref WorkspaceRef, localDir string) error
	Remove(ctx context.Context, ref WorkspaceRef) error
}

type noopWorkspaceStore struct{}

func (noopWorkspaceStore) Materialize(context.Context, WorkspaceRef) (string, error) {
	return "", nil
}

func (noopWorkspaceStore) Snapshot(context.Context, WorkspaceRef, string) error {
	return nil
}

func (noopWorkspaceStore) Remove(context.Context, WorkspaceRef) error {
	return nil
}

func normalizeWorkspaceRef(ref WorkspaceRef) (WorkspaceRef, error) {
	if ref.DAGRun.ID == "" {
		return WorkspaceRef{}, ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return WorkspaceRef{}, ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return WorkspaceRef{}, fmt.Errorf("missing root dag-run name for workspace %s", ref.DAGRun.ID)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
