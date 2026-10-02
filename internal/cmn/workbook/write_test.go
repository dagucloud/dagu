// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func orders() Table {
	return Table{
		Columns: []string{"Invoice No", "Amount", "Due", "Paid", "Note"},
		Rows: [][]any{
			{"INV-1", int64(10), "2026-10-01", true, "00123"},
			{"INV-2", 20.5, "2026-10-02T14:30:00", false, nil},
		},
	}
}

func TestWriteNewWorkbookTableStyle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.xlsx")
	result, err := Write(context.Background(), path, orders(), WriteOptions{Sheet: "Orders", Header: true})
	require.NoError(t, err)
	assert.Equal(t, "Orders", result.Sheet)
	assert.Equal(t, Changes{Sheet: "Orders", Range: "Orders!A1:E3", RowsAppended: 2, CellsChanged: 14}, result.Changes)
	assert.False(t, result.DryRun)

	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Invoice No", "Amount", "Due", "Paid", "Note"}, back.Headers)
	require.Equal(t, 2, back.Count)
	assert.Equal(t, int64(10), back.Rows[0]["Amount"])
	assert.Equal(t, 20.5, back.Rows[1]["Amount"])
	// A column mixing dates and datetimes is formatted as datetime.
	assert.Equal(t, "2026-10-01T00:00:00", back.Rows[0]["Due"])
	assert.Equal(t, "2026-10-02T14:30:00", back.Rows[1]["Due"])
	assert.Equal(t, true, back.Rows[0]["Paid"])
	assert.Equal(t, "00123", back.Rows[0]["Note"])
	assert.Nil(t, back.Rows[1]["Note"])

	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	headerStyle, err := f.GetCellStyle("Orders", "A1")
	require.NoError(t, err)
	style, err := f.GetStyle(headerStyle)
	require.NoError(t, err)
	assert.True(t, style.Font.Bold)
	assert.Equal(t, []string{headerFill}, style.Fill.Color)
	width, err := f.GetColWidth("Orders", "A")
	require.NoError(t, err)
	assert.Equal(t, 12.0, width)
	amountStyle, err := f.GetCellStyle("Orders", "B2")
	require.NoError(t, err)
	amount, err := f.GetStyle(amountStyle)
	require.NoError(t, err)
	require.NotNil(t, amount.CustomNumFmt)
	assert.Equal(t, fmtNumber, *amount.CustomNumFmt)
	dueStyle, err := f.GetCellStyle("Orders", "C2")
	require.NoError(t, err)
	due, err := f.GetStyle(dueStyle)
	require.NoError(t, err)
	require.NotNil(t, due.CustomNumFmt)
	assert.Equal(t, fmtDateTime, *due.CustomNumFmt)
	formatted, err := f.GetCellValue("Orders", "C2")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01 00:00:00", formatted)

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file left behind")
}

func TestWriteStyleNoneAndColumnWidthClamp(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "plain.xlsx")
	table := Table{Columns: []string{"長い見出しの列", "x"}, Rows: [][]any{{strings.Repeat("y", 200), 1}}}
	_, err := Write(context.Background(), path, table, WriteOptions{Header: true, Style: StyleNone})
	require.NoError(t, err)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	id, err := f.GetCellStyle("Sheet1", "A1")
	require.NoError(t, err)
	assert.Equal(t, 0, id)

	styled := filepath.Join(t.TempDir(), "styled.xlsx")
	_, err = Write(context.Background(), styled, table, WriteOptions{Header: true})
	require.NoError(t, err)
	g, err := excelize.OpenFile(styled)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	width, err := g.GetColWidth("Sheet1", "A")
	require.NoError(t, err)
	assert.Equal(t, maxColWidth, width)
	narrow, err := g.GetColWidth("Sheet1", "B")
	require.NoError(t, err)
	assert.Equal(t, minColWidth, narrow)
	assert.Equal(t, 14, displayWidth("長い見出しの列"))
}

