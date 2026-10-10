// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// templateBook is an invoice template: labels in column A, values to fill
// in column B, a bold total cell, a defined name for the customer cell, and
// a second sheet.
func templateBook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	setRow(t, f, "Sheet1", "A1", "Customer", "")
	setRow(t, f, "Sheet1", "A2", "Date", "")
	setRow(t, f, "Sheet1", "A3", "Qty", 2)
	setRow(t, f, "Sheet1", "A4", "Price", 10)
	setRow(t, f, "Sheet1", "A5", "Total", "old")
	require.NoError(t, f.SetCellStyle("Sheet1", "B5", "B5", bold))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Customer", RefersTo: "Sheet1!$B$1"}))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Block", RefersTo: "Sheet1!$A$1:$B$2"}))
	_, err := f.NewSheet("My Sheet")
	require.NoError(t, err)
	setRow(t, f, "My Sheet", "A1", "note")
	return saveBook(t, f, "template.xlsx")
}

func TestParseCells(t *testing.T) {
	t.Parallel()
	cells, err := ParseCells(map[string]any{
		"A1": "x", "B1": 3, "C1": true, "D1": nil,
		"E1": map[string]any{"formula": "=SUM(A1:A3)"},
		"F1": map[string]any{"value": "2026-10-01", "type": "string"},
		"G1": map[string]any{"value": nil},
	})
	require.NoError(t, err)
	assert.Equal(t, CellValue{Value: "x"}, cells["A1"])
	assert.Equal(t, CellValue{Value: int64(3)}, cells["B1"])
	assert.Equal(t, CellValue{Value: true}, cells["C1"])
	assert.Equal(t, CellValue{Clear: true}, cells["D1"])
	assert.Equal(t, CellValue{Formula: "=SUM(A1:A3)"}, cells["E1"])
	assert.Equal(t, CellValue{Value: "2026-10-01", Type: TypeString}, cells["F1"])
	assert.Equal(t, CellValue{Clear: true}, cells["G1"])

	// A canonical number in text, the form a reference arrives in, is a
	// number; anything else stays text unless a type pins it.
	numbers, err := ParseCells(map[string]any{
		"A1": "100", "B1": "-12.5", "C1": "0",
		"D1": map[string]any{"value": "100", "type": "string"},
		"E1": map[string]any{"value": "100", "type": "number"},
		"F1": map[string]any{"value": "250"},
	})
	require.NoError(t, err)
	assert.Equal(t, CellValue{Value: int64(100)}, numbers["A1"])
	assert.Equal(t, CellValue{Value: -12.5}, numbers["B1"])
	assert.Equal(t, CellValue{Value: int64(0)}, numbers["C1"])
	assert.Equal(t, CellValue{Value: "100", Type: TypeString}, numbers["D1"])
	assert.Equal(t, CellValue{Value: "100", Type: TypeNumber}, numbers["E1"], "a pinned value is converted when written")
	assert.Equal(t, CellValue{Value: int64(250)}, numbers["F1"])
	for _, text := range []string{"007", "1,234", "1e3", " 7", "+7", "１２３", "7.", ".5", "-", "2026-10-01"} {
		kept, err := ParseCells(map[string]any{"A1": text})
		require.NoError(t, err)
		assert.Equal(t, CellValue{Value: text}, kept["A1"], text)
	}

	for name, bad := range map[string]any{
		"list":          []any{1},
		"formula empty": map[string]any{"formula": ""},
		"formula extra": map[string]any{"formula": "A1", "value": 1},
		"no value":      map[string]any{"type": "number"},
		"bad type":      map[string]any{"value": 1, "type": "money"},
		"unknown key":   map[string]any{"value": 1, "bold": true},
	} {
		_, err := ParseCells(map[string]any{"A1": bad})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "cells.A1:", name)
	}
	_, err = ParseCells(map[string]any{})
	require.ErrorContains(t, err, "cells must not be empty")
	_, err = ParseCells("A1=x")
	require.ErrorContains(t, err, "cells must map cell addresses to values")
}

