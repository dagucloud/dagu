// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package data

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertCSVToJSON(t *testing.T) {
	t.Parallel()

	out := runDataConvert(t, map[string]any{
		"from": "csv",
		"to":   "json",
		"data": "name,age\nAlice,30\nBob,25\n",
	})

	assert.JSONEq(t, `[
		{"name":"Alice","age":"30"},
		{"name":"Bob","age":"25"}
	]`, out.String())
}

func TestConvertYAMLToCSV(t *testing.T) {
	t.Parallel()

	out := runDataConvert(t, map[string]any{
		"from":    "yaml",
		"to":      "csv",
		"columns": []any{"name", "age"},
		"data": `
- name: Alice
  age: 30
- name: Bob
  age: 25
`,
	})

	assert.Equal(t, "name,age\nAlice,30\nBob,25\n", out.String())
}

func TestConvertJSONToCSVWithoutHeader(t *testing.T) {
	t.Parallel()

	out := runDataConvert(t, map[string]any{
		"from":    "json",
		"to":      "csv",
		"headers": false,
		"columns": []any{"name", "age"},
		"data":    `[{"name":"Alice","age":30}]`,
	})

	assert.Equal(t, "Alice,30\n", out.String())
}

func TestPickYAMLScalarRaw(t *testing.T) {
	t.Parallel()

	out := runDataPick(t, map[string]any{
		"from":   "yaml",
		"select": ".spec.containers[0].image",
		"raw":    true,
		"data": `
spec:
  containers:
    - image: nginx:1.27
`,
	})

	assert.Equal(t, "nginx:1.27\n", out.String())
}

func TestPickCSVRowAsJSON(t *testing.T) {
	t.Parallel()

	out := runDataPick(t, map[string]any{
		"from":   "csv",
		"select": ".[1]",
		"data":   "name,age\nAlice,30\nBob,25\n",
	})

	assert.JSONEq(t, `{"name":"Bob","age":"25"}`, out.String())
}

func TestConvertInputFile(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "users.csv"), []byte("name\nAlice\n"), 0o600))

	step := dataStep(opConvert, map[string]any{
		"from":  "csv",
		"to":    "json",
		"input": "users.csv",
	})
	ctx := dataContext(workDir, step)

	exec, err := newExecutor(ctx, step)
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(ctx))
	assert.JSONEq(t, `[{"name":"Alice"}]`, out.String())
}

func TestConvertRejectsDataAndInput(t *testing.T) {
	t.Parallel()

	step := dataStep(opConvert, map[string]any{
		"from":  "csv",
		"to":    "json",
		"data":  "name\nAlice\n",
		"input": "users.csv",
	})

	_, err := newExecutor(context.Background(), step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data and input are mutually exclusive")
}

func TestConvertConfigSchemaRejectsMissingDataAndInput(t *testing.T) {
	t.Parallel()

	err := core.ValidateExecutorConfig(executorType, map[string]any{
		"from": "csv",
		"to":   "json",
	})
	require.Error(t, err)
}

func runDataConvert(t *testing.T, cfg map[string]any) *bytes.Buffer {
	t.Helper()

	exec, err := newExecutor(context.Background(), dataStep(opConvert, cfg))
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(context.Background()))
	return out
}

func runDataPick(t *testing.T, cfg map[string]any) *bytes.Buffer {
	t.Helper()

	exec, err := newExecutor(context.Background(), dataStep(opPick, cfg))
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(context.Background()))
	return out
}

func dataStep(op string, cfg map[string]any) core.Step {
	return core.Step{
		Name:     "convert",
		Commands: []core.CommandEntry{{Command: op}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   executorType,
			Config: cfg,
		},
	}
}

func dataContext(workDir string, step core.Step) context.Context {
	dag := &core.DAG{
		Name:               "data-test",
		WorkingDir:         workDir,
		WorkingDirExplicit: true,
	}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}
