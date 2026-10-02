// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"path/filepath"
)

// InspectOptions controls what Inspect samples.
type InspectOptions struct {
	Password string
	// SampleRows is how many typed rows per sheet to include; zero includes none.
	SampleRows int
}

// typeSampleRows is how many rows column types are detected from.
const typeSampleRows = 20

// Info describes a workbook the way a sheet picker or an agent needs it.
type Info struct {
	Path        string       `json:"path"`
	DateSystem  string       `json:"date_system"`
	Sheets      []SheetInfo  `json:"sheets"`
	NamedRanges []NamedRange `json:"named_ranges"`
	Warnings    []string     `json:"warnings"`
}

// SheetInfo describes one worksheet.
type SheetInfo struct {
	Name      string            `json:"name"`
	UsedRange string            `json:"used_range"`
	Range     string            `json:"range"`
	HeaderRow int               `json:"header_row"`
	Headers   []string          `json:"headers"`
	Types     map[string]string `json:"types"`
	RowCount  int               `json:"row_count"`
	Tables    []TableInfo       `json:"tables"`
	Sample    []Row             `json:"sample,omitempty"`
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

// Inspect describes every sheet: used range, detected table, headers,
// column types, row count, tables, and optionally sample rows.
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
	for _, sheet := range w.sheets {
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
	si := SheetInfo{Name: sheet, Headers: []string{}, Types: map[string]string{}, Tables: []TableInfo{}}
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

	limit := max(typeSampleRows, sampleRows)
	result, err := w.read(ctx, ReadOptions{Sheet: sheet, Range: reg.String(), MaxRows: limit, quietLimit: true})
	if err != nil {
		return si, err
	}
	for _, msg := range result.Warnings {
		warn(sheet + ": " + msg)
	}
	si.Headers = result.Headers
	si.Types = detectTypes(result.Headers, result.Rows)
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