func TestWriteCellsFillsATemplate(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	result, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{
		"Customer":      {Value: "Acme"},
		"B2":            {Value: "2026-10-01"},
		"Sheet1!B5":     {Formula: "=B3*B4"},
		"B4":            {Value: int64(10)}, // unchanged
		"'My Sheet'!A1": {Clear: true},
		"'My Sheet'!B1": {Value: "2026-10-01", Type: TypeString},
		"$B$3":          {Value: 3.0},
	}})
	require.NoError(t, err)
	assert.Equal(t, path, result.Path)
	assert.Equal(t, "Sheet1", result.Sheet)
	assert.Equal(t, Changes{Sheet: "Sheet1", Range: "Sheet1!B1:B5", RowsUpdated: 5, CellsChanged: 6}, result.Changes)

	rows, err := Read(context.Background(), path, ReadOptions{Header: HeaderSpec{Mode: HeaderNone}, Formulas: FormulaText})
	require.NoError(t, err)
	assert.Equal(t, "Acme", rows.Rows[0]["B"])
	assert.Equal(t, "2026-10-01", rows.Rows[1]["B"], "an ISO string becomes a date")
	assert.Equal(t, int64(3), rows.Rows[2]["B"])
	assert.Equal(t, "=B3*B4", rows.Rows[4]["B"])
	// The formula replaced a text cell: a default read evaluates it, since
	// the formula has no cached value, rather than returning the index of
	// the text the cell used to hold.
	cached, err := Read(context.Background(), path, ReadOptions{Header: HeaderSpec{Mode: HeaderNone}})
	require.NoError(t, err)
	assert.Equal(t, int64(30), cached.Rows[4]["B"])
	assert.Contains(t, cached.Warnings, "Sheet1!B5: formula had no cached value; evaluated")
	other, err := Read(context.Background(), path, ReadOptions{Sheet: "My Sheet", Header: HeaderSpec{Mode: HeaderNone}})
	require.NoError(t, err)
	assert.Nil(t, other.Rows[0]["A"])
	assert.Equal(t, "2026-10-01", other.Rows[0]["B"])

	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	bold, err := f.GetCellStyle("Sheet1", "B5")
	require.NoError(t, err)
	assert.NotEqual(t, 0, bold, "the formula cell keeps its bold style")
	dateStyle, err := f.GetCellStyle("Sheet1", "B2")
	require.NoError(t, err)
	assert.NotEqual(t, 0, dateStyle, "a date into a plain cell gains a date format")
	kind, err := f.GetCellType("My Sheet", "B1")
	require.NoError(t, err)
	assert.Equal(t, excelize.CellTypeSharedString, kind, "a pinned string stays text")

	// Writing the same things again changes nothing, dates included.
	again, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{
		"Customer": {Value: "Acme"}, "B2": {Value: "2026-10-01"}, "B3": {Value: int64(3)}, "B5": {Formula: "B3*B4"}, "'My Sheet'!A1": {Clear: true},
	}})
	require.NoError(t, err)
	assert.Equal(t, 0, again.Changes.CellsChanged)
	assert.Equal(t, "", again.Changes.Range)
}

func TestWriteCellsRejectsBadRequests(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Output: path, Cells: map[string]CellValue{"B1": {Value: 1}}})
	require.ErrorContains(t, err, "output must be a different file from path")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Output: filepath.Join(filepath.Dir(path), ".", "template.xlsx"), Cells: map[string]CellValue{"B1": {Value: 1}}})
	require.ErrorContains(t, err, "output must be a different file from path", "a spelling of the same path counts")
	_, err = ParseCells(map[string]any{"A1": map[string]any{"value": map[string]any{"nested": 1}}})
	require.ErrorContains(t, err, "cells.A1: value must be a scalar or null")
	_, err = ParseCells(map[string]any{"A1": map[string]any{"value": []any{1}}})
	require.ErrorContains(t, err, "cells.A1: value must be a scalar or null")

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for name, cells := range map[string]map[string]CellValue{
		"absolute":     {"B3": {Value: 3}, "$B$3": {Value: 2}},
		"defined name": {"Customer": {Value: "Acme"}, "Sheet1!B1": {Value: "Beta"}},
	} {
		_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: cells})
		require.ErrorContains(t, err, "name the same cell Sheet1!B", name)
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused request writes nothing")
}

