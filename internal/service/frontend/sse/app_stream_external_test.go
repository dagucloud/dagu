// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/service/frontend/sse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialRootWatchPathsDoesNotDescend(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested", "child"), 0o750))

	paths, err := sse.InitialRootWatchPathsForTest(root)

	require.NoError(t, err)
	assert.Equal(t, []string{root}, paths)
}

func TestDAGRunStatusFilePathsOnlyIncludesStatusJSONL(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "dag", "dag-runs", "2026", "06", "29", "dag-run_20260629_010203Z_run")
	statusFile := filepath.Join(runDir, "attempt_20260629_010203_000Z_attempt", "status.jsonl")
	childStatusFile := filepath.Join(runDir, "children", "child_child-run", "attempt_20260629_010204_000Z_child", "status.jsonl")
	noiseStatusFile := filepath.Join(runDir, "work", "status.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(statusFile), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(childStatusFile), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(noiseStatusFile), 0o750))
	require.NoError(t, os.WriteFile(statusFile, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(childStatusFile, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(noiseStatusFile, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(statusFile), "dag.json"), []byte("{}\n"), 0o600))

	paths, err := sse.DAGRunStatusFilePathsForTest(root)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"dag/dag-runs/2026/06/29/dag-run_20260629_010203Z_run/attempt_20260629_010203_000Z_attempt/status.jsonl",
		"dag/dag-runs/2026/06/29/dag-run_20260629_010203Z_run/children/child_child-run/attempt_20260629_010204_000Z_child/status.jsonl",
	}, paths)
}
