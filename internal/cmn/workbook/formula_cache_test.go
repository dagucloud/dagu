// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func cachedTotalsBook(t *testing.T) string {
	t.Helper()
	return rawBook(t, `<sheetData><row r="1">`+inline("A1", "amount")+inline("B1", "total")+inline("C1", "id")+
		`</row><row r="2"><c r="A2"><v>2</v></c><c r="B2"><f>SUM(A2:A4)</f><v>5</v></c>`+inline("C2", "one")+
		`</row><row r="3"><c r="A3"><v>3</v></c>`+inline("C3", "two")+`</row></sheetData>`)
}

func workbookPart(t *testing.T, path, part string) string {
	t.Helper()
	f, err := zip.OpenReader(path)
	require.NoError(t, err)
	// Closed before returning: Windows refuses to replace a file that is
	// still open, and tests write the workbook again after reading a part.
	defer func() { require.NoError(t, f.Close()) }()
	r, err := f.Open(part)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(data)
}

func TestWritersInvalidateFormulaCaches(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(string) (*WriteResult, error){
		"write_cells": func(path string) (*WriteResult, error) {
			return WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
		},
		"update_rows": func(path string) (*WriteResult, error) {
			return UpdateRows(context.Background(), path, UpdateOptions{Key: "id", Rows: []Row{{"id": "one", "amount": 20}}})
		},
		"append": func(path string) (*WriteResult, error) {
			return Append(context.Background(), path, Table{Columns: []string{"amount"}, Rows: [][]any{{18}}}, WriteOptions{Header: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := cachedTotalsBook(t)
			before, err := Read(context.Background(), path, ReadOptions{})
			require.NoError(t, err)
			require.Equal(t, int64(5), before.Rows[0]["total"])
			result, err := edit(path)
			require.NoError(t, err)
			require.Positive(t, result.Changes.CellsChanged)
			after, err := Read(context.Background(), path, ReadOptions{})
			require.NoError(t, err)
			assert.Equal(t, int64(23), after.Rows[0]["total"])
			calculated, err := Read(context.Background(), path, ReadOptions{Formulas: FormulaCalculate})
			require.NoError(t, err)
			assert.Equal(t, int64(23), calculated.Rows[0]["total"])
			xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
			assert.Contains(t, xml, "SUM(A2:A4)")
			assert.NotContains(t, xml, "<v>5</v>")
			t.Logf("cached before=%v, default after=%v, calculated after=%v; sheet=%s", before.Rows[0]["total"], after.Rows[0]["total"], calculated.Rows[0]["total"], xml)
		})
	}
}

// An array or data-table formula owns the cells it fills, which lose their
// caches with it; a data table's inputs are not read from its text, so any
// edit clears it. A shared formula depends on what each of its cells reads.
func TestFormulaCacheGroups(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"array", "dataTable", "shared"} {
		for _, edit := range []string{"A2", "D9"} {
			t.Run(kind+"/"+edit, func(t *testing.T) {
				t.Parallel()
				formula, follower := `<f t="array" ref="B1:B2" ca="1">A1:A2*2</f>`, ""
				switch kind {
				case "shared":
					formula = `<f t="shared" ref="B1:B2" ca="1" si="0">A1*2</f>`
					follower = `<f t="shared" si="0"/>`
				case "dataTable":
					formula = `<f t="dataTable" ref="B1:B2" ca="1" dtr="1" r1="A1"/>`
				}
				path := rawBook(t, `<sheetData><row r="1"><c r="A1"><v>2</v></c><c r="B1">`+formula+`<v>4</v></c></row><row r="2"><c r="A2"><v>3</v></c><c r="B2">`+follower+`<v>6</v></c></row></sheetData>`)
				_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{edit: {Value: 20}}})
				require.NoError(t, err)
				xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
				assert.Contains(t, xml, `t="`+kind+`" ref="B1:B2"`)
				assert.Contains(t, xml, `ca="true"`)
				switch kind {
				case "array":
					assert.Contains(t, xml, "A1:A2*2</f>")
				case "shared":
					assert.Contains(t, xml, `si="0">A1*2</f>`)
					assert.Contains(t, xml, `<f t="shared" si="0"></f>`)
				case "dataTable":
					assert.Contains(t, xml, `dtr="true" r1="A1"`)
				}
				b1, b2 := strings.Contains(xml, "<v>4</v>"), strings.Contains(xml, "<v>6</v>")
				switch {
				case kind == "dataTable" || kind == "array" && edit == "A2":
					assert.False(t, b1 || b2, "the group loses its caches")
				case kind == "shared" && edit == "A2":
					assert.True(t, b1, "B1 reads only A1")
					assert.False(t, b2, "B2 reads A2")
				default:
					assert.True(t, b1 && b2, "an edit nothing reads keeps the caches")
				}
			})
		}
	}
}

