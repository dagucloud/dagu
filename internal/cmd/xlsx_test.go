// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func writeTestWorkbook(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orders.xlsx")
	table := workbook.Table{
		Columns: []string{"Invoice No", "Amount", "Due"},
		Rows:    [][]any{{"INV-1", int64(10), "2026-10-01"}, {"INV-2", 20.5, "2026-10-02"}},
	}
	_, err := workbook.Write(context.Background(), path, table, workbook.WriteOptions{Sheet: "Orders", Header: true})
	require.NoError(t, err)
	return path
}

func runXlsx(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cmd.Xlsx()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestXlsxInspectJSON(t *testing.T) {
	t.Parallel()
	path := writeTestWorkbook(t)
	out, err := runXlsx(t, "inspect", path, "--format", "json", "--rows", "1")
	require.NoError(t, err)

	var info workbook.Info
	require.NoError(t, json.Unmarshal([]byte(out), &info))
	require.Len(t, info.Sheets, 1)
	assert.Equal(t, "Orders", info.Sheets[0].Name)
	assert.Equal(t, 1, info.Sheets[0].HeaderRow)
	assert.Equal(t, []string{"Invoice No", "Amount", "Due"}, info.Sheets[0].Headers)
	assert.Equal(t, "number", info.Sheets[0].Types["Amount"])
	assert.Equal(t, "date", info.Sheets[0].Types["Due"])
	assert.Equal(t, 2, info.Sheets[0].RowCount)
	require.Len(t, info.Sheets[0].Columns, 3)
	assert.Equal(t, workbook.ColumnInfo{Name: "Due", Type: "date", Filled: 2, Distinct: 2, Min: "2026-10-01", Max: "2026-10-02"}, info.Sheets[0].Columns[2])
	require.Len(t, info.Sheets[0].Sample, 1)
	assert.Equal(t, "INV-1", info.Sheets[0].Sample[0]["Invoice No"])
}

func TestXlsxInspectText(t *testing.T) {
	t.Parallel()
	path := writeTestWorkbook(t)
	out, err := runXlsx(t, "inspect", path, "--sheet", "orders")
	require.NoError(t, err)
	assert.Contains(t, out, "orders.xlsx: 1 sheets, 1900 date system")
	assert.Contains(t, out, `Sheet "Orders": used A1:C3, table Orders!A1:C3, header row 1, 2 rows`)
	assert.Contains(t, out, "Columns: Invoice No (string), Amount (number; 10..20.5), Due (date; 2026-10-01..2026-10-02)")
	assert.Contains(t, out, "Row 2: Invoice No=INV-1  Amount=10  Due=2026-10-01")

	_, err = runXlsx(t, "inspect", path, "--sheet", "Nope")
	require.ErrorContains(t, err, `sheet "Nope" not found; sheets present: Orders`)
}

func TestXlsxInspectTextProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "status.xlsx")
	table := workbook.Table{
		Columns: []string{"Status", "Qty"},
		Rows:    [][]any{{"済", int64(1)}, {"未", int64(2)}, {"済", "未定"}, {nil, int64(4)}},
	}
	_, err := workbook.Write(context.Background(), path, table, workbook.WriteOptions{Header: true})
	require.NoError(t, err)

	out, err := runXlsx(t, "inspect", path)
	require.NoError(t, err)
	assert.Contains(t, out, `Columns: Status (string: 済, 未; 1 blank), Qty (integer; 1..4; 1 odd: B4 "未定")`)
}

