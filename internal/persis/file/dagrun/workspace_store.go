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
)

var _ dagrun.WorkspaceStore = (*WorkspaceStore)(nil)

// WorkspaceStore manages DAG-run workspaces in the local run directory tree.
type WorkspaceStore struct {
	baseDir string
}

// NewWorkspaceStore creates a file-backed DAG-run workspace store.
func NewWorkspaceStore(baseDir string) *WorkspaceStore {
	return &WorkspaceStore{baseDir: baseDir}
}

func (s *WorkspaceStore) Materialize(ctx context.Context, ref dagrun.WorkspaceRef) (string, error) {
	dir, err := s.workspaceDir(ctx, ref)
	if err != nil {
		return "", err
	}
	if err := fileutil.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create workspace %s: %w", dir, err)
	}
	return dir, nil
}

func (*WorkspaceStore) Snapshot(context.Context, dagrun.WorkspaceRef, string) error {
	return nil
}

func (s *WorkspaceStore) Remove(ctx context.Context, ref dagrun.WorkspaceRef) error {
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

func (s *WorkspaceStore) workspaceDir(ctx context.Context, ref dagrun.WorkspaceRef) (string, error) {
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
