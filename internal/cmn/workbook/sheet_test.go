// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// monthlyBook has a styled Template sheet with a table and a defined
// name, plus a Summary sheet.
func monthlyBook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	require.NoError(t, f.SetSheetName("Sheet1", "Template"))
	bold := styleID(t, f, &excelize.Style{Font: &excelize.Font{Bold: true}})
	setRow(t, f, "Template", "A1", "Item", "Amount")
	setRow(t, f, "Template", "A2", "Rent", 100)
	require.NoError(t, f.SetCellStyle("Template", "A1", "B1", bold))
	require.NoError(t, f.SetColWidth("Template", "A", "A", 30))
	require.NoError(t, f.AddTable("Template", &excelize.Table{Range: "A1:B2", Name: "Items"}))
	require.NoError(t, f.SetDefinedName(&excelize.DefinedName{Name: "Total", RefersTo: "Template!$B$2"}))
	_, err := f.NewSheet("Summary")
	require.NoError(t, err)
	setRow(t, f, "Summary", "A1", "see template")
	return saveBook(t, f, "monthly.xlsx")
}

func runSheet(t *testing.T, path string, opts SheetOptions) *SheetResult {
	t.Helper()
	result, err := Sheet(context.Background(), path, opts)
	require.NoError(t, err)
	return result
}

func TestSheetAddCopyRenameDelete(t *testing.T) {
	t.Parallel()
	path := monthlyBook(t)

	added := runSheet(t, path, SheetOptions{Operation: SheetAdd, Sheet: "Notes", Position: 1})
	assert.Equal(t, []string{"Notes", "Template", "Summary"}, added.Sheets)
	assert.Equal(t, "Notes", added.Sheet)
	assert.Equal(t, "Notes", added.Changes.Sheet)
	assert.False(t, added.Skipped)

	copied := runSheet(t, path, SheetOptions{Operation: SheetCopy, Sheet: "template", To: "October"})
	assert.Equal(t, []string{"Notes", "Template", "October", "Summary"}, copied.Sheets, "a copy lands right after its source")
	assert.Equal(t, "October", copied.Sheet)
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	value, err := f.GetCellValue("October", "B2")
	require.NoError(t, err)
	assert.Equal(t, "100", value)
	style, err := f.GetCellStyle("October", "A1")
	require.NoError(t, err)
	assert.NotEqual(t, 0, style, "styles are copied")
	width, err := f.GetColWidth("October", "A")
	require.NoError(t, err)
	assert.Equal(t, 30.0, width, "widths are copied")
	tables, err := f.GetTables("October")
	require.NoError(t, err)
	assert.Empty(t, tables, "tables are not copied")
	require.NoError(t, f.Close())

	renamed := runSheet(t, path, SheetOptions{Operation: SheetRename, Sheet: "October", To: "2026-10"})
	assert.Equal(t, []string{"Notes", "Template", "2026-10", "Summary"}, renamed.Sheets)
	assert.Equal(t, "2026-10", renamed.Sheet)

	deleted := runSheet(t, path, SheetOptions{Operation: SheetDelete, Sheet: "Notes"})
	assert.Equal(t, []string{"Template", "2026-10", "Summary"}, deleted.Sheets)
	assert.Equal(t, "Notes", deleted.Sheet)

	sheets, err := ListSheets(path, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Template", "2026-10", "Summary"}, sheets)
}

func TestSheetRenameUpdatesDefinedNamesAndCase(t *testing.T) {
	t.Parallel()
	path := monthlyBook(t)
	runSheet(t, path, SheetOptions{Operation: SheetRename, Sheet: "Template", To: "Base"})
	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var total string
	for _, dn := range f.GetDefinedName() {
		if dn.Name == "Total" {
			total = dn.RefersTo
		}
	}
	assert.Equal(t, "Base!$B$2", total, "a defined name follows the renamed sheet")

	result := runSheet(t, path, SheetOptions{Operation: SheetRename, Sheet: "base", To: "BASE"})
	assert.Equal(t, "BASE", result.Sheet, "a rename that only changes case is allowed")
	assert.False(t, result.Skipped)
}

func TestSheetExistsAndMissingModes(t *testing.T) {
	t.Parallel()
	path := monthlyBook(t)

	_, err := Sheet(context.Background(), path, SheetOptions{Operation: SheetAdd, Sheet: "summary"})
	require.ErrorContains(t, err, `monthly.xlsx: sheet "Summary" already exists`)
	skipped := runSheet(t, path, SheetOptions{Operation: SheetAdd, Sheet: "Summary", IfExists: ExistsSkip})
	assert.True(t, skipped.Skipped)
	assert.Equal(t, []string{`sheet "Summary" already exists; nothing added`}, skipped.Warnings)
	assert.Equal(t, []string{"Template", "Summary"}, skipped.Sheets)

	replaced := runSheet(t, path, SheetOptions{Operation: SheetAdd, Sheet: "Summary", IfExists: ExistsReplace})
	assert.False(t, replaced.Skipped)
	rows, err := Read(context.Background(), path, ReadOptions{Sheet: "Summary", Header: HeaderSpec{Mode: HeaderNone}})
	require.NoError(t, err)
	assert.Equal(t, 0, rows.Count, "replace empties the sheet in place")
	assert.Equal(t, []string{"Template", "Summary"}, replaced.Sheets, "and keeps its position")

	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetCopy, Sheet: "Template", To: "Summary"})
	require.ErrorContains(t, err, `sheet "Summary" already exists`)
	over := runSheet(t, path, SheetOptions{Operation: SheetCopy, Sheet: "Template", To: "Summary", IfExists: ExistsReplace})
	assert.Equal(t, []string{"Template", "Summary"}, over.Sheets)
	rows, err = Read(context.Background(), path, ReadOptions{Sheet: "Summary"})
	require.NoError(t, err)
	assert.Equal(t, int64(100), rows.Rows[0]["Amount"], "the copy replaced the sheet's contents")
	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetCopy, Sheet: "Template", To: "template"})
	require.ErrorContains(t, err, "cannot be copied onto itself")

	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetDelete, Sheet: "Nope"})
	var missing *SheetNotFoundError
	require.ErrorAs(t, err, &missing)
	skippedDelete := runSheet(t, path, SheetOptions{Operation: SheetDelete, Sheet: "Nope", Missing: MissingSkip})
	assert.True(t, skippedDelete.Skipped)
	assert.Equal(t, []string{`sheet "Nope" not found; nothing deleted`}, skippedDelete.Warnings)
	skippedCopy := runSheet(t, path, SheetOptions{Operation: SheetCopy, Sheet: "Nope", To: "X", Missing: MissingSkip})
	assert.True(t, skippedCopy.Skipped)
	assert.Equal(t, []string{"Template", "Summary"}, skippedCopy.Sheets)

	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetRename, Sheet: "Template", To: "Summary"})
	require.ErrorContains(t, err, `sheet "Summary" already exists`)
	renamedOver := runSheet(t, path, SheetOptions{Operation: SheetRename, Sheet: "Template", To: "Summary", IfExists: ExistsReplace})
	assert.Equal(t, []string{"Summary"}, renamedOver.Sheets, "replace drops the sheet in the way")
	same := runSheet(t, path, SheetOptions{Operation: SheetRename, Sheet: "Summary", To: "Summary"})
	assert.True(t, same.Skipped)
}

