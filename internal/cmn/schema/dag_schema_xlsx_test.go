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
      set: {Status: status, Reviewed: {value: "yes"}}
      missing: skip
      wait_for_unlock: 5m
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
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
		{"empty key", "key: Invoice No", "key: \"\""},
		{"update_rows without key", "      key: Invoice No\n", ""},
		{"update_rows without rows", "      rows: ${steps.each.outputs.results}\n", ""},
		{"append without rows or input", "      input: rows.csv\n      format: csv\n", ""},
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
