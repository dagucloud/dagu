// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// formBook is a quote laid out as a form: a merged title, labels in column
// A, values in column B, and a note.
func formBook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	title := styleID(t, f, &excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDEBF7"}},
	})
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	require.NoError(t, f.SetCellValue("Sheet1", "A1", "御見積書"))
	require.NoError(t, f.MergeCell("Sheet1", "A1", "D1"))
	require.NoError(t, f.SetCellStyle("Sheet1", "A1", "D1", title))
	require.NoError(t, f.SetCellValue("Sheet1", "A3", "見積番号"))
	require.NoError(t, f.SetCellStyle("Sheet1", "A3", "A3", bold))
	require.NoError(t, f.SetCellValue("Sheet1", "B3", "Q-2026-001"))
	require.NoError(t, f.SetCellValue("Sheet1", "A5", "納期"))
	setStyled(t, f, "Sheet1", "B5", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), custom("yyyy-mm-dd"))
	require.NoError(t, f.SetCellValue("Sheet1", "A7", "合計金額"))
	require.NoError(t, f.SetCellValue("Sheet1", "B7", 123000))
	require.NoError(t, f.SetCellValue("Sheet1", "A9", "備考\n欄"))
	require.NoError(t, f.SetCellValue("Sheet1", "B9", strings.Repeat("あ", 250)))
	require.NoError(t, f.SetCellValue("Sheet1", "C11", true))
	return saveBook(t, f, "quote.xlsx")
}

func TestLayoutListsCells(t *testing.T) {
	t.Parallel()
	layout, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: true})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1", layout.Sheet)
	assert.Equal(t, "Sheet1!A1:C11", layout.Range, "the used range comes from the cells that hold values; a merge only reaches further")
	assert.Equal(t, 10, layout.Cells)
	lines := strings.Split(layout.Listing, "\n")
	require.Len(t, lines, 10)
	assert.Equal(t, "A1:D1 [text,bold,fill]: 御見積書", lines[0], "a merged region is listed once by its range")
	assert.Equal(t, "A3 [text,bold]: 見積番号", lines[1])
	assert.Equal(t, "B3 [text]: Q-2026-001", lines[2])
	assert.Equal(t, "A5 [text]: 納期", lines[3])
	assert.Equal(t, "B5 [date]: 2026-10-15", lines[4])
	assert.Equal(t, "A7 [text]: 合計金額", lines[5])
	assert.Equal(t, "B7 [number]: 123000", lines[6])
	assert.Equal(t, "A9 [text]: 備考 欄", lines[7], "line breaks become spaces")
	assert.Equal(t, "B9 [text]: "+strings.Repeat("あ", 200), lines[8], "text is cut to 200 characters")
	assert.Equal(t, "C11 [bool]: true", lines[9])
	assert.Equal(t, "備考 欄", layout.Labels["A9"])
	assert.Equal(t, "御見積書", layout.Labels["A1"], "a merged label is keyed by its origin cell")
	_, listed := layout.Labels["B7"]
	assert.False(t, listed, "a number is not a label")
}

func TestLayoutCutsLongText(t *testing.T) {
	t.Parallel()
	layout, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: true, Range: "B9"})
	require.NoError(t, err)
	assert.Equal(t, "B9 [text]: "+strings.Repeat("あ", 200), layout.Listing)
	assert.Equal(t, strings.Repeat("あ", 250), layout.Labels["B9"], "the label keeps its full text")
}

func TestLayoutSendValuesFalse(t *testing.T) {
	t.Parallel()
	layout, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: false})
	require.NoError(t, err)
	assert.Contains(t, layout.Listing, "A7 [text]: 合計金額\n")
	assert.Contains(t, layout.Listing, "B7 [number]\n")
	assert.Contains(t, layout.Listing, "B5 [date]\n")
	assert.Contains(t, layout.Listing, "C11 [bool]")
	assert.NotContains(t, layout.Listing, "123000")
	assert.NotContains(t, layout.Listing, "2026-10-15")
}

