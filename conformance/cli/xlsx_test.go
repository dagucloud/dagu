// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"encoding/json"
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
