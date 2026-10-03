// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaXlsxReadActions(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - id: inspect
    action: xlsx.info
    with:
      path: orders.xlsx
  - id: sheets
    action: xlsx.list_sheets
    with:
      path: orders.xlsx
      password: ${env.BOOK_PASSWORD}
  - id: read
    action: xlsx.read
    with:
      path: orders.xlsx
      sheet: Orders
      range: A1:H
      header: [1, 2]
      columns: [Status, {Invoice No: invoice_no}]
      merged: fill
      stop_at_blank: true
      keep_empty_rows: false
      trim: true
      formulas: cached
      types: {Amount: number, Due: date}
      on_type_error: null
      where: {Status: ""}
      max_rows: 100
  - id: write
    action: xlsx.write
    with:
      path: report.xlsx
      sheet: Report
      rows: ${steps.read.outputs.rows}
      columns: ${steps.read.outputs.headers}
      header: true
      mode: replace
      style: table
      types: {Amount: number}
      atomic: true
      dry_run: false
      wait_for_unlock: 5m
      artifact: true
  - id: append
    action: xlsx.append
    with:
      path: report.xlsx
      input: rows.csv
      format: csv
  - id: mark
    action: xlsx.update_rows
    with:
      path: orders.xlsx
      sheet: Orders
      key: Invoice No
      rows: ${steps.each.outputs.results}
      set: {Status: status, Reviewed: {value: "yes"}, Code: {value: "007", type: string}}
      missing: skip
      wait_for_unlock: 5m
  - id: check
    action: xlsx.validate
    with:
      path: orders.xlsx
      sheet: Orders
      required: [Invoice No, Amount]
      not_blank: Status
      unique: [Invoice No]
      types: {Amount: number}
      allowed: {Status: [Open, Done]}
      on_problem: fail
      max_problems: 50
  - id: fill
    action: xlsx.write_cells
    with:
      path: template.xlsx
      output: invoice.xlsx
      cells: {B2: Acme, D7: "2026-10-01", E9: 3, F1: null, Total: {formula: SUM(E2:E9)}, G1: {value: "007", type: string}}
      dry_run: false
  - id: month
    action: xlsx.sheet
    with:
      path: report.xlsx
      operation: copy
      sheet: Template
      to: October
      if_exists: skip
      missing: fail
      position: 2
  - id: export
    action: xlsx.convert
    with:
      path: orders.xlsx
      output: orders.csv
      format: csv
      encoding: shift_jis
      delimiter: ";"
  - id: fields
    action: xlsx.extract
    with:
      path: inbox/quote.xlsx
      sheet: Sheet1
      instruction: Find the quote number and the total
      schema:
        type: object
        properties:
          quote_no: {type: string, description: 見積番号}
          total: {type: number}
      send_values: false
      cache: true
      llm:
        provider: anthropic
        model: claude-sonnet-5
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	const literalSchema = "schema:\n        type: object\n        properties:\n          quote_no: {type: string, description: 見積番号}\n          total: {type: number}"
	require.Contains(t, source, literalSchema)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, strings.Replace(source, literalSchema, "schema: ${params.SCHEMA}", 1))),
		"a schema still held in a value reference is accepted; the run resolves it")
	for _, tc := range []struct{ name, from, to string }{
		{"unknown field", "trim: true", "strip: true"},
		{"missing path", "path: orders.xlsx\n      sheet: Orders", "sheet: Orders"},
		{"bad merged", "merged: fill", "merged: middle"},
		{"bad formulas", "formulas: cached", "formulas: eval"},
		{"bad type", "Due: date", "Due: money"},
		{"bad on_type_error", "on_type_error: null", "on_type_error: ignore"},
		{"max_rows below one", "max_rows: 100", "max_rows: 0"},
		{"where not an object", "where: {Status: \"\"}", "where: Status"},
		{"columns alias not a string", "{Invoice No: invoice_no}", "{Invoice No: 3}"},
		{"header zero", "header: [1, 2]", "header: 0"},
		{"bad mode", "mode: replace", "mode: upsert"},
		{"bad style", "style: table", "style: fancy"},
		{"bad format", "format: csv", "format: xml"},
		{"rows not a list", "rows: ${steps.read.outputs.rows}", "rows: {a: 1}"},
		{"bad missing", "missing: skip", "missing: ignore"},
		{"set value not a field or literal", "Reviewed: {value: \"yes\"}", "Reviewed: 3"},
		{"set literal with extra keys", "Reviewed: {value: \"yes\"}", "Reviewed: {value: \"yes\", other: 1}"},
		{"set literal with a bad type", "Code: {value: \"007\", type: string}", "Code: {value: \"007\", type: money}"},
		{"empty key", "key: Invoice No", "key: \"\""},
		{"update_rows without key", "      key: Invoice No\n", ""},
		{"update_rows without rows", "      rows: ${steps.each.outputs.results}\n", ""},
		{"append without rows or input", "      input: rows.csv\n      format: csv\n", ""},
		{"bad on_problem", "on_problem: fail", "on_problem: pause"},
		{"max_problems below one", "max_problems: 50", "max_problems: 0"},
		{"allowed value not a list", "allowed: {Status: [Open, Done]}", "allowed: {Status: Open}"},
		{"empty required", "required: [Invoice No, Amount]", "required: []"},
		{"empty allowed", "allowed: {Status: [Open, Done]}", "allowed: {}"},
		{"validate without rules", "      required: [Invoice No, Amount]\n      not_blank: Status\n      unique: [Invoice No]\n      types: {Amount: number}\n      allowed: {Status: [Open, Done]}\n", ""},
		{"cells not an object", "cells: {B2: Acme, D7: \"2026-10-01\", E9: 3, F1: null, Total: {formula: SUM(E2:E9)}, G1: {value: \"007\", type: string}}", "cells: [B2]"},
		{"cell formula with a value", "Total: {formula: SUM(E2:E9)}", "Total: {formula: 1, value: 2}"},
		{"cell with unknown key", "G1: {value: \"007\", type: string}", "G1: {value: \"007\", bold: true}"},
		{"write_cells without cells", "      cells: {B2: Acme, D7: \"2026-10-01\", E9: 3, F1: null, Total: {formula: SUM(E2:E9)}, G1: {value: \"007\", type: string}}\n", ""},
		{"bad operation", "operation: copy", "operation: move"},
		{"copy without to", "      to: October\n", ""},
		{"empty cells", "cells: {B2: Acme, D7: \"2026-10-01\", E9: 3, F1: null, Total: {formula: SUM(E2:E9)}, G1: {value: \"007\", type: string}}", "cells: {}"},
		{"bad if_exists", "if_exists: skip", "if_exists: overwrite"},
		{"position zero", "position: 2", "position: 0"},
		{"sheet without operation", "      operation: copy\n", ""},
		{"convert without output", "      output: orders.csv\n", ""},
		{"bad encoding", "encoding: shift_jis", "encoding: latin1"},
		{"delimiter too long", "delimiter: \";\"", "delimiter: \";;\""},
		{"extract without instruction", "      instruction: Find the quote number and the total\n", ""},
		{"extract without schema", "      schema:\n        type: object\n        properties:\n          quote_no: {type: string, description: 見積番号}\n          total: {type: number}\n", ""},
		{"extract schema not an object", "schema:\n        type: object\n        properties:\n          quote_no: {type: string, description: 見積番号}\n          total: {type: number}", "schema: [a]"},
		{"extract schema a number", "schema:\n        type: object\n        properties:\n          quote_no: {type: string, description: 見積番号}\n          total: {type: number}", "schema: 3"},
		{"extract schema not type object", "        type: object\n        properties:\n          quote_no", "        type: array\n        properties:\n          quote_no"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, source, tc.from)
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}

func TestDAGSchemaXlsxLegacyType(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - id: read
    type: xlsx
    command: read
    with:
      path: orders.xlsx
      sheet: Orders
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	doc := mustParseYAMLDocument(t, strings.Replace(source, "sheet: Orders", "strip: true", 1))
	require.Error(t, resolved.Validate(doc), "type: xlsx rejects unknown with fields like the action form")
}
