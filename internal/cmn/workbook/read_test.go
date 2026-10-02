// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func saveBook(t *testing.T, f *excelize.File, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, f.SaveAs(path))
	require.NoError(t, f.Close())
	return path
}

func styleID(t *testing.T, f *excelize.File, style *excelize.Style) int {
	t.Helper()
	id, err := f.NewStyle(style)
	require.NoError(t, err)
	return id
}

func setStyled(t *testing.T, f *excelize.File, sheet, cell string, value any, style *excelize.Style) {
	t.Helper()
	require.NoError(t, f.SetCellValue(sheet, cell, value))
	id := styleID(t, f, style)
	require.NoError(t, f.SetCellStyle(sheet, cell, cell, id))
}

func setRow(t *testing.T, f *excelize.File, sheet, cell string, values ...any) {
	t.Helper()
	require.NoError(t, f.SetSheetRow(sheet, cell, &values))
}

func custom(code string) *excelize.Style {
	return &excelize.Style{CustomNumFmt: &code}
}

// rawBook writes a minimal xlsx by hand so the test can author cell kinds
// excelize cannot: error cells, ISO date cells, and formulas with and
// without cached values.
func rawBook(t *testing.T, sheetXML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw.xlsx")
	out, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(out)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Raw" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` + sheetXML + `</worksheet>`,
	}
	for name, body := range parts {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, out.Close())
	return path
}

func inline(cell, text string) string {
	return `<c r="` + cell + `" t="inlineStr"><is><t>` + text + `</t></is></c>`
}

func TestReadTypingMatrix(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "int", "float", "big", "date", "datetime", "clock", "jpdate", "red", "flag", "lead", "textnum", "ws", "empty")
	require.NoError(t, f.SetCellValue(s, "A2", 42))
	require.NoError(t, f.SetCellValue(s, "B2", 3.5))
	require.NoError(t, f.SetCellValue(s, "C2", int64(12345678901234567)))
	setStyled(t, f, s, "D2", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), &excelize.Style{NumFmt: 14})
	setStyled(t, f, s, "E2", time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC), &excelize.Style{NumFmt: 22})
	setStyled(t, f, s, "F2", 0.5, &excelize.Style{NumFmt: 20})
	setStyled(t, f, s, "G2", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), custom(`yyyy"年"m"月"d"日"`))
	setStyled(t, f, s, "H2", 1.5, custom(`[Red]0.00`))
	require.NoError(t, f.SetCellBool(s, "I2", true))
	require.NoError(t, f.SetCellStr(s, "J2", "00123"))
	setStyled(t, f, s, "K2", "123", &excelize.Style{NumFmt: 49})
	require.NoError(t, f.SetCellStr(s, "L2", "　hello "))
	path := saveBook(t, f, "typing.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{Trim: true})
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
	row := result.Rows[0]
	assert.Equal(t, int64(42), row["int"])
	assert.Equal(t, 3.5, row["float"])
	assert.IsType(t, float64(0), row["big"])
	assert.Equal(t, "2026-10-01", row["date"])
	assert.Equal(t, "2026-10-01T14:30:00", row["datetime"])
	assert.Equal(t, "12:00:00", row["clock"])
	assert.Equal(t, "2026-10-01", row["jpdate"])
	assert.Equal(t, 1.5, row["red"])
	assert.Equal(t, true, row["flag"])
	assert.Equal(t, "00123", row["lead"])
	assert.Equal(t, "123", row["textnum"])
	assert.Equal(t, "hello", row["ws"])
	assert.Nil(t, row["empty"])
	assert.Equal(t, 2, row[RowNumberKey])
	assert.Equal(t, "Sheet1!A1:M2", result.Range)
	assert.Equal(t, []string{"int", "float", "big", "date", "datetime", "clock", "jpdate", "red", "flag", "lead", "textnum", "ws", "empty"}, result.Headers)
}

