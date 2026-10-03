// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestXlsxInspectAndReadCommands(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	dagu.RunWithEnv(env, "start", "xlsx_write.yaml").ExpectExitCode(0)

	inspect := dagu.Run("xlsx", "inspect", "orders.xlsx", "--format", "json", "--rows", "1")
	inspect.ExpectExitCode(0)
	var info struct {
		DateSystem string `json:"date_system"`
		Sheets     []struct {
			Name      string            `json:"name"`
			HeaderRow int               `json:"header_row"`
			Headers   []string          `json:"headers"`
			Types     map[string]string `json:"types"`
			RowCount  int               `json:"row_count"`
			Sample    []map[string]any  `json:"sample"`
		} `json:"sheets"`
	}
	require.NoError(t, json.Unmarshal([]byte(inspect.Stdout()), &info), inspect.Stdout())
	require.Equal(t, "1900", info.DateSystem)
	require.Len(t, info.Sheets, 1)
	require.Equal(t, "Orders", info.Sheets[0].Name)
	require.Equal(t, 1, info.Sheets[0].HeaderRow)
	require.Equal(t, []string{"Invoice No", "Amount", "Due"}, info.Sheets[0].Headers)
	require.Equal(t, "number", info.Sheets[0].Types["Amount"])
	require.Equal(t, "date", info.Sheets[0].Types["Due"])
	require.Equal(t, 2, info.Sheets[0].RowCount)
	require.Len(t, info.Sheets[0].Sample, 1)

	read := dagu.Run("xlsx", "read", "orders.xlsx", "--format", "json")
	read.ExpectExitCode(0)
	var result struct {
		Rows    []map[string]any `json:"rows"`
		Count   int              `json:"count"`
		Headers []string         `json:"headers"`
		Range   string           `json:"range"`
	}
	require.NoError(t, json.Unmarshal([]byte(read.Stdout()), &result), read.Stdout())
	require.Equal(t, 2, result.Count)
	require.Equal(t, []string{"Invoice No", "Amount", "Due"}, result.Headers)
	require.Equal(t, "Orders!A1:C3", result.Range)
	require.Equal(t, float64(2), result.Rows[0]["_row"])
	require.Equal(t, "2026-10-01", result.Rows[0]["Due"])

	text := dagu.Run("xlsx", "inspect", "orders.xlsx")
	text.ExpectExitCode(0)
	require.Contains(t, text.Stdout(), `Sheet "Orders": used A1:C3`)

	missing := dagu.Run("xlsx", "read", "missing.xlsx")
	missing.ExpectNonZeroExitCode()
	missing.ExpectStderrContains("missing.xlsx: workbook not found")
}

func TestXlsxReadCommandFlags(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(sharedEnv(t), "start", "xlsx_two_sheets.yaml").ExpectExitCode(0)

	type result struct {
		Rows      []map[string]any `json:"rows"`
		Count     int              `json:"count"`
		Headers   []string         `json:"headers"`
		Sheet     string           `json:"sheet"`
		Range     string           `json:"range"`
		Truncated bool             `json:"truncated"`
	}
	for _, tc := range []struct {
		name  string
		flags []string
		want  result
	}{
		{"sheet", []string{"--sheet", "Second"}, result{Count: 3, Headers: []string{"Quarterly report", "Q3"}, Sheet: "Second", Range: "Second!A1:B4"}},
		{"header row", []string{"--sheet", "Second", "--header", "2"}, result{Count: 2, Headers: []string{"Item", "Qty"}, Sheet: "Second", Range: "Second!A2:B4"}},
		{"range", []string{"--range", "B1:C"}, result{Count: 2, Headers: []string{"Amount", "Due"}, Sheet: "Orders", Range: "Orders!B1:C3"}},
		{"header false", []string{"--header", "false"}, result{Count: 3, Headers: []string{"A", "B", "C"}, Sheet: "Orders", Range: "Orders!A1:C3"}},
		{"columns", []string{"--columns", "Invoice No:inv,Amount"}, result{Count: 2, Headers: []string{"inv", "Amount"}, Sheet: "Orders", Range: "Orders!A1:C3"}},
		{"max rows", []string{"--max-rows", "1"}, result{Count: 1, Headers: []string{"Invoice No", "Amount", "Due"}, Sheet: "Orders", Range: "Orders!A1:C3", Truncated: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := dagu.Run(append([]string{"xlsx", "read", "orders.xlsx", "--format", "json"}, tc.flags...)...)
			read.ExpectExitCode(0)
			var got result
			require.NoError(t, json.Unmarshal([]byte(read.Stdout()), &got), read.Stdout())
			require.Len(t, got.Rows, tc.want.Count)
			got.Rows = nil
			require.Equal(t, tc.want, got)
		})
	}

	columns := dagu.Run("xlsx", "read", "orders.xlsx", "--format", "json", "--columns", "Invoice No:inv,Amount")
	columns.ExpectExitCode(0)
	var renamed result
	require.NoError(t, json.Unmarshal([]byte(columns.Stdout()), &renamed), columns.Stdout())
	require.Equal(t, "INV-1", renamed.Rows[0]["inv"])
	require.NotContains(t, renamed.Rows[0], "Due")

	text := dagu.Run("xlsx", "read", "orders.xlsx")
	text.ExpectExitCode(0)
	lines := strings.Split(strings.ReplaceAll(text.Stdout(), "\r\n", "\n"), "\n")
	require.Equal(t, "_row\tInvoice No\tAmount\tDue", lines[0], "the text form is tab-separated with _row first")
	require.Equal(t, "2\tINV-1\t10\t2026-10-01", lines[1])
	require.Len(t, lines, 4, "one line per row and a trailing newline")
}

func TestXlsxInspectCommandSheet(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(sharedEnv(t), "start", "xlsx_two_sheets.yaml").ExpectExitCode(0)

	all := dagu.Run("xlsx", "inspect", "orders.xlsx", "--format", "json")
	all.ExpectExitCode(0)
	var info struct {
		Sheets []struct {
			Name string `json:"name"`
		} `json:"sheets"`
	}
	require.NoError(t, json.Unmarshal([]byte(all.Stdout()), &info), all.Stdout())
	require.Len(t, info.Sheets, 2)

	one := dagu.Run("xlsx", "inspect", "orders.xlsx", "--format", "json", "--sheet", "Second")
	one.ExpectExitCode(0)
	require.NoError(t, json.Unmarshal([]byte(one.Stdout()), &info), one.Stdout())
	require.Len(t, info.Sheets, 1)
	require.Equal(t, "Second", info.Sheets[0].Name)

	missing := dagu.Run("xlsx", "inspect", "orders.xlsx", "--sheet", "Nope")
	missing.ExpectNonZeroExitCode()
	missing.ExpectStderrContains(`orders.xlsx: sheet "Nope" not found; sheets present: Orders, Second`)

	text := dagu.Run("xlsx", "inspect", "orders.xlsx", "--rows", "1")
	text.ExpectExitCode(0)
	lines := strings.Split(strings.ReplaceAll(text.Stdout(), "\r\n", "\n"), "\n")
	require.Equal(t, "orders.xlsx: 2 sheets, 1900 date system", lines[0])
	require.Equal(t, `Sheet "Orders": used A1:C3, table Orders!A1:C3, header row 1, 2 rows`, lines[1])
	require.Equal(t, "  Columns: Invoice No (string), Amount (number), Due (date)", lines[2])
	require.Equal(t, "  Row 2: Invoice No=INV-1  Amount=10  Due=2026-10-01", lines[3])
}