func TestWriteReplacePreservesOtherSheets(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Data"))
	setRow(t, f, "Data", "A1", "old", "data")
	setRow(t, f, "Data", "A2", 1, 2)
	_, err := f.NewSheet("Keep")
	require.NoError(t, err)
	setRow(t, f, "Keep", "A1", "kept")
	require.NoError(t, f.SetColWidth("Keep", "A", "A", 33))
	_, err = f.NewSheet("Last")
	require.NoError(t, err)
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "KeptName", RefersTo: "Keep!$A$1"}))
	path := saveBook(t, f, "multi.xlsx")

	result, err := Write(context.Background(), path, Table{Columns: []string{"new"}, Rows: [][]any{{"v"}}}, WriteOptions{Sheet: "Data", Header: true})
	require.NoError(t, err)
	assert.Equal(t, "Data", result.Sheet)

	sheets, err := ListSheets(path, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Data", "Keep", "Last"}, sheets, "replaced sheet keeps its position")
	back, err := Read(context.Background(), path, ReadOptions{Sheet: "Data"})
	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, back.Headers)
	assert.Equal(t, 1, back.Count)
	kept, err := Read(context.Background(), path, ReadOptions{Sheet: "Keep", Header: HeaderSpec{Mode: HeaderNone}})
	require.NoError(t, err)
	assert.Equal(t, "kept", kept.Rows[0]["A"])

	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	width, err := g.GetColWidth("Keep", "A")
	require.NoError(t, err)
	assert.Equal(t, 33.0, width)
	names := g.GetDefinedName()
	require.Len(t, names, 1)
	assert.Equal(t, "KeptName", names[0].Name)
}

func TestWriteCreatesMissingSheet(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "new-sheet.xlsx")
	_, err := Write(context.Background(), path, Table{Columns: []string{"a"}, Rows: [][]any{{1}}}, WriteOptions{Sheet: "Report", Header: true})
	require.NoError(t, err)
	sheets, err := ListSheets(path, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Sheet1", "Report"}, sheets)
}

func TestAppendCopiesStyles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "append.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)

	more := Table{Columns: orders().Columns, Rows: [][]any{{"INV-3", int64(30), "2026-10-03", true, "x"}}}
	result, err := Append(context.Background(), path, more, WriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, Changes{Sheet: "Sheet1", Range: "Sheet1!A4:E4", RowsAppended: 1, CellsChanged: 5}, result.Changes)

	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 3, back.Count)
	assert.Equal(t, "2026-10-03T00:00:00", back.Rows[2]["Due"], "the appended cell keeps the column's datetime format")

	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	above, err := f.GetCellStyle("Sheet1", "B3")
	require.NoError(t, err)
	below, err := f.GetCellStyle("Sheet1", "B4")
	require.NoError(t, err)
	assert.Equal(t, above, below)
	header, err := f.GetCellStyle("Sheet1", "A1")
	require.NoError(t, err)
	assert.NotEqual(t, header, below)
}

func TestAppendToEmptySheetAndWriteModeAppend(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "empty-append.xlsx")
	result, err := Append(context.Background(), path, Table{Columns: []string{"a", "b"}, Rows: [][]any{{1, 2}}}, WriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!A1:B2", result.Changes.Range, "an append that starts an empty sheet writes the header")

	result, err = Write(context.Background(), path, Table{Columns: []string{"a", "b"}, Rows: [][]any{{3, 4}}}, WriteOptions{Mode: WriteAppend, Header: true})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!A3:B3", result.Changes.Range, "an append below rows writes no header")
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, back.Headers)
	assert.Equal(t, 2, back.Count)
	assert.Equal(t, int64(3), back.Rows[1]["a"])
}

func TestWriteDryRunAndInPlace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dry.xlsx")
	result, err := Write(context.Background(), path, orders(), WriteOptions{Header: true, DryRun: true})
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, 2, result.Changes.RowsAppended)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, os.ErrNotExist)

	_, err = Write(context.Background(), path, orders(), WriteOptions{Header: true, InPlace: true})
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestWriteTypesConvertStrings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "typed.xlsx")
	table := Table{Columns: []string{"amount", "when", "code", "flag"}, Rows: [][]any{{"1,234.5", "2026/10/01", "2026-10-01", "yes"}}}
	types := map[string]ColumnType{"amount": TypeNumber, "when": TypeDate, "code": TypeString, "flag": TypeBoolean}
	_, err := Write(context.Background(), path, table, WriteOptions{Header: true, Types: types})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1234.5, back.Rows[0]["amount"])
	assert.Equal(t, "2026-10-01", back.Rows[0]["when"])
	assert.Equal(t, "2026-10-01", back.Rows[0]["code"])
	assert.Equal(t, true, back.Rows[0]["flag"])

	bad := Table{Columns: []string{"amount"}, Rows: [][]any{{"N/A"}}}
	_, err = Write(context.Background(), filepath.Join(t.TempDir(), "bad.xlsx"), bad, WriteOptions{Header: true, Types: map[string]ColumnType{"amount": TypeNumber}})
	require.EqualError(t, err, `bad.xlsx Sheet1!A2: expected number, found "N/A"`)
}