func TestReadRawCellKinds(t *testing.T) {
	t.Parallel()
	path := rawBook(t, `<dimension ref="A1:D2"/><sheetData><row r="1">`+inline("A1", "cached")+inline("B1", "nocache")+inline("C1", "err")+inline("D1", "iso")+`</row><row r="2"><c r="A2"><f>1+1</f><v>2</v></c><c r="B2"><f>1+2</f></c><c r="C2" t="e"><v>#N/A</v></c><c r="D2" t="d"><v>2026-10-01</v></c></row></sheetData>`)

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
	row := result.Rows[0]
	assert.Equal(t, int64(2), row["cached"])
	assert.Equal(t, int64(3), row["nocache"])
	assert.Nil(t, row["err"])
	assert.Equal(t, "2026-10-01", row["iso"])
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "Raw!B2: formula had no cached value; evaluated")
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "Raw!C2: error cell #N/A")

	text, err := Read(context.Background(), path, ReadOptions{Formulas: FormulaText})
	require.NoError(t, err)
	assert.Equal(t, "=1+1", text.Rows[0]["cached"])
	assert.Equal(t, "=1+2", text.Rows[0]["nocache"])
}

func TestRead1904DateSystem(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	yes := true
	require.NoError(t, f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &yes}))
	setRow(t, f, "Sheet1", "A1", "when")
	setStyled(t, f, "Sheet1", "A2", 1, &excelize.Style{NumFmt: 14})
	path := saveBook(t, f, "d1904.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "1904-01-02", result.Rows[0]["when"])
}

func TestReadMultiRowAndMergedHeaders(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "Invoice No", "Amount")
	require.NoError(t, f.MergeCell(s, "B1", "C1"))
	setRow(t, f, s, "A2", "", "Net", "Tax")
	require.NoError(t, f.SetCellStr(s, "D1", "Note\nLine"))
	setRow(t, f, s, "A3", "INV-1", 100, 10)
	require.NoError(t, f.SetCellStr(s, "D3", "x"))
	setRow(t, f, s, "A4", "INV-2", 200, 20)
	path := saveBook(t, f, "headers.xlsx")

	spec, err := ParseHeader([]any{float64(1), float64(2)})
	require.NoError(t, err)
	result, err := Read(context.Background(), path, ReadOptions{Header: spec})
	require.NoError(t, err)
	assert.Equal(t, []string{"Invoice No", "Amount Net", "Amount Tax", "Note Line"}, result.Headers)
	require.Equal(t, 2, result.Count)
	assert.Equal(t, int64(100), result.Rows[0]["Amount Net"])
	assert.Equal(t, int64(20), result.Rows[1]["Amount Tax"])
	assert.Equal(t, 3, result.Rows[0][RowNumberKey])

	first, err := Read(context.Background(), path, ReadOptions{Header: spec, Merged: MergedFirst})
	require.NoError(t, err)
	assert.Equal(t, []string{"Invoice No", "Amount Net", "Tax", "Note Line"}, first.Headers)
}

func TestReadMergedBodyFill(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "Customer", "Order")
	setRow(t, f, s, "A2", "ACME", 1)
	setRow(t, f, s, "A3", nil, 2)
	setRow(t, f, s, "A4", nil, 3)
	require.NoError(t, f.MergeCell(s, "A2", "A4"))
	path := saveBook(t, f, "merged.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	require.Equal(t, 3, result.Count)
	for _, row := range result.Rows {
		assert.Equal(t, "ACME", row["Customer"])
	}
	first, err := Read(context.Background(), path, ReadOptions{Merged: MergedFirst})
	require.NoError(t, err)
	assert.Equal(t, "ACME", first.Rows[0]["Customer"])
	assert.Nil(t, first.Rows[1]["Customer"])
}

func TestReadDuplicateAndEmptyHeaders(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "Name", "Name", "")
	setRow(t, f, "Sheet1", "A2", "a", "b", "c")
	path := saveBook(t, f, "dup.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Name", "Name_2", "C"}, result.Headers)
	assert.Equal(t, "c", result.Rows[0]["C"])
	assert.Len(t, result.Warnings, 2)
}

func TestReadTableDetectionOffset(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	require.NoError(t, f.SetCellStr(s, "A1", "Orders report"))
	setRow(t, f, s, "C4", "ID", "Qty")
	setRow(t, f, s, "C5", "a", 1)
	setRow(t, f, s, "C6", "b", 2)
	path := saveBook(t, f, "offset.xlsx")

	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!C4:D6", result.Range)
	assert.Equal(t, []string{"ID", "Qty"}, result.Headers)
	require.Equal(t, 2, result.Count)
	assert.Equal(t, 5, result.Rows[0][RowNumberKey])
}

