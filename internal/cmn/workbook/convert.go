// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/text/transform"
)

// ConvertFormat is the file format a sheet is exported to.
type ConvertFormat string

// Convert formats.
const (
	// ConvertCSV is comma-separated text with a header line.
	ConvertCSV ConvertFormat = "csv"
	// ConvertJSON is one JSON array of objects keyed by header.
	ConvertJSON ConvertFormat = "json"
	// ConvertJSONL is one JSON object per line.
	ConvertJSONL ConvertFormat = "jsonl"
)

// ParseConvertFormat reads a format option, or takes the format from the
// output file's extension when the option is empty.
func ParseConvertFormat(format, output string) (ConvertFormat, error) {
	name := strings.ToLower(strings.TrimSpace(format))
	fromExt := name == ""
	if fromExt {
		name = strings.TrimPrefix(strings.ToLower(filepath.Ext(output)), ".")
	}
	switch name {
	case "csv":
		return ConvertCSV, nil
	case "json":
		return ConvertJSON, nil
	case "jsonl", "ndjson":
		return ConvertJSONL, nil
	}
	if fromExt {
		return "", fmt.Errorf("output extension %q is not json, jsonl, or csv; set format", filepath.Ext(output))
	}
	return "", fmt.Errorf("format must be json, jsonl, or csv")
}

// ConvertOptions controls Convert.
type ConvertOptions struct {
	Password string
	// Output is the file written; its extension names the format unless
	// Format does.
	Output   string
	Format   ConvertFormat
	Sheet    string
	Range    string
	Header   HeaderSpec
	Columns  []ColumnSelect
	Types    map[string]ColumnType
	Trim     bool
	Merged   MergedMode
	Formulas FormulaMode
	// SkipHidden leaves out rows hidden by a filter or by hand.
	SkipHidden bool
	// Encoding applies to CSV; empty means UTF-8.
	Encoding Encoding
	// Delimiter applies to CSV; zero means a comma.
	Delimiter rune
	// InPlace writes the output directly instead of through a temporary
	// file beside it.
	InPlace bool
}

// ConvertResult is what a conversion publishes.
type ConvertResult struct {
	Path     string   `json:"path"`
	Format   string   `json:"format"`
	Count    int      `json:"count"`
	Sheet    string   `json:"sheet"`
	Range    string   `json:"range"`
	Warnings []string `json:"warnings"`
	// Artifact is the copy kept with the run, when the caller asked for one.
	Artifact string `json:"artifact,omitempty"`
}

// Convert writes the rows of a sheet to a CSV, JSON, or JSONL file. Every
// row is written, with no cap and no output budget, since the rows go to a
// file. Columns follow the header order, and the row number field is not
// written: the file is a table.
func Convert(ctx context.Context, path string, opts ConvertOptions) (*ConvertResult, error) {
	format := opts.Format
	if format == "" {
		var err error
		if format, err = ParseConvertFormat("", opts.Output); err != nil {
			return nil, err
		}
	}
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	rows, err := w.read(ctx, ReadOptions{
		Sheet: opts.Sheet, Range: opts.Range, Header: opts.Header, Columns: opts.Columns,
		Types: opts.Types, Trim: opts.Trim, Merged: opts.Merged, Formulas: opts.Formulas,
		SkipHidden: opts.SkipHidden, OnTypeError: TypeErrorFail, noLimit: true,
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	write := func(out io.Writer) error {
		switch format {
		case ConvertCSV:
			return writeCSV(ctx, out, rows, opts.Encoding, opts.Delimiter)
		case ConvertJSON:
			return writeJSONRows(ctx, out, rows, true)
		case ConvertJSONL:
			return writeJSONRows(ctx, out, rows, false)
		default:
			return fmt.Errorf("unknown format %q", format)
		}
	}
	if err := writeFileAtomic(opts.Output, opts.InPlace, write); err != nil {
		return nil, fmt.Errorf("%s: %w", Base(opts.Output), err)
	}
	return &ConvertResult{
		Path: opts.Output, Format: string(format), Count: rows.Count,
		Sheet: rows.Sheet, Range: rows.Range, Warnings: rows.Warnings,
	}, nil
}

// writeCSV writes a header line and one line per row. Dates stay ISO text,
// booleans are true and false, and empty cells are empty fields.
func writeCSV(ctx context.Context, out io.Writer, rows *ReadResult, enc Encoding, delimiter rune) error {
	target := io.Writer(out)
	if enc == EncodingUTF8BOM {
		if _, err := out.Write([]byte("\xEF\xBB\xBF")); err != nil {
			return err
		}
	}
	var flush func() error
	if cs := enc.charset(); cs != nil {
		tw := transform.NewWriter(out, cs.NewEncoder())
		target, flush = tw, tw.Close
	}
	cw := csv.NewWriter(target)
	if delimiter != 0 {
		cw.Comma = delimiter
	}
	if err := cw.Write(rows.Headers); err != nil {
		return err
	}
	record := make([]string, len(rows.Headers))
	for i, row := range rows.Rows {
		if err := cancelled(ctx, i); err != nil {
			return err
		}
		for j, name := range rows.Headers {
			record[j] = ""
			if v := row[name]; v != nil {
				record[j] = valueString(v)
			}
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	if flush != nil {
		return flush()
	}
	return nil
}

// writeJSONRows writes the rows as objects whose keys follow the header
// order: one array for JSON, one object per line for JSONL.
func writeJSONRows(ctx context.Context, out io.Writer, rows *ReadResult, array bool) error {
	bw := bufio.NewWriter(out)
	if array {
		if _, err := bw.WriteString("[\n"); err != nil {
			return err
		}
	}
	keys := make([][]byte, len(rows.Headers))
	for i, name := range rows.Headers {
		key, err := json.Marshal(name)
		if err != nil {
			return err
		}
		keys[i] = key
	}
	for i, row := range rows.Rows {
		if err := cancelled(ctx, i); err != nil {
			return err
		}
		if array && i > 0 {
			if _, err := bw.WriteString(",\n"); err != nil {
				return err
			}
		}
		if err := bw.WriteByte('{'); err != nil {
			return err
		}
		for j, name := range rows.Headers {
			if j > 0 {
				if err := bw.WriteByte(','); err != nil {
					return err
				}
			}
			value, err := json.Marshal(row[name])
			if err != nil {
				return err
			}
			if _, err := bw.Write(keys[j]); err != nil {
				return err
			}
			if err := bw.WriteByte(':'); err != nil {
				return err
			}
			if _, err := bw.Write(value); err != nil {
				return err
			}
		}
		if _, err := bw.WriteString(map[bool]string{true: "}", false: "}\n"}[array]); err != nil {
			return err
		}
	}
	if array {
		if _, err := bw.WriteString("\n]\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// cancelled reports the context's error every few hundred rows, so a long
// export stops soon after the run is cancelled without paying for a check
// on every row.
func cancelled(ctx context.Context, row int) error {
	if row%256 == 0 {
		return ctx.Err()
	}
	return nil
}