func TestFormulaCacheDryRunAndNoOp(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"write_cells", "update_rows", "append"} {
		for _, dryRun := range []bool{false, true} {
			name := method + "/no-op"
			if dryRun {
				name = method + "/dry-run"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				path := cachedTotalsBook(t)
				before := fileHash(t, path)
				output := filepath.Join(filepath.Dir(path), "copy.xlsx")
				value := 2
				if dryRun {
					value = 20
				}
				var result *WriteResult
				var err error
				switch method {
				case "write_cells":
					result, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: value}}, Output: output, DryRun: dryRun})
				case "update_rows":
					result, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "id", Rows: []Row{{"id": "one", "amount": value}}, Output: output, DryRun: dryRun})
				case "append":
					table := Table{Columns: []string{"amount"}}
					if dryRun {
						table.Rows = [][]any{{18}}
					}
					result, err = Append(context.Background(), path, table, WriteOptions{Header: true, Output: output, DryRun: dryRun})
				}
				require.NoError(t, err)
				assert.Equal(t, before, fileHash(t, path))
				if dryRun {
					assert.True(t, result.DryRun)
					_, err := os.Stat(output)
					require.ErrorIs(t, err, os.ErrNotExist)
					return
				}
				assert.Zero(t, result.Changes.CellsChanged)
				assert.FileExists(t, output, "an unchanged output copy is still saved")
				assert.Contains(t, workbookPart(t, output, "xl/worksheets/sheet1.xml"), "<v>5</v>")
				assert.NotContains(t, workbookPart(t, output, "xl/workbook.xml"), "fullCalcOnLoad")
			})
		}
	}
}

func TestFormulaCacheUnsupportedFunction(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "input")+inline("B1", "unknown")+
		`</row><row r="2"><c r="A2"><v>2</v></c><c r="B2"><f>UNSUPPORTED(A2)</f><v>5</v></c></row></sheetData>`)
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Nil(t, result.Rows[0]["unknown"])
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "UNSUPPORTED(A2) could not be evaluated")
}

func TestFormulaCacheNewSheet(t *testing.T) {
	t.Parallel()
	for _, dryRun := range []bool{false, true} {
		name := "saved"
		if dryRun {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "sheets")+
				`</row><row r="2"><c r="A2"><f>SHEETS()</f><v>1</v></c></row></sheetData>`)
			before := fileHash(t, path)
			result, err := Append(context.Background(), path, Table{}, WriteOptions{Sheet: "Added", Style: StyleNone, DryRun: dryRun})
			require.NoError(t, err)
			assert.Zero(t, result.Changes.CellsChanged)
			read, err := Read(context.Background(), path, ReadOptions{Sheet: "Raw"})
			require.NoError(t, err)
			require.Len(t, read.Rows, 1)
			if dryRun {
				assert.Equal(t, before, fileHash(t, path))
				assert.Equal(t, int64(1), read.Rows[0]["sheets"])
				return
			}
			assert.Equal(t, int64(2), read.Rows[0]["sheets"])
			assert.NotContains(t, workbookPart(t, path, "xl/worksheets/sheet1.xml"), "<v>1</v>")
		})
	}
}

