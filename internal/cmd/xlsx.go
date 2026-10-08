// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/spf13/cobra"
)

// xlsxPasswordEnv supplies a workbook password when --password is omitted.
const xlsxPasswordEnv = "DAGU_XLSX_PASSWORD" //nolint:gosec // This is an environment variable name, not a credential.

// Xlsx returns the command group for workbooks. inspect and read open a file
// directly, need no configuration or engine, and create no run, so a host
// can call them to show a sheet picker or a typed preview; cache clear is
// the exception, acting on the data directory of the configured host.
func Xlsx() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "xlsx",
		Short:        "Inspect and read .xlsx workbooks without a run",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(xlsxInspectCommand())
	cmd.AddCommand(xlsxReadCommand())
	cmd.AddCommand(xlsxCacheCommand())
	return cmd
}

func xlsxInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <path>",
		Short: "Describe the sheets of a workbook",
		Long: `Describe every sheet of a workbook: its used range, the detected data
block, the header row and column names, the type of each column, the
number of data rows, its tables, and a few typed sample rows. Named ranges
and the date system are listed for the workbook.

A sheet hidden from the workbook's tabs is marked (hidden), and a sheet
with rows hidden by a filter or by hand counts them, as in "300 rows, 12
hidden"; dagu xlsx read --skip-hidden leaves those rows out.

Types and a profile of each column come from every data row of the block,
up to 5000: how many cells are filled and blank, how many distinct values
there are, the values themselves when a few repeat, the lowest and highest
number or date, and the cells that do not read as the column's type, such
as 未定 in a number column. The text format shows the profile after each
column name:

  Columns: 状態 (string: 済, 未; 40 blank), 数量 (number; 1..250; 1 odd: D300 "未定")

This is what xlsx.info publishes, read straight from the file. Nothing is
written and no run is created.

A protected workbook needs a password. Set DAGU_XLSX_PASSWORD rather
than --password where other users share the host: a flag value shows in
the process list and stays in shell history. The flag wins when both are
set.

With --format json, the result is one JSON object: path, date_system,
sheets (each with name, hidden, used_range, range, header_row, headers,
types, row_count, hidden_rows, columns, profile_truncated, tables, and
sample), named_ranges, and warnings. Each column has name, type, filled,
blank, distinct, values, min, max, odd, and odd_cells.
`,
		Example: `  dagu xlsx inspect orders.xlsx
  dagu xlsx inspect orders.xlsx --sheet Orders --rows 10
  dagu xlsx inspect orders.xlsx --format json`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         runXlsxInspect,
	}
	cmd.Flags().IntP("rows", "n", 5, "Typed sample rows to show per sheet")
	cmd.Flags().String("sheet", "", "Describe this sheet only")
	cmd.Flags().String("password", "", "Password of a protected workbook; prefer DAGU_XLSX_PASSWORD, which keeps it out of the process list")
	cmd.Flags().StringP("format", "f", "text", "Output format: text or json (default: text)")
	return cmd
}

func xlsxReadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read <path>",
		Short: "Print the typed rows of a sheet",
		Long: `Print the rows of a sheet the way xlsx.read publishes them: numbers stay
numbers, dates become ISO 8601 text, text keeps its leading zeros, and each
row carries _row, its sheet row number. Rows hidden by a filter or by hand
are read unless --skip-hidden leaves them out.

With --format json, the result is one JSON object: rows, count, headers,
sheet, range, warnings, and truncated. The text format prints a tab-separated
header line and one line per row.

A protected workbook needs a password. Set DAGU_XLSX_PASSWORD rather
than --password where other users share the host: a flag value shows in
the process list and stays in shell history. The flag wins when both are
set.
`,
		Example: `  dagu xlsx read orders.xlsx
  dagu xlsx read orders.xlsx --sheet Orders --range A2:F --header false
  dagu xlsx read orders.xlsx --columns "Invoice No,Amount" --format json
  dagu xlsx read orders.xlsx --skip-hidden`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         runXlsxRead,
	}
	cmd.Flags().String("sheet", "", "Sheet name; the first sheet by default")
	cmd.Flags().String("range", "", "Cell range, Sheet!A2:F reference, named range, or table name")
	cmd.Flags().String("header", "true", "Header: true, false, a row number, or rows such as 3,4")
	cmd.Flags().String("columns", "", "Columns to keep, comma-separated, with optional name:alias renames")
	cmd.Flags().Int("max-rows", 0, "Most rows to print (default 5000)")
	cmd.Flags().Bool("skip-hidden", false, "Leave out rows hidden by a filter or by hand")
	cmd.Flags().String("password", "", "Password of a protected workbook; prefer DAGU_XLSX_PASSWORD, which keeps it out of the process list")
	cmd.Flags().StringP("format", "f", "text", "Output format: text or json (default: text)")
	return cmd
}