func TestLayoutRangeLimitsListing(t *testing.T) {
	t.Parallel()
	layout, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: true, Range: "A3:B5"})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1!A3:B5", layout.Range)
	assert.Equal(t, 4, layout.Cells)
	assert.NotContains(t, layout.Listing, "御見積書")
	assert.NotContains(t, layout.Listing, "123000")
}

func TestLayoutCapFails(t *testing.T) {
	t.Parallel()
	_, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: true, MaxCells: 4})
	require.EqualError(t, err, "quote.xlsx Sheet1: 10 cells in Sheet1!A1:C11 is more than 4; set range to the part of the sheet that holds the fields")
}

func TestLayoutKeyIsTheShapeNotTheText(t *testing.T) {
	t.Parallel()
	first, err := Layout(context.Background(), formBook(t), LayoutOptions{SendValues: true})
	require.NoError(t, err)

	// Another quote in the same template: every value differs.
	edited := formBook(t)
	f, err := excelize.OpenFile(edited)
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("Sheet1", "B3", "Q-2026-002"))
	require.NoError(t, f.SetCellValue("Sheet1", "B7", 99000))
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())
	second, err := Layout(context.Background(), edited, LayoutOptions{SendValues: true})
	require.NoError(t, err)
	assert.Equal(t, first.Key, second.Key, "values do not change the shape")

	// A label moved: the shape changed.
	moved := formBook(t)
	f, err = excelize.OpenFile(moved)
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("Sheet1", "A7", nil))
	require.NoError(t, f.SetCellValue("Sheet1", "A8", "合計金額"))
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())
	third, err := Layout(context.Background(), moved, LayoutOptions{SendValues: true})
	require.NoError(t, err)
	assert.NotEqual(t, first.Key, third.Key, "a moved label changes the shape")

	// A cell filled in within the same range: the shape changed.
	filled := formBook(t)
	f, err = excelize.OpenFile(filled)
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("Sheet1", "C7", "税込"))
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())
	fourth, err := Layout(context.Background(), filled, LayoutOptions{SendValues: true})
	require.NoError(t, err)
	assert.Equal(t, first.Range, fourth.Range, "the filled cell lies inside the range")
	assert.NotEqual(t, first.Key, fourth.Key, "a new filled cell changes the shape")

	// A cell filled in beyond the range: the range changed, so the shape did.
	widened := formBook(t)
	f, err = excelize.OpenFile(widened)
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("Sheet1", "D7", "税込"))
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())
	fifth, err := Layout(context.Background(), widened, LayoutOptions{SendValues: true})
	require.NoError(t, err)
	assert.NotEqual(t, first.Range, fifth.Range)
	assert.NotEqual(t, first.Key, fifth.Key, "a wider range changes the shape")
}

func TestLayoutRefusesAHugeRectangle(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	require.NoError(t, f.SetCellValue("Sheet1", "A1", "見積番号"))
	require.NoError(t, f.SetCellValue("Sheet1", "ZZ100000", "far"))
	path := filepath.Join(t.TempDir(), "sparse.xlsx")
	require.NoError(t, f.SaveAs(path))
	require.NoError(t, f.Close())
	_, err := Layout(context.Background(), path, LayoutOptions{SendValues: true})
	require.EqualError(t, err, "sparse.xlsx Sheet1: Sheet1!A1:ZZ100000 spans more than 1000000 cells; set range to the part of the sheet that holds the fields")
	layout, err := Layout(context.Background(), path, LayoutOptions{SendValues: true, Range: "A1:B5"})
	require.NoError(t, err)
	assert.Equal(t, 1, layout.Cells, "a range brings the walk down to the form")
}

func TestLayoutErrors(t *testing.T) {
	t.Parallel()
	_, err := Layout(context.Background(), formBook(t), LayoutOptions{Sheet: "Nope"})
	var missing *SheetNotFoundError
	require.ErrorAs(t, err, &missing)
	_, err = Layout(context.Background(), formBook(t), LayoutOptions{Range: "Totals"})
	require.ErrorContains(t, err, `range "Totals" is not a cell range, named range, or table`)
}