func TestWriteUnsupportedAndLocked(t *testing.T) {
	t.Parallel()
	_, err := Write(context.Background(), filepath.Join(t.TempDir(), "x.xls"), orders(), WriteOptions{})
	require.ErrorIs(t, err, ErrUnsupportedFormat)

	path := filepath.Join(t.TempDir(), "held.xlsx")
	require.NoError(t, os.WriteFile(lockFilePath(path), []byte("x"), 0o600))
	result, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err, "a lock file nobody holds does not block the write")
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], leftoverLockWording)
}

func TestDecodeRowsAndLoadTable(t *testing.T) {
	t.Parallel()
	table, err := DecodeRows(`[{"b": 1, "a": "x", "_row": 2}, {"a": "y", "c": true}]`, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a", "c"}, table.Columns, "JSON key order is kept and _row dropped")
	assert.Equal(t, [][]any{{float64(1), "x", nil}, {nil, "y", true}}, table.Rows)

	table, err = DecodeRows([]any{map[string]any{"z": 1, "a": 2}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "z"}, table.Columns, "decoded maps sort their keys")

	table, err = DecodeRows([]any{map[string]any{"z": 1, "a": 2}}, "z, a")
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns)
	assert.Equal(t, [][]any{{int64(1), int64(2)}}, table.Rows, "Go ints are normalized to int64")

	// YAML decodes positive integers as uint64 and keeps float32 elsewhere.
	table, err = DecodeRows([]any{map[string]any{"n": uint64(10), "f": float32(1.5)}, []any{}}, nil)
	require.Error(t, err, "mixed row shapes are rejected")
	table, err = DecodeRows([]any{map[string]any{"n": uint64(10), "f": float32(1.5)}}, nil)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{1.5, int64(10)}}, table.Rows)

	table, err = DecodeRows([]any{[]any{1, 2, 3}, []any{4}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B", "C"}, table.Columns)
	assert.Equal(t, [][]any{{int64(1), int64(2), int64(3)}, {int64(4), nil, nil}}, table.Rows)

	_, err = DecodeRows("not json", nil)
	require.Error(t, err)
	_, err = DecodeRows(`[1, 2]`, nil)
	require.ErrorContains(t, err, "rows[0] must be an object or an array")

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "in.csv")
	require.NoError(t, os.WriteFile(csvPath, []byte("\xEF\xBB\xBFid,name\n1,\"a, b\"\n2,c\n"), 0o600))
	table, err = LoadTable(csvPath, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, table.Columns)
	assert.Equal(t, [][]any{{"1", "a, b"}, {"2", "c"}}, table.Rows)

	jsonlPath := filepath.Join(dir, "in.jsonl")
	require.NoError(t, os.WriteFile(jsonlPath, []byte("{\"id\": 1}\n\n{\"id\": 2}\n"), 0o600))
	table, err = LoadTable(jsonlPath, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, table.Columns)
	assert.Len(t, table.Rows, 2)

	jsonPath := filepath.Join(dir, "in.txt")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`[{"id": 1}]`), 0o600))
	table, err = LoadTable(jsonPath, "json", nil)
	require.NoError(t, err)
	assert.Len(t, table.Rows, 1)
	_, err = LoadTable(jsonPath, "", nil)
	require.ErrorContains(t, err, `input format "txt" is not json, jsonl, or csv`)

	rows := []Row{{"a": 1, "b": 2, RowNumberKey: 5}}
	assert.Equal(t, Table{Columns: []string{"b", "a"}, Rows: [][]any{{2, 1}}}, RowsToTable(rows, []string{"a", "b"}, []string{"b", "a"}))
}

func TestWriteRoundTripKeepsDates(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src.xlsx")
	_, err := Write(context.Background(), src, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
	read, err := Read(context.Background(), src, ReadOptions{})
	require.NoError(t, err)

	dst := filepath.Join(t.TempDir(), "dst.xlsx")
	_, err = Write(context.Background(), dst, RowsToTable(read.Rows, read.Headers, nil), WriteOptions{Header: true})
	require.NoError(t, err)
	f, err := excelize.OpenFile(dst)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	kind, err := f.GetCellType("Sheet1", "C2")
	require.NoError(t, err)
	assert.NotEqual(t, excelize.CellTypeSharedString, kind, "an ISO date string is written as a date serial, not text")
	assert.NotEqual(t, excelize.CellTypeInlineString, kind)
	value, err := f.GetCellValue("Sheet1", "C2", excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	serial, err := excelize.ExcelDateToTime(mustFloat(t, value), false)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), serial)
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, ok := toFloat(s)
	require.True(t, ok, s)
	return f
}

