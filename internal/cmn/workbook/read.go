// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"strings"
)

// TypeErrorMode says what a cell that fails a pinned type does.
type TypeErrorMode string

// Type error modes.
const (
	// TypeErrorFail fails the read naming the cell.
	TypeErrorFail TypeErrorMode = "fail"
	// TypeErrorWarn reads the cell as null and records a warning.
	TypeErrorWarn TypeErrorMode = "warn"
	// TypeErrorNull is accepted as a spelling of TypeErrorWarn.
	TypeErrorNull TypeErrorMode = "null"
)

func (m TypeErrorMode) warns() bool {
	return m == TypeErrorWarn || m == TypeErrorNull
}

// ReadOptions selects and types the rows of one sheet.
type ReadOptions struct {
	Password      string
	Sheet         string
	Range         string
	Header        HeaderSpec
	Columns       []ColumnSelect
	Merged        MergedMode
	StopAtBlank   bool
	KeepEmptyRows bool
	Trim          bool
	Formulas      FormulaMode
	Types         map[string]ColumnType
	OnTypeError   TypeErrorMode
	Where         map[string]any
	// MaxRows caps the rows returned; zero means DefaultMaxRows.
	MaxRows int

	// quietLimit drops the warning a hit MaxRows adds; Inspect samples a
	// few rows on purpose and reports the row count separately.
	quietLimit bool
}

// ReadResult is what a read publishes.
type ReadResult struct {
	Rows      []Row    `json:"rows"`
	Count     int      `json:"count"`
	Headers   []string `json:"headers"`
	Sheet     string   `json:"sheet"`
	Range     string   `json:"range"`
	Warnings  []string `json:"warnings"`
	Truncated bool     `json:"truncated"`
}

// Read returns the typed rows of a sheet.
func Read(ctx context.Context, path string, opts ReadOptions) (*ReadResult, error) {
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	return w.read(ctx, opts)
}

func (w *file) read(ctx context.Context, opts ReadOptions) (*ReadResult, error) {
	result := &ReadResult{Rows: []Row{}, Headers: []string{}, Warnings: []string{}}
	warn := func(msg string) { result.Warnings = append(result.Warnings, msg) }

	loc, err := w.locate(opts.Sheet, opts.Range, opts.Header)
	if err != nil {
		return nil, err
	}
	sheet, reg, grid := loc.sheet, loc.reg, loc.grid
	result.Sheet = sheet
	if !loc.ok {
		result.Range = region{Sheet: sheet, C1: 1, R1: 1, C2: 1, R2: 1}.String()
		return result, nil
	}
	layout, err := layoutHeader(reg, opts.Header)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", w.base, sheet, err)
	}
	var merges mergeFill
	if opts.Merged != MergedFirst {
		if merges, err = w.mergeMap(sheet); err != nil {
			return nil, err
		}
	}
	headers := headerNames(reg, layout, grid, merges, warn)
	plan, err := w.planColumns(sheet, headers, opts)
	if err != nil {
		return nil, err
	}
	where, err := w.compileWhere(sheet, headers, plan, opts.Where)
	if err != nil {
		return nil, err
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}

	// Empty rows are held back until a later row proves they are not
	// trailing, so they do not survive at the end unless keep_empty_rows
	// asks for them, and they never count toward max_rows: the cap counts
	// rows that hold a value. Blank detection uses the resolved row, after
	// merged cells and formulas, not the raw text.
	var pending []Row
	counted := 0
	for r := layout.dataStart; r <= reg.R2; r++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full, empty, err := w.readRow(sheet, reg, r, grid, merges, headers, plan.types, opts, warn)
		if err != nil {
			return nil, err
		}
		if empty && opts.StopAtBlank {
			break
		}
		if !where.match(full) {
			continue
		}
		row := plan.project(full)
		if empty && !opts.KeepEmptyRows {
			pending = append(pending, row)
			continue
		}
		if counted >= maxRows {
			result.Truncated = true
			if !opts.quietLimit {
				warn(fmt.Sprintf("stopped after %d rows; set max_rows to read more", maxRows))
			}
			break
		}
		result.Rows = append(result.Rows, pending...)
		result.Rows = append(result.Rows, row)
		pending = nil
		counted++
	}
	result.Count = len(result.Rows)
	result.Headers = plan.names()
	result.Range = reg.String()
	return result, nil
}