func TestSheetPositionBoundsAndLastSheet(t *testing.T) {
	t.Parallel()
	path := monthlyBook(t)
	_, err := Sheet(context.Background(), path, SheetOptions{Operation: SheetAdd, Sheet: "X", Position: 4})
	require.ErrorContains(t, err, "position 4 is outside 1 to 3")
	last := runSheet(t, path, SheetOptions{Operation: SheetAdd, Sheet: "X", Position: 3})
	assert.Equal(t, []string{"Template", "Summary", "X"}, last.Sheets)
	first := runSheet(t, path, SheetOptions{Operation: SheetCopy, Sheet: "Summary", To: "Y", Position: 1})
	assert.Equal(t, []string{"Y", "Template", "Summary", "X"}, first.Sheets)

	for _, name := range []string{"Y", "Template", "Summary"} {
		runSheet(t, path, SheetOptions{Operation: SheetDelete, Sheet: name})
	}
	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetDelete, Sheet: "X"})
	require.ErrorContains(t, err, `monthly.xlsx: cannot delete the only sheet "X"`)
	_, err = Sheet(context.Background(), path, SheetOptions{Operation: SheetAdd, Sheet: "Bad:Name"})
	require.ErrorContains(t, err, `invalid sheet name "Bad:Name"`)
	_, err = Sheet(context.Background(), path, SheetOptions{Operation: "move", Sheet: "X"})
	require.ErrorContains(t, err, `unknown sheet operation "move"`)
}

func TestSheetDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	path := monthlyBook(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	result := runSheet(t, path, SheetOptions{Operation: SheetDelete, Sheet: "Summary", DryRun: true})
	assert.True(t, result.DryRun)
	assert.Equal(t, []string{"Template"}, result.Sheets, "the result shows what the save would have left")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	_, err = Sheet(context.Background(), filepath.Join(t.TempDir(), "none.xlsx"), SheetOptions{Operation: SheetAdd, Sheet: "A"})
	var missing *NotFoundError
	require.ErrorAs(t, err, &missing)
}
