// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

var _ dagrun.WorkDirStore = (*WorkDirStore)(nil)

// WorkDirStore manages file-backed DAG-run work directories.
type WorkDirStore struct {
	rootDir    string
	historyDir string
}

// NewWorkDirStore creates a work-directory store rooted at rootDir.
// Existing work directories nested under historyDir remain accessible.
func NewWorkDirStore(rootDir, historyDir string) *WorkDirStore {
	return &WorkDirStore{rootDir: rootDir, historyDir: historyDir}
}

func (s *WorkDirStore) Materialize(ctx context.Context, ref dagrun.WorkDirRef) (string, error) {
	dir := s.workDir(ref)
	exists, err := directoryExists(dir)
	if err != nil {
		return "", fmt.Errorf("inspect work directory %s: %w", dir, err)
	}
	if exists {
		return dir, nil
	}

	legacyDir, err := s.legacyWorkDir(ctx, ref)
	if err == nil {
		exists, err = directoryExists(legacyDir)
		if err != nil {
			return "", fmt.Errorf("inspect legacy work directory %s: %w", legacyDir, err)
		}
		if exists {
			return legacyDir, nil
		}
	} else if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
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

func (s *WorkDirStore) Remove(_ context.Context, ref dagrun.WorkDirRef) error {
	dir := s.workDir(ref)
	if ref.DAGRun == ref.RootDAGRun {
		dir = filepath.Dir(dir)
	}
	if err := fileutil.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove work directory %s: %w", dir, err)
	}
	return nil
}

func (s *WorkDirStore) workDir(ref dagrun.WorkDirRef) string {
	rootDir := filepath.Join(s.rootDir, dagDirName(ref.RootDAGRun.Name), runIDHash(ref.RootDAGRun.ID))
	if ref.DAGRun == ref.RootDAGRun {
		return filepath.Join(rootDir, "root")
	}
	return filepath.Join(rootDir, runIDHash(ref.DAGRun.ID))
}

func (s *WorkDirStore) legacyWorkDir(ctx context.Context, ref dagrun.WorkDirRef) (string, error) {
	root := NewDataRoot(s.historyDir, ref.RootDAGRun.Name)
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
	return SubDAGWorkDirPrefix + runIDHash(childRunID)
}

func runIDHash(runID string) string {
	sum := sha256.Sum256([]byte(runID))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:8]))
}

func directoryExists(path string) (bool, error) {
	info, err := fileutil.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, errors.New("not a directory")
	}
	return true, nil
}

func subDAGWorkDirParts(dagRunDir string) (rootDir, childRunID string, ok bool) {
	parentDir := filepath.Dir(dagRunDir)
	childRunID, ok = subDAGRunIDFromDir(filepath.Base(parentDir), filepath.Base(dagRunDir))
	if !ok {
		return "", "", false
	}
	return filepath.Dir(parentDir), childRunID, true
}
