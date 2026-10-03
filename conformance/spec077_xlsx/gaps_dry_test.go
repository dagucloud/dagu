// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestXlsxDryRunChecksMore covers the dry-run checks spec 077 names beyond
// the workbook, sheet, columns, and key: the names in types, where, and the
// validation rules, the set columns of update_rows, the sheets write_cells
// and sheet operations need, the input file of a writer, and that every
// problem of a step is listed and params resolve.
func TestXlsxDryRunChecksMore(t *testing.T) {
	t.Parallel()
	const warning = "Dry run: step may fail on this host"
	for _, tc := range []struct {
		file  string
		parts []string
	}{
		{"dry_types_column.yaml", []string{"field 'with.types'", "Nope9f3c2b1a", "not found in header row 1"}},
		{"dry_where_unknown.yaml", []string{"field 'with.where'", "Nope9f3c2b1a", "not found in header row 1"}},
		{"dry_rule_column.yaml", []string{"field 'with.required'", "Nope9f3c2b1a", "not found in header row 1"}},
		{"dry_set_column.yaml", []string{"field 'with.set'", "Nope9f3c2b1a", "not found in header row 1"}},
		{"dry_write_cells_sheet.yaml", []string{"field 'with.sheet'", "Nope9f3c2b1a", "not found; sheets present: Sheet1"}},
		{"dry_sheet_copy_missing.yaml", []string{"field 'with.sheet'", "Nope9f3c2b1a", "not found; sheets present: Sheet1"}},
		{"dry_sheet_add_missing_workbook.yaml", []string{"field 'with.path': none-9f3c2b1a.xlsx: workbook not found"}},
		{"dry_missing_input.yaml", []string{"field 'with.input': rows-9f3c2b1a.json: file not found"}},
		// Every problem of a step is listed, not only the first.
		{"dry_two_problems.yaml", []string{"field 'with.key'", "Nope9f3c2b1a", "field 'with.set'", "Also9f3c2b1a"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			dagu.Run("start", "dry_setup.yaml").ExpectExitCode(0)
			result := dagu.Run("dry", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStderrContains(append([]string{warning}, tc.parts...)...)
		})
	}
	for _, file := range []string{"dry_where_alias_ok.yaml", "dry_sheet_add_ok.yaml", "dry_params.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			dagu.Run("start", "dry_setup.yaml").ExpectExitCode(0)
			result := dagu.Run("dry", file)
			result.ExpectExitCode(0)
			result.ExpectStderrNotContains(warning)
		})
	}
	t.Run("params resolve", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "dry_setup.yaml").ExpectExitCode(0)
		result := dagu.Run("dry", "--params", "SHEET=Nope9f3c2b1a", "dry_params.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains(warning, "field 'with.sheet'", "Nope9f3c2b1a", "not found; sheets present: Sheet1")
	})
}
