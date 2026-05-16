// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactExecutorWriteReadAndList(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()

	writeOut := runArtifactAction(t, artifactDir, opWrite, map[string]any{
		"path":    "reports/summary.md",
		"content": "# Summary\nok\n",
	})

	var writeResult map[string]any
	require.NoError(t, json.Unmarshal(writeOut.Bytes(), &writeResult))
	assert.Equal(t, opWrite, writeResult["operation"])
	assert.Equal(t, "reports/summary.md", writeResult["path"])
	assert.Equal(t, float64(len("# Summary\nok\n")), writeResult["bytes"])
	assertFileContent(t, filepath.Join(artifactDir, "reports", "summary.md"), "# Summary\nok\n")

	readOut := runArtifactAction(t, artifactDir, opRead, map[string]any{
		"path": "reports/summary.md",
	})
	assert.Equal(t, "# Summary\nok\n", readOut.String())

	listOut := runArtifactAction(t, artifactDir, opList, map[string]any{
		"path":      ".",
		"recursive": true,
	})

	var listResult struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
		Entries   []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(listOut.Bytes(), &listResult))
	assert.Equal(t, opList, listResult.Operation)
	assert.Equal(t, ".", listResult.Path)
	require.Len(t, listResult.Entries, 1)
	assert.Equal(t, "reports/summary.md", listResult.Entries[0].Path)
	assert.Equal(t, "file", listResult.Entries[0].Type)
}

func TestArtifactExecutorRejectsEscapingPaths(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()

	for _, path := range []string{"../outside.txt", "nested/../outside.txt", "/tmp/outside.txt", `..\outside.txt`, `\tmp\outside.txt`} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			exec, err := newArtifactExecutorForTest(t, artifactDir, opWrite, map[string]any{
				"path":    path,
				"content": "blocked",
			})
			require.NoError(t, err)
			require.Error(t, exec.Run(context.Background()))
		})
	}
}

func TestArtifactExecutorRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	outsideDir := t.TempDir()
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(artifactDir, "link")))

	exec, err := newArtifactExecutorForTest(t, artifactDir, opWrite, map[string]any{
		"path":    "link/out.txt",
		"content": "blocked",
	})
	require.NoError(t, err)
	require.Error(t, exec.Run(context.Background()))
	assert.NoFileExists(t, filepath.Join(outsideDir, "out.txt"))
}

func TestArtifactExecutorRejectsNonAtomicOverwrite(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()

	_, err := newArtifactExecutorForTest(t, artifactDir, opWrite, map[string]any{
		"path":      "out.txt",
		"content":   "blocked",
		"overwrite": true,
		"atomic":    false,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overwrite requires atomic writes")
}

func TestArtifactExecutorRequiresArtifactDir(t *testing.T) {
	t.Parallel()

	step := artifactStep(opWrite, map[string]any{
		"path":    "out.txt",
		"content": "hello",
	})
	dag := &core.DAG{Name: "artifact-test"}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	_, err := newExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DAG_RUN_ARTIFACTS_DIR")
}

func runArtifactAction(t *testing.T, artifactDir, op string, cfg map[string]any) *bytes.Buffer {
	t.Helper()

	exec, err := newArtifactExecutorForTest(t, artifactDir, op, cfg)
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(context.Background()))
	return out
}

func newArtifactExecutorForTest(t *testing.T, artifactDir, op string, cfg map[string]any) (*executorImpl, error) {
	t.Helper()

	step := artifactStep(op, cfg)
	dag := &core.DAG{Name: "artifact-test"}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithArtifactDir(artifactDir))
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	artifactExec, ok := exec.(*executorImpl)
	require.True(t, ok)
	return artifactExec, nil
}

func artifactStep(op string, cfg map[string]any) core.Step {
	return core.Step{
		Name:     "artifact-step",
		Commands: []core.CommandEntry{{Command: op}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   executorType,
			Config: cfg,
		},
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(content))
}
