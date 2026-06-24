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

	"github.com/dagucloud/dagu"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	filedag "github.com/dagucloud/dagu/internal/persis/file/dag"

	"github.com/stretchr/testify/require"
)

func TestFirstLaunchExamplesLoadAndRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dagsDir := t.TempDir()
	store := filedag.New(dagsDir)
	initializer, ok := store.(interface{ Initialize() error })
	require.True(t, ok)
	require.NoError(t, initializer.Initialize())

	files := yamlFiles(t, dagsDir)
	require.NotEmpty(t, files)

	eng, err := dagu.New(ctx, dagu.Options{
		HomeDir:     t.TempDir(),
		DAGsDir:     dagsDir,
		DataDir:     t.TempDir(),
		LogDir:      t.TempDir(),
		ArtifactDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close(context.Background()))
	})

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(dagsDir, file)
			_, err := spec.Load(ctx, path, spec.WithoutEval(), spec.WithDAGsDir(dagsDir))
			require.NoError(t, err)

			run, err := eng.RunFile(ctx, path,
				dagu.WithRunID(strings.TrimSuffix(file, filepath.Ext(file))),
				dagu.WithDefaultWorkingDir(t.TempDir()),
			)
			require.NoError(t, err)

			status, err := run.Wait(ctx)
			require.NoError(t, err)
			require.NotNil(t, status)
			require.Equal(t, core.Succeeded.String(), status.Status)
		})
	}
}

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
