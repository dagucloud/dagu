// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// WorkspaceStore manages durable execution workspaces for DAG runs.
type WorkspaceStore interface {
	Materialize(ctx context.Context, ref WorkspaceRef) (string, error)
	Snapshot(ctx context.Context, ref WorkspaceRef, localDir string) error
	Remove(ctx context.Context, ref WorkspaceRef) error
}

// WorkspaceRef identifies the execution workspace belonging to a DAG run.
type WorkspaceRef struct {
	RootDAGRun ir.DAGRunRef
	DAGRun     ir.DAGRunRef
}
