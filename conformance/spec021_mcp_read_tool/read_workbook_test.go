// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
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
		columns, ok := sheet["columns"].([]any)
		require.True(t, ok)
		require.Len(t, columns, 3)
		amount, ok := columns[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "Amount", amount["name"])
		require.Equal(t, "number", amount["type"])
		require.Equal(t, float64(2), amount["filled"])
		require.Equal(t, float64(0), amount["blank"])
		require.Equal(t, float64(2), amount["distinct"])
		require.Equal(t, float64(10), amount["min"])
		require.Equal(t, 20.5, amount["max"])
		sample, ok := sheet["sample"].([]any)
		require.True(t, ok)
		require.Len(t, sample, 2)
		first, ok := sample[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "INV-1", first["Invoice No"])
		require.Equal(t, float64(2), first["_row"])
		require.NotContains(t, sheet, "profile_truncated", "a table the profile reads whole is not truncated")
	})

	t.Run("reports a truncated profile", func(t *testing.T) {
		long := filepath.Join(t.TempDir(), "long.xlsx")
		rows := make([][]any, workbook.DefaultMaxRows+1)
		for i := range rows {
			rows[i] = []any{int64(i + 1)}
		}
		_, err := workbook.Write(context.Background(), long, workbook.Table{Columns: []string{"n"}, Rows: rows}, workbook.WriteOptions{Header: true})
		require.NoError(t, err)

		result := callRead(t, session, map[string]any{"target": "workbook", "path": long})
		require.False(t, result.IsError)
		sheets, ok := requireData(t, mcptest.StructuredMap(t, result))["sheets"].([]any)
		require.True(t, ok)
		sheet, ok := sheets[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, float64(workbook.DefaultMaxRows+1), sheet["row_count"])
		require.Equal(t, true, sheet["profile_truncated"])
	})

	t.Run("reports hidden sheets and rows", func(t *testing.T) {
		f := excelize.NewFile()
		for i, row := range [][]any{{"ID", "Status"}, {"A-1", "Done"}, {"A-2", "Open"}} {
			require.NoError(t, f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i+1), &row))
		}
		require.NoError(t, f.SetRowVisible("Sheet1", 3, false))
		_, err := f.NewSheet("Archive")
		require.NoError(t, err)
		require.NoError(t, f.SetSheetVisible("Archive", false))
		hidden := filepath.Join(t.TempDir(), "hidden.xlsx")
		require.NoError(t, f.SaveAs(hidden))
		require.NoError(t, f.Close())

		result := callRead(t, session, map[string]any{"target": "workbook", "path": hidden})
		require.False(t, result.IsError)
		sheets, ok := requireData(t, mcptest.StructuredMap(t, result))["sheets"].([]any)
		require.True(t, ok)
		require.Len(t, sheets, 2)
		data, ok := sheets[0].(map[string]any)
		require.True(t, ok)
		require.NotContains(t, data, "hidden")
		require.Equal(t, float64(1), data["hidden_rows"])
		archive, ok := sheets[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, true, archive["hidden"])
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
