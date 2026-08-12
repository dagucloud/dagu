// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.DAGRunWorkspaceStore = (*DAGRunWorkspaceStore)(nil)

// DAGRunWorkspaceStore manages DAG-run workspaces in the local run directory tree.
type DAGRunWorkspaceStore struct {
	baseDir string
}

// NewDAGRunWorkspaceStore creates a file-backed DAG-run workspace store.
func NewDAGRunWorkspaceStore(baseDir string) *DAGRunWorkspaceStore {
	return &DAGRunWorkspaceStore{baseDir: baseDir}
}

func (s *DAGRunWorkspaceStore) Materialize(ctx context.Context, ref dagrun.DAGRunWorkspaceRef) (string, error) {
	dir, err := s.workspaceDir(ctx, ref)
	if err != nil {
		return "", err
	}
	if err := fileutil.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create workspace %s: %w", dir, err)
	}
	return dir, nil
}

func (*DAGRunWorkspaceStore) Snapshot(context.Context, dagrun.DAGRunWorkspaceRef, string) error {
	return nil
}

func (s *DAGRunWorkspaceStore) Remove(ctx context.Context, ref dagrun.DAGRunWorkspaceRef) error {
	dir, err := s.workspaceDir(ctx, ref)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil
		}
		return err
	}
	if err := fileutil.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove workspace %s: %w", dir, err)
	}
	return nil
}

func (s *DAGRunWorkspaceStore) workspaceDir(ctx context.Context, ref dagrun.DAGRunWorkspaceRef) (string, error) {
	root := NewDataRoot(s.baseDir, ref.RootDAGRun.Name)
	run, err := root.FindByDAGRunID(ctx, ref.RootDAGRun.ID)
	if err != nil {
		return "", fmt.Errorf("find root dag-run %s: %w", ref.RootDAGRun.ID, err)
	}
	if ref.DAGRun != ref.RootDAGRun {
		run, err = run.FindSubDAGRun(ctx, ref.DAGRun.ID)
		if err != nil {
			return "", fmt.Errorf("find child dag-run %s: %w", ref.DAGRun.ID, err)
		}
	}
	return workDirForDAGRunDir(run.baseDir), nil
}

func workDirForDAGRunDir(dagRunDir string) string {
	if rootDir, childRunID, ok := subDAGWorkDirParts(dagRunDir); ok {
		return filepath.Join(rootDir, subDAGWorkDirName(childRunID))
	}
	return filepath.Join(dagRunDir, "work")
}

func subDAGWorkDirName(childRunID string) string {
	sum := sha256.Sum256([]byte(childRunID))
	return SubDAGWorkDirPrefix + strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:8]),
	)
}

func subDAGWorkDirParts(dagRunDir string) (rootDir, childRunID string, ok bool) {
	parentDir := filepath.Dir(dagRunDir)
	childRunID, ok = subDAGRunIDFromDir(filepath.Base(parentDir), filepath.Base(dagRunDir))
	if !ok {
		return "", "", false
	}
	return filepath.Dir(parentDir), childRunID, true
}
