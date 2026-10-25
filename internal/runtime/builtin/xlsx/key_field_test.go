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

func TestUpdateRowsKeyField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"注文番号", "状態"}, []any{"INV-1", ""})
	tw, err := newTestWriter(t, dir, opUpdateRows, map[string]any{
		"path": "orders.xlsx", "key": "注文番号", "key_field": "order_id",
		"rows": `[{"order_id":"INV-1","_row":2,"status":"処理済み"},{"order_id":"INV-2","status":"追加"}]`,
		"set":  map[string]any{"状態": "status"}, "missing": "append",
	})
	require.NoError(t, err)
	require.NoError(t, tw.exec.Run(context.Background()))
	back, err := workbook.Read(context.Background(), filepath.Join(dir, "orders.xlsx"), workbook.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, back.Rows, 2)
	assert.Equal(t, "処理済み", back.Rows[0]["状態"])
	assert.Equal(t, "INV-2", back.Rows[1]["注文番号"])
	assert.Equal(t, "追加", back.Rows[1]["状態"])
}

func TestUpdateRowsKeyFieldValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ key, field, want string }{
		{"id", " ", "key_field must not be blank"},
		{"_row", "id", "key_field needs a key column"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			step := ir.Step{Commands: []ir.CommandEntry{{Command: opUpdateRows}},
				ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{
					"path": "a.xlsx", "key": tc.key, "key_field": tc.field, "rows": "[]",
				}}}
			require.ErrorContains(t, validateStep(step), tc.want)
		})
	}
}
