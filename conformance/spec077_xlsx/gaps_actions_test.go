// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Spec 077 "Validating": max_problems caps the problems kept while count
// reports every problem, rule names resolve through a columns alias or a
// loose match, the step writes one line to stdout, and each kept problem
// goes to stderr as a problem: line.
func TestXlsxValidateLimits(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "validate_limits.yaml").ExpectExitCode(0)
	var out struct {
		OK        bool      `json:"ok"`
		Count     int       `json:"count"`
		Rows      int       `json:"rows"`
		Truncated bool      `json:"truncated"`
		Headers   []string  `json:"headers"`
		Problems  []problem `json:"problems"`
		Warnings  []string  `json:"warnings"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.False(t, out.OK)
	require.Equal(t, 3, out.Count, "every problem is counted")
	require.Equal(t, 3, out.Rows)
	require.True(t, out.Truncated, "max_problems cut the list")
	require.Len(t, out.Problems, 2, "only max_problems problems are kept")
	require.Equal(t, "missing_column", out.Problems[0].Code)
	require.Equal(t, problem{Code: "duplicate", Sheet: "Sheet1", Cell: "A3", Row: 3, Column: "Invoice No", Message: `duplicate value "INV-1"; first at row 2`}, out.Problems[1],
		"a unique rule given by its columns alias checks the aliased header")

	require.Equal(t, "Validated 3 rows in orders.xlsx Sheet1!A1:C4: 3 problems\n", readFile(t, dagu, "check.out"),
		"the step writes one line to stdout")
	lines := strings.Split(strings.TrimRight(readFile(t, dagu, "check.err"), "\n"), "\n")
	require.Equal(t, []string{
		`problem: Sheet1: column "Nope" not found; headers present: Invoice No, Amount, Status`,
		`problem: Sheet1!A3: duplicate value "INV-1"; first at row 2`,
	}, lines, "each kept problem is one problem: line on stderr")
}

// Spec 077 "Writing cells": a pinned string stays text, a date written over
// text that reads the same is a change, and atomic and wait_for_unlock apply.
func TestXlsxWriteCellsRules(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_cells_rules.yaml").ExpectExitCode(0)
	var out struct {
		First    changes          `json:"first"`
		Second   changes          `json:"second"`
		Third    changes          `json:"third"`
		Rows     []map[string]any `json:"rows"`
		Warnings []string         `json:"warnings"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 2, out.First.CellsChanged)
	require.Equal(t, 1, out.Second.CellsChanged, "a date over text that reads the same is a change")
	require.Equal(t, 0, out.Third.CellsChanged, "the same text and the same date again are not")
	require.Equal(t, "007", out.Rows[0]["B"], "a pinned string keeps its leading zeros")
	require.Equal(t, "2026-10-01", out.Rows[1]["B"])
	require.Equal(t, float64(3), out.Rows[2]["B"], "a formula replaces the text the cell held and a read evaluates it")
	require.Contains(t, out.Warnings, "Sheet1!B3: formula had no cached value; evaluated")
}

// Spec 077 "Sheets": if_exists: replace, missing: skip, and renames that
// change nothing or only case.
func TestXlsxSheetModes(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "sheet_modes.yaml").ExpectExitCode(0)
	var out struct {
		CopyReplace           []string         `json:"copy_replace"`
		RenameSame            []string         `json:"rename_same"`
		RenameSameWarnings    []string         `json:"rename_same_warnings"`
		RenameCase            []string         `json:"rename_case"`
		CopyMissing           []string         `json:"copy_missing"`
		CopyMissingWarnings   []string         `json:"copy_missing_warnings"`
		RenameMissingWarnings []string         `json:"rename_missing_warnings"`
		AddReplace            []string         `json:"add_replace"`
		TemplateRows          []map[string]any `json:"template_rows"`
		DataCount             int              `json:"data_count"`
	}
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"Template", "Data"}, out.CopyReplace, "a copy over an existing sheet keeps its position")
	require.Equal(t, []string{"Template", "Data"}, out.RenameSame)
	require.Equal(t, []string{`sheet "Template" already has that name; nothing renamed`}, out.RenameSameWarnings)
	require.Equal(t, []string{"TEMPLATE", "Data"}, out.RenameCase, "a rename that only changes case is applied")
	require.Equal(t, []string{"TEMPLATE", "Data"}, out.CopyMissing)
	require.Equal(t, []string{`sheet "Nope" not found; nothing copied`}, out.CopyMissingWarnings)
	require.Equal(t, []string{`sheet "Nope" not found; nothing renamed`}, out.RenameMissingWarnings)
	require.Equal(t, []string{"TEMPLATE", "Data"}, out.AddReplace, "adding an existing sheet with replace keeps the list")
	require.Len(t, out.TemplateRows, 2, "the replaced sheet holds the copied rows")
	require.Equal(t, "Food", out.TemplateRows[0]["Item"])
	require.Equal(t, 0, out.DataCount, "add with replace empties the sheet")
}

// Spec 077 "Converting": utf-8-bom, atomic: false, and the sheet and
// warnings outputs.
func TestXlsxConvertOptions(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "convert_options.yaml").ExpectExitCode(0)
	raw, err := os.ReadFile(dagu.ProjectPath("bom.csv"))
	require.NoError(t, err)
	require.Equal(t, "\xEF\xBB\xBFItem,Qty\npear,3\n", string(raw), "the file starts with the byte order mark")
	var result struct {
		Sheet    string   `json:"sheet"`
		Format   string   `json:"format"`
		Count    int      `json:"count"`
		Warnings []string `json:"warnings"`
	}
	readJSON(t, dagu, "result.json", &result)
	require.Equal(t, "Orders", result.Sheet)
	require.Equal(t, "csv", result.Format)
	require.Equal(t, 1, result.Count)
	require.Empty(t, result.Warnings)
}

// Spec 077 "Errors", runtime: the messages a failed run reports.
func TestXlsxRuntimeErrorsActions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"runtime_missing_sheet.yaml", `sheet "Order" not found; sheets present: Orders, Summary`},
		{"runtime_bad_range.yaml", `range "Totals" is not a cell range, named range, or table`},
		{"runtime_bad_column.yaml", `columns: column "Nope" not found; headers present: Invoice No, Amount`},
		{"convert_type_error.yaml", `orders.xlsx Sheet1!B2: expected number, found "N/A"`},
		{"convert_unwritable.yaml", "out.csv: "},
		{"write_cells_range.yaml", `template.xlsx: "A1:B2" is not a single cell`},
		{"write_cells_same_cell.yaml", "name the same cell Sheet1!B3"},
		{"write_cells_missing_workbook.yaml", "template.xlsx: workbook not found"},
		{"write_cells_same_output.yaml", "output must be a different file from path"},
		{"sheet_exists.yaml", `report.xlsx: sheet "October" already exists`},
		{"sheet_copy_self.yaml", `sheet "Template" cannot be copied onto itself`},
		{"sheet_position.yaml", "position 5 is outside 1 to 2"},
		{"sheet_bad_name.yaml", `invalid sheet name "Bad:Name"`},
		{"sheet_missing_fail.yaml", `sheet "Nope" not found; sheets present: Template`},
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