func TestReadEmptySheet(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "empty.xlsx")
	result, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Count)
	assert.Empty(t, result.Headers)
	assert.Equal(t, "Sheet1", result.Sheet)
}

func TestReadRangeForms(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "h1", "h2", "h3")
	for r := 2; r <= 6; r++ {
		setRow(t, f, s, cellName(1, r), r, r*10, r*100)
	}
	_, err := f.NewSheet("My Sheet")
	require.NoError(t, err)
	setRow(t, f, "My Sheet", "B2", "k", "v")
	setRow(t, f, "My Sheet", "B3", "x", 1)
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Totals", RefersTo: "Sheet1!$B$1:$C$3"}))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Local", RefersTo: "'My Sheet'!$B$2:$C$3", Scope: "My Sheet"}))
	require.NoError(t, f.AddTable(s, &excelize.Table{Range: "A1:C4", Name: "Orders"}))
	path := saveBook(t, f, "ranges.xlsx")

	for _, tc := range []struct {
		name    string
		sheet   string
		rng     string
		header  HeaderSpec
		want    string
		headers []string
		count   int
	}{
		{"open end", "", "A2:C", HeaderSpec{Mode: HeaderNone}, "Sheet1!A2:C6", []string{"A", "B", "C"}, 5},
		{"closed", "", "A2:C4", HeaderSpec{Mode: HeaderNone}, "Sheet1!A2:C4", []string{"A", "B", "C"}, 3},
		{"sheet prefix", "", "'My Sheet'!B2:C", HeaderSpec{}, "My Sheet!B2:C3", []string{"k", "v"}, 1},
		{"columns only", "", "B:C", HeaderSpec{}, "Sheet1!B1:C6", []string{"h2", "h3"}, 5},
		{"named global", "my sheet", "totals", HeaderSpec{}, "Sheet1!B1:C3", []string{"h2", "h3"}, 2},
		{"named scoped", "My Sheet", "Local", HeaderSpec{}, "My Sheet!B2:C3", []string{"k", "v"}, 1},
		{"table", "", "orders", HeaderSpec{}, "Sheet1!A1:C4", []string{"h1", "h2", "h3"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Read(context.Background(), path, ReadOptions{Sheet: tc.sheet, Range: tc.rng, Header: tc.header})
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Range)
			assert.Equal(t, tc.headers, result.Headers)
			assert.Equal(t, tc.count, result.Count)
		})
	}

	_, err = Read(context.Background(), path, ReadOptions{Range: "Nope"})
	require.ErrorContains(t, err, `range "Nope" is not a cell range, named range, or table`)
	_, err = Read(context.Background(), path, ReadOptions{Sheet: "Order"})
	var notFound *SheetNotFoundError
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, `ranges.xlsx: sheet "Order" not found; sheets present: Sheet1, My Sheet`, err.Error())
}

