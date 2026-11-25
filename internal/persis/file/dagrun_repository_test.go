// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

// A run record stores its log path relative to paths.log_dir only while
// projects are on, and reads back the same path either way.
func TestDAGRunRepositoryRunPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		projects bool
		stored   string
	}{
		{name: "ProjectsOff", projects: false},
		{name: "ProjectsOn", projects: true, stored: "./etl/dag-run_run-1/dag-run.log"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			cfg := &config.Config{ProjectsEnabled: tc.projects}
			cfg.Paths.DataDir = filepath.Join(home, "data")
			cfg.Paths.DAGRunsDir = filepath.Join(home, "data", "dag-runs")
			cfg.Paths.LogDir = filepath.Join(home, "logs")
			cfg.Paths.ArtifactDir = filepath.Join(home, "data", "artifacts")
			logFile := filepath.Join(cfg.Paths.LogDir, "etl", "dag-run_run-1", "dag-run.log")
			if tc.stored == "" {
				tc.stored = logFile
			}
			repo := persisfile.NewDAGRunRepository(cfg)

			attempt, err := repo.CreateAttempt(ctx, &ir.DAG{Name: "etl"}, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
			require.NoError(t, err)
			require.NoError(t, attempt.Open(ctx))
			require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: "etl", DAGRunID: "run-1", Status: ir.Succeeded, Log: logFile}))
			require.NoError(t, attempt.Close(ctx))

			stored, err := filedagrun.ParseStatusFile(statusFile(t, cfg.Paths.DAGRunsDir))
			require.NoError(t, err)
			assert.Equal(t, tc.stored, stored.Log)

			found, err := repo.FindAttempt(ctx, ir.NewDAGRunRef("etl", "run-1"))
			require.NoError(t, err)
			status, err := found.ReadStatus(ctx)
			require.NoError(t, err)
			assert.Equal(t, logFile, status.Log)
		})
	}
}

func statusFile(t *testing.T, root string) string {
	t.Helper()
	var found string
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == filedagrun.JSONLStatusFile {
			found = path
		}
		return err
	}))
	require.NotEmpty(t, found)
	return found
}
