// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/spf13/cobra"
)

// Xlsx returns the command group for workbooks. The commands read a file
// directly, need no configuration or engine, and create no run, so a host
// can call them to show a sheet picker or a typed preview.
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

This is what xlsx.info publishes, read straight from the file. Nothing is
written and no run is created.

With --format json, the result is one JSON object: path, date_system,
sheets (each with name, used_range, range, header_row, headers, types,
row_count, tables, and sample), named_ranges, and warnings.
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
	cmd.Flags().StringP("format", "f", "text", "Output format: text or json (default: text)")
	return cmd
}

func xlsxReadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read <path>",
		Short: "Print the typed rows of a sheet",
		Long: `Print the rows of a sheet the way xlsx.read publishes them: numbers stay
numbers, dates become ISO 8601 text, text keeps its leading zeros, and each
row carries _row, its sheet row number.

With --format json, the result is one JSON object: rows, count, headers,
sheet, range, warnings, and truncated. The text format prints a tab-separated
header line and one line per row.
`,
		Example: `  dagu xlsx read orders.xlsx
  dagu xlsx read orders.xlsx --sheet Orders --range A2:F --header false
  dagu xlsx read orders.xlsx --columns "Invoice No,Amount" --format json`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         runXlsxRead,
	}
	cmd.Flags().String("sheet", "", "Sheet name; the first sheet by default")
	cmd.Flags().String("range", "", "Cell range, Sheet!A2:F reference, named range, or table name")
	cmd.Flags().String("header", "true", "Header: true, false, a row number, or rows such as 3,4")
	cmd.Flags().String("columns", "", "Columns to keep, comma-separated, with optional name:alias renames")
	cmd.Flags().Int("max-rows", 0, "Most rows to print (default 5000)")
	cmd.Flags().StringP("format", "f", "text", "Output format: text or json (default: text)")
	return cmd
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

	info, err := workbook.Inspect(cmd.Context(), path, workbook.InspectOptions{SampleRows: max(rows, 0)})
	if err != nil {
		return err
	}
	if sheet != "" {
		if err := keepSheet(info, sheet); err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	if format == "json" {
		return writeIndentedJSON(out, info)
	}
	return renderInfo(out, info)
}

func keepSheet(info *workbook.Info, name string) error {
	var kept []workbook.SheetInfo
	for _, s := range info.Sheets {
		if s.Name == name {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		for _, s := range info.Sheets {
			if strings.EqualFold(s.Name, name) {
				kept = append(kept, s)
			}
		}
	}
	if len(kept) == 0 {
		names := make([]string, 0, len(info.Sheets))
		for _, s := range info.Sheets {
			names = append(names, s.Name)
		}
		return fmt.Errorf("%s: sheet %q not found; sheets present: %s", workbook.Base(info.Path), name, strings.Join(names, ", "))
	}
	info.Sheets = kept
	return nil
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
	opts := workbook.ReadOptions{}
	opts.Sheet, _ = cmd.Flags().GetString("sheet")
	opts.Range, _ = cmd.Flags().GetString("range")
	opts.MaxRows, _ = cmd.Flags().GetInt("max-rows")
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
		if s.HeaderRow == 0 {
			w.printf("Sheet %q: empty\n", s.Name)
			continue
		}
		w.printf("Sheet %q: used %s, table %s, header row %d, %d rows\n",
			s.Name, strings.TrimPrefix(s.UsedRange, s.Name+"!"), s.Range, s.HeaderRow, s.RowCount)
		columns := make([]string, 0, len(s.Headers))
		for _, h := range s.Headers {
			columns = append(columns, fmt.Sprintf("%s (%s)", h, s.Types[h]))
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