func TestFormulaCacheMetadata(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Raw"))
	setRow(t, f, "Raw", "A1", "amount", "total", "id")
	setRow(t, f, "Raw", "A2", 2, 5, "one")
	setRow(t, f, "Raw", "A3", 3, nil, "two")
	require.NoError(t, f.SetCellFormula("Raw", "B2", "SUM(A2:A4)"))
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}, NumFmt: 2})
	require.NoError(t, f.SetCellStyle("Raw", "B2", "B2", bold))
	require.NoError(t, f.AddComment("Raw", excelize.Comment{Cell: "B2", Author: "author", Text: "keep this comment"}))
	require.NoError(t, f.SetCellHyperLink("Raw", "B2", "https://example.com", "External"))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Amounts", RefersTo: "Raw!$A$2:$A$4"}))
	mode, iterate, count, delta, precision := "manual", true, uint(12), 0.125, false
	require.NoError(t, f.SetCalcProps(&excelize.CalcPropsOptions{CalcMode: &mode, Iterate: &iterate, IterateCount: &count, IterateDelta: &delta, FullPrecision: &precision}))
	path := saveBook(t, f, "metadata.xlsx")
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	back, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, back.Close()) }()
	formula, err := back.GetCellFormula("Raw", "B2")
	require.NoError(t, err)
	assert.Equal(t, "SUM(A2:A4)", formula)
	style, err := back.GetCellStyle("Raw", "B2")
	require.NoError(t, err)
	assert.Equal(t, bold, style)
	comments, err := back.GetComments("Raw")
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Equal(t, "keep this comment", comments[0].Text)
	linked, target, err := back.GetCellHyperLink("Raw", "B2")
	require.NoError(t, err)
	assert.True(t, linked)
	assert.Equal(t, "https://example.com", target)
	assert.Equal(t, []excelize.DefinedName{{Name: "Amounts", RefersTo: "Raw!$A$2:$A$4", Scope: "Workbook"}}, back.GetDefinedName())
	props, err := back.GetCalcProps()
	require.NoError(t, err)
	assert.Equal(t, &mode, props.CalcMode)
	assert.Equal(t, &iterate, props.Iterate)
	assert.Equal(t, &count, props.IterateCount)
	assert.Equal(t, &delta, props.IterateDelta)
	assert.Equal(t, &precision, props.FullPrecision)
	require.NotNil(t, props.FullCalcOnLoad)
	assert.True(t, *props.FullCalcOnLoad)
	assert.Nil(t, props.ForceFullCalc, "only a recalculation on open is requested")
}

func TestFormulaCacheReferences(t *testing.T) {
	t.Parallel()
	for _, formula := range []string{"A2+3", "$A$2+3", "SUM(A2:A3)", "SUM(Raw!A2:A3)", `INDIRECT("A2")+3`} {
		t.Run(formula, func(t *testing.T) {
			t.Parallel()
			path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "input")+inline("B1", "result")+
				`</row><row r="2"><c r="A2"><v>2</v></c><c r="B2"><f>`+formula+`</f><v>5</v></c></row><row r="3"><c r="A3"><v>3</v></c></row></sheetData>`)
			_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
			require.NoError(t, err)
			result, err := Read(context.Background(), path, ReadOptions{})
			require.NoError(t, err)
			assert.Equal(t, int64(23), result.Rows[0]["result"])
		})
	}
}

// Formulas that read nothing an edit changed keep their cached values, even
// ones the calculation library cannot evaluate.
func TestFormulaCacheKeepsUnrelatedFormulas(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Raw"))
	setRow(t, f, "Raw", "A1", "input", "constant", "unknown", "other", "dependent")
	setRow(t, f, "Raw", "A2", 2, 21, 7, 33, 100)
	setRow(t, f, "Raw", "A3", 11)
	for cell, formula := range map[string]string{"B2": "20+1", "C2": "UNSUPPORTED(1)", "D2": "A3*3", "E2": "A2*50"} {
		require.NoError(t, f.SetCellFormula("Raw", cell, formula))
	}
	_, err := f.NewSheet("Other")
	require.NoError(t, err)
	setRow(t, f, "Other", "A1", "elsewhere")
	setRow(t, f, "Other", "A2", 55)
	require.NoError(t, f.SetCellFormula("Other", "A2", "UNSUPPORTED(Raw!A3)"))
	path := saveBook(t, f, "unrelated.xlsx")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Sheet: "Raw", Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	raw := workbookPart(t, path, "xl/worksheets/sheet1.xml")
	for _, cached := range []string{"<v>21</v>", "<v>7</v>", "<v>33</v>"} {
		assert.Contains(t, raw, cached)
	}
	assert.NotContains(t, raw, "<v>100</v>")
	assert.Contains(t, workbookPart(t, path, "xl/worksheets/sheet2.xml"), "<v>55</v>")
	result, err := Read(context.Background(), path, ReadOptions{Sheet: "Raw"})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), result.Rows[0]["dependent"])
	assert.Equal(t, []string{"Raw!E2: formula had no cached value; evaluated"}, result.Warnings)
}

