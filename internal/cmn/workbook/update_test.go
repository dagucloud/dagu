// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func ordersBook(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orders.xlsx")
	table := Table{
		Columns: []string{"Invoice No", "Amount", "Status", "Due"},
		Rows: [][]any{
			{"INV-1", int64(10), "", "2026-10-01"},
			{"INV-2", int64(20), "Done", "2026-10-02"},
			{"INV-3", int64(30), "", nil},
		},
	}
	_, err := Write(context.Background(), path, table, WriteOptions{Sheet: "Orders", Header: true})
	require.NoError(t, err)
	return path
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return sha256.Sum256(data)
}

func TestUpdateRowsByKeyAndSet(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	set, err := ParseSet(map[string]any{"Status": "status", "Checked": map[string]any{"value": "yes"}, "Due": "due"})
	require.NoError(t, err)
	rows := []Row{
		{"Invoice No": "INV-1", "status": "Submitted", "due": "2026-11-01"},
		{"Invoice No": "INV-3", "status": "Failed", "due": nil},
		{"Invoice No": "INV-2", "status": "Done", "due": "2026-10-02"},
	}
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Sheet: "Orders", Key: "Invoice No", Rows: rows, Set: set})
	require.NoError(t, err)
	// INV-1 changes Status, Checked, and Due; INV-3 Status and Checked; INV-2 only Checked.
	assert.Equal(t, Changes{Sheet: "Orders", Range: "Orders!A2:E4", RowsUpdated: 3, ColumnsAdded: 1, CellsChanged: 6}, result.Changes)

	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Invoice No", "Amount", "Status", "Due", "Checked"}, back.Headers)
	assert.Equal(t, "Submitted", back.Rows[0]["Status"])
	assert.Equal(t, "2026-11-01", back.Rows[0]["Due"])
	assert.Equal(t, "yes", back.Rows[0]["Checked"])
	assert.Equal(t, "Done", back.Rows[1]["Status"])
	assert.Equal(t, "Failed", back.Rows[2]["Status"])
	assert.Nil(t, back.Rows[2]["Due"])
	assert.Equal(t, int64(10), back.Rows[0]["Amount"], "untouched columns keep their values")

	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	header, err := f.GetCellStyle("Orders", "D1")
	require.NoError(t, err)
	added, err := f.GetCellStyle("Orders", "E1")
	require.NoError(t, err)
	assert.Equal(t, header, added, "the new header cell copies the header style")
	due, err := f.GetCellStyle("Orders", "D2")
	require.NoError(t, err)
	dueStyle, err := f.GetStyle(due)
	require.NoError(t, err)
	require.NotNil(t, dueStyle.CustomNumFmt)
	assert.Equal(t, fmtDate, *dueStyle.CustomNumFmt, "a date written into a date column keeps the format")
}

func TestUpdateRowsDefaultSetAndRowNumber(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	read, err := Read(context.Background(), path, ReadOptions{Where: map[string]any{"Status": ""}})
	require.NoError(t, err)
	require.Equal(t, 2, read.Count)
	for _, row := range read.Rows {
		row["Status"] = "Done"
		row["Amount"] = row["Amount"].(int64) + 1
	}
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: read.Rows})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Changes.RowsUpdated)
	assert.Equal(t, 4, result.Changes.CellsChanged, "Due is unchanged and the key is never written")
	assert.Equal(t, 0, result.Changes.ColumnsAdded)

	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(11), back.Rows[0]["Amount"])
	assert.Equal(t, "Done", back.Rows[0]["Status"])
	assert.Equal(t, int64(20), back.Rows[1]["Amount"])

	// key: _row addresses rows by number alone.
	result, err = UpdateRows(context.Background(), path, UpdateOptions{Key: RowNumberKey, Rows: []Row{{RowNumberKey: 4, "Status": "Archived"}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.RowsUpdated)
	back, err = Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Archived", back.Rows[2]["Status"])
}

func TestUpdateRowsShapeChecksLeaveFileUntouched(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	before := fileHash(t, path)
	rows := []Row{{"Invoice No": "INV-1", "Status": "x"}}

	_, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice", Rows: rows})
	require.EqualError(t, err, `orders.xlsx Orders: key column "Invoice" not found in header row 1; headers present: Invoice No, Amount, Status, Due`)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "invoice no", Rows: rows})
	require.EqualError(t, err, `orders.xlsx Orders: key column "invoice no" not found in header row 1; did you mean "Invoice No"?`)

	set, err := ParseSet(map[string]any{"status": "Status"})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: rows, Set: set})
	require.EqualError(t, err, `orders.xlsx Orders: column "status" not found in header row 1; did you mean "Status"?`)

	moved := []Row{{"Invoice No": "INV-1", RowNumberKey: 3, "Status": "x"}}
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: moved})
	require.EqualError(t, err, `orders.xlsx Orders!A3: expected key "INV-1", found "INV-2"; the sheet changed since it was read`)

	outside := []Row{{"Invoice No": "INV-1", RowNumberKey: 9, "Status": "x"}}
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: outside})
	require.ErrorContains(t, err, "row 9 is outside the data rows 2 to 4")

	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-9", "Status": "x"}}})
	require.EqualError(t, err, `orders.xlsx Orders: key "INV-9" not found`)

	keyOnly := []Row{{"Invoice No": "INV-1"}}
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: keyOnly})
	require.ErrorContains(t, err, "rows carry no fields to write")

	setKey, err := ParseSet(map[string]any{"Invoice No": "x"})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-1", "x": 1}}, Set: setKey})
	require.ErrorContains(t, err, "the key column cannot be updated")

	assert.Equal(t, before, fileHash(t, path), "no failed update touched the file")
}