// xlsxPassword returns the workbook password. A --password that was set,
// including an empty one, wins over DAGU_XLSX_PASSWORD.
func xlsxPassword(cmd *cobra.Command) string {
	if cmd.Flags().Changed("password") {
		password, _ := cmd.Flags().GetString("password")
		return password
	}
	return os.Getenv(xlsxPasswordEnv)
}

func xlsxFormat(cmd *cobra.Command) (string, error) {
	format, _ := cmd.Flags().GetString("format")
	if format != "text" && format != "json" {
		return "", fmt.Errorf("invalid format %q: use text or json", format)
	}
	return format, nil
}

func xlsxPath(arg string) (string, error) {
	path := strings.TrimSpace(arg)
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	resolved, err := fileutil.ResolvePath(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func runXlsxInspect(cmd *cobra.Command, args []string) error {
	format, err := xlsxFormat(cmd)
	if err != nil {
		return err
	}
	path, err := xlsxPath(args[0])
	if err != nil {
		return err
	}
	rows, _ := cmd.Flags().GetInt("rows")
	sheet, _ := cmd.Flags().GetString("sheet")

	info, err := workbook.Inspect(cmd.Context(), path, workbook.InspectOptions{
		Password:   xlsxPassword(cmd),
		Sheet:      sheet,
		SampleRows: max(rows, 0),
	})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if format == "json" {
		return writeIndentedJSON(out, info)
	}
	return renderInfo(out, info)
}

func runXlsxRead(cmd *cobra.Command, args []string) error {
	format, err := xlsxFormat(cmd)
	if err != nil {
		return err
	}
	path, err := xlsxPath(args[0])
	if err != nil {
		return err
	}
	opts := workbook.ReadOptions{Password: xlsxPassword(cmd)}
	opts.Sheet, _ = cmd.Flags().GetString("sheet")
	opts.Range, _ = cmd.Flags().GetString("range")
	opts.MaxRows, _ = cmd.Flags().GetInt("max-rows")
	opts.SkipHidden, _ = cmd.Flags().GetBool("skip-hidden")
	header, _ := cmd.Flags().GetString("header")
	if opts.Header, err = workbook.ParseHeader(header); err != nil {
		return fmt.Errorf("--header: %w", err)
	}
	columns, _ := cmd.Flags().GetString("columns")
	if strings.TrimSpace(columns) != "" {
		if opts.Columns, err = workbook.ParseColumns(columns); err != nil {
			return fmt.Errorf("--columns: %w", err)
		}
	}

	result, err := workbook.Read(cmd.Context(), path, opts)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if format == "json" {
		return writeIndentedJSON(out, result)
	}
	return renderRows(out, result)
}

func writeIndentedJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}

// lineWriter collects the first write error so a renderer can report a
// failed stdout instead of exiting successfully with partial output.
type lineWriter struct {
	out io.Writer
	err error
}

func (w *lineWriter) printf(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, args...)
}

func renderInfo(out io.Writer, info *workbook.Info) error {
	w := &lineWriter{out: out}
	w.printf("%s: %d sheets, %s date system\n", workbook.Base(info.Path), len(info.Sheets), info.DateSystem)
	for _, s := range info.Sheets {
		name := fmt.Sprintf("%q", s.Name)
		if s.Hidden {
			name += " (hidden)"
		}
		if s.HeaderRow == 0 {
			w.printf("Sheet %s: empty\n", name)
			continue
		}
		rows := fmt.Sprintf("%d rows", s.RowCount)
		if s.HiddenRows > 0 {
			rows += fmt.Sprintf(", %d hidden", s.HiddenRows)
		}
		if s.ProfileTruncated && len(s.Columns) > 0 {
			rows += fmt.Sprintf(", first %d profiled", s.Columns[0].Filled+s.Columns[0].Blank)
		}
		w.printf("Sheet %s: used %s, table %s, header row %d, %s\n",
			name, strings.TrimPrefix(s.UsedRange, s.Name+"!"), s.Range, s.HeaderRow, rows)
		columns := make([]string, 0, len(s.Columns))
		for _, c := range s.Columns {
			columns = append(columns, columnSummary(c))
		}
		w.printf("  Columns: %s\n", strings.Join(columns, ", "))
		for _, t := range s.Tables {
			w.printf("  Table: %s %s\n", t.Name, t.Range)
		}
		for _, row := range s.Sample {
			parts := make([]string, 0, len(s.Headers))
			for _, h := range s.Headers {
				parts = append(parts, h+"="+displayValue(row[h]))
			}
			w.printf("  Row %v: %s\n", row[workbook.RowNumberKey], strings.Join(parts, "  "))
		}
	}
	for _, n := range info.NamedRanges {
		w.printf("Named range: %s = %s (%s)\n", n.Name, n.RefersTo, n.Scope)
	}
	for _, msg := range info.Warnings {
		w.printf("Warning: %s\n", msg)
	}
	return w.err
}

