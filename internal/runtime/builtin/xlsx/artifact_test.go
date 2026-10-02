// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newArtifactWriter(t *testing.T, workDir, artifactsDir string, cfg map[string]any) (*writeExecutor, *bytes.Buffer, error) {
	t.Helper()
	step := ir.Step{
		Name:           "Write report",
		Commands:       []ir.CommandEntry{{Command: opWrite}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "xlsx-test", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	env := runtime.NewEnv(ctx, step)
	scope := value.NewEnvScope(nil, false)
	if artifactsDir != "" {
		scope = scope.WithEntry(runenv.EnvKeyDAGRunArtifactsDir, artifactsDir, value.EnvSourceDAGEnv)
	}
	env.Scope = scope
	ctx = runtime.WithEnv(ctx, env)
	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, nil, err
	}
	write := exec.(*writeExecutor)
	out := &bytes.Buffer{}
	write.SetStdout(out)
	write.SetStderr(&bytes.Buffer{})
	return write, out, nil
}

func TestArtifactCopiesTheSavedWorkbook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifacts := filepath.Join(t.TempDir(), "artifacts")
	exec, _, err := newArtifactWriter(t, dir, artifacts, map[string]any{"path": "report.xlsx", "rows": `[{"a": 1}]`, "artifact": true})
	require.NoError(t, err)
	require.NoError(t, exec.Run(context.Background()))

	outputs := exec.GetOutputs()
	assert.Equal(t, "xlsx/Write_report/report.xlsx", outputs["artifact"])
	copied := filepath.Join(artifacts, "xlsx", "Write_report", "report.xlsx")
	back, err := workbook.Read(context.Background(), copied, workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, back.Count)
	original, err := os.ReadFile(filepath.Join(dir, "report.xlsx"))
	require.NoError(t, err)
	copy, err := os.ReadFile(copied)
	require.NoError(t, err)
	assert.Equal(t, original, copy)
}

func TestArtifactDryRunCopiesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifacts := filepath.Join(t.TempDir(), "artifacts")
	exec, _, err := newArtifactWriter(t, dir, artifacts, map[string]any{"path": "report.xlsx", "rows": `[{"a": 1}]`, "artifact": true, "dry_run": true})
	require.NoError(t, err)
	require.NoError(t, exec.Run(context.Background()))
	_, hasArtifact := exec.GetOutputs()["artifact"]
	assert.False(t, hasArtifact)
	_, err = os.Stat(filepath.Join(artifacts, "xlsx"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestArtifactRequiresStorage(t *testing.T) {
	t.Parallel()
	_, _, err := newArtifactWriter(t, t.TempDir(), "", map[string]any{"path": "report.xlsx", "rows": `[{"a": 1}]`, "artifact": true})
	require.EqualError(t, err, "artifact requires artifact storage")
}
