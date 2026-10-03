// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Spec 077 "Writing cells": merge joins ranges before the cells are
// written, an address inside a new merged cell writes its top-left cell,
// a range already merged is not a change, and changes reports merged.
func TestXlsxWriteCellsMerge(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_cells_merge.yaml").ExpectExitCode(0)

	var summary struct {
		Fill  changes `json:"fill"`
		Again changes `json:"again"`
	}
	readJSON(t, dagu, "changes.json", &summary)
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!A1:B5", RowsUpdated: 3, CellsChanged: 3, Merged: 2}, summary.Fill)
	require.Equal(t, changes{Sheet: "Sheet1"}, summary.Again, "a range already merged exactly is left as it is")

	var out struct {
		Fill  []map[string]any `json:"fill"`
		First []map[string]any `json:"first"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, "Quote", out.Fill[0]["A"], "B1 inside the new merged cell wrote A1")
	require.Equal(t, "Quote", out.Fill[0]["B"], "a read fills the merged region")
	require.Nil(t, out.First[0]["B"], "merged: first leaves the covered cells empty")
	require.Nil(t, out.First[4]["B"])
	require.Equal(t, "Acme", out.Fill[1]["B"])
	require.Equal(t, "notes", out.Fill[4]["A"])
	require.Equal(t, "notes", out.Fill[4]["B"])
}

// Spec 077 "Errors": a merge that cannot be made, and writes that meet a
// merged cell in write_cells, appends, and update_rows.
func TestXlsxMergedCellErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"write_cells_merge_overlap.yaml", "template.xlsx: merge A1:D1 overlaps merged cell Sheet1!B1:E1"},
		{"write_cells_merge_value.yaml", "template.xlsx: merge A2:B2 would discard the value of Sheet1!B2; clear it first"},
		{"write_cells_merged_same_cell.yaml", `template.xlsx: "B10" and "C10" name the same merged cell Sheet1!B10:D10`},
		{"append_into_merged.yaml", "Sheet1!A3: cannot append into merged cell A3:B4; unmerge it to write this cell"},
		{"append_merged_header.yaml", `Sheet1!C1: merged cell B1:C1 covers the header cell of new column "note"; unmerge it to add the column`},
		{"update_rows_merged.yaml", `orders.xlsx Sheet1!C4: merged cell A4:C4 reaches outside column "Status" of the data rows; unmerge it to write this cell`},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

// Spec 077 "Japanese text under a pinned type": every family of amount,
// date, time, and yes/no form reads under its pinned type; text outside
// the forms fails the type, quoted as the cell holds it.
func TestXlsxTypesJapanese(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "types_japanese.yaml").ExpectExitCode(0)

	var out struct {
		Rows     []map[string]any `json:"rows"`
		Warnings []string         `json:"warnings"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Len(t, out.Rows, 14)
	for i, want := range []struct {
		amount float64
		due    string
		when   string
		done   any
		note   string
	}{
		{123456, "2026-10-03", "2026-10-03T14:30:00", true, "full-width digits; a slash date with a time; a circle"},
		{123000, "2026-10-03", "2026-10-03T14:30:00", false, "a yen sign; an era date; 時分; a cross"},
		{123000, "2026-10-03", "2026-10-03T14:30:15", true, "円 after; a short era date; 時分秒; はい"},
		{-5, "2019-05-01", "2026-10-03T14:30:15", false, "U+2212; the first year of an era; a dot date with seconds; いいえ"},
		{123500, "2026-10-03", "2026-10-03T14:30:00", true, "Arabic digits with 万; a weekday mark; 午後; 済"},
		{-1000, "2026-10-01", "2026-10-03T14:00:00", false, "▲; an era month; 時 alone; 未"},
		{-1000, "2026-10-01", "2026-10-03T09:00:00", false, "accounting parentheses; a kanji month; 午前; 無"},
		{123000, "2026-10-03", "2026-10-03T14:30:00", nil, "the trailing dash; kanji digits in place; (Fri); △ is not a boolean"},
		{10, "2026-10-03", "2026-10-03T14:30:00", false, "percent; an era ligature; a weekday before the time; a dash for none"},
		{123000, "2019-04-30", "2026-10-03T12:00:00", false, "a receipt in daiji; kanji numerals with units; 午後0時; an empty box"},
		{1000, "2026-10-03", "2026-10-03T00:00:00", true, "a price tag; a dot date; 午前12時; 有"},
		{150000000, "2019-04-30", "2026-10-03T00:00:00", true, "億; a dashed era date; a date alone; a check mark"},
		{1000, "2026-10-03", "2026-10-03T14:30:00", false, "a tag in parentheses; a kanji era date; a colon time; 不要"},
		{1000000, "2026-10-01", "2026-10-03T14:00:00", true, "百万; a slash month; 金曜日 before the time; 完了"},
	} {
		row := out.Rows[i]
		require.Equal(t, want.amount, row["Amount"], "row %d amount: %s", i+2, want.note)
		require.Equal(t, want.due, row["Due"], "row %d due: %s", i+2, want.note)
		require.Equal(t, want.when, row["When"], "row %d when: %s", i+2, want.note)
		require.Equal(t, want.done, row["Done"], "row %d done: %s", i+2, want.note)
	}
	require.Equal(t, "００７", out.Rows[0]["Code"], "an unpinned column keeps its text")
	require.Equal(t, []string{`Sheet1!D9: expected boolean, found "△"`}, out.Warnings)

	var warnings []string
	readJSON(t, dagu, "warnings.json", &warnings)
	require.Equal(t, []string{
		`Sheet1!E4: expected number, found "abc"`,
		`Sheet1!E5: expected number, found "1,000（千円）"`,
		`Sheet1!E6: expected number, found "12万3万"`,
		`Sheet1!D9: expected boolean, found "△"`,
	}, warnings, "text outside the forms is quoted as the cell holds it")
}