// columnSummary renders a column and its profile on one line: the type,
// the listed values, the range, the blank count, and the odd cells, as in
// `数量 (number; 1..250; 1 odd: D300 "未定")`.
func columnSummary(c workbook.ColumnInfo) string {
	head := c.Type
	if len(c.Values) > 0 {
		values := make([]string, 0, len(c.Values))
		for _, v := range c.Values {
			values = append(values, displayValue(v))
		}
		head += ": " + strings.Join(values, ", ")
	}
	parts := []string{head}
	if c.Min != nil {
		parts = append(parts, displayValue(c.Min)+".."+displayValue(c.Max))
	}
	if c.Blank > 0 {
		parts = append(parts, fmt.Sprintf("%d blank", c.Blank))
	}
	if c.Odd > 0 {
		cells := make([]string, 0, len(c.OddCells))
		for _, cell := range c.OddCells {
			cells = append(cells, cell.Cell+" "+strconv.Quote(cell.Text))
		}
		parts = append(parts, fmt.Sprintf("%d odd: %s", c.Odd, strings.Join(cells, ", ")))
	}
	return fmt.Sprintf("%s (%s)", c.Name, strings.Join(parts, "; "))
}

func renderRows(out io.Writer, result *workbook.ReadResult) error {
	w := &lineWriter{out: out}
	// Headers are escaped like cells: a header holding a tab or line break
	// would otherwise shift every column after it.
	headers := make([]string, 0, len(result.Headers)+1)
	headers = append(headers, workbook.RowNumberKey)
	for _, h := range result.Headers {
		headers = append(headers, textEscaper.Replace(h))
	}
	w.printf("%s\n", strings.Join(headers, "\t"))
	for _, row := range result.Rows {
		parts := make([]string, 0, len(result.Headers)+1)
		parts = append(parts, displayValue(row[workbook.RowNumberKey]))
		for _, h := range result.Headers {
			parts = append(parts, displayValue(row[h]))
		}
		w.printf("%s\n", strings.Join(parts, "\t"))
	}
	for _, msg := range result.Warnings {
		w.printf("Warning: %s\n", msg)
	}
	return w.err
}

// textEscaper keeps a cell on one line and in one column of the
// tab-separated text output.
var textEscaper = strings.NewReplacer("\\", `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// displayValue renders a typed value for the text output. Tabs, line
// breaks, and backslashes inside text are escaped so a cell never spills
// into another column or line.
func displayValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return textEscaper.Replace(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

// xlsxCacheKind names the cache in command output.
const xlsxCacheKind = "xlsx"

func xlsxCacheCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "cache",
		Short: "Manage the replay cache of xlsx.extract steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(NewCommand(&cobra.Command{
		Use:   "clear [flags] <DAG>",
		Short: "Clear the cells a DAG's xlsx.extract steps keep by sheet layout",
		Long: `Clear the cells that a DAG's xlsx.extract steps found with a model and
replay on later runs of the same sheet layout. The next run of a cleared step
asks the model again and keeps the new cells.

Identify the DAG by name or by YAML file path. Without --step, every step of
the DAG is cleared.

The cache is kept on the host that ran the step. In distributed mode, run
this command on the worker.

Examples:
  dagu xlsx cache clear quotes                   # Clear every step
  dagu xlsx cache clear quotes --step fields     # Clear one step
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{replayCacheStepFlag}, func(ctx *Context, args []string) error {
		return clearReplayCache(ctx, args[0], namedReplayCache{kind: xlsxCacheKind, store: xlsxReplayCache(ctx)})
	}))
	return cmd
}

func xlsxReplayCache(ctx *Context) *replaycache.Store {
	return replaycache.New(filepath.Join(ctx.Config.Paths.DataDir, workbook.DataDirName))
}
