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

// A log directory that is a junction to another volume, and holds the admin
// logs, keeps the project's logs where the junction leads. Following the
// junction needs more than filepath.EvalSymlinks, which does not.
func TestRunKeepsSplitJunctionOnItsDisk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Symlink(root, filepath.Join(root, "probe")); err != nil {
		t.Skipf("creating symbolic links needs a privilege: %v", err)
	}
	cfg := v2Install(root)
	disk := filepath.Join(root, "disk", "logs")
	writeFile(t, filepath.Join(disk, "etl", "run_1.log"))
	writeFile(t, filepath.Join(disk, "admin", "audit", "a.jsonl"))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", cfg.Paths.LogDir, disk).CombinedOutput()
	require.NoError(t, err, "%s", out)

	require.NoError(t, newStateMigrator(t, cfg).Run(context.Background()))

	logs := filepath.Join(cfg.Paths.DataDir, "projects", "default", "logs")
	assert.FileExists(t, filepath.Join(logs, "etl", "run_1.log"))
	assert.FileExists(t, filepath.Join(disk, ".projects", "default", "etl", "run_1.log"), "the logs stay where the junction leads")
	assert.FileExists(t, filepath.Join(cfg.Paths.LogDir, "admin", "audit", "a.jsonl"))
}
