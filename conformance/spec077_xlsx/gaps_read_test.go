// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// readOut is what a reading fixture captures for one xlsx.read step.
type readOut struct {
	Sheet     string           `json:"sheet"`
	Range     string           `json:"range"`
	Count     int              `json:"count"`
	Headers   []string         `json:"headers"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
	Warnings  []string         `json:"warnings"`
}

func names(rows []map[string]any, column string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprint(row[column]))
	}
	return out
}

// writeCSV writes a header line and n numbered rows the fixture loads with
// xlsx.write input; width pads the text column so a row costs that many
// bytes in the output.
func writeCSV(t *testing.T, dagu *harness.Runner, name string, n, width int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("n,text\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d,%s\n", i, strings.Repeat("x", width))
	}
	dagu.WriteFile(name, b.String())
}

func TestXlsxReadAddressing(t *testing.T) {
	t.Parallel()
	t.Run("paths", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "read_paths.yaml", "--", "BOOK="+dagu.ProjectPath("abs.xlsx")).ExpectExitCode(0)
		var out struct {
			Tilde int `json:"tilde"`
			Abs   int `json:"abs"`
		}
		readJSON(t, dagu, "out.json", &out)
		require.Equal(t, 1, out.Tilde, "~ resolves to the home directory")
		require.Equal(t, 2, out.Abs, "an absolute path is used as written")
	})
	t.Run("ranges", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "read_ranges.yaml").ExpectExitCode(0)
		var out map[string]readOut
		readJSON(t, dagu, "out.json", &out)

		closed := out["closed"]
		require.Equal(t, "Other!A2:C3", closed.Range)
		require.Equal(t, []string{"A", "B", "C"}, closed.Headers)
		require.Equal(t, []string{"1", "4"}, names(closed.Rows, "A"))

		columns := out["columns_only"]
		require.Equal(t, "Other!A1:C4", columns.Range, "A:C ends at the last used row")
		require.Equal(t, []string{"a", "b", "c"}, columns.Headers)
		require.Equal(t, 3, columns.Count)

		single := out["single"]
		require.Equal(t, "Other!B2:B2", single.Range)
		require.Equal(t, []string{"B"}, single.Headers)
		require.Equal(t, []string{"2"}, names(single.Rows, "B"))

		quoted := out["quoted_sheet"]
		require.Equal(t, "My Sheet", quoted.Sheet, "the sheet in the range wins over sheet")
		require.Equal(t, "My Sheet!A2:C3", quoted.Range, "the range output does not quote a sheet name")
		require.Equal(t, []string{"10", "13"}, names(quoted.Rows, "A"))

		plain := out["plain_sheet"]
		require.Equal(t, "Other", plain.Sheet)
		require.Equal(t, "Other!B2:C4", plain.Range)
		require.Equal(t, []string{"2", "5", "8"}, names(plain.Rows, "B"))
	})
	t.Run("headers", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "read_headers.yaml").ExpectExitCode(0)
		var out readOut
		readJSON(t, dagu, "out.json", &out)
		require.Equal(t, []string{"Invoice", "B", "Amount", "Amount_2", "Note Text"}, out.Headers,
			"header text is trimmed, line breaks become spaces, an empty header is the column letter, a duplicate gets _2")
		require.Equal(t, "Sheet1!A3:E5", out.Range, "header: 3 starts the block at row 3")
		require.Equal(t, 2, out.Count)
		require.Equal(t, []string{"INV-1", "INV-2"}, names(out.Rows, "Invoice"))
		require.Equal(t, []string{"x", "y"}, names(out.Rows, "B"))
		require.Equal(t, []string{"2", "4"}, names(out.Rows, "Amount_2"))
		require.Len(t, out.Warnings, 2)
		require.Contains(t, out.Warnings[0], "Sheet1!B3: header is empty; column named B")
		require.Contains(t, out.Warnings[1], `Sheet1: duplicate header "Amount" renamed Amount_2`)
	})
}

func TestXlsxFormulasAndEmptyRows(t *testing.T) {
	t.Parallel()
	t.Run("formulas", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "read_formulas.yaml").ExpectExitCode(0)
		var out map[string]readOut
		readJSON(t, dagu, "out.json", &out)

		cached := out["cached"]
		require.Equal(t, float64(4), cached.Rows[0]["b"], "a formula without a cached value is evaluated")
		require.Nil(t, cached.Rows[1]["b"], "a formula that evaluates to an error is null")
		require.Len(t, cached.Warnings, 2)
		require.Contains(t, cached.Warnings[0], "Sheet1!B2: formula had no cached value; evaluated")
		require.Contains(t, cached.Warnings[1], "Sheet1!B3: formula NA() could not be evaluated: #N/A")

		text := out["text"]
		require.Equal(t, "=A2*2", text.Rows[0]["b"])
		require.Equal(t, "=NA()", text.Rows[1]["b"])
		require.Empty(t, text.Warnings)

		calculate := out["calculate"]
		require.Equal(t, float64(4), calculate.Rows[0]["b"])
		require.Nil(t, calculate.Rows[1]["b"])
	})
	t.Run("empty rows", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.Run("start", "read_empty_rows.yaml").ExpectExitCode(0)
		var out map[string]readOut
		readJSON(t, dagu, "out.json", &out)

		require.Equal(t, 3, out["default"].Count, "trailing empty rows are dropped")
		require.Equal(t, []string{"1", "<nil>", "2"}, names(out["default"].Rows, "n"))
		require.False(t, out["default"].Truncated)
		require.Equal(t, 5, out["keep"].Count, "keep_empty_rows keeps every row of the range")
		require.Equal(t, 1, out["stop"].Count, "stop_at_blank stops at the first blank row")
		require.Equal(t, []string{"1"}, names(out["stop"].Rows, "n"))
		capped := out["capped"]
		require.Equal(t, 3, capped.Count, "a blank row between counted rows is kept without counting")
		require.False(t, capped.Truncated, "exactly max_rows countable rows is not truncated")
		require.Empty(t, capped.Warnings)
	})
}

func TestXlsxWhere(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "read_where.yaml").ExpectExitCode(0)
	var out map[string][]map[string]any
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, []string{"a"}, names(out["scalar"], "Name"), "a scalar matches equal values")
	require.Equal(t, []string{"b"}, names(out["empty"], "Name"), `"" matches empty cells`)
	require.Equal(t, []string{"a", " c "}, names(out["in_list"], "Name"), "in matches a list")
	require.Equal(t, []string{"a", "b"}, names(out["numeric"], "Name"), "numbers compare numerically, text that reads as the number included")
	require.Equal(t, []string{" c "}, names(out["loose_key"], "Name"), "a key may match a header loosely")
	require.Equal(t, []string{" c "}, names(out["trimmed"], "Name"), "text compares trimmed")
}

func TestXlsxLimits(t *testing.T) {
	t.Parallel()
	t.Run("default cap", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		writeCSV(t, dagu, "many.csv", 5001, 1)
		dagu.Run("start", "many_rows.yaml").ExpectExitCode(0)
		var out map[string]readOut
		readJSON(t, dagu, "out.json", &out)
		require.Equal(t, 5000, out["default"].Count, "max_rows defaults to 5000")
		require.True(t, out["default"].Truncated)
		require.Len(t, out["default"].Warnings, 1)
		require.Contains(t, out["default"].Warnings[0], "stopped after 5000 rows; set max_rows to read more")
		require.Equal(t, 5001, out["raised"].Count)
		require.False(t, out["raised"].Truncated)
		require.Empty(t, out["raised"].Warnings)
		require.Equal(t, 5001, out["convert"].Count, "convert reads every row")
		require.Equal(t, 5001, strings.Count(readFile(t, dagu, "many.jsonl"), "\n"))
	})
	t.Run("output budget", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		writeCSV(t, dagu, "wide.csv", 200, 100)
		dagu.Run("start", "output_budget.yaml").ExpectExitCode(0)
		var out readOut
		readJSON(t, dagu, "out.json", &out)
		require.True(t, out.Truncated, "rows beyond the output budget are left out")
		require.Less(t, out.Count, 200)
		require.Greater(t, out.Count, 0)
		require.Len(t, out.Warnings, 1)
		require.Contains(t, out.Warnings[0], fmt.Sprintf("output truncated to %d of 200 rows", out.Count))
	})
}