func TestReadWhereColumnsTypes(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "Invoice No", "Status", "Amount")
	setRow(t, f, s, "A2", "INV-1", "Done", 10)
	setRow(t, f, s, "A3", "INV-2", nil, 20)
	setRow(t, f, s, "A4", "INV-3", "Failed", "N/A")
	setRow(t, f, s, "A5", "INV-4", "", 40)
	path := saveBook(t, f, "where.xlsx")

	todo, err := Read(context.Background(), path, ReadOptions{Where: map[string]any{"Status": ""}})
	require.NoError(t, err)
	assert.Equal(t, 2, todo.Count)
	assert.Equal(t, "INV-2", todo.Rows[0]["Invoice No"])

	notDone, err := Read(context.Background(), path, ReadOptions{Where: map[string]any{"status": map[string]any{"ne": "Done"}}})
	require.NoError(t, err)
	assert.Equal(t, 3, notDone.Count)

	in, err := Read(context.Background(), path, ReadOptions{Where: map[string]any{"Amount": map[string]any{"in": []any{float64(10), "40"}}}})
	require.NoError(t, err)
	assert.Equal(t, 2, in.Count)

	cols, err := ParseColumns([]any{"Invoice No: invoice_no", map[string]any{"Amount": "amount"}})
	require.NoError(t, err)
	renamed, err := Read(context.Background(), path, ReadOptions{Columns: cols, Where: map[string]any{"invoice_no": "INV-1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"invoice_no", "amount"}, renamed.Headers)
	require.Equal(t, 1, renamed.Count)
	assert.Equal(t, Row{"invoice_no": "INV-1", "amount": int64(10), RowNumberKey: 2}, renamed.Rows[0])

	_, err = Read(context.Background(), path, ReadOptions{Types: map[string]ColumnType{"Amount": TypeNumber}})
	var cellErr *CellError
	require.ErrorAs(t, err, &cellErr)
	assert.Equal(t, `where.xlsx Sheet1!C4: expected number, found "N/A"`, err.Error())

	lenient, err := Read(context.Background(), path, ReadOptions{Types: map[string]ColumnType{"Amount": TypeNumber}, OnTypeError: TypeErrorNull})
	require.NoError(t, err)
	assert.Nil(t, lenient.Rows[2]["Amount"])
	assert.Contains(t, lenient.Warnings[0], `Sheet1!C4: expected number, found "N/A"`)

	_, err = Read(context.Background(), path, ReadOptions{Columns: []ColumnSelect{{Source: "Nope", As: "n"}}})
	require.ErrorContains(t, err, `columns: column "Nope" not found; headers present: Invoice No, Status, Amount`)
	_, err = Read(context.Background(), path, ReadOptions{Where: map[string]any{"Status": map[string]any{"like": "x"}}})
	require.ErrorContains(t, err, `unknown operator "like"`)
}

func TestReadLimitsAndEmptyRows(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "n")
	for r := 2; r <= 11; r++ {
		if r == 6 {
			continue
		}
		setRow(t, f, s, cellName(1, r), r)
	}
	require.NoError(t, f.SetCellStr(s, "A13", ""))
	path := saveBook(t, f, "limits.xlsx")

	limited, err := Read(context.Background(), path, ReadOptions{MaxRows: 3})
	require.NoError(t, err)
	assert.Equal(t, 3, limited.Count)
	assert.True(t, limited.Truncated)
	assert.Contains(t, limited.Warnings[0], "stopped after 3 rows")

	all, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 10, all.Count)
	assert.Nil(t, all.Rows[4]["n"])
	assert.Equal(t, 11, all.Rows[9][RowNumberKey])

	stop, err := Read(context.Background(), path, ReadOptions{StopAtBlank: true})
	require.NoError(t, err)
	assert.Equal(t, 4, stop.Count)

	keep, err := Read(context.Background(), path, ReadOptions{Range: "A1:A13", KeepEmptyRows: true})
	require.NoError(t, err)
	assert.Equal(t, 12, keep.Count)
	dropped, err := Read(context.Background(), path, ReadOptions{Range: "A1:A13"})
	require.NoError(t, err)
	assert.Equal(t, 10, dropped.Count)
}

func TestFitRows(t *testing.T) {
	t.Parallel()
	rows := make([]Row, 0, 100)
	for i := range 100 {
		rows = append(rows, Row{"i": int64(i), "text": strings.Repeat("x", 50)})
	}
	full := encodedSize(rows)
	kept, truncated := FitRows(rows, full)
	assert.False(t, truncated)
	assert.Len(t, kept, 100)

	kept, truncated = FitRows(rows, full/4)
	assert.True(t, truncated)
	assert.Less(t, len(kept), 30)
	assert.Greater(t, len(kept), 15)
	assert.LessOrEqual(t, encodedSize(kept), full/4)

	kept, truncated = FitRows(rows, 0)
	assert.False(t, truncated)
	assert.Len(t, kept, 100)
}

