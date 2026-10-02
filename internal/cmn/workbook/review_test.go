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

func TestShortDefinedNameWinsOverBareColumn(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "k", "v")
	setRow(t, f, "Sheet1", "A2", "a", 1)
	setRow(t, f, "Sheet1", "A3", "b", 2)
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Tax", RefersTo: "Sheet1!$A$1:$B$2"}))
	path := saveBook(t, f, "names.xlsx")

	named, err := Read(context.Background(), path, ReadOptions{Range: "Tax"})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!A1:B2", named.Range, "a short name is a defined name, not column TAX")
	assert.Equal(t, 1, named.Count)

	column, err := Read(context.Background(), path, ReadOptions{Range: "B"})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!B1:B3", column.Range, "a letter with no matching name is still a column")
}

func TestRangeRejectsRowZero(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "zero.xlsx")
	for _, ref := range []string{"A0", "A1:B0", "A0:B2"} {
		_, err := Read(context.Background(), path, ReadOptions{Range: ref})
		require.ErrorContains(t, err, "is not a row number", ref)
	}
}

func TestDuplicateHeadersGetUnusedSuffixes(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "Name", "Name", "Name_2", RowNumberKey)
	setRow(t, f, "Sheet1", "A2", "a", "b", "c", "d")
	path := saveBook(t, f, "dup.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Name", "Name_2", "Name_2_2", "_row_2"}, result.Headers)
	assert.Equal(t, "a", result.Rows[0]["Name"])
	assert.Equal(t, "b", result.Rows[0]["Name_2"])
	assert.Equal(t, "c", result.Rows[0]["Name_2_2"])
	assert.Equal(t, "d", result.Rows[0]["_row_2"], "a header spelled _row does not shadow the row number")
	assert.Equal(t, 2, result.Rows[0][RowNumberKey])
}

func TestHeaderRowMustBeInsideTheRange(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "h")
	setRow(t, f, "Sheet1", "A2", 1)
	path := saveBook(t, f, "range.xlsx")
	_, err := Read(context.Background(), path, ReadOptions{Range: "A1:A2", Header: HeaderSpec{Mode: HeaderRows, Rows: []int{3}}})
	require.ErrorContains(t, err, "header row 3 is outside Sheet1!A1:A2")
}

func TestDuplicateColumnAliasesAreRejected(t *testing.T) {
	t.Parallel()
	_, err := ParseColumns([]any{"a: x", "b: x"})
	require.ErrorContains(t, err, `duplicate output name "x"`)
}

func TestFormulasCalculateRecomputesCachedCells(t *testing.T) {
	t.Parallel()
	// The cache says 5 but the formula is 1+2.
	path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "f")+`</row><row r="2"><c r="A2"><f>1+2</f><v>5</v></c></row></sheetData>`)
	cached, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(5), cached.Rows[0]["f"])
	fresh, err := Read(context.Background(), path, ReadOptions{Formulas: FormulaCalculate})
	require.NoError(t, err)
	assert.Equal(t, int64(3), fresh.Rows[0]["f"])
}

func TestCoerceRejectsNonFiniteAndOutOfRange(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"NaN", "Inf", "-Inf"} {
		_, err := coerce(bad, TypeNumber, false)
		require.Error(t, err, bad)
	}
	_, err := coerce("1e30", TypeInteger, false)
	require.ErrorContains(t, err, "expected integer")
	v, err := coerce("12", TypeInteger, false)
	require.NoError(t, err)
	assert.Equal(t, int64(12), v)
}

func TestPinnedDateHonorsThe1904Epoch(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	yes := true
	require.NoError(t, f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &yes}))
	setRow(t, f, "Sheet1", "A1", "serial")
	setRow(t, f, "Sheet1", "A2", 1)
	path := saveBook(t, f, "serial1904.xlsx")
	result, err := Read(context.Background(), path, ReadOptions{Types: map[string]ColumnType{"serial": TypeDate}})
	require.NoError(t, err)
	assert.Equal(t, "1904-01-02", result.Rows[0]["serial"])
}

func TestElapsedTimeFormat46StaysNumeric(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "elapsed")
	setStyled(t, f, "Sheet1", "A2", 1.5, &excelize.Style{NumFmt: 46})
	path := saveBook(t, f, "elapsed.xlsx")
	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1.5, result.Rows[0]["elapsed"], "36 hours is a day and a half, not 12:00:00")
}

