// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package migration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A v2 directory that is a junction, the usual way to place a directory on
// another volume on Windows, moves as a link. Renaming it needs no privilege
// to create links, and its state stays where it leads.
func TestRunMovesJunctionAsLink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := v2Install(root)
	disk := filepath.Join(root, "disk", "dag-runs")
	writeFile(t, filepath.Join(disk, "etl", "status.jsonl"))
	require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0o750))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", cfg.Paths.DAGRunsDir, disk).CombinedOutput()
	require.NoError(t, err, "%s", out)

	require.NoError(t, newStateMigrator(t, cfg).Run(context.Background()))

	runs := filepath.Join(cfg.Paths.DataDir, "projects", "default", "runs")
	assert.FileExists(t, filepath.Join(runs, "etl", "status.jsonl"))
	assert.FileExists(t, filepath.Join(disk, "etl", "status.jsonl"), "the runs stay where the junction leads")
	_, err = os.Lstat(cfg.Paths.DAGRunsDir)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
