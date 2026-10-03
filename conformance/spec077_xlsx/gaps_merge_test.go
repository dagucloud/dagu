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

// Spec 077 "Typing": Japanese text under a pinned type reads as its ASCII
// form, a yen amount, a kanji date, or an era date; text that is none of
// these still fails the type, quoted as the cell holds it.
func TestXlsxTypesJapanese(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "types_japanese.yaml").ExpectExitCode(0)

	var rows []map[string]any
	readJSON(t, dagu, "out.json", &rows)
	require.Len(t, rows, 4)
	require.Equal(t, float64(123456), rows[0]["Amount"], "full-width digits and comma")
	require.Equal(t, "2026-10-03", rows[0]["Due"], "full-width slashes")
	require.Equal(t, "００７", rows[0]["Code"], "an unpinned column keeps its text")
	require.Equal(t, float64(123000), rows[1]["Amount"], "a yen sign before the amount")
	require.Equal(t, "2026-10-03", rows[1]["Due"], "an era date, long")
	require.Equal(t, float64(123000), rows[2]["Amount"], "円 after the amount")
	require.Equal(t, "2026-10-03", rows[2]["Due"], "an era date, short")
	require.Equal(t, float64(-5), rows[3]["Amount"], "the minus sign U+2212")
	require.Equal(t, "2019-05-01", rows[3]["Due"], "the first year of an era")

	var warnings []string
	readJSON(t, dagu, "warnings.json", &warnings)
	require.Equal(t, []string{
		`Sheet1!C4: expected number, found "abc"`,
		`Sheet1!C5: expected number, found "x"`,
	}, warnings, "full-width digits pinned to number convert; other text is quoted as it is")
}
