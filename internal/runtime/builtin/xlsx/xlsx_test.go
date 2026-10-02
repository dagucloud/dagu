// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func writeBook(t *testing.T, dir, name string, rows ...[]any) string {
	t.Helper()
	f := excelize.NewFile()
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		require.NoError(t, err)
		require.NoError(t, f.SetSheetRow("Sheet1", cell, &row))
	}
	path := filepath.Join(dir, name)
	require.NoError(t, f.SaveAs(path))
	require.NoError(t, f.Close())
	return path
}

type testExecutor struct {
	exec   *readExecutor
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newTestExecutor(t *testing.T, workDir, op string, cfg map[string]any, dagOpts ...func(*ir.DAG)) (*testExecutor, error) {
	t.Helper()
	step := ir.Step{
		Name:           "xlsx-step",
		Commands:       []ir.CommandEntry{{Command: op}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "xlsx-test", WorkingDir: workDir, WorkingDirExplicit: true}
	for _, opt := range dagOpts {
		opt(dag)
	}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	read, ok := exec.(*readExecutor)
	require.True(t, ok)
	te := &testExecutor{exec: read, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	read.SetStdout(te.stdout)
	read.SetStderr(te.stderr)
	return te, nil
}

func TestReadPublishesRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx",
		[]any{"Invoice No", "Amount", "Status"},
		[]any{"INV-1", 10, "Done"},
		[]any{"INV-2", 20.5, nil},
	)
	te, err := newTestExecutor(t, dir, opRead, map[string]any{"path": "orders.xlsx", "where": map[string]any{"Status": ""}})
	require.NoError(t, err)
	require.NoError(t, te.exec.Run(context.Background()))

	outputs := te.exec.GetOutputs()
	rows, ok := outputs["rows"].([]workbook.Row)
	require.True(t, ok)
	require.Len(t, rows, 1)
	assert.Equal(t, "INV-2", rows[0]["Invoice No"])
	assert.Equal(t, 20.5, rows[0]["Amount"])
	assert.Equal(t, 3, rows[0][workbook.RowNumberKey])
	assert.Equal(t, 1, outputs["count"])
	assert.Equal(t, []string{"Invoice No", "Amount", "Status"}, outputs["headers"])
	assert.Equal(t, "Sheet1", outputs["sheet"])
	assert.Equal(t, "Sheet1!A1:C3", outputs["range"])
	assert.Equal(t, false, outputs["truncated"])
	assert.Equal(t, "Read 1 rows from orders.xlsx Sheet1!A1:C3\n", te.stdout.String())
	assert.True(t, te.exec.PublishesDeclaredOutputs())
	assert.Equal(t, 0, te.exec.ExitCode())
}

func TestReadFitsOutputBudget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := [][]any{{"n", "text"}}
	for i := range 200 {
		rows = append(rows, []any{i, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"})
	}
	writeBook(t, dir, "big.xlsx", rows...)
	te, err := newTestExecutor(t, dir, opRead, map[string]any{"path": "big.xlsx"}, func(d *ir.DAG) {
		d.MaxOutputSize = outputMargin + 2048
	})
	require.NoError(t, err)
	require.NoError(t, te.exec.Run(context.Background()))
	outputs := te.exec.GetOutputs()
	assert.Equal(t, true, outputs["truncated"])
	assert.Less(t, outputs["count"].(int), 200)
	assert.Contains(t, te.stderr.String(), "output truncated to")
}

func TestReadErrorsNameTheCell(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"Amount"}, []any{"N/A"})
	te, err := newTestExecutor(t, dir, opRead, map[string]any{"path": "orders.xlsx", "types": map[string]any{"Amount": "number"}})
	require.NoError(t, err)
	err = te.exec.Run(context.Background())
	require.EqualError(t, err, `orders.xlsx Sheet1!A2: expected number, found "N/A"`)
	assert.Equal(t, 1, te.exec.ExitCode())
	assert.Nil(t, te.exec.GetOutputs())
}

func TestOnTypeErrorNullSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"Amount"}, []any{"N/A"}, []any{7})
	// An unquoted YAML null arrives as nil; it means warn.
	te, err := newTestExecutor(t, dir, opRead, map[string]any{"path": "orders.xlsx", "types": map[string]any{"Amount": "number"}, "on_type_error": nil})
	require.NoError(t, err)
	require.NoError(t, te.exec.Run(context.Background()))
	rows := te.exec.GetOutputs()["rows"].([]workbook.Row)
	require.Len(t, rows, 2)
	assert.Nil(t, rows[0]["Amount"])
	assert.Equal(t, int64(7), rows[1]["Amount"])
	assert.Contains(t, te.stderr.String(), `warning: Sheet1!A2: expected number, found "N/A"`)
}

func TestInfoAndListSheets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBook(t, dir, "orders.xlsx", []any{"Invoice No", "Amount"}, []any{"INV-1", 10})

	info, err := newTestExecutor(t, dir, opInfo, map[string]any{"path": path})
	require.NoError(t, err)
	require.NoError(t, info.exec.Run(context.Background()))
	outputs := info.exec.GetOutputs()
	sheets, ok := outputs["sheets"].([]workbook.SheetInfo)
	require.True(t, ok)
	require.Len(t, sheets, 1)
	assert.Equal(t, []string{"Invoice No", "Amount"}, sheets[0].Headers)
	assert.Equal(t, 1, sheets[0].HeaderRow)
	assert.Equal(t, "1900", outputs["date_system"])
	assert.Equal(t, "orders.xlsx: 1 sheets\n", info.stdout.String())

	list, err := newTestExecutor(t, dir, opListSheets, map[string]any{"path": "orders.xlsx"})
	require.NoError(t, err)
	require.NoError(t, list.exec.Run(context.Background()))
	assert.Equal(t, map[string]any{"sheets": []string{"Sheet1"}, "count": 1}, list.exec.GetOutputs())
}

func TestValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   string
		cfg  map[string]any
		want string
	}{
		{"missing path", opRead, map[string]any{}, "path is required for read"},
		{"unknown field", opRead, map[string]any{"path": "a.xlsx", "strip": true}, "invalid keys: strip"},
		{"foreign field", opInfo, map[string]any{"path": "a.xlsx", "range": "A1:B2"}, "with.range is not valid for xlsx.info"},
		{"bad merged", opRead, map[string]any{"path": "a.xlsx", "merged": "middle"}, "merged must be fill or first"},
		{"bad formulas", opRead, map[string]any{"path": "a.xlsx", "formulas": "eval"}, "formulas must be cached, text, or calculate"},
		{"bad on_type_error", opRead, map[string]any{"path": "a.xlsx", "on_type_error": "ignore"}, "on_type_error must be fail, warn, or null"},
		{"bad header", opRead, map[string]any{"path": "a.xlsx", "header": "yes"}, "header must be true, false, a row number"},
		{"bad columns", opRead, map[string]any{"path": "a.xlsx", "columns": map[string]any{"a": "b"}}, "columns must be a list"},
		{"bad type", opRead, map[string]any{"path": "a.xlsx", "types": map[string]any{"a": "money"}}, `types.a: unknown column type "money"`},
		{"max_rows zero", opRead, map[string]any{"path": "a.xlsx", "max_rows": 0}, "max_rows must be >= 1"},
		{"bad where", opRead, map[string]any{"path": "a.xlsx", "where": map[string]any{"a": map[string]any{"like": "x"}}}, `where: a: unknown operator "like"`},
		{"unsupported op", "pivot", map[string]any{"path": "a.xlsx"}, `unsupported operation "pivot"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: tc.op}},
				ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: tc.cfg},
			}
			err := validateStep(step)
			require.ErrorContains(t, err, tc.want)
		})
	}
	require.NoError(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "file"}}))
}

func TestValidationDefersValueReferences(t *testing.T) {
	t.Parallel()
	// At build time typed fields may still hold references; they are
	// checked at run time, when they have been resolved.
	step := ir.Step{
		Commands: []ir.CommandEntry{{Command: opRead}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
			"path":          "${params.BOOK}",
			"max_rows":      "${params.LIMIT}",
			"trim":          "${params.TRIM}",
			"on_type_error": "${params.MODE}",
			"header":        "${params.HEADER}",
		}},
	}
	require.NoError(t, validateStep(step))

	write := ir.Step{
		Commands: []ir.CommandEntry{{Command: opWrite}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
			"path":  "out.xlsx",
			"input": "${params.INPUT}",
		}},
	}
	require.NoError(t, validateStep(write))

	// Unknown fields are still rejected at build time.
	step.ExecutorConfig.Config["strip"] = "${params.X}"
	require.ErrorContains(t, validateStep(step), "with.strip is not valid for xlsx.read")

	// A literal value that is wrong is still rejected at build time.
	step.ExecutorConfig.Config = map[string]any{"path": "${params.BOOK}", "max_rows": "soon"}
	require.Error(t, validateStep(step))

	// A reference nested in a map or list field defers the whole field.
	nested := ir.Step{
		Commands: []ir.CommandEntry{{Command: opRead}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
			"path":    "book.xlsx",
			"types":   map[string]any{"amount": "${params.TYPE}"},
			"where":   map[string]any{"status": map[string]any{"in": []any{"${params.STATUS}"}}},
			"columns": []any{"${params.COLUMN}"},
		}},
	}
	require.NoError(t, validateStep(nested))
	nested.ExecutorConfig.Config["types"] = map[string]any{"amount": "money"}
	require.ErrorContains(t, validateStep(nested), "types.amount")
}

func TestOutputBudgetStaysPositive(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		limit int
		want  int
	}{
		{0, outputBudget},
		{ir.DefaultMaxOutputSize, outputBudget},
		{outputMargin + 100, 100},
		{outputMargin, outputMargin / 2},
		{32 << 10, 16 << 10},
		{1, 1},
	} {
		dag := &ir.DAG{MaxOutputSize: tc.limit}
		assert.Equal(t, tc.want, budgetFor(runtime.Env{DAG: dag}), "limit %d", tc.limit)
	}
}

func TestConfigSchemaRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	require.NoError(t, registry.ValidateExecutorConfig(executorType, map[string]any{"path": "a.xlsx", "max_rows": 10}))
	require.Error(t, registry.ValidateExecutorConfig(executorType, map[string]any{"path": "a.xlsx", "strip": true}))
	require.Error(t, registry.ValidateExecutorConfig(executorType, map[string]any{"path": "a.xlsx", "merged": "middle"}))
}

func TestResolvePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := resolvePath(dir, "sub/orders.xlsx")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "sub", "orders.xlsx"), got)
	abs := filepath.Join(dir, "a.xlsx")
	got, err = resolvePath("elsewhere", abs)
	require.NoError(t, err)
	assert.Equal(t, abs, got)
	_, err = resolvePath(dir, "  ")
	require.Error(t, err)
}