func TestParseHeaderAndColumns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   any
		want HeaderSpec
	}{
		{nil, HeaderSpec{Mode: HeaderFirstRow}},
		{true, HeaderSpec{Mode: HeaderFirstRow}},
		{false, HeaderSpec{Mode: HeaderNone}},
		{"false", HeaderSpec{Mode: HeaderNone}},
		{3, HeaderSpec{Mode: HeaderRows, Rows: []int{3}}},
		{float64(2), HeaderSpec{Mode: HeaderRows, Rows: []int{2}}},
		{"3,4", HeaderSpec{Mode: HeaderRows, Rows: []int{3, 4}}},
		{[]any{float64(4), float64(3)}, HeaderSpec{Mode: HeaderRows, Rows: []int{3, 4}}},
	} {
		got, err := ParseHeader(tc.in)
		require.NoError(t, err, "%v", tc.in)
		assert.Equal(t, tc.want, got, "%v", tc.in)
	}
	_, err := ParseHeader("yes")
	require.Error(t, err)
	_, err = ParseHeader(0)
	require.Error(t, err)
	_, err = ParseHeader([]any{})
	require.Error(t, err)

	cols, err := ParseColumns("a, b:c")
	require.NoError(t, err)
	assert.Equal(t, []ColumnSelect{{Source: "a", As: "a"}, {Source: "b", As: "c"}}, cols)
	cols, err = ParseColumns(`["x", {"y": "z"}]`)
	require.NoError(t, err)
	assert.Equal(t, []ColumnSelect{{Source: "x", As: "x"}, {Source: "y", As: "z"}}, cols)
	_, err = ParseColumns([]any{map[string]any{"a": 1}})
	require.Error(t, err)
	_, err = ParseColumns(5)
	require.Error(t, err)
}

func TestCustomFormatKind(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]cellKind{
		`yyyy"年"m"月"d"日"`:        kindDate,
		`[$-411]ggge"年"m"月"d"日"`: kindDate,
		`yyyy-mm-dd hh:mm:ss`:    kindDateTime,
		`h:mm AM/PM`:             kindTime,
		`[h]:mm:ss`:              kindNumber,
		`[Red]0.00`:              kindNumber,
		`#,##0.00`:               kindNumber,
		`0%`:                     kindNumber,
		`mmm-yy`:                 kindDate,
		`@`:                      kindNumber,
	} {
		assert.Equal(t, want, customFormatKind(code), code)
	}
}

