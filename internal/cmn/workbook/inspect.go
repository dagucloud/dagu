// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// InspectOptions controls what Inspect describes.
type InspectOptions struct {
	Password string
	// Sheet limits the description to one sheet, matched the way a read
	// matches its sheet; empty describes every sheet.
	Sheet string
	// SampleRows is how many typed rows per sheet to include; zero includes none.
	SampleRows int
}

// maxSheetWarnings caps the read warnings reported per sheet: the profile
// reads every row, and a column of error cells warns on each one.
const maxSheetWarnings = 20

// Info describes a workbook the way a sheet picker or an agent needs it.
type Info struct {
	Path        string       `json:"path"`
	DateSystem  string       `json:"date_system"`
	Sheets      []SheetInfo  `json:"sheets"`
	NamedRanges []NamedRange `json:"named_ranges"`
	Warnings    []string     `json:"warnings"`
}

// SheetInfo describes one worksheet. Types and Columns cover the data rows
// of the detected table, up to DefaultMaxRows of them.
type SheetInfo struct {
	Name      string            `json:"name"`
	UsedRange string            `json:"used_range"`
	Range     string            `json:"range"`
	HeaderRow int               `json:"header_row"`
	Headers   []string          `json:"headers"`
	Types     map[string]string `json:"types"`
	RowCount  int               `json:"row_count"`
	// Columns profiles each column, in header order.
	Columns []ColumnInfo `json:"columns"`
	// ProfileTruncated is true when the table holds more data rows than
	// Types and Columns cover.
	ProfileTruncated bool        `json:"profile_truncated,omitempty"`
	Tables           []TableInfo `json:"tables"`
	Sample           []Row       `json:"sample,omitempty"`
}

// ColumnInfo profiles the cells of one column. Counts cover the rows that
// hold a value; a row whose cells are all empty is not counted.
type ColumnInfo struct {
	Name string `json:"name"`
	// Type is the column's entry in SheetInfo.Types.
	Type string `json:"type"`
	// Filled and Blank count the cells that hold a value and those that
	// are empty or white space only.
	Filled int `json:"filled"`
	Blank  int `json:"blank"`
	// Distinct counts the distinct values, compared as trimmed text, up
	// to 1000.
	Distinct int `json:"distinct"`
	// Values lists the distinct values in order of first appearance when
	// there are 12 or fewer and at least one repeats, so a column of
	// unique identifiers lists none. A value longer than 40 characters is
	// cut and ends in "…".
	Values []string `json:"values,omitempty"`
	// Min and Max are the lowest and highest value of an integer, number,
	// date, or datetime column, over the cells that read as that type the
	// way a pinned type reads them, so １２ counts as 12.
	Min any `json:"min,omitempty"`
	Max any `json:"max,omitempty"`
	// Odd counts the cells that hold a value which does not read as Type,
	// even when the type is pinned with types.
	Odd int `json:"odd,omitempty"`
	// OddCells names the first three odd cells.
	OddCells []OddCell `json:"odd_cells,omitempty"`
}

// OddCell is a cell whose value does not read as its column's type.
type OddCell struct {
	// Cell is the A1 address, such as D300.
	Cell string `json:"cell"`
	// Text is the cell's text, cut like ColumnInfo.Values.
	Text string `json:"text"`
}

// TableInfo is a table defined on a sheet.
type TableInfo struct {
	Name  string `json:"name"`
	Range string `json:"range"`
}

// NamedRange is a workbook or sheet scoped defined name.
type NamedRange struct {
	Name     string `json:"name"`
	RefersTo string `json:"refers_to"`
	Scope    string `json:"scope"`
}