func TestUpdateRowsMissingModesAndDuplicates(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	rows := []Row{{"Invoice No": "INV-9", "Status": "New", "Amount": 90}, {"Invoice No": "INV-1", "Status": "Seen"}}

	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: rows, Missing: MissingSkip, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.RowsUpdated)
	assert.Equal(t, 0, result.Changes.RowsAppended)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], `key "INV-9" not found; row skipped`)
	assert.True(t, result.DryRun)

	result, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: rows, Missing: MissingAppend})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.RowsUpdated)
	assert.Equal(t, 1, result.Changes.RowsAppended)
	assert.Equal(t, "Orders!A2:D5", result.Changes.Range)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	require.Equal(t, 4, back.Count)
	assert.Equal(t, "INV-9", back.Rows[3]["Invoice No"])
	assert.Equal(t, int64(90), back.Rows[3]["Amount"])
	assert.Equal(t, "New", back.Rows[3]["Status"])
	assert.Equal(t, "Seen", back.Rows[0]["Status"])

	dup := Table{Columns: []string{"k", "v"}, Rows: [][]any{{"a", 1}, {"a", 2}}}
	dupPath := filepath.Join(t.TempDir(), "dup.xlsx")
	_, err = Write(context.Background(), dupPath, dup, WriteOptions{Header: true})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), dupPath, UpdateOptions{Key: "k", Rows: []Row{{"k": "a", "v": 3}}})
	require.EqualError(t, err, `dup.xlsx Sheet1: key "a" appears at rows 2 and 3`)

	// Numeric keys match whether they arrive as numbers or text.
	numPath := filepath.Join(t.TempDir(), "num.xlsx")
	_, err = Write(context.Background(), numPath, Table{Columns: []string{"id", "v"}, Rows: [][]any{{int64(7), "a"}}}, WriteOptions{Header: true})
	require.NoError(t, err)
	result, err = UpdateRows(context.Background(), numPath, UpdateOptions{Key: "id", Rows: []Row{{"id": "7", "v": "b"}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.RowsUpdated)
}

func TestParseSetAndDecodeUpdateRows(t *testing.T) {
	t.Parallel()
	set, err := ParseSet(map[string]any{"Status": "status", "Note": map[string]any{"value": 3}})
	require.NoError(t, err)
	assert.Equal(t, map[string]SetValue{"Status": {Field: "status"}, "Note": {Literal: 3, IsLiteral: true}}, set)
	// A literal that is a canonical number in text, the form a reference
	// arrives in, becomes a number unless a type pins it.
	literals, err := ParseSet(map[string]any{
		"Amount": map[string]any{"value": "100"},
		"Code":   map[string]any{"value": "007", "type": "string"},
		"Rate":   map[string]any{"value": "1,234.5", "type": "number"},
		"Memo":   map[string]any{"value": "007"},
	})
	require.NoError(t, err)
	assert.Equal(t, SetValue{Literal: int64(100), IsLiteral: true}, literals["Amount"])
	assert.Equal(t, SetValue{Literal: "007", IsLiteral: true, Type: TypeString}, literals["Code"])
	assert.Equal(t, SetValue{Literal: "1,234.5", IsLiteral: true, Type: TypeNumber}, literals["Rate"])
	assert.Equal(t, SetValue{Literal: "007", IsLiteral: true}, literals["Memo"], "leading zeros stay text")
	for _, bad := range []any{"status", map[string]any{}, map[string]any{"a": 1}, map[string]any{"a": map[string]any{"x": 1}}, map[string]any{"": "f"},
		map[string]any{"a": map[string]any{"value": 1, "type": "money"}}, map[string]any{"a": map[string]any{"value": 1, "bold": true}},
		map[string]any{"a": map[string]any{"value": 1, "type": 2}}} {
		_, err := ParseSet(bad)
		require.Error(t, err, "%v", bad)
	}
	_, err = ParseSet(map[string]any{"a": map[string]any{"value": 1, "type": "money"}})
	require.ErrorContains(t, err, `set.a: type: unknown column type "money"`)
	nilSet, err := ParseSet(nil)
	require.NoError(t, err)
	assert.Nil(t, nilSet)

	rows, err := DecodeUpdateRows(`[{"k": "a", "_row": 2}, {"k": "b"}]`)
	require.NoError(t, err)
	assert.Equal(t, []Row{{"k": "a", RowNumberKey: 2}, {"k": "b"}}, rows)
	rows, err = DecodeUpdateRows(map[string]any{"k": "a", RowNumberKey: float64(5)})
	require.NoError(t, err)
	assert.Equal(t, 5, rows[0][RowNumberKey])
	_, err = DecodeUpdateRows(`[1]`)
	require.Error(t, err)
	_, err = DecodeUpdateRows(`[{"_row": "two"}]`)
	require.Error(t, err)
	_, err = DecodeUpdateRows("nope")
	require.Error(t, err)
}

func TestUpdateRowsLiteralNumericText(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	set, err := ParseSet(map[string]any{
		"Amount": map[string]any{"value": "100"},
		"Code":   map[string]any{"value": "007", "type": "string"},
		"Rate":   map[string]any{"value": "1,234.5", "type": "number"},
	})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-1"}}, Set: set})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(100), back.Rows[0]["Amount"], "a canonical numeric literal is a number")
	assert.Equal(t, "007", back.Rows[0]["Code"], "a pinned string keeps its zeros")
	assert.Equal(t, 1234.5, back.Rows[0]["Rate"], "a pinned number converts the way a column type does")
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

func TestUpdateRowsTypedLiteralReplacesTextThatReadsTheSame(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	text, err := ParseSet(map[string]any{"Amount": map[string]any{"value": "100", "type": "string"}})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-1"}}, Set: text})
	require.NoError(t, err)
	number, err := ParseSet(map[string]any{"Amount": map[string]any{"value": "100", "type": "number"}})
	require.NoError(t, err)
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-1"}}, Set: number})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.CellsChanged, "a number over text that reads the same is a change")
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(100), back.Rows[0]["Amount"])
}

func TestUpdateRowsDatetimeLiteralKeepsItsFormatAtMidnight(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	// Amount is a number column, so the literal brings its own format, as a
	// date written into a plain cell does; a date column would keep its own.
	set, err := ParseSet(map[string]any{"Amount": map[string]any{"value": "2026-10-01T00:00:00", "type": "datetime"}})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-1"}}, Set: set})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01T00:00:00", back.Rows[0]["Amount"], "a literal pinned to datetime keeps a date-time format even at midnight")
}

