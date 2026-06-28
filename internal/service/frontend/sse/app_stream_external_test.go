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
	statusFile := filepath.Join(root, "dag", "dag-runs", "2026", "06", "29", "run", "attempt", "status.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(statusFile), 0o750))
	require.NoError(t, os.WriteFile(statusFile, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(statusFile), "dag.json"), []byte("{}\n"), 0o600))

	paths, err := sse.DAGRunStatusFilePathsForTest(root)

	require.NoError(t, err)
	assert.Equal(t, []string{"dag/dag-runs/2026/06/29/run/attempt/status.jsonl"}, paths)
}
