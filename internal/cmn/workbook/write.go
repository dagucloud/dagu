// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/xuri/excelize/v2"
)

// WriteMode says what happens to a sheet that already exists.
type WriteMode string

// Write modes.
const (
	// WriteReplace replaces the sheet's contents.
	WriteReplace WriteMode = "replace"
	// WriteAppend adds rows below the last used row.
	WriteAppend WriteMode = "append"
)

// StyleMode says how a written sheet looks.
type StyleMode string

// Style modes.
const (
	// StyleTable formats a new sheet like a finished table: bold frozen
	// header, fitted widths, and number formats by column type.
	StyleTable StyleMode = "table"
	// StyleNone writes bare cells.
	StyleNone StyleMode = "none"
)

// WriteOptions controls Write.
type WriteOptions struct {
	Password string
	// Sheet is the target sheet; empty means the first sheet, and a sheet
	// that does not exist is created.
	Sheet string
	Mode  WriteMode
	// Header writes the column names as the first row.
	Header bool
	Style  StyleMode
	// Types pins how string values are written: date and datetime strings
	// become real dates, number strings become numbers, and string keeps
	// ISO-looking text as text.
	Types map[string]ColumnType
	// InPlace saves directly instead of through a temporary file.
	InPlace bool
	DryRun  bool
	Lock    LockOptions
}

const (
	headerFill   = "DDEBF7"
	minColWidth  = 8.0
	maxColWidth  = 60.0
	fmtInteger   = 1
	fmtText      = 49
	fmtNumber    = "#,##0.00"
	fmtDate      = "yyyy-mm-dd"
	fmtDateTime  = "yyyy-mm-dd hh:mm:ss"
	tmpSheetName = "__dagu_replace__"
)

// Write creates a workbook or writes a sheet from a table. With
// WriteReplace an existing sheet is replaced; with WriteAppend rows are
// added below its last used row and no header is written. Other sheets,
// widths, styles, and defined names are preserved.
func Write(ctx context.Context, path string, table Table, opts WriteOptions) (*WriteResult, error) {
	if opts.Mode == "" {
		opts.Mode = WriteReplace
	}
	if opts.Style == "" {
		opts.Style = StyleTable
	}
	return withLock(ctx, path, opts.Lock, func() (*WriteResult, error) {
		return writeOnce(ctx, path, table, opts)
	})
}

// Append is Write with WriteAppend and no header.
func Append(ctx context.Context, path string, table Table, opts WriteOptions) (*WriteResult, error) {
	opts.Mode = WriteAppend
	opts.Header = false
	return Write(ctx, path, table, opts)
}

