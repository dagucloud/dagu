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
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, source, tc.from)
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