func TestLeadingBlankRowsDoNotConsumeMaxRows(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "n")
	setRow(t, f, "Sheet1", "A4", 1)
	setRow(t, f, "Sheet1", "A5", 2)
	path := saveBook(t, f, "gaps.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{Range: "A1:A5", MaxRows: 2})
	require.NoError(t, err)
	assert.Equal(t, 4, result.Count, "the two blank rows before the data are kept as null rows and do not count")
	assert.Nil(t, result.Rows[0]["n"])
	assert.Equal(t, int64(2), result.Rows[3]["n"])
	assert.False(t, result.Truncated)

	kept, err := Read(context.Background(), path, ReadOptions{Range: "A1:A5", MaxRows: 2, KeepEmptyRows: true})
	require.NoError(t, err)
	assert.Equal(t, 2, kept.Count, "with keep_empty_rows the blanks are rows like any other")
	assert.True(t, kept.Truncated)
}

func TestStopAtBlankUsesResolvedValues(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "Customer", "Order")
	setRow(t, f, s, "A2", "ACME", 1)
	setRow(t, f, s, "A3", nil, 2)
	require.NoError(t, f.MergeCell(s, "A2", "A3"))
	setRow(t, f, s, "A5", "Other", 3)
	path := saveBook(t, f, "merged-stop.xlsx")
	result, err := Read(context.Background(), path, ReadOptions{StopAtBlank: true})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Count, "row 3 is filled by the merge and row 4 is the first blank")
}

func TestDecodeJSONRejectsTrailingText(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"[]]", "{} }", "[1] [2]"} {
		_, err := decodeJSON(bad)
		require.Error(t, err, bad)
	}
}

func TestWriterColumnsKeepColonsAndDropRowNumber(t *testing.T) {
	t.Parallel()
	table, err := DecodeRows(`[{"Time: start": "09:00", "_row": 2, "b": 1}]`, `["Time: start", "_row", "b"]`)
	require.NoError(t, err)
	assert.Equal(t, []string{"Time: start", "b"}, table.Columns)
	assert.Equal(t, [][]any{{"09:00", float64(1)}}, table.Rows)

	table, err = DecodeRows([]any{map[string]any{"Time: start": "x"}}, []any{"Time: start"})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"x"}}, table.Rows)
}

func TestLoadTableJSONLKeepsOrderAndReportsLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "in.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"z\": 1, \"a\": 2}\n\n{\"z\": 3, \"a\": 4}\n"), 0o600))
	table, err := LoadTable(path, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns, "JSONL keys keep their text order")
	assert.Len(t, table.Rows, 2)

	bad := filepath.Join(dir, "bad.jsonl")
	require.NoError(t, os.WriteFile(bad, []byte("{\"a\": 1}\n\n\nnot json\n"), 0o600))
	_, err = LoadTable(bad, "", nil)
	require.ErrorContains(t, err, "line 4 is not valid JSON")

	long := filepath.Join(dir, "long.jsonl")
	require.NoError(t, os.WriteFile(long, []byte("{\"a\": \""+strings.Repeat("x", 65<<20)+"\"}\n"), 0o600))
	_, err = LoadTable(long, "", nil)
	require.ErrorContains(t, err, "token too long")
}

func TestLoadTableCSVRejectsDuplicateHeaders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "dup.csv")
	require.NoError(t, os.WriteFile(path, []byte("id,name,id\n1,a,2\n"), 0o600))
	_, err := LoadTable(path, "", nil)
	require.ErrorContains(t, err, `duplicate header "id" in columns 1 and 3`)
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

func TestUpdateRowsAbsentFieldLeavesCellAndNullClears(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	rows := []Row{
		{"Invoice No": "INV-2", "Amount": nil},             // explicit null clears
		{"Invoice No": "INV-3", "Status": "Checked"},       // Amount absent, left alone
		{"Invoice No": "INV-1", "Amount": "10", "Due": ""}, // "10" differs from 10 by type
	}
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: rows})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Nil(t, back.Rows[1]["Amount"])
	assert.Equal(t, int64(30), back.Rows[2]["Amount"])
	assert.Equal(t, "Checked", back.Rows[2]["Status"])
	assert.Equal(t, "10", back.Rows[0]["Amount"], "a text 10 replaces the number 10")
	assert.Nil(t, back.Rows[0]["Due"], "an empty string clears like null")
	assert.Equal(t, 3, result.Changes.RowsUpdated)
}