func TestWriteCellsChangesTheStoredKind(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	// Text that looks like a date, written as a date, is a change: the cell
	// reads back as the same text either way, so only the stored kind tells.
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: "2026-10-01", Type: TypeString}}})
	require.NoError(t, err)
	result, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: "2026-10-01"}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.CellsChanged, "a date over text is a change")
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	kind, err := f.GetCellType("Sheet1", "B2")
	require.NoError(t, f.Close())
	require.NoError(t, err)
	assert.NotContains(t, []excelize.CellType{excelize.CellTypeSharedString, excelize.CellTypeInlineString}, kind, "the cell now holds a date")

	// And text pinned over the date is a change back.
	result, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: "2026-10-01", Type: TypeString}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.CellsChanged, "text over a date is a change")
	result, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: "2026-10-01", Type: TypeString}}})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Changes.CellsChanged, "the same text again is not")
}

func TestWriteCellsRejectsAHardLinkedOutput(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	link := filepath.Join(filepath.Dir(path), "link.xlsx")
	if err := os.Link(path, link); err != nil {
		t.Skipf("hard links are not supported here: %v", err)
	}
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Output: link, Cells: map[string]CellValue{"B1": {Value: 1}}})
	require.ErrorContains(t, err, "output must be a different file from path")
}

func TestWriteCellsOutputLeavesTheTemplateAlone(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	output := filepath.Join(filepath.Dir(path), "filled.xlsx")
	result, err := WriteCells(context.Background(), path, WriteCellsOptions{Output: output, Cells: map[string]CellValue{"B1": {Value: "Acme"}}})
	require.NoError(t, err)
	assert.Equal(t, output, result.Path)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the template is byte-identical")
	filled, err := Read(context.Background(), output, ReadOptions{Header: HeaderSpec{Mode: HeaderNone}})
	require.NoError(t, err)
	assert.Equal(t, "Acme", filled.Rows[0]["B"])

	// A second fill overwrites the output atomically.
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Output: output, Cells: map[string]CellValue{"B1": {Value: "Beta"}}})
	require.NoError(t, err)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "no temporary file is left behind")

	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Output: filepath.Join(filepath.Dir(path), "out.csv"), Cells: map[string]CellValue{"B1": {Value: 1}}})
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestWriteCellsWaitsForOutput(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	output := filepath.Join(t.TempDir(), "filled.xlsx")
	holdWorkbook(t, output)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := WriteCells(ctx, path, WriteCellsOptions{
		Output: output,
		Cells:  map[string]CellValue{"B1": {Value: "Acme"}},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(output)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestWriteCellsDryRunAndErrors(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	result, err := WriteCells(context.Background(), path, WriteCellsOptions{DryRun: true, Cells: map[string]CellValue{"B1": {Value: "Acme"}}})
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, 1, result.Changes.CellsChanged)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	for name, cells := range map[string]map[string]CellValue{
		"range":       {"A1:B2": {Value: 1}},
		"named range": {"Block": {Value: 1}},
		"bare column": {"B": {Value: 1}},
	} {
		_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: cells})
		require.ErrorContains(t, err, "is not a single cell", name)
	}
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"Nope!A1": {Value: 1}}})
	var missingSheet *SheetNotFoundError
	require.ErrorAs(t, err, &missingSheet)
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B1": {Value: "x", Type: TypeNumber}}})
	require.ErrorContains(t, err, `template.xlsx Sheet1!B1: expected number, found "x"`)
	_, err = WriteCells(context.Background(), filepath.Join(t.TempDir(), "none.xlsx"), WriteCellsOptions{Cells: map[string]CellValue{"B1": {Value: 1}}})
	var missing *NotFoundError
	require.ErrorAs(t, err, &missing)
}

