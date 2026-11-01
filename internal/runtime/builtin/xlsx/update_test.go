// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateRowsWritesBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx",
		[]any{"Invoice No", "Status"},
		[]any{"INV-1", ""},
		[]any{"INV-2", ""},
	)
	// Results arrive as the JSON text of a step output, with the _row the
	// read step published.
	results := `[{"Invoice No": "INV-1", "_row": 2, "status": "Submitted"}, {"Invoice No": "INV-2", "_row": 3, "status": "Failed"}]`
	tw, err := newTestWriter(t, dir, opUpdateRows, map[string]any{
		"path": "orders.xlsx",
		"key":  "Invoice No",
		"rows": results,
		"set":  map[string]any{"Status": "status", "Checked": map[string]any{"value": "yes"}},
	})
	require.NoError(t, err)
	require.NoError(t, tw.exec.Run(context.Background()))

	changes := tw.exec.GetOutputs()["changes"].(workbook.Changes)
	assert.Equal(t, 2, changes.RowsUpdated)
	assert.Equal(t, 1, changes.ColumnsAdded)
	assert.Equal(t, "Updated 2 rows in orders.xlsx Sheet1 (1 columns added)\n", tw.stdout.String())

	back, err := workbook.Read(context.Background(), filepath.Join(dir, "orders.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Submitted", back.Rows[0]["Status"])
	assert.Equal(t, "yes", back.Rows[1]["Checked"])
}

func TestUpdateRowsShapeCheckFailsTheStep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"Invoice No", "Status"}, []any{"INV-1", ""})
	tw, err := newTestWriter(t, dir, opUpdateRows, map[string]any{
		"path": "orders.xlsx",
		"key":  "Invoice",
		"rows": `[{"Invoice": "INV-1", "Status": "x"}]`,
	})
	require.NoError(t, err)
	err = tw.exec.Run(context.Background())
	require.ErrorContains(t, err, `key column "Invoice" not found in header row 1`)
	assert.Equal(t, 1, tw.exec.ExitCode())
}

// A set deferred at build time is still parsed once its references resolve.
func TestUpdateRowsChecksDeferredSetAtRun(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{"path": "a.xlsx", "key": "_row", "rows": `[{"_row": 2}]`,
		"set": map[string]any{"Status": map[string]any{"value": "${steps.reg.outputs.ticket}", "extra": 1}}}
	require.NoError(t, validateStep(ir.Step{
		Commands:       []ir.CommandEntry{{Command: opUpdateRows}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}))

	cfg["set"] = map[string]any{"Status": map[string]any{"value": "1", "extra": 1}}
	_, err := newTestWriter(t, t.TempDir(), opUpdateRows, cfg)
	require.ErrorContains(t, err, "set.Status: use a field name or {value: literal}")
}

func TestUpdateRowsValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no key", map[string]any{"path": "a.xlsx", "rows": "[]"}, "key is required for update_rows"},
		{"no rows", map[string]any{"path": "a.xlsx", "key": "id"}, "update_rows requires with.rows"},
		{"bad missing", map[string]any{"path": "a.xlsx", "key": "id", "rows": "[]", "missing": "ignore"}, "missing must be fail, skip, or append"},
		{"skip with _row", map[string]any{"path": "a.xlsx", "key": "_row", "rows": "[]", "missing": "skip"}, "missing: skip needs a key column"},
		{"bad set", map[string]any{"path": "a.xlsx", "key": "id", "rows": "[]", "set": map[string]any{"Status": 3}}, "set.Status: use a field name or {value: literal}"},
		{"header false", map[string]any{"path": "a.xlsx", "key": "id", "rows": "[]", "header": false}, "header: false is not supported"},
		{"input not allowed", map[string]any{"path": "a.xlsx", "key": "id", "rows": "[]", "input": "x.csv"}, "with.input is not valid for xlsx.update_rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: opUpdateRows}},
				ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: tc.cfg},
			}
			require.ErrorContains(t, validateStep(step), tc.want)
		})
	}
}

func TestUpdateRowsAndAppendWriteToOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	book := writeBook(t, dir, "orders.xlsx",
		[]any{"Invoice No", "Status"},
		[]any{"INV-1", ""},
	)
	before, err := os.ReadFile(book)
	require.NoError(t, err)

	artifacts := filepath.Join(dir, "artifacts")
	update, err := newTestWriterWithArtifacts(t, dir, artifacts, opUpdateRows, map[string]any{
		"path":     "orders.xlsx",
		"output":   "updated.xlsx",
		"key":      "Invoice No",
		"rows":     `[{"Invoice No": "INV-1", "Status": "Submitted"}]`,
		"artifact": true,
	})
	require.NoError(t, err)
	require.NoError(t, update.exec.Run(context.Background()))
	assert.Equal(t, filepath.Join(dir, "updated.xlsx"), update.exec.GetOutputs()["path"])
	assert.Equal(t, filepath.ToSlash(filepath.Join("xlsx", "xlsx-write", "updated.xlsx")), filepath.ToSlash(update.exec.GetOutputs()["artifact"].(string)),
		"the output is the artifact")
	assert.Equal(t, "Updated 1 rows in updated.xlsx Sheet1\n", update.stdout.String())
	updated, err := workbook.Read(context.Background(), filepath.Join(dir, "updated.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Submitted", updated.Rows[0]["Status"])

	appended, err := newTestWriter(t, dir, opAppend, map[string]any{
		"path":   "orders.xlsx",
		"output": "appended.xlsx",
		"rows":   `[{"Invoice No": "INV-2", "Status": "New"}]`,
	})
	require.NoError(t, err)
	require.NoError(t, appended.exec.Run(context.Background()))
	assert.Equal(t, filepath.Join(dir, "appended.xlsx"), appended.exec.GetOutputs()["path"])
	assert.Equal(t, "Appended 1 rows to appended.xlsx Sheet1\n", appended.stdout.String())
	back, err := workbook.Read(context.Background(), filepath.Join(dir, "appended.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, back.Count)
	assert.Equal(t, "INV-2", back.Rows[1]["Invoice No"])

	after, err := os.ReadFile(book)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the workbook at path is untouched")
}

func TestOutputValidationForUpdateRowsAndAppend(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   string
		cfg  map[string]any
		want string
	}{
		{"update_rows csv output", opUpdateRows, map[string]any{"path": "a.xlsx", "key": "id", "rows": "[]", "output": "out.csv"}, "output: out.csv: only .xlsx and .xlsm workbooks are supported; save as .xlsx"},
		{"append csv output", opAppend, map[string]any{"path": "a.xlsx", "rows": "[]", "output": "out.csv"}, "output: out.csv: only .xlsx and .xlsm workbooks are supported; save as .xlsx"},
		{"output on write", opWrite, map[string]any{"path": "a.xlsx", "rows": "[]", "output": "b.xlsx"}, "with.output is not valid for xlsx.write"},
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
}
