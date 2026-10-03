// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/japanese"
)

type problem struct {
	Code    string `json:"code"`
	Sheet   string `json:"sheet"`
	Cell    string `json:"cell"`
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}

func TestXlsxValidate(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "validate_ok.yaml").ExpectExitCode(0)
	var ok struct {
		OK       bool      `json:"ok"`
		Count    int       `json:"count"`
		Rows     int       `json:"rows"`
		Headers  []string  `json:"headers"`
		Problems []problem `json:"problems"`
		Range    string    `json:"range"`
	}
	readJSON(t, dagu, "out.json", &ok)
	require.True(t, ok.OK)
	require.Equal(t, 0, ok.Count)
	require.Equal(t, 2, ok.Rows)
	require.Equal(t, []string{"Invoice No", "Amount", "Status"}, ok.Headers)
	require.Empty(t, ok.Problems)
	require.Equal(t, "Sheet1!A1:C3", ok.Range)

	problems := harness.NewRunner(t)
	problems.Run("start", "validate_problems.yaml").ExpectExitCode(0)
	var found struct {
		OK       bool      `json:"ok"`
		Count    int       `json:"count"`
		Rows     int       `json:"rows"`
		Problems []problem `json:"problems"`
	}
	readJSON(t, problems, "out.json", &found)
	require.False(t, found.OK)
	require.Equal(t, 5, found.Count)
	require.Equal(t, 3, found.Rows)
	require.Len(t, found.Problems, 5)
	codes := map[string]problem{}
	for _, p := range found.Problems {
		codes[p.Code] = p
	}
	require.Equal(t, problem{Code: "missing_column", Sheet: "Sheet1", Column: "Nope", Message: `column "Nope" not found; headers present: Invoice No, Amount, Status`}, codes["missing_column"])
	require.Equal(t, problem{Code: "type", Sheet: "Sheet1", Cell: "B3", Row: 3, Column: "Amount", Message: `expected number, found "N/A"`}, codes["type"])
	require.Equal(t, problem{Code: "duplicate", Sheet: "Sheet1", Cell: "A3", Row: 3, Column: "Invoice No", Message: `duplicate value "INV-1"; first at row 2`}, codes["duplicate"])
	require.Equal(t, problem{Code: "not_allowed", Sheet: "Sheet1", Cell: "C3", Row: 3, Column: "Status", Message: `value "Pending" is not one of Done, Open`}, codes["not_allowed"])
	require.Equal(t, problem{Code: "blank", Sheet: "Sheet1", Cell: "C4", Row: 4, Column: "Status", Message: "Status is blank"}, codes["blank"])

	failing := harness.NewRunner(t)
	result := failing.Run("start", "validate_fail.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("2 problems found in orders.xlsx Sheet1")
	failing.ExpectNoFile("after.txt")
	require.Equal(t, "problem: Sheet1!B3: expected number, found \"N/A\"\nproblem: Sheet1!A3: duplicate value \"INV-1\"; first at row 2\n", readFile(t, failing, "check.err"),
		"the problems are listed on stderr before the step fails")
}

func TestXlsxValidateThenHumanTask(t *testing.T) {
	t.Parallel()
	// With no problems the human task's precondition is unmet, the task is
	// skipped, and the run finishes.
	clean := harness.NewRunner(t)
	clean.Run("start", "validate_pause.yaml").ExpectExitCode(0)
	require.Equal(t, "ran", strings.TrimSpace(readFile(t, clean, "after.txt")))

	// With a problem the run stops at the human task and waits.
	paused := harness.NewRunner(t)
	result := paused.Run("start", "validate_pause.yaml", "--", "STATUS=Pending")
	result.ExpectExitCode(0)
	result.ExpectStderrContains("waiting for human input")
	paused.ExpectNoFile("after.txt")
}

