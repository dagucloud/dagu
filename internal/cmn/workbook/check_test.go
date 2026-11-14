// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestCheckReportsWhatARunWouldFailOn(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "Invoice No", "Amount", "Status")
	setRow(t, f, "Sheet1", "A2", "INV-1", 10, "Done")
	path := saveBook(t, f, "orders.xlsx")

	require.NoError(t, Check(context.Background(), path, CheckOptions{}))
	require.NoError(t, Check(context.Background(), path, CheckOptions{Sheet: "sheet1", Columns: []ColumnCheck{{Field: "key", Name: "invoice no"}}}),
		"sheets and columns match loosely, as the run does")

	err := Check(context.Background(), filepath.Join(t.TempDir(), "none.xlsx"), CheckOptions{})
	require.ErrorContains(t, err, "field 'with.path': none.xlsx: workbook not found")

	err = Check(context.Background(), path, CheckOptions{Sheet: "Nope"})
	require.ErrorContains(t, err, `field 'with.sheet': orders.xlsx: sheet "Nope" not found; sheets present: Sheet1`)

	err = Check(context.Background(), path, CheckOptions{Columns: []ColumnCheck{
		{Field: "key", Name: "Invoice"}, {Field: "set", Name: "Nope"}, {Field: "set", Name: "Amount"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `field 'with.key': column "Invoice" not found in header row 1; headers present: Invoice No, Amount, Status`)
	assert.Contains(t, err.Error(), `field 'with.set': column "Nope" not found in header row 1; headers present: Invoice No, Amount, Status`)
	assert.NotContains(t, err.Error(), `"Amount" not found`)

	err = Check(context.Background(), path, CheckOptions{Columns: []ColumnCheck{{Field: "columns", Name: " status "}}})
	require.NoError(t, err, "a loose match is what a read accepts")
	err = Check(context.Background(), path, CheckOptions{Columns: []ColumnCheck{{Field: "key", Name: "invoice no", Exact: true}}})
	require.ErrorContains(t, err, `field 'with.key': column "invoice no" not found in header row 1; did you mean "Invoice No"?`)
	err = Check(context.Background(), path, CheckOptions{Columns: []ColumnCheck{{Field: "columns", Name: "stat"}}})
	require.ErrorContains(t, err, `column "stat" not found in header row 1; headers present`)

	require.NoError(t, Check(context.Background(), path, CheckOptions{Header: HeaderSpec{Mode: HeaderNone}, Columns: []ColumnCheck{{Field: "columns", Name: "Z"}}}),
		"with header: false columns are letters and are not checked")

	empty := saveBook(t, excelize.NewFile(), "empty.xlsx")
	err = Check(context.Background(), empty, CheckOptions{Columns: []ColumnCheck{{Field: "key", Name: "a", Exact: true}, {Field: "set", Name: "b", Exact: true}}})
	require.ErrorContains(t, err, `field 'with.key': column "a" not found; the sheet "Sheet1" is empty`)
	require.ErrorContains(t, err, `field 'with.set': column "b" not found; the sheet "Sheet1" is empty`, "every column is reported")
	require.NoError(t, Check(context.Background(), empty, CheckOptions{Columns: []ColumnCheck{{Field: "required", Name: "a"}}}),
		"a read of an empty sheet succeeds, so its columns are not reported")

	err = Check(context.Background(), path, CheckOptions{Range: "Nope!A1:B2"})
	require.ErrorContains(t, err, `field 'with.range': orders.xlsx: sheet "Nope" not found`)
}