func writeOnce(ctx context.Context, path string, table Table, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &WriteResult{Path: path, DryRun: opts.DryRun, Warnings: []string{}}
	warning, err := checkLockFile(path)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	w, created, err := openOrCreate(path, opts.Password, opts.Sheet)
	if err != nil {
		return nil, err
	}
	defer w.close()

	sheet, err := w.targetSheet(opts.Sheet, created)
	if err != nil {
		return nil, err
	}
	result.Sheet = sheet
	result.Changes.Sheet = sheet

	startRow := 1
	fresh := created
	if !created {
		used, err := w.usedRange(sheet)
		if err != nil {
			return nil, err
		}
		empty := used.R2 == 1 && used.C2 == 1 && cellAt(mustGrid(w, sheet), 1, 1) == ""
		switch {
		case opts.Mode == WriteAppend && !empty:
			startRow = lastUsedRow(mustGrid(w, sheet), used) + 1
		case opts.Mode == WriteAppend:
			fresh = true
		case !empty:
			if err := w.replaceSheet(sheet); err != nil {
				return nil, err
			}
			fresh = true
		default:
			fresh = true
		}
	}

	kinds := columnKinds(table, opts.Types)
	dataRow := startRow
	cells := 0
	// An append below existing rows never writes a header; an append that
	// starts an empty sheet writes one so the first run creates a table.
	writeHeader := opts.Header
	if opts.Mode == WriteAppend {
		writeHeader = fresh && len(table.Columns) > 0
	}
	if writeHeader {
		for c, name := range table.Columns {
			if err := w.f.SetCellStr(sheet, cellName(c+1, startRow), name); err != nil {
				return nil, w.cellError(sheet, c+1, startRow, err.Error())
			}
		}
		cells += len(table.Columns)
		dataRow++
	}
	for i, row := range table.Rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r := dataRow + i
		for c, value := range row {
			if c >= len(table.Columns) {
				break
			}
			// Only a pinned type converts values; the detected kind picks
			// the column's number format and leaves mixed columns alone.
			v, err := outValue(value, opts.Types[table.Columns[c]])
			if err != nil {
				return nil, w.cellError(sheet, c+1, r, err.Error())
			}
			if v == nil {
				continue
			}
			if err := w.setCell(sheet, c+1, r, v); err != nil {
				return nil, err
			}
			cells++
		}
	}
	lastRow := dataRow + len(table.Rows) - 1
	if lastRow < startRow {
		lastRow = startRow
	}
	if len(table.Columns) > 0 {
		result.Changes.Range = region{Sheet: sheet, C1: 1, R1: startRow, C2: len(table.Columns), R2: lastRow}.String()
	}
	result.Changes.RowsAppended = len(table.Rows)
	result.Changes.CellsChanged = cells

	switch {
	case fresh && opts.Style == StyleTable && len(table.Columns) > 0:
		if err := w.styleTable(sheet, table, kinds, startRow, writeHeader, lastRow); err != nil {
			return nil, err
		}
	case !fresh && len(table.Rows) > 0:
		w.copyStylesFromAbove(sheet, startRow, lastRow, len(table.Columns), table, kinds)
	}
	w.forget(sheet)
	if opts.DryRun {
		return result, nil
	}
	if err := w.save(opts.InPlace); err != nil {
		return nil, err
	}
	return result, nil
}

func mustGrid(w *file, sheet string) [][]string {
	grid, _ := w.grid(sheet)
	return grid
}

// targetSheet resolves the sheet a write goes to, creating it when the name
// is new.
func (w *file) targetSheet(name string, created bool) (string, error) {
	if created {
		return w.sheets[0], nil
	}
	sheet, err := w.resolveSheet(name)
	if err == nil {
		return sheet, nil
	}
	var notFound *SheetNotFoundError
	if !errors.As(err, &notFound) {
		return "", err
	}
	if _, err := w.f.NewSheet(name); err != nil {
		return "", fmt.Errorf("%s: invalid sheet name %q: %v", w.base, name, err)
	}
	w.sheets = w.f.GetSheetList()
	return name, nil
}

// replaceSheet empties a sheet while keeping its name and position: a
// temporary sheet is created, the old one deleted, and the new one renamed
// and moved back, because the last sheet of a workbook cannot be deleted.
func (w *file) replaceSheet(name string) error {
	list := w.f.GetSheetList()
	index := -1
	for i, s := range list {
		if s == name {
			index = i
		}
	}
	if _, err := w.f.NewSheet(tmpSheetName); err != nil {
		return fmt.Errorf("%s: %w", w.base, err)
	}
	if err := w.f.DeleteSheet(name); err != nil {
		return fmt.Errorf("%s: %w", w.base, err)
	}
	if err := w.f.SetSheetName(tmpSheetName, name); err != nil {
		return fmt.Errorf("%s: %w", w.base, err)
	}
	if index >= 0 && index < len(list)-1 {
		if err := w.f.MoveSheet(name, list[index+1]); err != nil {
			return fmt.Errorf("%s: %w", w.base, err)
		}
	}
	w.sheets = w.f.GetSheetList()
	w.forget(name)
	return nil
}