func TestXlsxWriteCells(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_cells.yaml").ExpectExitCode(0)
	var written changes
	readJSON(t, dagu, "changes.json", &written)
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!B1:B5", RowsUpdated: 4, CellsChanged: 4}, written,
		"B4 already held 10 and is not a change")
	var rows []map[string]any
	readJSON(t, dagu, "out.json", &rows)
	require.Len(t, rows, 5)
	require.Equal(t, "Acme", rows[0]["B"])
	require.Equal(t, "2026-10-01", rows[1]["B"], "an ISO string becomes a date")
	require.Nil(t, rows[2]["B"], "null clears the cell")
	require.Equal(t, float64(10), rows[3]["B"])
	require.Equal(t, "=B3*B4", rows[4]["B"])

	output := harness.NewRunner(t)
	output.Run("start", "write_cells_output.yaml").ExpectExitCode(0)
	var copied struct {
		Template []map[string]any `json:"template"`
		Filled   []map[string]any `json:"filled"`
	}
	readJSON(t, output, "out.json", &copied)
	require.Equal(t, output.ProjectPath("filled.xlsx"), strings.TrimSpace(readFile(t, output, "path.txt")))
	require.Nil(t, copied.Template[0]["B"], "the template is untouched")
	require.Equal(t, "Acme", copied.Filled[0]["B"])

	dry := harness.NewRunner(t)
	dry.Run("start", "write_cells_dry_run.yaml").ExpectExitCode(0)
	var preview struct {
		DryRun  bool             `json:"dry_run"`
		Changes changes          `json:"changes"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dry, "out.json", &preview)
	require.True(t, preview.DryRun)
	require.Equal(t, 1, preview.Changes.CellsChanged)
	require.Nil(t, preview.Rows[0]["B"], "a dry run writes nothing")
}

func TestXlsxSheetOperations(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "sheet_ops.yaml").ExpectExitCode(0)
	var out struct {
		Add     []string         `json:"add"`
		Copy    []string         `json:"copy"`
		Rename  []string         `json:"rename"`
		Delete  []string         `json:"delete"`
		Deleted string           `json:"deleted"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"Notes", "Template"}, out.Add)
	require.Equal(t, []string{"Notes", "Template", "October"}, out.Copy, "a copy lands after its source")
	require.Equal(t, []string{"Notes", "Template", "2026-10"}, out.Rename)
	require.Equal(t, []string{"Template", "2026-10"}, out.Delete)
	require.Equal(t, "Notes", out.Deleted)
	require.Len(t, out.Rows, 1)
	require.Equal(t, float64(100), out.Rows[0]["Amount"], "the copy carries the cells")

	skip := harness.NewRunner(t)
	skip.Run("start", "sheet_skip.yaml").ExpectExitCode(0)
	var skipped struct {
		AddSheets      []string `json:"add_sheets"`
		AddWarnings    []string `json:"add_warnings"`
		DeleteSheets   []string `json:"delete_sheets"`
		DeleteWarnings []string `json:"delete_warnings"`
	}
	readJSON(t, skip, "out.json", &skipped)
	require.Equal(t, []string{"Template"}, skipped.AddSheets)
	require.Len(t, skipped.AddWarnings, 1)
	require.Equal(t, []string{"Template"}, skipped.DeleteSheets)
	require.Len(t, skipped.DeleteWarnings, 1)

	last := harness.NewRunner(t)
	result := last.Run("start", "sheet_delete_last.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`report.xlsx: cannot delete the only sheet "Sheet1"`)
}