// mergedBook is an order slip: order 1 has two lines, rows 2 and 3, whose
// Order, Status, and Due cells are merged, and order 2 has one line. The
// merges replace the default ones when given.
func mergedBook(t *testing.T, merges ...string) string {
	t.Helper()
	f := excelize.NewFile()
	const s = "Sheet1"
	setRow(t, f, s, "A1", "ID", "Order", "Status", "Due")
	setRow(t, f, s, "A2", 1, "A-1")
	setRow(t, f, s, "A3", 2)
	setRow(t, f, s, "A4", 3, "A-2")
	if len(merges) == 0 {
		merges = []string{"B2:B3", "C2:C3", "D2:D3"}
	}
	for _, m := range merges {
		first, last, _ := strings.Cut(m, ":")
		require.NoError(t, f.MergeCell(s, first, last))
	}
	return saveBook(t, f, "merged.xlsx")
}

func TestUpdateRowsMergedCellConflict(t *testing.T) {
	t.Parallel()
	path := mergedBook(t)
	before := fileHash(t, path)
	_, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 1, "Status": "first"}, {"ID": 2, "Status": "second"}}})
	require.EqualError(t, err, "merged.xlsx Sheet1: rows[0] and rows[1] write different values to merged cell Sheet1!C2:C3")
	// A row whose value the cell already holds still claims it.
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 1, "Status": nil}, {"ID": 2, "Status": "second"}}})
	require.EqualError(t, err, "merged.xlsx Sheet1: rows[0] and rows[1] write different values to merged cell Sheet1!C2:C3")
	assert.Equal(t, before, fileHash(t, path))
}