// lastUsedRow walks back over fully empty rows at the end of the used range.
func lastUsedRow(grid [][]string, used region) int {
	r := used.R2
	for r > 0 && rowIsEmpty(grid, r, 1, used.C2) {
		r--
	}
	return r
}

// columnKinds picks the kind each column is written as: the pinned type, or
// the dominant kind of its values.
func columnKinds(table Table, types map[string]ColumnType) []ColumnType {
	kinds := make([]ColumnType, len(table.Columns))
	for c, name := range table.Columns {
		if t, ok := types[name]; ok {
			kinds[c] = t
			continue
		}
		counts := map[string]int{}
		for _, row := range table.Rows {
			if c < len(row) {
				if k := detectKind(row[c]); k != "" {
					counts[k]++
				}
			}
		}
		best, bestCount := "", 0
		for _, k := range []string{string(TypeString), string(TypeInteger), string(TypeNumber), string(TypeDate), string(TypeDateTime), string(TypeBoolean)} {
			if counts[k] > bestCount {
				best, bestCount = k, counts[k]
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
		kinds[c] = ColumnType(best)
	}
	return kinds
}

// outValue converts a table value into what the cell receives. Pinned
// types convert strings; otherwise ISO date and datetime strings become
// dates and everything else is written as it is.
func outValue(v any, kind ColumnType) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch kind {
	case TypeString:
		return valueString(v), nil
	case TypeNumber, TypeInteger, TypeBoolean:
		return coerce(v, kind)
	case TypeDate, TypeDateTime:
		t, ok := toTime(v)
		if !ok {
			return nil, fmt.Errorf("expected %s, found %s", kind, describe(v))
		}
		return t, nil
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(dateLayout, s); err == nil {
			return t, nil
		}
		if t, err := time.Parse(dateTimeLayout, s); err == nil {
			return t, nil
		}
	}
	return v, nil
}

func (w *file) setCell(sheet string, col, row int, v any) error {
	cell := cellName(col, row)
	var err error
	switch x := v.(type) {
	case bool:
		err = w.f.SetCellBool(sheet, cell, x)
	case int64:
		err = w.f.SetCellInt(sheet, cell, x)
	case int:
		err = w.f.SetCellInt(sheet, cell, int64(x))
	case float64:
		if x == math.Trunc(x) && math.Abs(x) <= maxExactInt {
			err = w.f.SetCellInt(sheet, cell, int64(x))
		} else {
			err = w.f.SetCellFloat(sheet, cell, x, -1, 64)
		}
	case time.Time:
		err = w.f.SetCellValue(sheet, cell, x)
	case string:
		err = w.f.SetCellStr(sheet, cell, x)
	default:
		err = w.f.SetCellStr(sheet, cell, fmt.Sprint(x))
	}
	if err != nil {
		return w.cellError(sheet, col, row, err.Error())
	}
	return nil
}

// styleTable makes a fresh sheet look finished: bold header on a light
// fill, frozen below the header, widths fitted to content, and number
// formats by column kind.
func (w *file) styleTable(sheet string, table Table, kinds []ColumnType, startRow int, header bool, lastRow int) error {
	if header {
		id, err := w.f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{headerFill}},
			Alignment: &excelize.Alignment{Vertical: "center"},
		})
		if err != nil {
			return err
		}
		if err := w.f.SetCellStyle(sheet, cellName(1, startRow), cellName(len(table.Columns), startRow), id); err != nil {
			return err
		}
		if err := w.f.SetPanes(sheet, &excelize.Panes{
			Freeze: true, YSplit: startRow, TopLeftCell: cellName(1, startRow+1), ActivePane: "bottomLeft",
		}); err != nil {
			return err
		}
	}
	dataRow := startRow
	if header {
		dataRow++
	}
	for c, name := range table.Columns {
		width := displayWidth(name)
		for _, row := range table.Rows {
			if c < len(row) {
				width = max(width, displayWidth(valueString(row[c])))
			}
		}
		col, _ := excelize.ColumnNumberToName(c + 1)
		if err := w.f.SetColWidth(sheet, col, col, math.Min(math.Max(float64(width)+2, minColWidth), maxColWidth)); err != nil {
			return err
		}
		if lastRow < dataRow {
			continue
		}
		style := kindStyle(kinds[c])
		if style == nil {
			continue
		}
		id, err := w.f.NewStyle(style)
		if err != nil {
			return err
		}
		if err := w.f.SetCellStyle(sheet, cellName(c+1, dataRow), cellName(c+1, lastRow), id); err != nil {
			return err
		}
	}
	return nil
}

