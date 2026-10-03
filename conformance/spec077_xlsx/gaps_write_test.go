// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestXlsxWriteSources covers where xlsx.write takes rows from: JSON text
// keeps its key order and drops _row, YAML maps are written in sorted key
// order, and input files are read by extension or format with a byte order
// mark dropped.
func TestXlsxWriteSources(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_sources.yaml").ExpectExitCode(0)

	type table struct {
		Headers []string         `json:"headers"`
		Count   int              `json:"count"`
		Rows    []map[string]any `json:"rows"`
	}
	var out struct {
		JSONText   table `json:"json_text"`
		YAMLMap    table `json:"yaml_map"`
		InputJSON  table `json:"input_json"`
		InputJSONL table `json:"input_jsonl"`
		InputCSV   table `json:"input_csv"`
		InputBOM   table `json:"input_bom"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"b", "a"}, out.JSONText.Headers, "JSON text keeps its key order")
	require.Len(t, out.JSONText.Rows, 1)
	require.Equal(t, float64(2), out.JSONText.Rows[0]["_row"], "_row is the sheet row, never the written field")
	require.Equal(t, []string{"a", "b"}, out.YAMLMap.Headers, "YAML maps are written in sorted key order")
	for name, got := range map[string]table{"json": out.InputJSON, "jsonl": out.InputJSONL, "csv": out.InputCSV} {
		require.Equal(t, []string{"id", "name"}, got.Headers, name)
		require.Equal(t, 2, got.Count, name)
	}
	require.Equal(t, []string{"id", "name"}, out.InputBOM.Headers, "the byte order mark is dropped")
	require.Equal(t, "1", out.InputBOM.Rows[0]["id"], "csv cells are text unless types says otherwise")
}

// TestXlsxWriteModes covers mode: append on xlsx.write, an append that
// starts a sheet, types on write, style: none, and atomic: false.
func TestXlsxWriteModes(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_modes.yaml").ExpectExitCode(0)

	type table struct {
		Headers []string         `json:"headers"`
		Count   int              `json:"count"`
		Rows    []map[string]any `json:"rows"`
	}
	var out struct {
		AppendChanges changes          `json:"append_changes"`
		Log           table            `json:"log"`
		FreshChanges  changes          `json:"fresh_changes"`
		Fresh         table            `json:"fresh"`
		Typed         []map[string]any `json:"typed"`
		PlainCount    int              `json:"plain_count"`
		InPlaceCount  int              `json:"in_place_count"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!A4:B4", RowsAppended: 1, CellsChanged: 2}, out.AppendChanges, "an append below rows writes no header")
	require.Equal(t, []string{"when", "what"}, out.Log.Headers)
	require.Equal(t, 3, out.Log.Count)
	require.Equal(t, "2026-10-03", out.Log.Rows[2]["when"], "the appended cell copies the date style above it")
	require.Equal(t, changes{Sheet: "Fresh", Range: "Fresh!A1:B2", RowsAppended: 1, CellsChanged: 4}, out.FreshChanges, "an append that starts a sheet writes the header")
	require.Equal(t, []string{"when", "what"}, out.Fresh.Headers)
	require.Equal(t, 1, out.Fresh.Count)
	require.Equal(t, "2026-10-04", out.Fresh.Rows[0]["when"])
	require.Len(t, out.Typed, 1)
	require.Equal(t, "007", out.Typed[0]["code"], "types pins text on write")
	require.Equal(t, 12.5, out.Typed[0]["amount"], "types converts a number on write")
	require.Equal(t, 1, out.PlainCount)
	require.Equal(t, 2, out.InPlaceCount)
}

// TestXlsxAppendPreview is the spec's preview-then-append example: a dry
// run reports the same summary as the append that follows and saves nothing.
func TestXlsxAppendPreview(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "--params", `ROWS="[{\"when\":\"2026-10-02\",\"what\":\"work\"},{\"when\":\"2026-10-03\",\"what\":\"done\"}]"`, "append_preview.yaml").ExpectExitCode(0)

	var out struct {
		Preview        changes          `json:"preview"`
		PreviewDryRun  bool             `json:"preview_dry_run"`
		PreviewedCount int              `json:"previewed_count"`
		Append         changes          `json:"append"`
		AppendedCount  int              `json:"appended_count"`
		Rows           []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.True(t, out.PreviewDryRun)
	require.Equal(t, 1, out.PreviewedCount, "the preview saved nothing")
	require.Equal(t, out.Append, out.Preview, "the preview computed the summary of the append")
	require.Equal(t, 2, out.Append.RowsAppended)
	require.Equal(t, 3, out.AppendedCount)
	require.Equal(t, "done", out.Rows[2]["what"])
}

// TestXlsxUpdateRowsForeach is the spec's first example: rows still to do
// are read, acted on in a foreach, and the results written back from the
// loop's aggregate output. One submission fails, so the loop and the run
// end partially succeeded, yet the write-back runs: the rows that
// succeeded are marked and the failed row keeps its empty Status, so the
// next run's where retries only it.
func TestXlsxUpdateRowsForeach(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "update_rows_foreach.yaml")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stdout:\n%s\nstderr:\n%s", result.Stdout(), result.Stderr())
		}
	})
	result.ExpectExitCode(0)

	var out struct {
		Changes changes          `json:"changes"`
		Rows    []map[string]any `json:"rows"`
		Results struct {
			Summary struct {
				Total     int `json:"total"`
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"summary"`
			Outputs []map[string]any `json:"outputs"`
		} `json:"results"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 3, out.Results.Summary.Total, "where kept the rows with an empty Status")
	require.Equal(t, 2, out.Results.Summary.Succeeded)
	require.Equal(t, 1, out.Results.Summary.Failed)
	require.Len(t, out.Results.Outputs, 2, "outputs holds the collected object of the items that succeeded")
	require.Equal(t, map[string]any{"order_id": "INV-4", "status": "submitted"}, out.Results.Outputs[1])
	require.Equal(t, 2, out.Changes.RowsUpdated, "rows takes the aggregate and uses its outputs list")
	require.Len(t, out.Rows, 4)
	require.Equal(t, "submitted", out.Rows[0]["Status"])
	require.Nil(t, out.Rows[1]["Status"], "the row whose submission failed is untouched, so the next run retries it")
	require.Equal(t, "Done", out.Rows[2]["Status"], "the row where left out is untouched")
	require.Equal(t, "submitted", out.Rows[3]["Status"])
}

// TestXlsxUpdateRowsRules covers the matching and writing rules of
// update_rows: trimmed keys, _row matches, explicit null, typed change
// counting, a date into a text column, missing: skip, and literals.
func TestXlsxUpdateRowsRules(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "update_rows_rules.yaml").ExpectExitCode(0)

	var out struct {
		Changes  changes          `json:"changes"`
		Warnings []string         `json:"warnings"`
		Headers  []string         `json:"headers"`
		Rows     []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"Invoice No", "Amount", "Note", "Status", "Reviewed"}, out.Headers, "a column not in the header row is added at the right")
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!A2:E4", RowsUpdated: 2, ColumnsAdded: 1, CellsChanged: 5}, out.Changes, "the range is the table's data rows, added column included")
	require.Equal(t, []string{`Sheet1: key "INV-99" not found; row skipped`}, out.Warnings)
	require.Len(t, out.Rows, 3)
	require.Nil(t, out.Rows[0]["Status"], "an explicit null empties the cell")
	require.Equal(t, float64(7), out.Rows[0]["Amount"], "the number 7 replaced the text 7")
	require.Equal(t, "n1", out.Rows[0]["Note"], "an equal value is not a change")
	require.Equal(t, "yes", out.Rows[0]["Reviewed"])
	require.Equal(t, "2026-11-01", out.Rows[1]["Note"], "a date written elsewhere gets a date format")
	require.Equal(t, "Open", out.Rows[1]["Status"], "a field no row carries leaves the cell as it is")
	require.Equal(t, "yes", out.Rows[1]["Reviewed"])
	require.Equal(t, "Open", out.Rows[2]["Status"])
	require.Nil(t, out.Rows[2]["Reviewed"], "the skipped row and the untouched row get no literal")
}