// A formula that reads a cleared formula is cleared in turn, whichever way
// the chain runs through the sheet.
func TestFormulaCacheChains(t *testing.T) {
	t.Parallel()
	// Column B sums downward and column C upward; the caches are distinct
	// so the saved XML shows which ones survive.
	path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "input")+inline("B1", "down")+inline("C1", "up")+`</row>`+
		`<row r="2"><c r="A2"><v>1</v></c><c r="B2"><f>A2*100</f><v>100</v></c><c r="C2"><f>C3+A2</f><v>43</v></c></row>`+
		`<row r="3"><c r="A3"><v>2</v></c><c r="B3"><f>B2+A3</f><v>102</v></c><c r="C3"><f>C4+A3</f><v>42</v></c></row>`+
		`<row r="4"><c r="A4"><v>4</v></c><c r="B4"><f>B3+A4</f><v>106</v></c><c r="C4"><f>A4*10</f><v>40</v></c></row></sheetData>`)
	ctx := context.Background()
	column := func(rows []Row, name string) []any {
		values := make([]any, 0, len(rows))
		for _, row := range rows {
			values = append(values, row[name])
		}
		return values
	}

	_, err := WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 5}}})
	require.NoError(t, err)
	xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
	assert.NotContains(t, xml, "<v>106</v>", "the downward chain is cleared to its end")
	assert.NotContains(t, xml, "<v>43</v>")
	assert.Contains(t, xml, "<v>42</v>", "C3 does not read A2")
	assert.Contains(t, xml, "<v>40</v>")
	result, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []any{int64(500), int64(502), int64(506)}, column(result.Rows, "down"))
	assert.Equal(t, []any{int64(47), int64(42), int64(40)}, column(result.Rows, "up"))

	_, err = WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"A4": {Value: 6}}})
	require.NoError(t, err)
	xml = workbookPart(t, path, "xl/worksheets/sheet1.xml")
	assert.NotContains(t, xml, "<v>42</v>", "the upward chain is cleared to its start")
	assert.NotContains(t, xml, "<v>40</v>")
	result, err = Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []any{int64(500), int64(502), int64(508)}, column(result.Rows, "down"))
	assert.Equal(t, []any{int64(67), int64(62), int64(60)}, column(result.Rows, "up"))
}

// A structured reference reads its whole table, whether it names the table
// or means the one that holds the formula.
func TestFormulaCacheTables(t *testing.T) {
	t.Parallel()
	for edit, want := range map[string]struct{ table, summary, other bool }{
		"A2": {table: false, summary: false, other: true},
		"D9": {table: true, summary: true, other: false},
	} {
		t.Run(edit, func(t *testing.T) {
			t.Parallel()
			f := excelize.NewFile()
			require.NoError(t, f.SetSheetName("Sheet1", "Stock"))
			setRow(t, f, "Stock", "A1", "qty", "double")
			setRow(t, f, "Stock", "A2", 2, 4)
			setRow(t, f, "Stock", "A3", 3, 6)
			require.NoError(t, f.AddTable("Stock", &excelize.Table{Range: "A1:B3", Name: "Items"}))
			for _, cell := range []string{"B2", "B3"} {
				require.NoError(t, f.SetCellFormula("Stock", cell, "Items[[#This Row],[qty]]*2"))
			}
			_, err := f.NewSheet("Summary")
			require.NoError(t, err)
			setRow(t, f, "Summary", "A1", "total", "other")
			setRow(t, f, "Summary", "A2", 5, 9)
			require.NoError(t, f.SetCellFormula("Summary", "A2", "SUM(Items[qty])"))
			require.NoError(t, f.SetCellFormula("Summary", "B2", "Stock!D9+9"))
			path := saveBook(t, f, "tables.xlsx")
			_, err = WriteCells(context.Background(), path, WriteCellsOptions{Sheet: "Stock", Cells: map[string]CellValue{edit: {Value: 20}}})
			require.NoError(t, err)
			stock, summary := workbookPart(t, path, "xl/worksheets/sheet1.xml"), workbookPart(t, path, "xl/worksheets/sheet2.xml")
			assert.Equal(t, want.table, strings.Contains(stock, "<v>4</v>") && strings.Contains(stock, "<v>6</v>"), stock)
			assert.Equal(t, want.summary, strings.Contains(summary, "<v>5</v>"), summary)
			assert.Equal(t, want.other, strings.Contains(summary, "<v>9</v>"), summary)
		})
	}
}

