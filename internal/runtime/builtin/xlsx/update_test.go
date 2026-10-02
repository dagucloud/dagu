// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
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