func TestXlsxConvert(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "convert_files.yaml").ExpectExitCode(0)
	require.Equal(t, "Item,Qty,Done,When\n\"pear, \"\"ripe\"\"\",3,true,2026-10-01\nfig,,false,\n", readFile(t, dagu, "out.csv"))
	require.Equal(t, "[\n{\"Qty\":3,\"name\":\"pear, \\\"ripe\\\"\"},\n{\"Qty\":null,\"name\":\"fig\"}\n]\n", readFile(t, dagu, "out.json"),
		"keys follow the column order and _row is not written")
	lines := strings.Split(strings.TrimRight(readFile(t, dagu, "out.ndjson"), "\n"), "\n")
	require.Len(t, lines, 2)
	var row map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &row))
	require.Equal(t, float64(3), row["Qty"])
	require.Equal(t, true, row["Done"])
	require.Equal(t, "2026-10-01", row["When"])
	var result struct {
		CSV struct {
			Format string `json:"format"`
			Count  int    `json:"count"`
			Range  string `json:"range"`
		} `json:"csv"`
		JSONLFormat string `json:"jsonl_format"`
	}
	readJSON(t, dagu, "result.json", &result)
	require.Equal(t, dagu.ProjectPath("out.csv"), strings.TrimSpace(readFile(t, dagu, "path.txt")))
	require.Equal(t, "csv", result.CSV.Format)
	require.Equal(t, 2, result.CSV.Count)
	require.Equal(t, "Sheet1!A1:D3", result.CSV.Range)
	require.Equal(t, "jsonl", result.JSONLFormat)

	sjis := harness.NewRunner(t)
	sjis.Run("start", "convert_sjis.yaml").ExpectExitCode(0)
	raw, err := os.ReadFile(sjis.ProjectPath("sjis.csv"))
	require.NoError(t, err)
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(raw)
	require.NoError(t, err)
	require.Equal(t, "品名;数量\nりんご;3\n", string(decoded), "columns keeps the named columns and the delimiter separates them")
	require.NotEqual(t, "品名", string(raw[:len("品名")]), "the bytes are Shift_JIS, not UTF-8")

	input := harness.NewRunner(t)
	encoded, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("品名;数量\nみかん;5\n"))
	require.NoError(t, err)
	input.WriteFile("input.csv", string(encoded))
	input.Run("start", "input_sjis.yaml").ExpectExitCode(0)
	var back struct {
		Headers []string         `json:"headers"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, input, "out.json", &back)
	require.Equal(t, []string{"品名", "数量"}, back.Headers)
	require.Equal(t, "みかん", back.Rows[0]["品名"])
}

func TestXlsxDryRunChecks(t *testing.T) {
	t.Parallel()
	const warning = "Dry run: step may fail on this host"
	for _, tc := range []struct {
		file  string
		parts []string
	}{
		// The log escapes quotes, so the tokens are the names themselves.
		{"dry_missing_workbook.yaml", []string{"field 'with.path': none-9f3c2b1a.xlsx: workbook not found"}},
		{"dry_missing_sheet.yaml", []string{"field 'with.sheet'", "Nope9f3c2b1a", "not found; sheets present: Sheet1"}},
		{"dry_missing_column.yaml", []string{"field 'with.columns'", "Nope9f3c2b1a", "not found in header row 1"}},
		{"dry_update_rows_key.yaml", []string{"field 'with.key'", "invoice no", "did you mean"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			dagu.Run("start", "dry_setup.yaml").ExpectExitCode(0)
			result := dagu.Run("dry", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStderrContains(append([]string{warning}, tc.parts...)...)
			dagu.ExpectNoFile("dry-out.json")
		})
	}
	for _, file := range []string{"dry_ok.yaml", "dry_reference_skipped.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			dagu.Run("start", "dry_setup.yaml").ExpectExitCode(0)
			result := dagu.Run("dry", file)
			result.ExpectExitCode(0)
			result.ExpectStderrNotContains(warning)
		})
	}
}

func TestXlsxReleaseTwoValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"validation_validate_no_rules.yaml", "validate requires at least one of with.required, with.not_blank, with.unique, with.types, or with.allowed"},
		{"validation_write_cells_no_cells.yaml", "write_cells requires with.cells"},
		{"validation_sheet_bad_operation.yaml", "move does not equal any of: [add copy rename delete]"},
		{"validation_convert_no_output.yaml", "convert requires with.output"},
		{"validation_bad_encoding.yaml", "latin1 does not equal any of: [utf-8 utf-8-bom shift_jis cp932 windows-31j sjis ms932]"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

func readFile(t *testing.T, dagu *harness.Runner, name string) string {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(name))
	require.NoError(t, err, name)
	return string(data)
}