// A sheet that is added, renamed, or deleted counts as a change to all of
// its cells, and functions that read the sheet list depend on any change.
func TestFormulaCacheSheetOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts SheetOptions
		// reader is whether Main!B2, which reads Data!A1, keeps its cache.
		reader bool
	}{
		{name: "add", opts: SheetOptions{Operation: SheetAdd, Sheet: "Extra"}, reader: true},
		{name: "rename", opts: SheetOptions{Operation: SheetRename, Sheet: "Data", To: "Input"}},
		{name: "delete", opts: SheetOptions{Operation: SheetDelete, Sheet: "Data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := excelize.NewFile()
			require.NoError(t, f.SetSheetName("Sheet1", "Main"))
			setRow(t, f, "Main", "A1", "sheets", "data")
			setRow(t, f, "Main", "A2", 2, 10)
			require.NoError(t, f.SetCellFormula("Main", "A2", "SHEETS()"))
			require.NoError(t, f.SetCellFormula("Main", "B2", "Data!A1*2"))
			_, err := f.NewSheet("Data")
			require.NoError(t, err)
			setRow(t, f, "Data", "A1", 5)
			path := saveBook(t, f, "sheets.xlsx")
			_, err = Sheet(context.Background(), path, tc.opts)
			require.NoError(t, err)
			xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
			assert.NotContains(t, xml, "<v>2</v>", "SHEETS() reads the sheet list")
			assert.Equal(t, tc.reader, strings.Contains(xml, "<v>10</v>"), xml)
		})
	}
}

func TestFormulaCacheOtherSheet(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Raw"))
	setRow(t, f, "Raw", "A1", "amount", "total")
	setRow(t, f, "Raw", "A2", 2, 5)
	setRow(t, f, "Raw", "A3", 3)
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Amounts", RefersTo: "Raw!$A$2:$A$3"}))
	require.NoError(t, f.SetCellFormula("Raw", "B2", "SUM(Amounts)"))
	_, err := f.NewSheet("Summary")
	require.NoError(t, err)
	setRow(t, f, "Summary", "A1", "doubled")
	setRow(t, f, "Summary", "A2", 10)
	require.NoError(t, f.SetCellFormula("Summary", "A2", "Raw!B2*2"))
	path := saveBook(t, f, "cross-sheet.xlsx")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	result, err := Read(context.Background(), path, ReadOptions{Sheet: "Summary"})
	require.NoError(t, err)
	t.Log(workbookPart(t, path, "xl/worksheets/sheet2.xml"))
	require.Len(t, result.Rows, 1, "%+v", result)
	assert.Equal(t, int64(46), result.Rows[0]["doubled"])
	assert.NotContains(t, workbookPart(t, path, "xl/worksheets/sheet2.xml"), "<v>10</v>")
}