func TestUpdateRowsMergedCellEqualValues(t *testing.T) {
	t.Parallel()
	path := mergedBook(t)
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 1, "Status": "Done"}, {"ID": 2, "Status": "Done"}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.CellsChanged, "the merged cell is written once")

	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Done", back.Rows[0]["Status"])
	assert.Equal(t, "Done", back.Rows[1]["Status"])
	assert.Nil(t, back.Rows[2]["Status"])
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	merges, err := f.GetMergeCells("Sheet1")
	require.NoError(t, err)
	assert.Len(t, merges, 3)
}

func TestUpdateRowsMergedCellFromItsSecondRow(t *testing.T) {
	t.Parallel()
	path := mergedBook(t)
	_, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 2, "Status": "Done", "Due": "2026-11-01"}}})
	require.NoError(t, err)
	back, err := Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Done", back.Rows[0]["Status"])
	assert.Equal(t, "2026-11-01", back.Rows[0]["Due"], "the date format lands on the cell that shows the date")

	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 2, "Status": nil}}})
	require.NoError(t, err)
	back, err = Read(context.Background(), path, ReadOptions{})
	require.NoError(t, err)
	assert.Nil(t, back.Rows[0]["Status"])
}

// A merged cell reaching outside the column's data cells would carry the
// write into the key, the header, or a column the update does not set.
func TestUpdateRowsMergedCellOutsideItsColumn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, merge, message string
		row                  Row
		missing              MissingMode
	}{
		{"key column", "A4:C4", `merged.xlsx Sheet1!C4: merged cell A4:C4 reaches outside column "Status" of the data rows; unmerge it to write this cell`, Row{"ID": 3, "Status": "Done"}, ""},
		{"header row", "C1:C2", `merged.xlsx Sheet1!C2: merged cell C1:C2 reaches outside column "Status" of the data rows; unmerge it to write this cell`, Row{"ID": 1, "Status": "Done"}, ""},
		{"appended row", "A5:D6", `merged.xlsx Sheet1!A5: merged cell A5:D6 reaches outside column "ID" of the data rows; unmerge it to write this cell`, Row{"ID": 9, "Status": "New"}, MissingAppend},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := mergedBook(t, tc.merge)
			before := fileHash(t, path)
			_, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{tc.row}, Missing: tc.missing})
			require.EqualError(t, err, tc.message)
			assert.Equal(t, before, fileHash(t, path))
		})
	}
}

func TestUpdateRowsComparesTextExactly(t *testing.T) {
	t.Parallel()
	path := ordersBook(t)
	result, err := UpdateRows(context.Background(), path, UpdateOptions{Key: "Invoice No", Rows: []Row{{"Invoice No": "INV-2", "Status": "Done "}}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changes.CellsChanged, "surrounding space is part of the value")

	merged := mergedBook(t)
	_, err = UpdateRows(context.Background(), merged, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 1, "Status": "Ready"}, {"ID": 2, "Status": "Ready "}}})
	require.ErrorContains(t, err, "rows[0] and rows[1] write different values to merged cell Sheet1!C2:C3")
}

// A header cell merged across the column a new one would take: the new
// header would land in the merged cell's top-left cell, an existing header.
func TestUpdateRowsAddedColumnUnderMergedHeader(t *testing.T) {
	t.Parallel()
	path := mergedBook(t, "D1:E1")
	before := fileHash(t, path)
	set, err := ParseSet(map[string]any{"Checked": map[string]any{"value": "yes"}})
	require.NoError(t, err)
	_, err = UpdateRows(context.Background(), path, UpdateOptions{Key: "ID", Rows: []Row{{"ID": 1}}, Set: set})
	require.EqualError(t, err, `merged.xlsx Sheet1!E1: merged cell D1:E1 covers the header cell of new column "Checked"; unmerge it to add the column`)
	assert.Equal(t, before, fileHash(t, path))
}
