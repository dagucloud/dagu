// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

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

func TestFileExecutorWriteReadAndStat(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	writeOut := runFileAction(t, workDir, opWrite, map[string]any{
		"path":        "out/data.txt",
		"content":     "hello",
		"create_dirs": true,
	})

	var writeResult map[string]any
	require.NoError(t, json.Unmarshal(writeOut.Bytes(), &writeResult))
	assert.Equal(t, opWrite, writeResult["operation"])
	assert.Equal(t, float64(len("hello")), writeResult["bytes"])

	content, err := os.ReadFile(filepath.Join(workDir, "out", "data.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	readOut := runFileAction(t, workDir, opRead, map[string]any{
		"path": "out/data.txt",
	})
	assert.Equal(t, "hello", readOut.String())

	statOut := runFileAction(t, workDir, opStat, map[string]any{
		"path": "out/data.txt",
	})

	var statResult map[string]any
	require.NoError(t, json.Unmarshal(statOut.Bytes(), &statResult))
	assert.Equal(t, opStat, statResult["operation"])
	assert.Equal(t, true, statResult["exists"])
	assert.Equal(t, "file", statResult["type"])
	assert.Equal(t, float64(len("hello")), statResult["size"])
}

func TestFileExecutorCopyMoveListAndDelete(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "input"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "input", "a.txt"), []byte("alpha"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "input", "b.log"), []byte("beta"), 0o600))

	copyOut := runFileAction(t, workDir, opCopy, map[string]any{
		"source":      "input/a.txt",
		"destination": "stage/a.txt",
		"create_dirs": true,
	})
	assertJSONField(t, copyOut, "operation", opCopy)
	assertFileContent(t, filepath.Join(workDir, "stage", "a.txt"), "alpha")

	moveOut := runFileAction(t, workDir, opMove, map[string]any{
		"source":      "stage/a.txt",
		"destination": "stage/moved.txt",
	})
	assertJSONField(t, moveOut, "operation", opMove)
	assert.NoFileExists(t, filepath.Join(workDir, "stage", "a.txt"))
	assertFileContent(t, filepath.Join(workDir, "stage", "moved.txt"), "alpha")

	listOut := runFileAction(t, workDir, opList, map[string]any{
		"path":      ".",
		"recursive": true,
		"pattern":   "**/*.txt",
	})

	var listResult struct {
		Operation string `json:"operation"`
		Entries   []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(listOut.Bytes(), &listResult))
	assert.Equal(t, opList, listResult.Operation)
	require.Len(t, listResult.Entries, 2)
	assert.Equal(t, "input/a.txt", listResult.Entries[0].Path)
	assert.Equal(t, "file", listResult.Entries[0].Type)
	assert.Equal(t, "stage/moved.txt", listResult.Entries[1].Path)

	deleteOut := runFileAction(t, workDir, opDelete, map[string]any{
		"path": "stage/moved.txt",
	})
	assertJSONField(t, deleteOut, "operation", opDelete)
	assert.NoFileExists(t, filepath.Join(workDir, "stage", "moved.txt"))
}

func TestFileExecutorConservativeDefaults(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "existing.txt"), []byte("old"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "dir"), 0o750))

	writeExec, writeErr := newFileExecutorForTest(t, workDir, opWrite, map[string]any{
		"path":    "existing.txt",
		"content": "new",
	})
	require.NoError(t, writeErr)
	require.Error(t, writeExec.Run(context.Background()))
	assertFileContent(t, filepath.Join(workDir, "existing.txt"), "old")

	deleteExec, err := newFileExecutorForTest(t, workDir, opDelete, map[string]any{
		"path": "dir",
	})
	require.NoError(t, err)
	require.Error(t, deleteExec.Run(context.Background()))
	assert.DirExists(t, filepath.Join(workDir, "dir"))

	rootExec, err := newFileExecutorForTest(t, workDir, opDelete, map[string]any{
		"path":      filepath.VolumeName(workDir) + string(os.PathSeparator),
		"recursive": true,
	})
	require.NoError(t, err)
	require.Error(t, rootExec.Run(context.Background()))
}

func TestFileExecutorMkdirParentsAndMissingOK(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	mkdirOut := runFileAction(t, workDir, opMkdir, map[string]any{
		"path": "a/b/c",
	})
	assertJSONField(t, mkdirOut, "operation", opMkdir)
	assert.DirExists(t, filepath.Join(workDir, "a", "b", "c"))

	deleteOut := runFileAction(t, workDir, opDelete, map[string]any{
		"path":       "not-there.txt",
		"missing_ok": true,
	})
	assertJSONField(t, deleteOut, "operation", opDelete)
}

func runFileAction(t *testing.T, workDir, op string, cfg map[string]any) *bytes.Buffer {
	t.Helper()

	exec, err := newFileExecutorForTest(t, workDir, op, cfg)
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(context.Background()))
	return out
}

func newFileExecutorForTest(t *testing.T, workDir, op string, cfg map[string]any) (*executorImpl, error) {
	t.Helper()

	step := core.Step{
		Name:     "file-step",
		Commands: []core.CommandEntry{{Command: op}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   executorType,
			Config: cfg,
		},
	}
	dag := &core.DAG{
		Name:               "file-test",
		WorkingDir:         workDir,
		WorkingDirExplicit: true,
	}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	fileExec, ok := exec.(*executorImpl)
	require.True(t, ok)
	return fileExec, nil
}

func assertJSONField(t *testing.T, buf *bytes.Buffer, field string, want any) {
	t.Helper()

	var result map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, want, result[field])
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(content))
}
