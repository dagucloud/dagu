// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGRunWorkspaceRef identifies the execution workspace belonging to a DAG run.
type DAGRunWorkspaceRef struct {
	RootDAGRun ir.DAGRunRef
	DAGRun     ir.DAGRunRef
}

// DAGRunWorkspaceStore manages durable execution workspaces for DAG runs.
type DAGRunWorkspaceStore interface {
	Materialize(ctx context.Context, ref DAGRunWorkspaceRef) (string, error)
	Snapshot(ctx context.Context, ref DAGRunWorkspaceRef, localDir string) error
	Remove(ctx context.Context, ref DAGRunWorkspaceRef) error
}

type noopDAGRunWorkspaceStore struct{}

func (noopDAGRunWorkspaceStore) Materialize(context.Context, DAGRunWorkspaceRef) (string, error) {
	return "", nil
}

func (noopDAGRunWorkspaceStore) Snapshot(context.Context, DAGRunWorkspaceRef, string) error {
	return nil
}

func (noopDAGRunWorkspaceStore) Remove(context.Context, DAGRunWorkspaceRef) error {
	return nil
}

func normalizeWorkspaceRef(ref DAGRunWorkspaceRef) (DAGRunWorkspaceRef, error) {
	if ref.DAGRun.ID == "" {
		return DAGRunWorkspaceRef{}, ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return DAGRunWorkspaceRef{}, ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return DAGRunWorkspaceRef{}, fmt.Errorf("missing root dag-run name for workspace %s", ref.DAGRun.ID)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
