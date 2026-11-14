// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/spec"

	"github.com/stretchr/testify/require"
)

func TestFirstLaunchExamplesLoadAndRun(t *testing.T) {
	ctx := context.Background()

	dagsDir := t.TempDir()
	store := filedag.NewStore(dagsDir)
	require.NoError(t, store.Initialize())

	files := yamlFiles(t, dagsDir)
	require.Contains(t, files, retryExampleFile)

	dataDir := t.TempDir()
	eng, err := dagu.New(ctx, dagu.Options{
		HomeDir:     t.TempDir(),
		DAGsDir:     dagsDir,
		DataDir:     dataDir,
		LogDir:      t.TempDir(),
		ArtifactDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close(context.Background()))
	})

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			path := filepath.Join(dagsDir, file)
			_, err := spec.Load(runCtx, path, spec.WithoutEval(), spec.WithDAGsDir(dagsDir))
			require.NoError(t, err)

			run, err := eng.RunFile(runCtx, path,
				dagu.WithRunID(strings.TrimSuffix(file, filepath.Ext(file))),
				dagu.WithDefaultWorkingDir(t.TempDir()),
			)
			require.NoError(t, err)

			status, err := run.Wait(runCtx)
			require.NoError(t, err)
			require.NotNil(t, status)
			require.Equal(t, ir.Succeeded.String(), status.Status)
		})
	}

	// The retry demo fails its first attempt on purpose, so the run history
	// shows a step that succeeded on its retry.
	t.Run("RetryDemoRetriesOnce", func(t *testing.T) {
		name := strings.TrimSuffix(retryExampleFile, filepath.Ext(retryExampleFile))
		attempt, err := filedagrun.NewStore(filepath.Join(dataDir, "dag-runs")).
			FindAttempt(ctx, ir.NewDAGRunRef(name, name))
		require.NoError(t, err)
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)

		retried := make(map[string]int)
		for _, node := range status.Nodes {
			retried[node.Step.ID] = node.RetryCount
		}
		require.Equal(t, map[string]int{"call_api": 1, "report": 0}, retried)
	})
}

const retryExampleFile = "example-06-retry-on-failure.yaml"

func yamlFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
}