func TestFormulaCacheHeaders(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"read", "check", "validate", "update_rows", "append"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			// Header formulas are saved without a cache, as some generators do.
			path := rawBook(t, `<sheetData><row r="1"><c r="A1" t="str"><f>"amount"</f></c><c r="B1" t="str"><f>"total"</f></c><c r="C1" t="str"><f>"id"</f></c>`+
				`</row><row r="2"><c r="A2"><v>2</v></c><c r="B2"><f>SUM(A2:A4)</f><v>5</v></c>`+inline("C2", "one")+
				`</row><row r="3"><c r="A3"><v>3</v></c>`+inline("C3", "two")+`</row></sheetData>`)
			ctx := context.Background()
			_, err := WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
			require.NoError(t, err)
			switch operation {
			case "read":
				result, err := Read(ctx, path, ReadOptions{})
				require.NoError(t, err)
				assert.Equal(t, []string{"amount", "total", "id"}, result.Headers)
				assert.Equal(t, int64(23), result.Rows[0]["total"])
				assert.Contains(t, result.Warnings, "Raw!A1: formula had no cached value; evaluated")
			case "check":
				err := Check(ctx, path, CheckOptions{Columns: []ColumnCheck{{Name: "id", Exact: true}, {Name: "amount", Exact: true}}})
				require.NoError(t, err)
			case "validate":
				result, err := Validate(ctx, path, ValidateOptions{Required: []string{"amount", "total", "id"}, NotBlank: []string{"amount"}})
				require.NoError(t, err)
				assert.True(t, result.OK, "%+v", result.Problems)
			case "update_rows":
				result, err := UpdateRows(ctx, path, UpdateOptions{Key: "id", Rows: []Row{{"id": "one", "amount": 30}}})
				require.NoError(t, err)
				assert.Equal(t, 1, result.Changes.RowsUpdated)
				assert.Zero(t, result.Changes.ColumnsAdded)
			case "append":
				result, err := Append(ctx, path, Table{Columns: []string{"amount"}, Rows: [][]any{{7}}}, WriteOptions{Header: true})
				require.NoError(t, err)
				assert.Zero(t, result.Changes.ColumnsAdded, "the computed header is matched rather than duplicated")
				read, err := Read(ctx, path, ReadOptions{})
				require.NoError(t, err)
				assert.Equal(t, int64(30), read.Rows[0]["total"])
			}
		})
	}
}