func TestPinnedDateDropsTimeOfDay(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "midnight.xlsx")
	table := Table{Columns: []string{"when"}, Rows: [][]any{{"2026-10-01T14:30:00"}}}
	_, err := Write(context.Background(), path, table, WriteOptions{Header: true, Types: map[string]ColumnType{"when": TypeDate}})
	require.NoError(t, err)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	raw, err := f.GetCellValue("Sheet1", "A2", excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	serial, err := excelize.ExcelDateToTime(mustFloat(t, raw), false)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), serial)
}

func TestReplaceKeepsSheetScopedNamesAndReferences(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Data"))
	setRow(t, f, "Data", "A1", "v")
	setRow(t, f, "Data", "A2", 10)
	require.NoError(t, f.MergeCell("Data", "A3", "B3"))
	require.NoError(t, f.AddTable("Data", &excelize.Table{Range: "A1:A2", Name: "DataTable"}))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Local", RefersTo: "Data!$A$2", Scope: "Data"}))
	_, err := f.NewSheet("Summary")
	require.NoError(t, err)
	require.NoError(t, f.SetCellFormula("Summary", "A1", "SUM(Data!A:A)"))
	path := saveBook(t, f, "refs.xlsx")

	_, err = Write(context.Background(), path, Table{Columns: []string{"v"}, Rows: [][]any{{20}, {22}}}, WriteOptions{Sheet: "Data", Header: true})
	require.NoError(t, err)

	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	assert.Equal(t, []string{"Data", "Summary"}, g.GetSheetList())
	names := g.GetDefinedName()
	require.Len(t, names, 1)
	assert.Equal(t, "Local", names[0].Name)
	assert.Equal(t, "Data", names[0].Scope)
	assert.Equal(t, "Data!$A$2", names[0].RefersTo, "the name still points where it did")
	formula, err := g.GetCellFormula("Summary", "A1")
	require.NoError(t, err)
	assert.Equal(t, "SUM(Data!A:A)", formula)
	total, err := g.CalcCellValue("Summary", "A1")
	require.NoError(t, err)
	assert.Equal(t, "42", total, "the formula sees the replaced data")
	merges, err := g.GetMergeCells("Data")
	require.NoError(t, err)
	assert.Empty(t, merges)
	tables, err := g.GetTables("Data")
	require.NoError(t, err)
	assert.Empty(t, tables)
}

func TestAppendedDateKeepsTheCellStyleAbove(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "when")
	bordered, err := f.NewStyle(&excelize.Style{Border: []excelize.Border{{Type: "left", Color: "000000", Style: 1}}})
	require.NoError(t, err)
	require.NoError(t, f.SetCellStr("Sheet1", "A2", "plain text"))
	require.NoError(t, f.SetCellStyle("Sheet1", "A2", "A2", bordered))
	path := saveBook(t, f, "border.xlsx")

	_, err = Append(context.Background(), path, Table{Columns: []string{"when"}, Rows: [][]any{{"2026-10-01"}}}, WriteOptions{})
	require.NoError(t, err)
	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	id, err := g.GetCellStyle("Sheet1", "A3")
	require.NoError(t, err)
	style, err := g.GetStyle(id)
	require.NoError(t, err)
	require.Len(t, style.Border, 1, "the border of the cell above survives")
	require.NotNil(t, style.CustomNumFmt)
	assert.Equal(t, fmtDate, *style.CustomNumFmt)
}

func TestSaveUsesShortTempName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, strings.Repeat("n", 230)+".xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err, "a name near the component limit still saves")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestSaveKeepsPermissionBits(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("permission bits are not a Windows concept")
	}
	path := filepath.Join(t.TempDir(), "private.xlsx")
	_, err := Write(context.Background(), path, orders(), WriteOptions{Header: true})
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o600))

	_, err = Append(context.Background(), path, Table{Columns: orders().Columns, Rows: [][]any{{"INV-3", 1, nil, nil, nil}}}, WriteOptions{})
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the rewritten workbook keeps its restrictive mode")
}