// merge joins ranges before the cells are written, so a title can span a
// filled template, and an address inside a new merged cell writes its
// top-left cell.
func TestWriteCellsMerge(t *testing.T) {
	t.Parallel()
	path := templateBook(t)
	result, err := WriteCells(context.Background(), path, WriteCellsOptions{
		Merge: []string{"A1:C1", "Sheet1!A7:B8"},
		Cells: map[string]CellValue{"B1": {Value: "Acme"}, "B8": {Value: "notes"}},
	})
	require.NoError(t, err)
	assert.Equal(t, Changes{Sheet: "Sheet1", Range: "Sheet1!A1:A7", RowsUpdated: 2, CellsChanged: 2, Merged: 2}, result.Changes)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	merges, err := f.GetMergeCells("Sheet1")
	require.NoError(t, err)
	require.Len(t, merges, 2)
	assert.Equal(t, "A1:C1", merges[0].GetStartAxis()+":"+merges[0].GetEndAxis())
	title, err := f.GetCellValue("Sheet1", "A1")
	require.NoError(t, err)
	assert.Equal(t, "Acme", title, "an address inside the new merged cell writes its top-left cell")
	notes, err := f.GetCellValue("Sheet1", "A7")
	require.NoError(t, err)
	assert.Equal(t, "notes", notes)
	require.NoError(t, f.Close())

	again, err := WriteCells(context.Background(), path, WriteCellsOptions{Merge: []string{"A1:C1"}, Cells: map[string]CellValue{"A1": {Value: "Acme"}}})
	require.NoError(t, err)
	assert.Equal(t, Changes{Sheet: "Sheet1"}, again.Changes, "a range already merged exactly is not a change")

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	dry, err := WriteCells(context.Background(), path, WriteCellsOptions{DryRun: true, Merge: []string{"A10:C10"}, Cells: map[string]CellValue{"A10": {Value: 1}}})
	require.NoError(t, err)
	assert.Equal(t, 1, dry.Changes.Merged)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a dry run merges nothing")

	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Merge: []string{"B1:D1"}, Cells: map[string]CellValue{"A3": {Value: 1}}})
	require.EqualError(t, err, "template.xlsx: merge B1:D1 overlaps merged cell Sheet1!A1:C1")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Merge: []string{"A3:B3"}, Cells: map[string]CellValue{"A4": {Value: 1}}})
	require.EqualError(t, err, "template.xlsx: merge A3:B3 would discard the value of Sheet1!B3; clear it first")
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Merge: []string{"Nope!A1:B1"}, Cells: map[string]CellValue{"A3": {Value: 1}}})
	var missingSheet *SheetNotFoundError
	require.ErrorAs(t, err, &missingSheet)
	f, err = excelize.OpenFile(path)
	require.NoError(t, err)
	merges, err = f.GetMergeCells("Sheet1")
	require.NoError(t, err)
	assert.Len(t, merges, 2, "a refused request merges nothing")
	require.NoError(t, f.Close())
}

func TestCheckMergeRange(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"A1:C1", "Sheet1!A5:B6", "'My Sheet'!$A$1:$C$1", " a1:c1 "} {
		assert.NoError(t, CheckMergeRange(ref), ref)
	}
	for _, ref := range []string{"A1", "A1:A", "A:C", "Total", "", "Sheet1!B2", "A1:A1"} {
		assert.EqualError(t, CheckMergeRange(ref), fmt.Sprintf("%q is not a range", ref), ref)
	}
}

// mergedTemplate is a template whose total box spans B2:D2 and is named
// Total, the way Excel names a merged cell: over its whole area.
func mergedTemplate(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "Customer", "")
	setRow(t, f, "Sheet1", "A2", "Total", "old")
	require.NoError(t, f.MergeCell("Sheet1", "B2", "D2"))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Total", RefersTo: "Sheet1!$B$2:$D$2"}))
	return saveBook(t, f, "merged.xlsx")
}

func TestWriteCellsMergedCell(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"Total", "C2", "B2:D2"} {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			path := mergedTemplate(t)
			result, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{addr: {Value: int64(42)}}})
			require.NoError(t, err)
			assert.Equal(t, Changes{Sheet: "Sheet1", Range: "Sheet1!B2:B2", RowsUpdated: 1, CellsChanged: 1}, result.Changes)
			f, err := excelize.OpenFile(path)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()
			total, err := f.GetCellValue("Sheet1", "B2")
			require.NoError(t, err)
			assert.Equal(t, "42", total)
		})
	}
}

func TestWriteCellsRejectsTwoAddressesInOneMergedCell(t *testing.T) {
	t.Parallel()
	path := mergedTemplate(t)
	before := fileHash(t, path)
	_, err := WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2": {Value: 1}, "C2": {Value: 2}}})
	require.EqualError(t, err, `merged.xlsx: "B2" and "C2" name the same merged cell Sheet1!B2:D2`)
	_, err = WriteCells(context.Background(), path, WriteCellsOptions{Cells: map[string]CellValue{"B2:C2": {Value: 1}}})
	require.ErrorContains(t, err, `"B2:C2" is not a single cell`, "a range that is only part of a merged cell")
	assert.Equal(t, before, fileHash(t, path))
}
