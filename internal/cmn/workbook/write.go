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
	// Header says the sheet has a header row. A replace, or an append that
	// starts an empty sheet, writes the column names as the first row; an
	// append below existing rows matches each column to the header row by
	// name. False means no header row: nothing is written for one and rows
	// are appended by position.
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
	headerFill  = "DDEBF7"
	minColWidth = 8.0
	maxColWidth = 60.0
	fmtInteger  = 1
	fmtText     = 49
	fmtNumber   = "#,##0.00"
	fmtDate     = "yyyy-mm-dd"
	fmtDateTime = "yyyy-mm-dd hh:mm:ss"
)

// Write creates a workbook or writes a sheet from a table. With
// WriteReplace an existing sheet is replaced; with WriteAppend rows are
// added below its last used row, each column under the header cell of the
// same name, and no header is written. Other sheets, widths, styles, and
// defined names are preserved.
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

// Append is Write with WriteAppend.
func Append(ctx context.Context, path string, table Table, opts WriteOptions) (*WriteResult, error) {
	opts.Mode = WriteAppend
	return Write(ctx, path, table, opts)
}

func writeOnce(ctx context.Context, path string, table Table, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &WriteResult{Path: path, DryRun: opts.DryRun, Warnings: []string{}}
	warn := func(msg string) { result.Warnings = append(result.Warnings, msg) }
	warning, err := checkLockFile(path)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		warn(warning)
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
		default:
			// A replace clears every existing sheet, even one with no
			// values: its cells may still carry styles or hyperlinks.
			if err := w.clearSheet(sheet); err != nil {
				return nil, err
			}
			fresh = true
		}
	}

	kinds := columnKinds(table, opts.Types)
	// cols is the sheet column each table column is written to: its own
	// position, or the column of the header cell with its name when rows
	// are appended below a header row.
	cols := make([]int, len(table.Columns))
	for c := range cols {
		cols[c] = c + 1
	}
	headerRow := 0
	if opts.Mode == WriteAppend && !fresh {
		targets, err := w.alignAppend(sheet, table, opts.Header, warn)
		if err != nil {
			return nil, err
		}
		cols, headerRow = targets.cols, targets.headerRow
		result.Changes.ColumnsAdded = len(targets.added)
	}
	dataRow := startRow
	cells := 0
	// Rows appended below existing ones take their styles from the row
	// above the first of them, read once per column; a header row's style
	// never spreads into the rows below it.
	var bases []int
	if !fresh {
		bases = make([]int, len(table.Columns))
		if startRow-1 != headerRow {
			for c := range table.Columns {
				bases[c] = w.styleAt(sheet, cols[c], startRow-1)
			}
		}
	}
	// An append below existing rows never writes a header; an append that
	// starts an empty sheet writes one so the first run creates a table.
	writeHeader := opts.Header
	if opts.Mode == WriteAppend {
		writeHeader = opts.Header && fresh && len(table.Columns) > 0
	}
	if writeHeader {
		for c, name := range table.Columns {
			if err := w.f.SetCellStr(sheet, cellName(cols[c], startRow), name); err != nil {
				return nil, w.cellError(sheet, cols[c], startRow, err.Error())
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
			v, err := outValue(value, opts.Types[table.Columns[c]], w.date1904)
			if err != nil {
				return nil, w.cellError(sheet, cols[c], r, err.Error())
			}
			if v == nil {
				continue
			}
			if err := w.setCell(sheet, cols[c], r, v); err != nil {
				return nil, err
			}
			if !fresh {
				w.styleWrittenCell(sheet, cols[c], r, bases[c], v, opts.Types[table.Columns[c]])
			}
			cells++
		}
	}
	lastRow := max(dataRow+len(table.Rows)-1, startRow)
	if len(table.Columns) > 0 {
		c1, c2 := cols[0], cols[0]
		for _, c := range cols {
			c1, c2 = min(c1, c), max(c2, c)
		}
		result.Changes.Range = region{Sheet: sheet, C1: c1, R1: startRow, C2: c2, R2: lastRow}.String()
	}
	result.Changes.RowsAppended = len(table.Rows)
	result.Changes.CellsChanged = cells

	if fresh && opts.Style == StyleTable && len(table.Columns) > 0 {
		if err := w.styleTable(sheet, table, kinds, startRow, writeHeader, lastRow); err != nil {
			return nil, err
		}
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

// appendTargets says where an append below existing rows puts each table
// column.
type appendTargets struct {
	// cols is the sheet column of each table column.
	cols []int
	// headerRow is the row the names were matched against; zero when the
	// rows were placed by position.
	headerRow int
	// added lists the table columns the header row lacked, which were given
	// a header cell at the right.
	added []int
}

// alignAppend maps the columns of rows appended below existing ones to the
// sheet's columns: by position when the sheet has no header row or the rows
// are arrays, otherwise by the exact name in the header row, the way
// update_rows resolves its columns. A name the header row holds only
// loosely is refused rather than guessed; a name it lacks becomes a new
// column at the right, so no value lands under a header it was not meant
// for.
func (w *file) alignAppend(sheet string, table Table, header bool, warn func(string)) (appendTargets, error) {
	targets := appendTargets{cols: make([]int, len(table.Columns))}
	if !header || table.Positional {
		for c := range targets.cols {
			targets.cols[c] = c + 1
		}
		return targets, nil
	}
	spec := HeaderSpec{Mode: HeaderFirstRow}
	loc, err := w.locate(sheet, "", spec)
	if err != nil {
		return targets, err
	}
	if !loc.ok {
		return targets, w.sheetError(sheet, "no header row found; use header: false to append rows by position")
	}
	layout, err := layoutHeader(loc.reg, spec)
	if err != nil {
		return targets, fmt.Errorf("%s %s: %w", w.base, sheet, err)
	}
	merges, err := w.mergeMap(sheet)
	if err != nil {
		return targets, err
	}
	headers := headerNames(loc.reg, layout, loc.grid, merges, warn)
	targets.headerRow = layout.rows[0]
	next := loc.reg.C2
	seen := make(map[string]bool, len(table.Columns))
	for c, name := range table.Columns {
		// Two table columns with one name would land on one header cell and
		// the later value would overwrite the earlier one.
		if seen[name] {
			return targets, w.sheetError(sheet, fmt.Sprintf("column %q is given twice; appended columns must have distinct names", name))
		}
		seen[name] = true
		i, near := findColumn(headers, name)
		switch {
		case i >= 0:
			targets.cols[c] = loc.reg.C1 + i
		case near != "":
			return targets, w.sheetError(sheet, fmt.Sprintf("column %q not found in header row %d; did you mean %q?", name, targets.headerRow, near))
		default:
			next++
			if err := w.addHeaderColumn(sheet, targets.headerRow, next, name); err != nil {
				return targets, err
			}
			targets.cols[c] = next
			targets.added = append(targets.added, c)
		}
	}
	return targets, nil
}

// addHeaderColumn writes the header cell of a column added at the right of
// a header row, copying the style of the header cell to its left so the
// header keeps one look.
func (w *file) addHeaderColumn(sheet string, headerRow, col int, name string) error {
	cell := cellName(col, headerRow)
	if err := w.f.SetCellStr(sheet, cell, name); err != nil {
		return w.cellError(sheet, col, headerRow, err.Error())
	}
	if style, err := w.f.GetCellStyle(sheet, cellName(col-1, headerRow)); err == nil && style != 0 {
		_ = w.f.SetCellStyle(sheet, cell, cell, style)
	}
	return nil
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
	if _, ok := errors.AsType[*SheetNotFoundError](err); !ok {
		return "", err
	}
	if _, err := w.f.NewSheet(name); err != nil {
		return "", fmt.Errorf("%s: invalid sheet name %q: %v", w.base, name, err)
	}
	w.sheets = w.f.GetSheetList()
	return name, nil
}

// clearCellBudget caps the rectangle clearSheet sweeps. Within it every
// cell of the stored dimension is cleared, which also catches cells that
// only carry a style; beyond it only cells holding a value or formula are
// cleared, so a sparse sheet with one far cell does not cost a sweep of
// the whole grid.
const clearCellBudget = 1 << 20

// clearSheet empties a sheet in place: its merged regions and tables are
// removed and every used cell loses its value, style, and hyperlink, while
// the sheet itself, its position, the defined names scoped to it, and
// formulas on other sheets that refer to it by name all stay valid.
// Deleting and recreating the sheet would lose those.
func (w *file) clearSheet(name string) error {
	merges, err := w.f.GetMergeCells(name, true)
	if err != nil {
		return fmt.Errorf("%s %s: %w", w.base, name, err)
	}
	for _, mc := range merges {
		if err := w.f.UnmergeCell(name, mc.GetStartAxis(), mc.GetEndAxis()); err != nil {
			return fmt.Errorf("%s %s: %w", w.base, name, err)
		}
	}
	if tables, err := w.f.GetTables(name); err == nil {
		for _, t := range tables {
			if err := w.f.DeleteTable(t.Name); err != nil {
				return fmt.Errorf("%s %s: %w", w.base, name, err)
			}
		}
	}
	grid, err := w.grid(name)
	if err != nil {
		return err
	}
	used, err := w.usedRange(name)
	if err != nil {
		return err
	}
	// The stored dimension, when Excel kept it, also covers cells that hold
	// only a style; it is swept when the rectangle stays within budget.
	if dim, ok := w.storedDimension(name); ok && withinClearBudget(dim) {
		used.R2 = max(used.R2, dim.R2)
		used.C2 = max(used.C2, dim.C2)
	}
	if withinClearBudget(used) {
		for r := 1; r <= used.R2; r++ {
			for c := 1; c <= used.C2; c++ {
				if err := w.clearCell(name, c, r); err != nil {
					return err
				}
			}
		}
	} else {
		for r := 1; r < len(grid); r++ {
			for c := 1; c < len(grid[r]); c++ {
				if err := w.clearCell(name, c, r); err != nil {
					return err
				}
			}
		}
	}
	w.forget(name)
	return nil
}

// withinClearBudget reports whether a rectangle from A1 to the region's
// end has at most clearCellBudget cells, without multiplying, since a
// whole-sheet rectangle overflows a 32-bit int.
func withinClearBudget(reg region) bool {
	return reg.C2 > 0 && reg.R2 <= clearCellBudget/reg.C2
}

// clearCell empties one cell: value and formula, style, and hyperlink. A
// cell with none of those is left as it was and dropped on save.
func (w *file) clearCell(name string, c, r int) error {
	cell := cellName(c, r)
	if err := w.f.SetCellDefault(name, cell, ""); err != nil {
		return w.cellError(name, c, r, err.Error())
	}
	if err := w.f.SetCellStyle(name, cell, cell, 0); err != nil {
		return w.cellError(name, c, r, err.Error())
	}
	if err := w.f.SetCellHyperLink(name, cell, "", "None"); err != nil {
		return w.cellError(name, c, r, err.Error())
	}
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
// dates and everything else is written as it is. date1904 says which epoch
// a numeric date serial counts from.
func outValue(v any, kind ColumnType, date1904 bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch kind {
	case TypeString:
		return valueString(v), nil
	case TypeNumber, TypeInteger, TypeBoolean:
		return coerce(v, kind, date1904)
	case TypeDate, TypeDateTime:
		t, ok := toTime(v, date1904)
		if !ok {
			return nil, fmt.Errorf("expected %s, found %s", kind, describe(v))
		}
		if kind == TypeDate {
			// A date column stores whole days; a time of day would hide
			// behind the format and surprise a later comparison.
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
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
	case TypeBoolean:
		return nil
	default:
		return nil
	}
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

// datedKey identifies a style derived from a base style for a date kind.
type datedKey struct {
	base int
	kind ColumnType
}

// styleAt returns the style of a cell, or zero for a plain cell or a row
// above the sheet.
func (w *file) styleAt(sheet string, col, row int) int {
	if row < 1 {
		return 0
	}
	id, err := w.f.GetCellStyle(sheet, cellName(col, row))
	if err != nil {
		return 0
	}
	return id
}

// styleWrittenCell styles a cell written into an existing sheet, outside a
// freshly styled table. base is the style the cell inherits: the cell
// above for an appended row, its own style for an updated one. A time
// under a base without a date format gets base with a date number format,
// so the date shows as a date and keeps the base's borders and fill; any
// other value takes base as it is. pinned is the column's pinned type, if
// any, which decides between a date and a date-time format.
func (w *file) styleWrittenCell(sheet string, col, row, base int, value any, pinned ColumnType) {
	cell := cellName(col, row)
	if t, ok := value.(time.Time); ok && (base == 0 || w.styleKind(base) == kindNumber) {
		if id := w.datedStyle(base, kindFor(t, pinned)); id != 0 {
			_ = w.f.SetCellStyle(sheet, cell, cell, id)
		}
		return
	}
	if base != 0 {
		_ = w.f.SetCellStyle(sheet, cell, cell, base)
	}
}

// datedStyle returns a style like base with the number format of kind,
// made once per base and kind for the life of the open workbook. Zero
// means no style could be made.
func (w *file) datedStyle(base int, kind ColumnType) int {
	key := datedKey{base: base, kind: kind}
	if id, ok := w.dated[key]; ok {
		return id
	}
	id := 0
	if style := kindStyle(kind); style != nil {
		if base != 0 {
			if existing, err := w.f.GetStyle(base); err == nil && existing != nil {
				existing.NumFmt = style.NumFmt
				existing.CustomNumFmt = style.CustomNumFmt
				style = existing
			}
		}
		if made, err := w.f.NewStyle(style); err == nil {
			id = made
		}
	}
	if w.dated == nil {
		w.dated = map[datedKey]int{}
	}
	w.dated[key] = id
	return id
}