// ListSheets returns the sheet names of a workbook in order.
func ListSheets(path, password string) ([]string, error) {
	w, err := open(path, password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	return append([]string(nil), w.sheets...), nil
}

// Inspect describes every sheet, or the one opts.Sheet names: used range,
// detected table, headers, column types, a profile of each column, row
// count, tables, and optionally sample rows.
func Inspect(ctx context.Context, path string, opts InspectOptions) (*Info, error) {
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()

	info := &Info{Path: path, DateSystem: "1900", Sheets: []SheetInfo{}, NamedRanges: []NamedRange{}, Warnings: []string{}}
	if w.date1904 {
		info.DateSystem = "1904"
	}
	for _, dn := range w.f.GetDefinedName() {
		info.NamedRanges = append(info.NamedRanges, NamedRange{Name: dn.Name, RefersTo: dn.RefersTo, Scope: dn.Scope})
	}
	sheets := w.sheets
	if strings.TrimSpace(opts.Sheet) != "" {
		sheet, err := w.resolveSheet(opts.Sheet)
		if err != nil {
			return nil, err
		}
		sheets = []string{sheet}
	}
	for _, sheet := range sheets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		si, err := w.inspectSheet(ctx, sheet, opts.SampleRows, func(msg string) {
			info.Warnings = append(info.Warnings, msg)
		})
		if err != nil {
			// Keep what was computed before the failure, such as the used
			// range and tables, and report the rest as a warning.
			info.Warnings = append(info.Warnings, sheet+": "+err.Error())
		}
		info.Sheets = append(info.Sheets, si)
	}
	return info, nil
}

func (w *file) inspectSheet(ctx context.Context, sheet string, sampleRows int, warn func(string)) (SheetInfo, error) {
	si := SheetInfo{Name: sheet, Headers: []string{}, Types: map[string]string{}, Columns: []ColumnInfo{}, Tables: []TableInfo{}}
	used, err := w.usedRange(sheet)
	if err != nil {
		return si, err
	}
	si.UsedRange = used.String()
	if tables, err := w.f.GetTables(sheet); err == nil {
		for _, t := range tables {
			si.Tables = append(si.Tables, TableInfo{Name: t.Name, Range: t.Range})
		}
	}
	grid, err := w.grid(sheet)
	if err != nil {
		return si, err
	}
	reg, ok, err := w.detectTable(sheet, grid)
	if err != nil || !ok {
		si.Range = si.UsedRange
		return si, err
	}
	si.Range = reg.String()
	si.HeaderRow = reg.R1
	si.RowCount = max(reg.rows()-1, 0)

	limit := max(DefaultMaxRows, sampleRows)
	result, err := w.read(ctx, ReadOptions{Sheet: sheet, Range: reg.String(), MaxRows: limit, quietLimit: true})
	if err != nil {
		return si, err
	}
	for i, msg := range result.Warnings {
		if i == maxSheetWarnings {
			warn(fmt.Sprintf("%s: %d more warnings", sheet, len(result.Warnings)-i))
			break
		}
		warn(sheet + ": " + msg)
	}
	si.Headers = result.Headers
	// A sample larger than the profile is read whole, but the profile
	// still stops at DefaultMaxRows.
	profiled, more := profileRows(result.Rows, result.Headers, DefaultMaxRows)
	si.Types = detectTypes(result.Headers, profiled)
	si.Columns = w.profileColumns(reg, result.Headers, si.Types, profiled)
	si.ProfileTruncated = result.Truncated || more
	if sampleRows > 0 {
		si.Sample = result.Rows[:min(sampleRows, len(result.Rows))]
	}
	return si, nil
}

// detectTypes picks the dominant non-empty kind of each column.
func detectTypes(headers []string, rows []Row) map[string]string {
	types := make(map[string]string, len(headers))
	for _, h := range headers {
		counts := map[string]int{}
		for _, row := range rows {
			if kind := detectKind(row[h]); kind != "" {
				counts[kind]++
			}
		}
		best, bestCount := string(TypeString), 0
		for _, kind := range []string{string(TypeString), string(TypeInteger), string(TypeNumber), string(TypeDate), string(TypeDateTime), string(TypeBoolean)} {
			if counts[kind] > bestCount {
				best, bestCount = kind, counts[kind]
			}
		}
		// A column mixing integers and decimals is a number column, and
		// one mixing dates and datetimes keeps the time.
		if best == string(TypeInteger) && counts[string(TypeNumber)] > 0 {
			best = string(TypeNumber)
		}
		if best == string(TypeDate) && counts[string(TypeDateTime)] > 0 {
			best = string(TypeDateTime)
		}
		types[h] = best
	}
	return types
}

// Base returns the workbook file name used in messages.
func Base(path string) string { return filepath.Base(path) }