// location is the sheet and region a read covers. ok is false for an
// empty sheet.
type location struct {
	sheet string
	reg   region
	grid  [][]string
	ok    bool
}

// locate resolves the sheet and region a read covers from the sheet name,
// an optional range reference, and the header mode.
func (w *file) locate(sheetName, rangeRef string, header HeaderSpec) (location, error) {
	sheet, err := w.resolveSheet(sheetName)
	if err != nil {
		return location{}, err
	}
	if strings.TrimSpace(rangeRef) != "" {
		reg, err := w.parseRange(sheet, rangeRef)
		if err != nil {
			return location{}, err
		}
		grid, err := w.grid(reg.Sheet)
		if err != nil {
			return location{}, err
		}
		return location{sheet: reg.Sheet, reg: reg, grid: grid, ok: true}, nil
	}
	grid, err := w.grid(sheet)
	if err != nil {
		return location{}, err
	}
	loc := location{sheet: sheet, grid: grid, ok: len(grid) > 1}
	switch header.Mode {
	case HeaderNone:
		loc.reg, err = w.usedRange(sheet)
		return loc, err
	case HeaderRows:
		loc.reg, err = w.usedRange(sheet)
		if err != nil {
			return location{}, err
		}
		first := header.Rows[0]
		for _, r := range header.Rows {
			first = min(first, r)
		}
		loc.reg.R1 = min(first, loc.reg.R2+1)
		return loc, nil
	case HeaderFirstRow:
		loc.reg, loc.ok, err = w.detectTable(sheet, grid)
		return loc, err
	default:
		return location{}, fmt.Errorf("unknown header mode %d", header.Mode)
	}
}

// readRow types every cell of one sheet row under the region's headers.
func (w *file) readRow(sheet string, reg region, r int, grid [][]string, merges mergeFill, headers []string, types map[string]ColumnType, opts ReadOptions, warn func(string)) (row Row, empty bool, err error) {
	row = make(Row, len(headers)+1)
	empty = true
	for i, c := range columnsOf(reg) {
		oc, or := merges.origin(c, r)
		raw := cellAt(grid, oc, or)
		value, err := w.cellValue(sheet, oc, or, raw, opts, warn)
		if err != nil {
			return nil, false, err
		}
		name := headers[i]
		if t, ok := types[name]; ok && value != nil {
			coerced, cerr := coerce(value, t, w.date1904)
			if cerr != nil {
				if opts.OnTypeError.warns() {
					warn(fmt.Sprintf("%s!%s: %v", sheet, cellName(c, r), cerr))
					coerced = nil
				} else {
					return nil, false, w.cellError(sheet, c, r, cerr.Error())
				}
			}
			value = coerced
		}
		if value != nil {
			empty = false
		}
		row[name] = value
	}
	row[RowNumberKey] = r
	return row, empty, nil
}

func columnsOf(reg region) []int {
	cols := make([]int, 0, reg.C2-reg.C1+1)
	for c := reg.C1; c <= reg.C2; c++ {
		cols = append(cols, c)
	}
	return cols
}

// columnPlan is the selection, renaming, and typing a read applies to
// each row.
type columnPlan struct {
	selects []ColumnSelect // resolved: Source is an existing header
	all     []string
	types   map[string]ColumnType // keyed by resolved header
}

func (p columnPlan) names() []string {
	if p.selects == nil {
		return append([]string(nil), p.all...)
	}
	names := make([]string, 0, len(p.selects))
	for _, s := range p.selects {
		names = append(names, s.As)
	}
	return names
}

func (p columnPlan) project(row Row) Row {
	if p.selects == nil {
		return row
	}
	out := make(Row, len(p.selects)+1)
	for _, s := range p.selects {
		out[s.As] = row[s.Source]
	}
	out[RowNumberKey] = row[RowNumberKey]
	return out
}

// alias maps a name as a caller wrote it to the header it refers to: the
// exact header, a loose match, or the alias of a selected column.
func (p columnPlan) alias(name string) (string, bool) {
	for _, s := range p.selects {
		if s.As == name {
			return s.Source, true
		}
	}
	if i, near := findColumn(p.all, name); i >= 0 {
		return p.all[i], true
	} else if near != "" {
		return near, true
	}
	return "", false
}

