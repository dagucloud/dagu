// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestReadCellsTypedValues(t *testing.T) {
	t.Parallel()
	path := formBook(t)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("Sheet1", "B11", "00123"))
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())

	result, err := ReadCells(context.Background(), path, ReadCellsOptions{Cells: map[string]string{
		"quote_no": "B3",
		"delivery": "B5",
		"total":    "B7",
		"code":     "B11",
		"agreed":   "C11",
		"title":    "Sheet1!B1",
		"note":     "'Sheet1'!B9",
	}})
	require.NoError(t, err)
	assert.Equal(t, "Sheet1", result.Sheet)
	assert.Equal(t, "Q-2026-001", result.Values["quote_no"])
	assert.Equal(t, "2026-10-15", result.Values["delivery"], "a dated number reads as an ISO date")
	assert.Equal(t, int64(123000), result.Values["total"])
	assert.Equal(t, "00123", result.Values["code"], "leading zeros survive")
	assert.Equal(t, true, result.Values["agreed"])
	assert.Equal(t, "御見積書", result.Values["title"], "a covered cell reads its merged region's value")
	assert.Equal(t, "Sheet1!A1", result.Cells["title"], "and is reported at the region's origin")
	assert.Equal(t, "Sheet1!B3", result.Cells["quote_no"])
	assert.Empty(t, result.Warnings)
}

func TestReadCellsEmptyAndAbsent(t *testing.T) {
	t.Parallel()
	result, err := ReadCells(context.Background(), formBook(t), ReadCellsOptions{Cells: map[string]string{
		"empty":  "D5",
		"absent": "",
	}})
	require.NoError(t, err)
	assert.Nil(t, result.Values["empty"])
	assert.Equal(t, "Sheet1!D5", result.Cells["empty"])
	assert.Nil(t, result.Values["absent"])
	assert.Equal(t, "", result.Cells["absent"])
}

func TestReadCellsPinnedTypes(t *testing.T) {
	t.Parallel()
	result, err := ReadCells(context.Background(), formBook(t), ReadCellsOptions{
		Cells: map[string]string{"total": "B7", "quote_no": "B3"},
		Types: map[string]ColumnType{"total": TypeString, "quote_no": TypeString},
	})
	require.NoError(t, err)
	assert.Equal(t, "123000", result.Values["total"], "a pinned string reads the number as text")

	_, err = ReadCells(context.Background(), formBook(t), ReadCellsOptions{
		Cells: map[string]string{"total": "B3"},
		Types: map[string]ColumnType{"total": TypeNumber},
	})
	require.EqualError(t, err, `quote.xlsx Sheet1!B3: expected number, found "Q-2026-001"`)
}

func TestReadCellsRejectsBadAddresses(t *testing.T) {
	t.Parallel()
	path := formBook(t)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	_, err = f.NewSheet("Other")
	require.NoError(t, err)
	require.NoError(t, f.Save())
	require.NoError(t, f.Close())

	for name, tc := range map[string]struct {
		cells map[string]string
		rng   string
		want  string
	}{
		"range":       {cells: map[string]string{"total": "A1:B2"}, want: `quote.xlsx: field "total": "A1:B2" is not a single cell`},
		"open range":  {cells: map[string]string{"total": "B7:B"}, want: `quote.xlsx: field "total": "B7:B" is not a single cell`},
		"other sheet": {cells: map[string]string{"total": "Other!B2"}, want: `quote.xlsx: field "total": "Other!B2" is not on sheet Sheet1`},
		"outside":     {cells: map[string]string{"total": "B7"}, rng: "A1:D5", want: `quote.xlsx: field "total": "B7" is outside Sheet1!A1:D5`},
		"not a cell":  {cells: map[string]string{"total": "123000"}, want: `quote.xlsx: field "total": "123000" is not a cell address`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadCells(context.Background(), path, ReadCellsOptions{Cells: tc.cells, Range: tc.rng})
			var addr *AddressError
			require.ErrorAs(t, err, &addr)
			assert.EqualError(t, err, tc.want)
		})
	}
}