func TestXlsxHiddenSheetsAndRows(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	for i, row := range [][]any{{"ID", "Status"}, {"A-1", "Done"}, {"A-2", "Open"}, {"A-3", "Done"}} {
		require.NoError(t, f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i+1), &row))
	}
	require.NoError(t, f.SetRowVisible("Sheet1", 3, false))
	_, err := f.NewSheet("Archive")
	require.NoError(t, err)
	require.NoError(t, f.SetSheetRow("Archive", "A1", &[]any{"ID", "Note"}))
	require.NoError(t, f.SetSheetVisible("Archive", false))
	path := filepath.Join(t.TempDir(), "hidden.xlsx")
	require.NoError(t, f.SaveAs(path))
	require.NoError(t, f.Close())

	out, err := runXlsx(t, "inspect", path)
	require.NoError(t, err)
	assert.Contains(t, out, `Sheet "Sheet1": used A1:B4, table Sheet1!A1:B4, header row 1, 3 rows, 1 hidden`)
	assert.Contains(t, out, `Sheet "Archive" (hidden): used A1:B1, table Archive!A1:B1, header row 1, 0 rows`)

	out, err = runXlsx(t, "read", path, "--skip-hidden", "--format", "json")
	require.NoError(t, err)
	var result workbook.ReadResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, 2, result.Count)
	assert.Equal(t, "A-3", result.Rows[1]["ID"])
}

func TestXlsxReadJSONAndText(t *testing.T) {
	t.Parallel()
	path := writeTestWorkbook(t)
	out, err := runXlsx(t, "read", path, "--format", "json", "--columns", "Invoice No:id,Amount", "--range", "A1:C")
	require.NoError(t, err)
	var result workbook.ReadResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, []string{"id", "Amount"}, result.Headers)
	require.Equal(t, 2, result.Count)
	assert.Equal(t, "INV-1", result.Rows[0]["id"])
	assert.Equal(t, float64(10), result.Rows[0]["Amount"])
	assert.Equal(t, float64(2), result.Rows[0][workbook.RowNumberKey])
	assert.Equal(t, "Orders!A1:C3", result.Range)

	text, err := runXlsx(t, "read", path, "--header", "false", "--max-rows", "1")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.Equal(t, "_row\tA\tB\tC", lines[0])
	assert.Equal(t, "1\tInvoice No\tAmount\tDue", lines[1])
	assert.Contains(t, text, "Warning: stopped after 1 rows")
}

func TestXlsxReadTextEscapesDelimiters(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "notes.xlsx")
	table := workbook.Table{Columns: []string{"note"}, Rows: [][]any{{"line one\nline two\twith tab\\slash"}}}
	_, err := workbook.Write(context.Background(), path, table, workbook.WriteOptions{Header: true})
	require.NoError(t, err)

	out, err := runXlsx(t, "read", path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2, "one cell stays on one line")
	assert.Equal(t, `2	line one\nline two\twith tab\\slash`, lines[1])
}

func TestXlsxReadTextEscapesHeaders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "headers.xlsx")
	// A sheet header keeps its backslashes, and a --columns alias may carry
	// a tab; neither may shift the header line's columns.
	table := workbook.Table{Columns: []string{`unit\price`, "qty"}, Rows: [][]any{{"a", "b"}}}
	_, err := workbook.Write(context.Background(), path, table, workbook.WriteOptions{Header: true})
	require.NoError(t, err)

	out, err := runXlsx(t, "read", path, "--columns", "unit\\price,qty:count\tper box")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, `_row	unit\\price	count\tper box`, lines[0])
	assert.Equal(t, "2\ta\tb", lines[1])
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func TestXlsxTextOutputReportsWriteErrors(t *testing.T) {
	t.Parallel()
	path := writeTestWorkbook(t)
	for _, args := range [][]string{{"inspect", path}, {"read", path}} {
		root := cmd.Xlsx()
		root.SetOut(failingWriter{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)
		require.ErrorContains(t, root.ExecuteContext(context.Background()), "stdout closed", "%v", args)
	}
}

func TestXlsxErrors(t *testing.T) {
	t.Parallel()
	_, err := runXlsx(t, "read", filepath.Join(t.TempDir(), "missing.xlsx"))
	require.ErrorContains(t, err, "missing.xlsx: workbook not found")
	_, err = runXlsx(t, "inspect", "book.xls")
	require.ErrorContains(t, err, "only .xlsx and .xlsm workbooks are supported; save as .xlsx")
	_, err = runXlsx(t, "read", "book.xlsx", "--format", "xml")
	require.ErrorContains(t, err, `invalid format "xml": use text or json`)
	_, err = runXlsx(t, "read", "book.xlsx", "--header", "yes")
	require.ErrorContains(t, err, "--header: header must be true, false, a row number")
}