// TestXlsxUpdateRowsErrors covers the runtime errors of update_rows and
// that a refused update leaves the workbook as it was.
func TestXlsxUpdateRowsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"update_rows_dup_key.yaml", `key "INV-1" appears at rows 2 and 3`},
		{"update_rows_missing_fail.yaml", `key "INV-99" not found`},
		{"update_rows_same_row.yaml", `rows[0] and rows[1] both address row 2`},
		{"update_rows_set_unknown_field.yaml", `set.Status: field "x" is not in any row`},
		{"update_rows_set_key.yaml", `set.Status: the key column cannot be updated`},
		{"update_rows_set_loose.yaml", `column "status" not found in header row 1; did you mean "Status"?`},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)

			read := dagu.Run("xlsx", "read", dagu.ProjectPath("orders.xlsx"), "--format", "json")
			read.ExpectExitCode(0)
			var got struct {
				Rows []map[string]any `json:"rows"`
			}
			require.NoError(t, json.Unmarshal([]byte(read.Stdout()), &got), read.Stdout())
			require.NotEmpty(t, got.Rows)
			for _, row := range got.Rows {
				require.Equal(t, "Open", row["Status"], "the workbook is untouched")
			}
		})
	}
}

// TestXlsxSheetCopyThenAppend is the spec's month example run twice: the
// copy is skipped on the rerun and the append adds below the first rows.
func TestXlsxSheetCopyThenAppend(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)

	type out struct {
		Sheets   []string `json:"sheets"`
		Warnings []string `json:"warnings"`
		Count    int      `json:"count"`
		Headers  []string `json:"headers"`
	}
	dagu.Run("start", "sheet_copy_then_append.yaml").ExpectExitCode(0)
	var first out
	readJSON(t, dagu, "out.json", &first)
	require.Equal(t, out{Sheets: []string{"Template", "2026-10"}, Warnings: []string{}, Count: 2, Headers: []string{"item", "amount"}}, first)

	dagu.Run("start", "sheet_copy_then_append.yaml").ExpectExitCode(0)
	var second out
	readJSON(t, dagu, "out.json", &second)
	require.Equal(t, out{Sheets: []string{"Template", "2026-10"}, Warnings: []string{`sheet "2026-10" already exists; nothing copied`}, Count: 4, Headers: []string{"item", "amount"}}, second)
}