func (w *file) planColumns(sheet string, headers []string, opts ReadOptions) (columnPlan, error) {
	plan := columnPlan{all: headers, types: map[string]ColumnType{}}
	for name, t := range opts.Types {
		i, near := findColumn(headers, name)
		switch {
		case i >= 0:
			plan.types[headers[i]] = t
		case near != "":
			plan.types[near] = t
		default:
			return plan, w.sheetError(sheet, fmt.Sprintf("types: column %q not found; headers present: %s", name, strings.Join(headers, ", ")))
		}
	}
	if opts.Columns == nil {
		return plan, nil
	}
	plan.selects = make([]ColumnSelect, 0, len(opts.Columns))
	for _, sel := range opts.Columns {
		i, near := findColumn(headers, sel.Source)
		var source string
		switch {
		case i >= 0:
			source = headers[i]
		case near != "":
			source = near
		default:
			return plan, w.sheetError(sheet, fmt.Sprintf("columns: column %q not found; headers present: %s", sel.Source, strings.Join(headers, ", ")))
		}
		plan.selects = append(plan.selects, ColumnSelect{Source: source, As: sel.As})
	}
	return plan, nil
}

// whereClause is one compiled filter: column op values.
type whereClause struct {
	column string
	op     string
	values []any
}

type whereFilter []whereClause

func (w *file) compileWhere(sheet string, headers []string, plan columnPlan, where map[string]any) (whereFilter, error) {
	if len(where) == 0 {
		return nil, nil
	}
	var filter whereFilter
	for _, name := range sortedKeys(where) {
		column, ok := plan.alias(name)
		if !ok {
			return nil, w.sheetError(sheet, fmt.Sprintf("where: column %q not found; headers present: %s", name, strings.Join(headers, ", ")))
		}
		clause, err := parseWhereClause(column, where[name])
		if err != nil {
			return nil, w.sheetError(sheet, "where: "+err.Error())
		}
		filter = append(filter, clause)
	}
	return filter, nil
}

// ValidateWhere checks a where option without a workbook: every value must
// be a scalar, a list, or an object with exactly one of eq, ne, or in.
func ValidateWhere(where map[string]any) error {
	for _, name := range sortedKeys(where) {
		if _, err := parseWhereClause(name, where[name]); err != nil {
			return err
		}
	}
	return nil
}

func parseWhereClause(column string, v any) (whereClause, error) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) != 1 {
			return whereClause{}, fmt.Errorf("%s must use one of eq, ne, or in", column)
		}
		for op, operand := range x {
			switch op {
			case "eq", "ne":
				return whereClause{column: column, op: op, values: []any{operand}}, nil
			case "in":
				list, ok := operand.([]any)
				if !ok {
					return whereClause{}, fmt.Errorf("%s: in must be a list", column)
				}
				return whereClause{column: column, op: op, values: list}, nil
			default:
				return whereClause{}, fmt.Errorf("%s: unknown operator %q; use eq, ne, or in", column, op)
			}
		}
	case []any:
		return whereClause{column: column, op: "in", values: x}, nil
	}
	return whereClause{column: column, op: "eq", values: []any{v}}, nil
}

func (f whereFilter) match(row Row) bool {
	for _, clause := range f {
		cell := row[clause.column]
		switch clause.op {
		case "eq":
			if !equalValue(cell, clause.values[0]) {
				return false
			}
		case "ne":
			if equalValue(cell, clause.values[0]) {
				return false
			}
		case "in":
			found := false
			for _, v := range clause.values {
				if equalValue(cell, v) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// equalValue compares a cell with a filter operand: empty matches null,
// numbers compare numerically, everything else compares as trimmed text.
func equalValue(cell, want any) bool {
	if cell == nil {
		return want == nil || trimSpace(valueString(want)) == ""
	}
	if want == nil {
		return false
	}
	if a, ok := toFloatStrict(cell); ok {
		if b, ok := toFloatStrict(want); ok {
			return a == b
		}
	}
	return trimSpace(valueString(cell)) == trimSpace(valueString(want))
}

func toFloatStrict(v any) (float64, bool) {
	switch x := v.(type) {
	case int64, int, float64:
		return toFloat(x)
	case string:
		return toFloat(strings.TrimSpace(x))
	default:
		return 0, false
	}
}
