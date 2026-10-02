// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/stretchr/testify/require"
)

func TestReadWorkbookTarget(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	// The server runs in this process, so a temporary workbook is a file it
	// can read.
	path := filepath.Join(t.TempDir(), "orders.xlsx")
	table := workbook.Table{
		Columns: []string{"Invoice No", "Amount", "Due"},
		Rows:    [][]any{{"INV-1", int64(10), "2026-10-01"}, {"INV-2", 20.5, "2026-10-02"}},
	}
	_, err := workbook.Write(context.Background(), path, table, workbook.WriteOptions{Sheet: "Orders", Header: true})
	require.NoError(t, err)

	t.Run("describes the workbook", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "workbook", "path": path})
		require.False(t, result.IsError)
		output := mcptest.StructuredMap(t, result)
		require.Equal(t, "workbook", output["target"])
		require.Equal(t, path, output["path"])
		require.NotContains(t, output, "uri")
		requireReferences(t, output["references"])

		data := requireData(t, output)
		require.Equal(t, "1900", data["date_system"])
		sheets, ok := data["sheets"].([]any)
		require.True(t, ok)
		require.Len(t, sheets, 1)
		sheet, ok := sheets[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "Orders", sheet["name"])
		require.Equal(t, float64(1), sheet["header_row"])
		require.Equal(t, []any{"Invoice No", "Amount", "Due"}, sheet["headers"])
		types, ok := sheet["types"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "number", types["Amount"])
		require.Equal(t, "date", types["Due"])
		require.Equal(t, float64(2), sheet["row_count"])
		sample, ok := sheet["sample"].([]any)
		require.True(t, ok)
		require.Len(t, sample, 2)
		first, ok := sample[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "INV-1", first["Invoice No"])
		require.Equal(t, float64(2), first["_row"])
	})

	t.Run("requires path", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "workbook"})
		output := requireReadError(t, result, "invalid_tool_input")
		require.Equal(t, "path", output["field"])
	})

	t.Run("rejects other formats", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "workbook", "path": filepath.Join(t.TempDir(), "book.xls")})
		output := requireReadError(t, result, "invalid_tool_input")
		require.Equal(t, "path", output["field"])
		require.Contains(t, output["message"], "save as .xlsx")
	})

	t.Run("missing workbook", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "workbook", "path": filepath.Join(t.TempDir(), "missing.xlsx")})
		output := requireReadError(t, result, "resource_not_found")
		require.Contains(t, output["message"], "missing.xlsx: workbook not found")
	})

	t.Run("path stays forbidden elsewhere", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "dags", "path": path})
		output := requireReadError(t, result, "invalid_tool_input")
		require.Equal(t, "path", output["field"])
	})

	t.Run("other fields are forbidden", func(t *testing.T) {
		result := callRead(t, session, map[string]any{"target": "workbook", "path": path, "name": "x"})
		output := requireReadError(t, result, "invalid_tool_input")
		require.Equal(t, "name", output["field"])
	})
}
