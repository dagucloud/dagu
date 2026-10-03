// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec077_xlsx_test covers the xlsx actions through the CLI. Every
// workbook is created by xlsx.write inside the fixture, and step outputs are
// captured with file.write steps because outputs do not go to stdout.
package spec077_xlsx_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

type changes struct {
	Sheet        string `json:"sheet"`
	Range        string `json:"range"`
	RowsUpdated  int    `json:"rows_updated"`
	RowsAppended int    `json:"rows_appended"`
	ColumnsAdded int    `json:"columns_added"`
	CellsChanged int    `json:"cells_changed"`
}

func readJSON(t *testing.T, dagu *harness.Runner, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(name))
	require.NoError(t, err, name)
	require.NoError(t, json.Unmarshal(data, into), "%s: %s", name, string(data))
}

func TestXlsxRoundTrip(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "roundtrip.yaml").ExpectExitCode(0)

	var written changes
	readJSON(t, dagu, "changes.json", &written)
	require.Equal(t, changes{Sheet: "Orders", Range: "Orders!A1:E3", RowsAppended: 2, CellsChanged: 14}, written)

	var rows []map[string]any
	readJSON(t, dagu, "rows.json", &rows)
	require.Len(t, rows, 2)
	require.Equal(t, "INV-1", rows[0]["Invoice No"])
	require.Equal(t, float64(10), rows[0]["Amount"])
	require.Equal(t, "2026-10-01T00:00:00", rows[0]["Due"], "a column mixing dates and datetimes is a datetime column")
	require.Equal(t, true, rows[0]["Paid"])
	require.Equal(t, "00123", rows[0]["Note"], "leading zeros survive")
	require.Equal(t, float64(2), rows[0]["_row"])
	require.Equal(t, 20.5, rows[1]["Amount"])
	require.Equal(t, "2026-10-02T14:30:00", rows[1]["Due"])
	require.Equal(t, false, rows[1]["Paid"])
	require.Nil(t, rows[1]["Note"])

	var meta struct {
		Count     int      `json:"count"`
		Headers   []string `json:"headers"`
		Sheet     string   `json:"sheet"`
		Range     string   `json:"range"`
		Truncated bool     `json:"truncated"`
	}
	readJSON(t, dagu, "meta.json", &meta)
	require.Equal(t, 2, meta.Count)
	require.Equal(t, []string{"Invoice No", "Amount", "Due", "Paid", "Note"}, meta.Headers)
	require.Equal(t, "Orders", meta.Sheet)
	require.Equal(t, "Orders!A1:E3", meta.Range)
	require.False(t, meta.Truncated)

	var info []struct {
		Name      string            `json:"name"`
		Range     string            `json:"range"`
		HeaderRow int               `json:"header_row"`
		Headers   []string          `json:"headers"`
		Types     map[string]string `json:"types"`
		RowCount  int               `json:"row_count"`
	}
	readJSON(t, dagu, "info.json", &info)
	require.Len(t, info, 1)
	require.Equal(t, "Orders", info[0].Name)
	require.Equal(t, "Orders!A1:E3", info[0].Range)
	require.Equal(t, 1, info[0].HeaderRow)
	require.Equal(t, meta.Headers, info[0].Headers)
	require.Equal(t, "number", info[0].Types["Amount"])
	require.Equal(t, "datetime", info[0].Types["Due"])
	require.Equal(t, "boolean", info[0].Types["Paid"])
	require.Equal(t, 2, info[0].RowCount)

	var sheets []string
	readJSON(t, dagu, "sheets.json", &sheets)
	require.Equal(t, []string{"Orders"}, sheets)
}

func TestXlsxReadOptions(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "read_options.yaml").ExpectExitCode(0)

	var ci struct {
		Headers []string         `json:"headers"`
		Range   string           `json:"range"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "ci.json", &ci)
	require.Equal(t, []string{"B", "C"}, ci.Headers)
	require.Equal(t, "Second!B2:C3", ci.Range)
	require.Len(t, ci.Rows, 2)
	require.Equal(t, "k", ci.Rows[0]["B"])
	require.Equal(t, float64(1), ci.Rows[1]["C"])

	var where struct {
		Headers []string         `json:"headers"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "where.json", &where)
	require.Equal(t, []string{"state", "Name"}, where.Headers)
	require.Len(t, where.Rows, 2)
	require.Nil(t, where.Rows[0]["state"])
	require.Equal(t, "b", where.Rows[0]["Name"], "trim removes the full-width space")
	require.Equal(t, "Failed", where.Rows[1]["state"])
}

func TestXlsxMultiRowHeaderAndTableDetection(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "multirow_header.yaml").ExpectExitCode(0)
	var multi struct {
		Headers []string         `json:"headers"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &multi)
	require.Equal(t, []string{"Invoice No", "Amount Net", "Tax"}, multi.Headers)
	require.Len(t, multi.Rows, 1)
	require.Equal(t, float64(100), multi.Rows[0]["Amount Net"])
	require.Equal(t, float64(3), multi.Rows[0]["_row"])

	offset := harness.NewRunner(t)
	offset.Run("start", "table_detection.yaml").ExpectExitCode(0)
	var detected struct {
		Headers []string         `json:"headers"`
		Range   string           `json:"range"`
		Count   int              `json:"count"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, offset, "out.json", &detected)
	require.Equal(t, []string{"ID", "Qty"}, detected.Headers)
	require.Equal(t, "Sheet1!C4:D6", detected.Range)
	require.Equal(t, 2, detected.Count)
	require.Equal(t, float64(5), detected.Rows[0]["_row"])
}

