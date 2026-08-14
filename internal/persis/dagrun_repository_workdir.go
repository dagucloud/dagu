// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

// MaterializeWorkDir makes a DAG-run work directory available locally.
func (r *DAGRunRepository) MaterializeWorkDir(ctx context.Context, ref dagrun.WorkDirRef) (string, error) {
	normalized, err := normalizeWorkDirRef(ref)
	if err != nil {
		return "", err
	}
	return r.workDirs.Materialize(ctx, normalized)
}

// SnapshotWorkDir persists the current state of a DAG-run work directory.
func (r *DAGRunRepository) SnapshotWorkDir(ctx context.Context, ref dagrun.WorkDirRef, localDir string) error {
	normalized, err := normalizeWorkDirRef(ref)
	if err != nil {
		return err
	}
	return r.workDirs.Snapshot(ctx, normalized, localDir)
}

type noopWorkDirStore struct{}

func (noopWorkDirStore) Materialize(context.Context, dagrun.WorkDirRef) (string, error) {
	return "", nil
}

func (noopWorkDirStore) Snapshot(context.Context, dagrun.WorkDirRef, string) error {
	return nil
}

func (noopWorkDirStore) Remove(context.Context, dagrun.WorkDirRef) error {
	return nil
}

func normalizeWorkDirRef(ref dagrun.WorkDirRef) (dagrun.WorkDirRef, error) {
	if ref.DAGRun.ID == "" {
		return dagrun.WorkDirRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return dagrun.WorkDirRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return dagrun.WorkDirRef{}, fmt.Errorf(
			"missing root dag-run name for work directory %s",
			ref.DAGRun.ID,
		)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
