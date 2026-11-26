// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package noderuns_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/stretchr/testify/require"
)

func TestWriteRead(t *testing.T) {
	t.Parallel()

	dir := noderuns.Dir(filepath.Join(t.TempDir(), "deploy.out"), "deploy")
	require.False(t, noderuns.Exists(dir))
	_, err := noderuns.Read(dir)
	require.ErrorIs(t, err, fs.ErrNotExist)

	runs := []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunSucceeded, DAGRunID: "a"},
		{Node: "web02", Status: ir.NodeRunRemoved},
	}
	counts, err := noderuns.Write(dir, runs)
	require.NoError(t, err)
	require.Equal(t, ir.NodeRunCounts{Total: 2, Succeeded: 1, Removed: 1}, counts)

	require.True(t, noderuns.Exists(dir))
	records, err := noderuns.Read(dir)
	require.NoError(t, err)
	require.Equal(t, runs, records.Runs)
	stored, err := noderuns.ReadCounts(dir)
	require.NoError(t, err)
	require.Equal(t, counts, stored)
}

func TestDirWithoutLogFile(t *testing.T) {
	t.Parallel()

	require.Empty(t, noderuns.Dir("", "deploy"))
}