func kindStyle(kind ColumnType) *excelize.Style {
	switch kind {
	case TypeInteger:
		return &excelize.Style{NumFmt: fmtInteger}
	case TypeNumber:
		f := fmtNumber
		return &excelize.Style{CustomNumFmt: &f}
	case TypeDate:
		f := fmtDate
		return &excelize.Style{CustomNumFmt: &f}
	case TypeDateTime:
		f := fmtDateTime
		return &excelize.Style{CustomNumFmt: &f}
	case TypeString:
		return &excelize.Style{NumFmt: fmtText}
	default:
		return nil
	}
}

// copyStylesFromAbove gives appended cells the style of the cell above
// them, so a date column stays a date column, and applies a date style
// when a time lands under a cell that is not one.
func (w *file) copyStylesFromAbove(sheet string, startRow, lastRow, columns int, table Table, kinds []ColumnType) {
	for c := 1; c <= columns; c++ {
		above, err := w.f.GetCellStyle(sheet, cellName(c, startRow-1))
		if err != nil {
			continue
		}
		kind := kindNumber
		if above != 0 {
			kind = w.styleKind(above)
		}
		for r := startRow; r <= lastRow; r++ {
			i := r - startRow
			if opts := table.Rows; i >= len(opts) || c-1 >= len(opts[i]) || opts[i][c-1] == nil {
				continue
			}
			isTime := kinds[c-1] == TypeDate || kinds[c-1] == TypeDateTime
			if !isTime {
				if _, ok := outValueIsTime(table.Rows[i][c-1]); ok {
					isTime = true
				}
			}
			switch {
			case isTime && kind == kindNumber:
				if style := kindStyle(kindFor(table.Rows[i][c-1], kinds[c-1])); style != nil {
					if id, err := w.f.NewStyle(style); err == nil {
						_ = w.f.SetCellStyle(sheet, cellName(c, r), cellName(c, r), id)
					}
				}
			case above != 0:
				_ = w.f.SetCellStyle(sheet, cellName(c, r), cellName(c, r), above)
			}
		}
	}
}

func outValueIsTime(v any) (time.Time, bool) {
	out, err := outValue(v, "")
	if err != nil {
		return time.Time{}, false
	}
	t, ok := out.(time.Time)
	return t, ok
}

func kindFor(v any, pinned ColumnType) ColumnType {
	if pinned == TypeDate || pinned == TypeDateTime {
		return pinned
	}
	if s, ok := v.(string); ok && len(s) > len(dateLayout) {
		return TypeDateTime
	}
	if t, ok := v.(time.Time); ok && (t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0) {
		return TypeDateTime
	}
	return TypeDate
}

// displayWidth approximates how many character cells the widest line of a
// string takes, with East Asian characters counting double.
func displayWidth(s string) int {
	width, line := 0, 0
	for _, r := range s {
		switch {
		case r == '\n':
			line = 0
		case r >= 0x1100 && !isNarrow(r):
			line += 2
		default:
			line++
		}
		width = max(width, line)
	}
	return width
}

func isNarrow(r rune) bool {
	return r >= 0xFF61 && r <= 0xFF9F // half-width katakana
}
