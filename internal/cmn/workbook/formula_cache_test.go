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
	t.Cleanup(func() { require.NoError(t, f.Close()) })
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

func TestFormulaCacheGroups(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"array", "dataTable", "shared"} {
		t.Run(kind, func(t *testing.T) {
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
			_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
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
			assert.NotContains(t, xml, "<v>4</v>")
			assert.NotContains(t, xml, "<v>6</v>")
			f, err := excelize.OpenFile(path)
			require.NoError(t, err)
			defer func() { require.NoError(t, f.Close()) }()
			cached, err := f.GetCellValue("Raw", "B2", rawValues)
			require.NoError(t, err)
			assert.Empty(t, cached, "a formula follower must not retain its previous result")
		})
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
	require.NotNil(t, props.ForceFullCalc)
	assert.True(t, *props.ForceFullCalc)
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

func TestFormulaCacheUnrelatedFormulas(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<sheetData><row r="1">`+inline("A1", "input")+inline("B1", "constant")+inline("C1", "unknown")+
		`</row><row r="2"><c r="A2"><v>2</v></c><c r="B2"><f>1+1</f><v>2</v></c><c r="C2"><f>UNSUPPORTED(1)</f><v>7</v></c></row></sheetData>`)
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"A2": {Value: 20}}})
	require.NoError(t, err)
	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), result.Rows[0]["constant"])
	assert.Nil(t, result.Rows[0]["unknown"])
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "UNSUPPORTED(1) could not be evaluated")
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "Raw!B2: formula had no cached value; evaluated")
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
			path := rawBook(t, `<sheetData><row r="1"><c r="A1" t="str"><f>"amount"</f><v>amount</v></c><c r="B1" t="str"><f>"total"</f><v>total</v></c><c r="C1" t="str"><f>"id"</f><v>id</v></c>`+
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
	path := rawBook(t, `<sheetData><row r="1"><c r="A1" t="str"><f>UNSUPPORTED()</f><v>old header</v></c>`+inline("B1", "id")+
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
	require.NoError(t, f.SetCellFormula("Sheet1", "A1", "1+1"))
	dateStyle := styleID(t, f, &excelize.Style{NumFmt: 14})
	require.NoError(t, f.SetCellStyle("Sheet1", "A1", "A1", dateStyle))
	path := saveBook(t, f, "formula-header.xlsx")
	ctx := context.Background()
	before, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	_, err = WriteCells(ctx, path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: 20}}})
	require.NoError(t, err)
	after, err := Read(ctx, path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"2", "input"}, before.Headers)
	assert.Equal(t, before.Headers, after.Headers, "header names do not convert numeric results through date styles")
}

func TestFormulaCacheXMLPreservation(t *testing.T) {
	t.Parallel()
	for _, namespace := range []string{"http://schemas.openxmlformats.org/spreadsheetml/2006/main", "http://purl.oclc.org/ooxml/spreadsheetml/main"} {
		t.Run(namespace, func(t *testing.T) {
			before := `<s:worksheet xmlns:s="` + namespace + `" xmlns:q="` + namespace + `" xmlns:x="urn:extension"><s:sheetData><s:row r="1">` +
				`<s:c r="A1" t="str" s="4" cm="2" vm="3" x:note="literal t='kept'"><q:f t="array" ref="A1:A2" ca="1">B1:B2</q:f><q:v>old</q:v><x:v>keep</x:v></s:c>` +
				`<s:c r="B1" t="n"><x:f>not a formula</x:f><q:v>2</q:v></s:c></s:row><s:row r="2"><s:c r="A2" t="b"><s:v>1</s:v></s:c></s:row></s:sheetData>` +
				`<s:extLst><x:extension><s:c r="A1"><s:f>keep</s:f><s:v>keep</s:v></s:c></x:extension></s:extLst></s:worksheet>`
			after, err := clearFormulaCaches([]byte(before))
			require.NoError(t, err)
			want := strings.Replace(before, `<s:c r="A1" t="str"`, `<s:c r="A1"`, 1)
			want = strings.Replace(want, "<q:v>old</q:v>", "", 1)
			want = strings.Replace(want, `<s:c r="A2" t="b"><s:v>1</s:v>`, `<s:c r="A2">`, 1)
			assert.Equal(t, want, string(after), "only cached values and their types change")
		})
	}
	nonWorksheet := `<custom xmlns="urn:custom"><sheetData><row><c r="A1"><f>1+1</f><v>2</v></c></row></sheetData></custom>`
	after, err := clearFormulaCaches([]byte(nonWorksheet))
	require.NoError(t, err)
	assert.Equal(t, nonWorksheet, string(after))
}

func TestFormulaCacheSerialization(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	f := excelize.NewFile()
	defer func() { require.NoError(t, f.Close()) }()
	f.Pkg.Store("[Content_Types].xml", []byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/custom/data.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`))
	writer := &formulaCacheZipWriter{ZipWriter: zip.NewWriter(&archive), workbook: f}
	other := `<custom><sheetData><row><c r="A1"><f>1+1</f><v>2</v></c></row></sheetData></custom>`
	out, err := writer.Create("custom/metadata.xml")
	require.NoError(t, err)
	_, err = io.WriteString(out, other)
	require.NoError(t, err)
	// The part URI need not be the usual xl/worksheets/sheet1.xml, and an
	// io.Writer may receive the opening tag across several writes.
	const part = "custom/data.xml"
	worksheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><f>1+1</f><v>2</v></c></row></sheetData></worksheet>`
	out, err = writer.Create(part)
	require.NoError(t, err)
	_, err = io.WriteString(out, worksheet[:9])
	require.NoError(t, err)
	_, err = io.WriteString(out, worksheet[9:])
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	require.NoError(t, err)
	for name, want := range map[string]string{"custom/metadata.xml": other, part: strings.Replace(worksheet, "<v>2</v>", "", 1)} {
		r, err := reader.Open(name)
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, want, string(data))
	}
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