func TestFormulaCacheUnsupportedHeader(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<sheetData><row r="1"><c r="A1" t="str"><f>UNSUPPORTED()</f></c>`+inline("B1", "id")+
		`</row><row r="2"><c r="A2"><v>2</v></c>`+inline("B2", "one")+`</row></sheetData>`)
	ctx := context.Background()
	_, err := WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	result, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "id"}, result.Headers)
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "UNSUPPORTED() could not be evaluated")
	assert.Contains(t, result.Warnings, "Raw!A1: header is empty; column named A")
	require.NoError(t, Check(ctx, path, CheckOptions{Columns: []ColumnCheck{{Name: "A", Exact: true}}}))
	_, err = UpdateRows(ctx, path, UpdateOptions{Key: "id", Rows: []Row{{"id": "one", "A": 30}}})
	require.NoError(t, err)
}

func TestFormulaCacheHeaderKeepsRawText(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", 2, "input")
	setRow(t, f, "Sheet1", "A2", 7, 2)
	require.NoError(t, f.SetCellFormula("Sheet1", "A1", "B2*1"))
	dateStyle := styleID(t, f, &excelize.Style{NumFmt: 14})
	require.NoError(t, f.SetCellStyle("Sheet1", "A1", "A1", dateStyle))
	path := saveBook(t, f, "formula-header.xlsx")
	ctx := context.Background()
	before, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	_, err = WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: 3}}})
	require.NoError(t, err)
	after, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"2", "input"}, before.Headers)
	assert.Equal(t, []string{"3", "input"}, after.Headers, "header names do not convert numeric results through date styles")
}

func TestFormulaCacheXMLPreservation(t *testing.T) {
	t.Parallel()
	for _, namespace := range []string{"http://schemas.openxmlformats.org/spreadsheetml/2006/main", "http://purl.oclc.org/ooxml/spreadsheetml/main"} {
		t.Run(namespace, func(t *testing.T) {
			before := `<s:worksheet xmlns:s="` + namespace + `" xmlns:q="` + namespace + `" xmlns:x="urn:extension"><s:sheetData><s:row r="1">` +
				`<s:c r="A1" t="str" s="4" cm="2" vm="3" x:note="literal t='kept'"><q:f t="array" ref="A1:A2" ca="1">B1:B2</q:f><q:v>old</q:v><x:v>keep</x:v></s:c>` +
				`<s:c r="B1" t="n"><x:f>not a formula</x:f><q:v>2</q:v></s:c></s:row><s:row r="2"><s:c r="A2" t="b"><s:v>1</s:v></s:c></s:row></s:sheetData>` +
				`<s:extLst><x:extension><s:c r="A1"><s:f>keep</s:f><s:v>keep</s:v></s:c></x:extension></s:extLst></s:worksheet>`
			after, err := clearCaches([]byte(before), map[[2]int]bool{{1, 1}: true}, []region{{Sheet: "raw", C1: 1, R1: 1, C2: 1, R2: 2}})
			require.NoError(t, err)
			want := strings.Replace(before, `<s:c r="A1" t="str"`, `<s:c r="A1"`, 1)
			want = strings.Replace(want, "<q:v>old</q:v>", "", 1)
			want = strings.Replace(want, `<s:c r="A2" t="b"><s:v>1</s:v>`, `<s:c r="A2">`, 1)
			assert.Equal(t, want, string(after), "only cached values and their types change")
		})
	}
	nonWorksheet := `<custom xmlns="urn:custom"><sheetData><row><c r="A1"><f>1+1</f><v>2</v></c></row></sheetData></custom>`
	after, err := clearCaches([]byte(nonWorksheet), map[[2]int]bool{{1, 1}: true}, nil)
	require.NoError(t, err)
	assert.Equal(t, nonWorksheet, string(after))
}

func TestFormulaCacheWorksheetPartNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"data", "DATA.XML", "data.xws"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			original, err := zip.OpenReader(cachedTotalsBook(t))
			require.NoError(t, err)
			defer func() { require.NoError(t, original.Close()) }()
			path := filepath.Join(t.TempDir(), "custom-part.xlsx")
			file, err := os.Create(path)
			require.NoError(t, err)
			writer := zip.NewWriter(file)
			for _, part := range original.File {
				reader, err := part.Open()
				require.NoError(t, err)
				data, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				partName := strings.Replace(part.Name, "worksheets/sheet1.xml", "worksheets/"+name, 1)
				if part.Name == "[Content_Types].xml" || part.Name == "xl/_rels/workbook.xml.rels" {
					data = bytes.ReplaceAll(data, []byte("worksheets/sheet1.xml"), []byte("worksheets/"+name))
					if name == "data.xws" && part.Name == "[Content_Types].xml" {
						data = bytes.ReplaceAll(data, []byte(`<Override PartName="/xl/worksheets/data.xws"`), []byte(`<Default Extension="xws"`))
					}
				}
				out, err := writer.Create(partName)
				require.NoError(t, err)
				_, err = out.Write(data)
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			require.NoError(t, file.Close())
			ctx := context.Background()
			before, err := Read(ctx, path, ReadOptions{})
			require.NoError(t, err)
			assert.Equal(t, int64(5), before.Rows[0]["total"])
			_, err = WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
			require.NoError(t, err)
			after, err := Read(ctx, path, ReadOptions{})
			require.NoError(t, err)
			assert.Equal(t, int64(23), after.Rows[0]["total"])
			assert.NotContains(t, workbookPart(t, path, "xl/worksheets/"+name), "<v>5</v>")
		})
	}
}

func TestFormulaCacheEncrypted(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "amount", "total")
	setRow(t, f, "Sheet1", "A2", 2, 5)
	setRow(t, f, "Sheet1", "A3", 3)
	require.NoError(t, f.SetCellFormula("Sheet1", "B2", "SUM(A2:A4)"))
	path := filepath.Join(t.TempDir(), "encrypted.xlsx")
	require.NoError(t, f.SaveAs(path, excelize.Options{Password: "test-password"}))
	require.NoError(t, f.Close())
	_, err := Read(context.Background(), path, ReadOptions{Password: "test-password"})
	require.NoError(t, err, "the encrypted fixture is readable before mutation")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Password: "test-password", InPlace: true, Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	_, err = excelize.OpenFile(path)
	require.Error(t, err, "the saved workbook remains encrypted")
	result, err := Read(context.Background(), path, ReadOptions{Password: "test-password"})
	require.NoError(t, err)
	require.NotEmpty(t, result.Rows)
	assert.Equal(t, int64(23), result.Rows[0]["total"])
}

func TestFormulaCacheAppendKeepsTrailingFormulaRow(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Raw"))
	setRow(t, f, "Raw", "A1", "total")
	setRow(t, f, "Raw", "A2", 5)
	require.NoError(t, f.SetCellFormula("Raw", "A2", "LEN(Other!A1)"))
	_, err := f.NewSheet("Other")
	require.NoError(t, err)
	setRow(t, f, "Other", "A1", "input")
	path := saveBook(t, f, "append-formula.xlsx")
	ctx := context.Background()
	_, err = WriteCells(ctx, path, WriteCellsOptions{Sheet: "Other", Cells: map[string]CellValue{"A1": {Value: "changed"}}})
	require.NoError(t, err)
	require.NotContains(t, workbookPart(t, path, "xl/worksheets/sheet1.xml"), "<v>5</v>", "the edit must clear the formula cache first")
	_, err = Append(ctx, path, Table{Columns: []string{"total"}, Rows: [][]any{{9}}}, WriteOptions{Sheet: "Raw", Header: true})
	require.NoError(t, err)
	got, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, got.Close()) }()
	formula, err := got.GetCellFormula("Raw", "A2")
	require.NoError(t, err)
	assert.Equal(t, "LEN(Other!A1)", formula)
	appended, err := got.GetCellValue("Raw", "A3")
	require.NoError(t, err)
	assert.Equal(t, "9", appended)
}

func TestFormulaCacheKeepsLiteralOverSharedFollower(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<sheetData><row r="1"><c r="A1"><v>2</v></c><c r="B1"><f t="shared" ref="B1:B3" si="0">A1*2</f><v>4</v></c></row>`+
		`<row r="2"><c r="A2"><v>3</v></c><c r="B2"><f t="shared" si="0"/><v>6</v></c></row>`+
		`<row r="3"><c r="A3"><v>4</v></c><c r="B3"><f t="shared" si="0"/><v>8</v></c></row></sheetData>`)
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A1": {Value: 5}, "B2": {Value: 20}}})
	require.NoError(t, err)
	xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
	assert.Contains(t, xml, `<c r="B2"><v>20</v></c>`)
	assert.Contains(t, xml, `<c r="B1"><f t="shared" ref="B1:B3" si="0">A1*2</f></c>`, "the owner reads A1, so its cache is cleared")
	assert.Contains(t, xml, `<c r="B3"><f t="shared" si="0"></f><v>8</v></c>`, "B3 reads A3, so its cache stays")
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	b2, err := f.GetCellValue("Raw", "B2", rawValues)
	require.NoError(t, err)
	assert.Equal(t, "20", b2)
	b3, err := f.GetCellFormula("Raw", "B3")
	require.NoError(t, err)
	assert.Equal(t, "A3*2", b3)
}