func TestCoerce(t *testing.T) {
	t.Parallel()
	v, err := coerce("1,234.5", TypeNumber, false)
	require.NoError(t, err)
	assert.Equal(t, 1234.5, v)
	v, err = coerce(float64(3), TypeInteger, false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), v)
	_, err = coerce(3.5, TypeInteger, false)
	require.ErrorContains(t, err, "expected integer, found 3.5")
	v, err = coerce("yes", TypeBoolean, false)
	require.NoError(t, err)
	assert.Equal(t, true, v)
	v, err = coerce("2026/10/01", TypeDate, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01", v)
	v, err = coerce(int64(46296), TypeDate, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01", v)
	v, err = coerce("2026-10-01", TypeDateTime, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01T00:00:00", v)
	v, err = coerce(int64(7), TypeString, false)
	require.NoError(t, err)
	assert.Equal(t, "7", v)
	_, err = coerce("soon", TypeDate, false)
	require.ErrorContains(t, err, `expected date, found "soon"`)
}

func TestInspect(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Orders"))
	setRow(t, f, "Orders", "A1", "Invoice No", "Amount", "Due")
	setRow(t, f, "Orders", "A2", "INV-1", 10, nil)
	setStyled(t, f, "Orders", "C2", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), &excelize.Style{NumFmt: 14})
	setRow(t, f, "Orders", "A3", "INV-2", 20.5, nil)
	require.NoError(t, f.AddTable("Orders", &excelize.Table{Range: "A1:C3", Name: "OrderTable"}))
	_, err := f.NewSheet("Notes")
	require.NoError(t, err)
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Total", RefersTo: "Orders!$B$2:$B$3"}))
	path := saveBook(t, f, "inspect.xlsx")

	info, err := Inspect(context.Background(), path, InspectOptions{SampleRows: 1})
	require.NoError(t, err)
	assert.Equal(t, "1900", info.DateSystem)
	require.Len(t, info.Sheets, 2)
	orders := info.Sheets[0]
	assert.Equal(t, "Orders", orders.Name)
	assert.Equal(t, "Orders!A1:C3", orders.UsedRange)
	assert.Equal(t, "Orders!A1:C3", orders.Range)
	assert.Equal(t, 1, orders.HeaderRow)
	assert.Equal(t, []string{"Invoice No", "Amount", "Due"}, orders.Headers)
	assert.Equal(t, map[string]string{"Invoice No": "string", "Amount": "number", "Due": "date"}, orders.Types)
	assert.Equal(t, 2, orders.RowCount)
	assert.Equal(t, []TableInfo{{Name: "OrderTable", Range: "A1:C3"}}, orders.Tables)
	require.Len(t, orders.Sample, 1)
	assert.Equal(t, "INV-1", orders.Sample[0]["Invoice No"])
	notes := info.Sheets[1]
	assert.Equal(t, 0, notes.HeaderRow)
	assert.Empty(t, notes.Headers)
	assert.Equal(t, []NamedRange{{Name: "Total", RefersTo: "Orders!$B$2:$B$3", Scope: "Workbook"}}, info.NamedRanges)

	sheets, err := ListSheets(path, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Orders", "Notes"}, sheets)
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := Read(context.Background(), filepath.Join(dir, "book.xls"), ReadOptions{})
	require.ErrorIs(t, err, ErrUnsupportedFormat)
	assert.Equal(t, "book.xls: only .xlsx and .xlsm workbooks are supported; save as .xlsx", err.Error())

	_, err = Read(context.Background(), filepath.Join(dir, "missing.xlsx"), ReadOptions{})
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Equal(t, "missing.xlsx: workbook not found", err.Error())

	bad := filepath.Join(dir, "bad.xlsx")
	require.NoError(t, os.WriteFile(bad, []byte("not a zip"), 0o600))
	_, err = Read(context.Background(), bad, ReadOptions{})
	require.ErrorIs(t, err, ErrNotWorkbook)
}

func TestLockFile(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "locked.xlsx")
	warning, err := checkLockFile(path)
	require.NoError(t, err)
	assert.Empty(t, warning)

	// A lock file nobody holds is a leftover, not a lock, on every platform.
	require.NoError(t, os.WriteFile(lockFilePath(path), []byte("x"), 0o600))
	warning, err = checkLockFile(path)
	require.NoError(t, err)
	assert.Equal(t, "~$locked.xlsx "+leftoverLockWording, warning)
}

// leftoverLockWording is what a lock file nobody holds is reported as:
// where the file can be probed the warning says so, elsewhere it can only
// say the workbook may be open.
var leftoverLockWording = func() string {
	if lockProbeSupported {
		return "exists but no program holds it; the workbook may have been closed without cleanup"
	}
	return "exists; the workbook may be open in another program, or the lock file may be a leftover"
}()

func TestWithLockRetry(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var waits []time.Duration
	var logs []string
	opts := LockOptions{
		WaitFor: 10 * time.Second,
		Log:     func(msg string) { logs = append(logs, msg) },
		sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			clock = clock.Add(d)
			return nil
		},
		now: func() time.Time { return clock },
	}
	attempts := 0
	err := withLockRetry(context.Background(), "orders.xlsx", opts, func() error {
		attempts++
		if attempts < 3 {
			return &LockedError{Path: "orders.xlsx"}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second}, waits)
	assert.Equal(t, "orders.xlsx is open in another program; retrying in 2s (10s left)", logs[0])

	waits = nil
	attempts = 0
	err = withLockRetry(context.Background(), "orders.xlsx", opts, func() error {
		attempts++
		return &LockedError{Path: "orders.xlsx"}
	})
	var locked *LockedError
	require.ErrorAs(t, err, &locked)
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second}, waits)
	assert.Equal(t, 4, attempts)

	attempts = 0
	err = withLockRetry(context.Background(), "orders.xlsx", LockOptions{}, func() error {
		attempts++
		return &LockedError{Path: "orders.xlsx"}
	})
	require.ErrorAs(t, err, &locked)
	assert.Equal(t, 1, attempts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = withLockRetry(ctx, "orders.xlsx", LockOptions{WaitFor: time.Minute}, func() error {
		return &LockedError{Path: "orders.xlsx"}
	})
	require.True(t, errors.Is(err, context.Canceled))
}

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

func TestFitRowsKeepsTheLongestFittingPrefix(t *testing.T) {
	t.Parallel()
	rows := []Row{{"a": "x"}, {"a": "y"}, {"a": strings.Repeat("z", 10_000)}}
	kept, truncated := FitRows(rows, encodedSize(rows[:2])+1)
	assert.True(t, truncated)
	assert.Len(t, kept, 2, "two small rows fit even though the third is huge")
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
