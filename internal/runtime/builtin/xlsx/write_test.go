// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWriter struct {
	exec   *writeExecutor
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newTestWriter(t *testing.T, workDir, op string, cfg map[string]any) (*testWriter, error) {
	t.Helper()
	step := ir.Step{
		Name:           "xlsx-write",
		Commands:       []ir.CommandEntry{{Command: op}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "xlsx-test", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	write, ok := exec.(*writeExecutor)
	require.True(t, ok)
	tw := &testWriter{exec: write, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	write.SetStdout(tw.stdout)
	write.SetStderr(tw.stderr)
	return tw, nil
}

func TestWriteFromRowsJSONString(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Rows arrive as the JSON text of a step output.
	rows := `[{"Invoice No": "INV-1", "Amount": 10, "Due": "2026-10-01", "_row": 2}, {"Invoice No": "INV-2", "Amount": 20.5, "Due": null, "_row": 3}]`
	tw, err := newTestWriter(t, dir, opWrite, map[string]any{"path": "out.xlsx", "sheet": "Orders", "rows": rows})
	require.NoError(t, err)
	require.NoError(t, tw.exec.Run(context.Background()))

	outputs := tw.exec.GetOutputs()
	assert.Equal(t, filepath.Join(dir, "out.xlsx"), outputs["path"])
	assert.Equal(t, "Orders", outputs["sheet"])
	changes, ok := outputs["changes"].(workbook.Changes)
	require.True(t, ok)
	assert.Equal(t, workbook.Changes{Sheet: "Orders", Range: "Orders!A1:C3", RowsAppended: 2, CellsChanged: 8}, changes)
	assert.Equal(t, false, outputs["dry_run"])
	assert.Equal(t, "Wrote 2 rows to out.xlsx Orders\n", tw.stdout.String())

	back, err := workbook.Read(context.Background(), filepath.Join(dir, "out.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Invoice No", "Amount", "Due"}, back.Headers, "_row is not written and JSON order is kept")
	assert.Equal(t, "2026-10-01", back.Rows[0]["Due"])
}

func TestWriteColumnsOrderAndAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tw, err := newTestWriter(t, dir, opWrite, map[string]any{
		"path":    "out.xlsx",
		"rows":    []any{map[string]any{"b": 1, "a": 2}},
		"columns": `["b", "a"]`,
		"header":  true,
	})
	require.NoError(t, err)
	require.NoError(t, tw.exec.Run(context.Background()))
	back, err := workbook.Read(context.Background(), filepath.Join(dir, "out.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, back.Headers)

	app, err := newTestWriter(t, dir, opAppend, map[string]any{"path": "out.xlsx", "rows": `[{"b": 3, "a": 4}]`, "columns": "b, a"})
	require.NoError(t, err)
	require.NoError(t, app.exec.Run(context.Background()))
	assert.Equal(t, "Appended 1 rows to out.xlsx Sheet1\n", app.stdout.String())
	back, err = workbook.Read(context.Background(), filepath.Join(dir, "out.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, back.Count)
	assert.Equal(t, int64(3), back.Rows[1]["b"])
}

func TestWriteFromInputFileAndDryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.csv"), []byte("id,name\n1,a\n"), 0o600))
	tw, err := newTestWriter(t, dir, opWrite, map[string]any{"path": "out.xlsx", "input": "in.csv", "dry_run": true})
	require.NoError(t, err)
	require.NoError(t, tw.exec.Run(context.Background()))
	assert.Equal(t, true, tw.exec.GetOutputs()["dry_run"])
	assert.Equal(t, "Wrote 1 rows to out.xlsx Sheet1 (dry run)\n", tw.stdout.String())
	_, err = os.Stat(filepath.Join(dir, "out.xlsx"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWriteValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   string
		cfg  map[string]any
		want string
	}{
		{"no rows", opWrite, map[string]any{"path": "a.xlsx"}, "write requires with.rows or with.input"},
		{"both", opAppend, map[string]any{"path": "a.xlsx", "rows": "[]", "input": "x.csv"}, "append accepts with.rows or with.input, not both"},
		{"read field", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "where": map[string]any{"a": 1}}, "with.where is not valid for xlsx.write"},
		{"mode on append", opAppend, map[string]any{"path": "a.xlsx", "rows": "[]", "mode": "append"}, "with.mode is not valid for xlsx.append"},
		{"bad mode", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "mode": "upsert"}, "mode must be replace or append"},
		{"bad style", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "style": "fancy"}, "style must be table or none"},
		{"bad format", opWrite, map[string]any{"path": "a.xlsx", "input": "x", "format": "xml"}, "format must be json, jsonl, or csv"},
		{"bad wait", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "wait_for_unlock": "soon"}, "wait_for_unlock must be a duration"},
		{"row-number header", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "header": 3}, "header must be true or false for write"},
		{"header list on append", opAppend, map[string]any{"path": "a.xlsx", "rows": "[]", "header": []any{1, 2}}, "with.header is not valid for xlsx.append"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: tc.op}},
				ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: tc.cfg},
			}
			require.ErrorContains(t, validateStep(step), tc.want)
		})
	}
	ok := ir.Step{
		Commands:       []ir.CommandEntry{{Command: opWrite}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{"path": "a.xlsx", "rows": "[]", "wait_for_unlock": "5m", "atomic": false}},
	}
	require.NoError(t, validateStep(ok))
}

func TestWriteBadRowsFailAtRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tw, err := newTestWriter(t, dir, opWrite, map[string]any{"path": "out.xlsx", "rows": "not json"})
	require.NoError(t, err)
	err = tw.exec.Run(context.Background())
	require.ErrorContains(t, err, "rows must be a JSON array")
	assert.Equal(t, 1, tw.exec.ExitCode())
}