func TestFormulaCacheKeepsResultKinds(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "text")+inline("B1", "flag")+inline("C1", "number")+inline("D1", "spelled")+inline("E1", "input")+
		`</row><row r="2"><c r="A2" t="str"><f>IF(E2&gt;0,"001","")</f><v>001</v></c><c r="B2" t="b"><f>E2&gt;1</f><v>1</v></c>`+
		`<c r="C2"><f>E2+0.5</f><v>2.5</v></c><c r="D2" t="str"><f>IF(E2&gt;0,"1e3","")</f><v>1e3</v></c><c r="E2"><v>2</v></c></row></sheetData>`)
	ctx := context.Background()
	before, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	_, err = WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"E2": {Value: 3}}})
	require.NoError(t, err)
	xml := workbookPart(t, path, "xl/worksheets/sheet1.xml")
	require.NotContains(t, xml, "<v>001</v>")
	require.NotContains(t, xml, "<v>2.5</v>")
	after, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	for column, want := range map[string][2]any{"text": {"001", "001"}, "flag": {true, true}, "number": {2.5, 3.5}, "spelled": {"1e3", "1e3"}} {
		assert.Equal(t, want[0], before.Rows[0][column], "cached %s", column)
		assert.Equal(t, want[1], after.Rows[0][column], "uncached %s", column)
	}
}
