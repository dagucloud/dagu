// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Every message the spec's Validation list gives is what dagu validate
// prints: a value outside an enum is reported by the schema, every other
// shape problem by the executor's configuration check.
func TestXlsxValidationMessages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"validation_unknown_field.yaml", `unexpected additional properties ["strip"]`},
		{"validation_bad_merged.yaml", "join does not equal any of: [fill first]"},
		{"validation_bad_formulas.yaml", "raw does not equal any of: [cached text calculate]"},
		{"validation_bad_on_type_error.yaml", "skip does not equal any of: [fail warn null"},
		{"validation_bad_mode.yaml", "merge does not equal any of: [replace append]"},
		{"validation_bad_style.yaml", "fancy does not equal any of: [table none]"},
		{"validation_bad_missing.yaml", "ignore does not equal any of: [fail skip append]"},
		{"validation_bad_type.yaml", "money does not equal any of: [string number integer boolean date datetime]"},
		{"validation_bad_header.yaml", "header must be true, false, a row number, or a list of row numbers"},
		{"validation_max_rows_zero.yaml", "max_rows must be >= 1"},
		{"validation_bad_where_op.yaml", `where: Status: unknown operator "like"; use eq, ne, or in`},
		{"validation_bad_set.yaml", "set.Status: use a field name or {value: literal}"},
		{"validation_row_key_missing_skip.yaml", "missing: skip needs a key column; with key: _row nothing else identifies a row"},
		{"validation_bad_wait.yaml", "wait_for_unlock must be a duration such as 30s or 5m"},
		{"validation_bad_on_problem.yaml", "pause does not equal any of: [warn fail]"},
		{"validation_max_problems_zero.yaml", "max_problems must be >= 1"},
		{"validation_allowed_not_list.yaml", "allowed.Status must be a list of values"},
		{"validation_empty_cells.yaml", "cells must not be empty"},
		{"validation_bad_cell_shape.yaml", "cells.B2: use a scalar, null, {value: v, type: t}, or {formula: text}"},
		{"validation_empty_formula.yaml", "cells.B2: formula must not be empty"},
		{"validation_write_cells_bad_output.yaml", "output: out.csv: only .xlsx and .xlsm workbooks are supported; save as .xlsx"},
		{"validation_sheet_no_operation.yaml", "operation is required for sheet"},
		{"validation_sheet_no_sheet.yaml", "sheet is required for sheet"},
		{"validation_sheet_copy_no_to.yaml", "to is required for copy"},
		{"validation_sheet_to_on_add.yaml", "to is only valid for copy and rename"},
		{"validation_sheet_if_exists_on_delete.yaml", "if_exists is only valid for add, copy, and rename"},
		{"validation_sheet_missing_on_add.yaml", "missing is only valid for copy, rename, and delete"},
		{"validation_sheet_position_on_rename.yaml", "position is only valid for add and copy"},
		{"validation_sheet_position_zero.yaml", "position must be >= 1"},
		{"validation_convert_bad_ext.yaml", `output extension ".txt" is not json, jsonl, or csv; set format`},
		{"validation_convert_encoding_json.yaml", "encoding applies to csv only"},
		{"validation_convert_delimiter_json.yaml", "delimiter applies to csv only"},
		{"validation_bad_delimiter.yaml", "delimiter must be a single character"},
		{"validation_encoding_without_input.yaml", "encoding requires with.input"},
		{"validation_delimiter_without_input.yaml", "delimiter requires with.input"},
		{"validation_rows_and_input.yaml", "write accepts with.rows or with.input, not both"},
		{"validation_update_no_rows.yaml", "update_rows requires with.rows"},
		{"validation_writer_header_rows.yaml", "header must be true or false for write"},
		{"validation_update_header_false.yaml", "update_rows needs a header row; header: false is not supported"},
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