func TestUpdateRowsRejectsTwoInputsForOneRow(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	rows := []Row{{"Invoice No": "INV-1", "Status": "a"}, {"Invoice No": "INV-1", "Status": "b"}}
	_, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: rows})
	require.ErrorContains(t, err, "rows[0] and rows[1] both address row 2")
}

func TestForeachAggregateIsUnwrapped(t *testing.T) {
	t.Parallel()
	aggregate := `{"summary": {"total": 2, "succeeded": 1, "failed": 1}, "items": [{"index": 0, "key": "a", "status": "succeeded"}], "outputs": [{"order_id": "a", "status": 200}]}`
	rows, err := DecodeUpdateRows(aggregate)
	require.NoError(t, err)
	assert.Equal(t, []Row{{"order_id": "a", "status": float64(200)}}, rows)
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"order_id", "status"}, table.Columns)
}

func TestFitRowsKeepsTheLongestFittingPrefix(t *testing.T) {
	t.Parallel()
	rows := []Row{{"a": "x"}, {"a": "y"}, {"a": strings.Repeat("z", 10_000)}}
	kept, truncated := FitRows(rows, encodedSize(rows[:2])+1)
	assert.True(t, truncated)
	assert.Len(t, kept, 2, "two small rows fit even though the third is huge")
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

func TestForeachAggregateKeepsKeyOrder(t *testing.T) {
	t.Parallel()
	aggregate := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [{"index": 0, "key": "a", "status": "succeeded"}], "outputs": [{"zeta": 1, "alpha": 2}]}`
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"zeta", "alpha"}, table.Columns, "the order of the collected object is kept")

	// An ordinary row that happens to have these field names is not an
	// aggregate: its summary is not the foreach shape.
	plain := `{"summary": "quarterly", "items": 3, "outputs": [1, 2]}`
	rows, err := DecodeUpdateRows(plain)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "quarterly", rows[0]["summary"])

	// A row with the exact foreach fields plus one of its own is a row too:
	// the aggregate has nothing but those three fields.
	extra := `{"order_id": 42, "summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "outputs": []}`
	table, err = DecodeRows(extra, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.ElementsMatch(t, []string{"order_id", "summary", "items", "outputs"}, table.Columns)

	// A field differing only in case is another field, not the outputs.
	cased := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "outputs": [{"a": 1}], "Outputs": [{"b": 2}]}`
	table, err = DecodeRows(cased, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1, "four fields make it a row, not an aggregate")
	assert.Contains(t, table.Columns, "Outputs")
}

func TestStaleDimensionDoesNotWidenTheUsedRange(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetRow("Sheet1", "A1", &[]any{"id", "name"}))
	require.NoError(t, f.SetSheetRow("Sheet1", "A2", &[]any{1, "a"}))
	// A formula far below the text has no cached value but is a cell all
	// the same, so the used range reaches it whatever the dimension says.
	require.NoError(t, f.SetCellFormula("Sheet1", "A400", "A2*2"))
	require.NoError(t, f.SetSheetDimension("Sheet1", "A1:A1"))
	near := saveBook(t, f, "near.xlsx")
	w, err := open(near, "")
	require.NoError(t, err)
	used, err := w.usedRange("Sheet1")
	require.NoError(t, err)
	w.close()
	assert.Equal(t, "Sheet1!A1:B400", used.String())

	// A dimension claiming the whole sheet is stale and ignored, so a read
	// stays proportional to the data.
	g := excelize.NewFile()
	require.NoError(t, g.SetSheetRow("Sheet1", "A1", &[]any{"id", "name"}))
	require.NoError(t, g.SetSheetRow("Sheet1", "A2", &[]any{1, "a"}))
	require.NoError(t, g.SetSheetDimension("Sheet1", "A1:XFD1048576"))
	stale := saveBook(t, g, "stale.xlsx")
	w, err = open(stale, "")
	require.NoError(t, err)
	used, err = w.usedRange("Sheet1")
	require.NoError(t, err)
	w.close()
	assert.Equal(t, "Sheet1!A1:B2", used.String())
	result, err := Read(context.Background(), stale, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
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

func TestForeachAggregateWithEscapedKeyKeepsOrder(t *testing.T) {
	t.Parallel()
	// JSON may spell a key with escapes; the decoded name is what counts.
	aggregate := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "outputs": [{"z": 1, "a": 2}]}`
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns)
}