func TestClearedSheetDropsOldStyles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "restyle.xlsx")
	dates := Table{Columns: []string{"when"}, Rows: [][]any{{"2026-10-01"}, {"2026-10-02"}}}
	_, err := Write(context.Background(), path, dates, WriteOptions{Header: true})
	require.NoError(t, err)

	numbers := Table{Columns: []string{"n"}, Rows: [][]any{{int64(7)}}}
	_, err = Write(context.Background(), path, numbers, WriteOptions{Header: true, Style: StyleNone})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(7), back.Rows[0]["n"], "a number written where a date column was is read as a number")
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	id, err := f.GetCellStyle("Sheet1", "A3")
	require.NoError(t, err)
	assert.Equal(t, 0, id, "cells outside the new block lose their old style too")
}

func TestClearedSheetDropsStyledEmptyCellsAndHyperlinks(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	require.NoError(t, f.SetSheetRow("Sheet1", "A1", &[]any{"id"}))
	require.NoError(t, f.SetSheetRow("Sheet1", "A2", &[]any{"one"}))
	require.NoError(t, f.SetCellHyperLink("Sheet1", "A2", "https://example.com/one", "External"))
	// B1 and C3 carry a style and no value, as Excel writes a formatted but
	// empty cell, and the dimension Excel keeps covers them.
	require.NoError(t, f.SetCellStyle("Sheet1", "B1", "B1", bold))
	require.NoError(t, f.SetCellStyle("Sheet1", "C3", "C3", bold))
	require.NoError(t, f.SetSheetDimension("Sheet1", "A1:C3"))
	path := saveBook(t, f, "linked.xlsx")

	numbers := Table{Columns: []string{"id", "n"}, Rows: [][]any{{"two", int64(2)}}}
	_, err := Write(context.Background(), path, numbers, WriteOptions{Header: true, Style: StyleNone})
	require.NoError(t, err)

	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	for _, cell := range []string{"B1", "C3"} {
		id, err := g.GetCellStyle("Sheet1", cell)
		require.NoError(t, err)
		assert.Equal(t, 0, id, "%s: a styled empty cell loses its style", cell)
	}
	linked, _, err := g.GetCellHyperLink("Sheet1", "A2")
	require.NoError(t, err)
	assert.False(t, linked, "the old hyperlink does not attach to the new text")
	value, err := g.GetCellValue("Sheet1", "A2")
	require.NoError(t, err)
	assert.Equal(t, "two", value)
}

func TestSingleCellDimensionIsClearedOnReplace(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	// The only thing on the sheet is a styled empty cell, so Excel stores
	// the single-cell dimension C3.
	require.NoError(t, f.SetCellStyle("Sheet1", "C3", "C3", bold))
	require.NoError(t, f.SetSheetDimension("Sheet1", "C3"))
	path := saveBook(t, f, "lone.xlsx")

	w, err := open(path, "")
	require.NoError(t, err)
	dim, ok := w.storedDimension("Sheet1")
	w.close()
	require.True(t, ok)
	assert.Equal(t, "Sheet1!A1:C3", dim.String())

	_, err = Write(context.Background(), path, Table{Columns: []string{"id"}, Rows: [][]any{{int64(1)}}}, WriteOptions{Header: true, Style: StyleNone})
	require.NoError(t, err)
	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	id, err := g.GetCellStyle("Sheet1", "C3")
	require.NoError(t, err)
	assert.Equal(t, 0, id, "the lone styled cell is cleared along with the sheet")
}

func TestReplaceClearsAnA1OnlySheet(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	// A1 holds a style and a hyperlink but no value, and the dimension is
	// A1, so the sheet counts as empty for an append yet must still be
	// cleared by a replace.
	require.NoError(t, f.SetCellStyle("Sheet1", "A1", "A1", bold))
	require.NoError(t, f.SetCellHyperLink("Sheet1", "A1", "https://example.com/old", "External"))
	require.NoError(t, f.SetSheetDimension("Sheet1", "A1"))
	path := saveBook(t, f, "a1only.xlsx")

	_, err := Write(context.Background(), path, Table{Columns: []string{"id"}, Rows: [][]any{{int64(1)}}}, WriteOptions{Header: true, Style: StyleNone})
	require.NoError(t, err)
	g, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	id, err := g.GetCellStyle("Sheet1", "A1")
	require.NoError(t, err)
	assert.Equal(t, 0, id, "the old style does not survive the replace")
	linked, _, err := g.GetCellHyperLink("Sheet1", "A1")
	require.NoError(t, err)
	assert.False(t, linked, "the old hyperlink does not attach to the new header")
}