func TestXlsxMaxRows(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "max_rows.yaml").ExpectExitCode(0)
	var out struct {
		Count     int      `json:"count"`
		Truncated bool     `json:"truncated"`
		Warnings  []string `json:"warnings"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 4, out.Count)
	require.True(t, out.Truncated)
	require.Len(t, out.Warnings, 1)
	require.Contains(t, out.Warnings[0], "stopped after 4 rows")
}

func TestXlsxAppend(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "append.yaml").ExpectExitCode(0)
	var appended changes
	readJSON(t, dagu, "changes.json", &appended)
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!A4:B5", RowsAppended: 2, CellsChanged: 4}, appended)
	var out struct {
		Count int              `json:"count"`
		Rows  []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 4, out.Count)
	require.Equal(t, "2026-10-04", out.Rows[3]["when"], "appended dates keep the column's date format")
	require.Equal(t, "done", out.Rows[3]["what"])
}

func TestXlsxUpdateRows(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "update_rows.yaml").ExpectExitCode(0)
	var marked changes
	readJSON(t, dagu, "changes.json", &marked)
	require.Equal(t, changes{Sheet: "Sheet1", Range: "Sheet1!A2:C4", RowsUpdated: 2, ColumnsAdded: 1, CellsChanged: 4}, marked)
	var out struct {
		Headers []string         `json:"headers"`
		Rows    []map[string]any `json:"rows"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"Invoice No", "Status", "Checked"}, out.Headers)
	require.Equal(t, "Submitted", out.Rows[0]["Status"])
	require.Equal(t, "yes", out.Rows[0]["Checked"])
	require.Equal(t, "Failed", out.Rows[1]["Status"])
	require.Equal(t, "Done", out.Rows[2]["Status"], "rows not in the update keep their values")
	require.Nil(t, out.Rows[2]["Checked"])

	missing := harness.NewRunner(t)
	missing.Run("start", "update_rows_missing_append.yaml").ExpectExitCode(0)
	var appended changes
	readJSON(t, missing, "changes.json", &appended)
	require.Equal(t, 1, appended.RowsUpdated)
	require.Equal(t, 1, appended.RowsAppended)
}

func TestXlsxUpdateRowsShapeChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"update_rows_shape_check.yaml", `key column "Invoice" not found in header row 1`},
		{"update_rows_row_check.yaml", `expected key "INV-1", found "INV-2"; the sheet changed since it was read`},
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

func TestXlsxDryRunAndPreservedSheets(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "dry_run.yaml").ExpectExitCode(0)
	dagu.ExpectNoFile("preview.xlsx")
	var preview struct {
		DryRun  bool    `json:"dry_run"`
		Changes changes `json:"changes"`
	}
	readJSON(t, dagu, "changes.json", &preview)
	require.True(t, preview.DryRun)
	require.Equal(t, 2, preview.Changes.RowsAppended)

	multi := harness.NewRunner(t)
	multi.Run("start", "preserve_sheets.yaml").ExpectExitCode(0)
	var out struct {
		Sheets   []string         `json:"sheets"`
		AHeaders []string         `json:"a_headers"`
		ACount   int              `json:"a_count"`
		BRows    []map[string]any `json:"b_rows"`
	}
	readJSON(t, multi, "out.json", &out)
	require.Equal(t, []string{"A", "B"}, out.Sheets, "a replaced sheet keeps its position")
	require.Equal(t, []string{"new"}, out.AHeaders)
	require.Equal(t, 1, out.ACount)
	require.Len(t, out.BRows, 1)
	require.Equal(t, "yes", out.BRows[0]["kept"])
}

func TestXlsxTypeErrors(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "types_error.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`amounts.xlsx Sheet1!A2: expected number, found "N/A"`)

	warn := harness.NewRunner(t)
	warn.Run("start", "types_warn.yaml").ExpectExitCode(0)
	var out struct {
		Rows     []map[string]any `json:"rows"`
		Warnings []string         `json:"warnings"`
	}
	readJSON(t, warn, "out.json", &out)
	require.Len(t, out.Rows, 2)
	require.Nil(t, out.Rows[0]["Amount"])
	require.Equal(t, float64(7), out.Rows[1]["Amount"])
	require.Len(t, out.Warnings, 1)
	require.Contains(t, out.Warnings[0], `Sheet1!A2: expected number, found "N/A"`)

	// on_type_error: null is another spelling of warn.
	var asNull struct {
		Rows     []map[string]any `json:"rows"`
		Warnings []string         `json:"warnings"`
	}
	readJSON(t, warn, "out_null.json", &asNull)
	require.Equal(t, out.Rows, asNull.Rows)
	require.Equal(t, out.Warnings, asNull.Warnings)
}

func TestXlsxUnsupportedFormat(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "unsupported_format.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("only .xlsx and .xlsm workbooks are supported; save as .xlsx")
}

func TestXlsxLockFileNobodyHolds(t *testing.T) {
	t.Parallel()
	// A ~$ lock file left behind by a crash does not block the write on any
	// platform; one that Excel still holds is covered by the Windows unit
	// tests of the workbook package.
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_only.yaml").ExpectExitCode(0)
	dagu.WriteFile("~$held.xlsx", "held")
	dagu.Run("start", "append_only.yaml").ExpectExitCode(0)
	// The step warns about the leftover; the rest of the text says whether
	// this platform could tell that no program holds it.
	var warnings []string
	readJSON(t, dagu, "warnings.json", &warnings)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "~$held.xlsx exists")
}

func TestXlsxValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"validation_read_missing_path.yaml", "path is required for read"},
		{"validation_write_missing_rows.yaml", "write requires with.rows or with.input"},
		{"validation_update_missing_key.yaml", "key is required for update_rows"},
		{"validation_fixed_outputs.yaml", "xlsx actions have fixed outputs"},
		{"validation_foreign_field.yaml", "with.range is not valid for xlsx.info"},
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
