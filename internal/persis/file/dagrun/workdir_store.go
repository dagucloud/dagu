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

var _ dagrun.WorkDirStore = (*WorkDirStore)(nil)

// WorkDirStore manages file-backed DAG-run work directories.
type WorkDirStore struct {
	rootDir string
	nested  bool
}

// NewWorkDirStore creates a work-directory store rooted at rootDir.
func NewWorkDirStore(rootDir string) *WorkDirStore {
	return &WorkDirStore{rootDir: rootDir}
}

// NewNestedWorkDirStore creates a work-directory store nested in DAG-run storage.
func NewNestedWorkDirStore(dagRunsDir string) *WorkDirStore {
	return &WorkDirStore{rootDir: dagRunsDir, nested: true}
}

func (s *WorkDirStore) Materialize(ctx context.Context, ref dagrun.WorkDirRef) (string, error) {
	dir, err := s.workDir(ctx, ref)
	if err != nil {
		return "", err
	}
	if err := fileutil.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create work directory %s: %w", dir, err)
	}
	return dir, nil
}

func (*WorkDirStore) Snapshot(context.Context, dagrun.WorkDirRef, string) error {
	return nil
}

func (s *WorkDirStore) Remove(ctx context.Context, ref dagrun.WorkDirRef) error {
	dir, err := s.workDir(ctx, ref)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil
		}
		return err
	}
	if !s.nested {
		dir = filepath.Dir(dir)
	}
	if err := fileutil.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove work directory %s: %w", dir, err)
	}
	return nil
}

func (s *WorkDirStore) workDir(ctx context.Context, ref dagrun.WorkDirRef) (string, error) {
	if !s.nested {
		rootDir := filepath.Join(s.rootDir, workDirKey(ref.RootDAGRun.Name+"\x00"+ref.RootDAGRun.ID))
		if ref.DAGRun == ref.RootDAGRun {
			return filepath.Join(rootDir, "work"), nil
		}
		return filepath.Join(rootDir, "children", workDirKey(ref.DAGRun.ID), "work"), nil
	}

	root := NewDataRoot(s.rootDir, ref.RootDAGRun.Name)
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

func workDirKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
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
