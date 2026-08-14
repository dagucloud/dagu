// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// WorkDirStore manages durable execution work directories for DAG runs.
type WorkDirStore interface {
	Materialize(ctx context.Context, ref WorkDirRef) (string, error)
	Snapshot(ctx context.Context, ref WorkDirRef, localDir string) error
	Remove(ctx context.Context, ref WorkDirRef) error
}

// WorkDirRef identifies the execution work directory belonging to a DAG run.
type WorkDirRef struct {
	RootDAGRun ir.DAGRunRef
	DAGRun     ir.DAGRunRef
}
