// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// SetValue says where an updated column's value comes from: a field of
// each row, or one literal for every row.
type SetValue struct {
	Field     string
	Literal   any
	IsLiteral bool
	// Type pins the literal the way a column type does; empty writes it by
	// its own kind, with a canonical numeric string as a number.
	Type ColumnType
}

// MissingMode says what happens to an input row whose key is not in the
// sheet.
type MissingMode string

// Missing modes.
const (
	// MissingFail fails the update.
	MissingFail MissingMode = "fail"
	// MissingSkip leaves the row out with a warning.
	MissingSkip MissingMode = "skip"
	// MissingAppend adds the row below the last used row.
	MissingAppend MissingMode = "append"
)

// UpdateOptions controls UpdateRows.
type UpdateOptions struct {
	Password string
	Sheet    string
	Header   HeaderSpec
	// Key names the column that identifies a row, or RowNumberKey to
	// address rows by the _row each input row carries.
	Key  string
	Rows []Row
	// Set maps sheet columns to values. Nil writes every field of each row
	// other than the key and _row to the column of the same name.
	Set     map[string]SetValue
	Missing MissingMode
	InPlace bool
	DryRun  bool
	Lock    LockOptions
}

// ParseSet reads a set option: column names mapped to a field name, or to
// {value: literal}.
func ParseSet(v any) (map[string]SetValue, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("set must map column names to row fields or {value: literal}")
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("set must not be empty")
	}
	out := make(map[string]SetValue, len(raw))
	for column, spec := range raw {
		if strings.TrimSpace(column) == "" {
			return nil, fmt.Errorf("set: column name must not be empty")
		}
		switch x := spec.(type) {
		case string:
			if strings.TrimSpace(x) == "" {
				return nil, fmt.Errorf("set.%s: field name must not be empty", column)
			}
			out[column] = SetValue{Field: strings.TrimSpace(x)}
		case map[string]any:
			literal, ok := x["value"]
			if !ok || len(x) > 2 {
				return nil, fmt.Errorf("set.%s: use a field name or {value: literal}", column)
			}
			sv := SetValue{Literal: literal, IsLiteral: true}
			if typeSpec, hasType := x["type"]; hasType {
				text, isText := typeSpec.(string)
				if !isText {
					return nil, fmt.Errorf("set.%s: type must be a column type", column)
				}
				t, err := ParseColumnType(text)
				if err != nil {
					return nil, fmt.Errorf("set.%s: type: %v", column, err)
				}
				sv.Type = t
			} else {
				if len(x) != 1 {
					return nil, fmt.Errorf("set.%s: use a field name or {value: literal}", column)
				}
				// A reference interpolated into the literal arrives as text;
				// a canonical number in it is written as a number.
				if s, isText := literal.(string); isText {
					sv.Literal = numericText(s)
				}
			}
			out[column] = sv
		default:
			return nil, fmt.Errorf("set.%s: use a field name or {value: literal}", column)
		}
	}
	return out, nil
}

// DecodeUpdateRows reads the rows of an update: a JSON string or a list of
// objects, as a step output or a loop item arrives. A _row value becomes
// an int.
func DecodeUpdateRows(value any) ([]Row, error) {
	list, _, err := rowList(value, "objects")
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rows[%d] must be an object", i)
		}
		row := make(Row, len(obj))
		for k, v := range obj {
			row[k] = normalizeScalar(v)
		}
		if n, ok := row[RowNumberKey]; ok && n != nil {
			r, ok := toRowNumber(n)
			if !ok {
				return nil, fmt.Errorf("rows[%d]: _row must be a row number", i)
			}
			row[RowNumberKey] = r
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// UpdateRows writes columns back to the rows they came from. Before any
// cell changes, the key column and every existing column in Set must still
// be in the header row by name, and every row carrying _row must still hold
// its key there; either failure aborts with nothing saved.
func UpdateRows(ctx context.Context, path string, opts UpdateOptions) (*WriteResult, error) {
	if opts.Missing == "" {
		opts.Missing = MissingFail
	}
	if strings.TrimSpace(opts.Key) == "" {
		return nil, fmt.Errorf("%s: key is required", Base(path))
	}
	return withLock(ctx, path, opts.Lock, func() (*WriteResult, error) {
		return updateOnce(ctx, path, opts)
	})
}

type updatePlan struct {
	sheet     string
	reg       region
	layout    headerLayout
	headers   []string
	keyCol    int // 0 when Key is _row
	set       []setColumn
	grid      [][]string
	merges    mergeFill
	headerRow int
}

// appendBase is the row whose styles appended rows inherit: the last data
// row, or zero when the sheet has no data rows yet, so a header's bold
// never spreads into new rows.
func (p *updatePlan) appendBase() int {
	if p.reg.R2 >= p.layout.dataStart {
		return p.reg.R2
	}
	return 0
}

type setColumn struct {
	name   string
	column int
	value  SetValue
	added  bool
}

func updateOnce(ctx context.Context, path string, opts UpdateOptions) (*WriteResult, error) {
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
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()

	plan, err := w.planUpdate(opts, warn)
	if err != nil {
		return nil, err
	}
	result.Sheet = plan.sheet
	result.Changes.Sheet = plan.sheet

	targets, appends, err := w.locateRows(plan, opts, warn)
	if err != nil {
		return nil, err
	}

	// Add missing columns at the right of the header row.
	for i := range plan.set {
		if !plan.set[i].added {
			continue
		}
		plan.reg.C2++
		plan.set[i].column = plan.reg.C2
		if err := w.addHeaderColumn(plan.sheet, plan.headerRow, plan.reg.C2, plan.set[i].name); err != nil {
			return nil, err
		}
		result.Changes.ColumnsAdded++
	}

	lastRow := plan.reg.R2
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changed, err := w.applyRow(plan, target.row, target.input, false)
		if err != nil {
			return nil, err
		}
		if changed > 0 {
			result.Changes.RowsUpdated++
			result.Changes.CellsChanged += changed
		}
	}
	for _, input := range appends {
		lastRow++
		if plan.keyCol > 0 {
			if err := w.setCell(plan.sheet, plan.keyCol, lastRow, input[opts.Key]); err != nil {
				return nil, err
			}
			w.styleWrittenCell(plan.sheet, plan.keyCol, lastRow, w.styleAt(plan.sheet, plan.keyCol, plan.appendBase()), input[opts.Key], "")
			result.Changes.CellsChanged++
		}
		changed, err := w.applyRow(plan, lastRow, input, true)
		if err != nil {
			return nil, err
		}
		result.Changes.RowsAppended++
		result.Changes.CellsChanged += changed
	}
	plan.reg.R2 = lastRow
	result.Changes.Range = region{Sheet: plan.sheet, C1: plan.reg.C1, R1: plan.layout.dataStart, C2: plan.reg.C2, R2: max(lastRow, plan.layout.dataStart)}.String()
	w.forget(plan.sheet)
	if opts.DryRun {
		return result, nil
	}
	if err := w.save(opts.InPlace); err != nil {
		return nil, err
	}
	return result, nil
}

// planUpdate resolves the sheet, headers, key column, and set columns, and
// runs the first shape check.
func (w *file) planUpdate(opts UpdateOptions, warn func(string)) (*updatePlan, error) {
	loc, err := w.locate(opts.Sheet, "", opts.Header)
	if err != nil {
		return nil, err
	}
	sheet, reg, grid := loc.sheet, loc.reg, loc.grid
	if !loc.ok {
		return nil, w.sheetError(sheet, "sheet is empty; nothing to update")
	}
	layout, err := layoutHeader(reg, opts.Header)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", w.base, sheet, err)
	}
	if len(layout.rows) == 0 {
		return nil, w.sheetError(sheet, "update_rows needs a header row; header: false is not supported")
	}
	merges, err := w.mergeMap(sheet)
	if err != nil {
		return nil, err
	}
	headers := headerNames(reg, layout, grid, merges, warn)
	plan := &updatePlan{
		sheet: sheet, reg: reg, layout: layout, headers: headers, grid: grid, merges: merges,
		headerRow: layout.rows[len(layout.rows)-1],
	}
	headerRow := layout.rows[0]
	if opts.Key != RowNumberKey {
		i, near := findColumn(headers, opts.Key)
		switch {
		case i >= 0:
			plan.keyCol = reg.C1 + i
		case near != "":
			return nil, w.sheetError(sheet, fmt.Sprintf("key column %q not found in header row %d; did you mean %q?", opts.Key, headerRow, near))
		default:
			return nil, w.sheetError(sheet, fmt.Sprintf("key column %q not found in header row %d; headers present: %s", opts.Key, headerRow, strings.Join(headers, ", ")))
		}
	}
	set := opts.Set
	if set == nil {
		set = defaultSet(opts.Rows, opts.Key)
		if len(set) == 0 {
			return nil, w.sheetError(sheet, "rows carry no fields to write; set is empty")
		}
	}
	for column, value := range set {
		if !value.IsLiteral && !anyRowHas(opts.Rows, value.Field) {
			return nil, w.sheetError(sheet, fmt.Sprintf("set.%s: field %q is not in any row", column, value.Field))
		}
	}
	for _, column := range sortedSetColumns(set) {
		sc := setColumn{name: column, value: set[column]}
		i, near := findColumn(headers, column)
		switch {
		case i >= 0:
			sc.name = headers[i]
			sc.column = reg.C1 + i
		case near != "":
			return nil, w.sheetError(sheet, fmt.Sprintf("column %q not found in header row %d; did you mean %q?", column, headerRow, near))
		default:
			sc.added = true
		}
		if sc.column != 0 && sc.column == plan.keyCol {
			return nil, w.sheetError(sheet, fmt.Sprintf("set.%s: the key column cannot be updated", column))
		}
		plan.set = append(plan.set, sc)
	}
	return plan, nil
}

func defaultSet(rows []Row, key string) map[string]SetValue {
	set := map[string]SetValue{}
	for _, row := range rows {
		for field := range row {
			if field == key || field == RowNumberKey {
				continue
			}
			set[field] = SetValue{Field: field}
		}
	}
	return set
}

func anyRowHas(rows []Row, field string) bool {
	for _, row := range rows {
		if _, ok := row[field]; ok {
			return true
		}
	}
	return false
}

func sortedSetColumns(set map[string]SetValue) []string {
	columns := make([]string, 0, len(set))
	for c := range set {
		columns = append(columns, c)
	}
	sort.Strings(columns)
	return columns
}

type rowTarget struct {
	index int // position in the input rows
	row   int
	input Row
}

// locateRows finds the sheet row of every input row and runs the second
// shape check: a row addressed by _row must still hold its key there.
func (w *file) locateRows(plan *updatePlan, opts UpdateOptions, warn func(string)) (targets []rowTarget, appends []Row, err error) {
	byKey := map[string][]int{}
	if plan.keyCol > 0 {
		for r := plan.layout.dataStart; r <= plan.reg.R2; r++ {
			k, err := w.keyAt(plan, r)
			if err != nil {
				return nil, nil, err
			}
			if k != "" {
				byKey[k] = append(byKey[k], r)
			}
		}
	}
	for i, input := range opts.Rows {
		rowNum, hasRow := input[RowNumberKey].(int)
		want := ""
		if plan.keyCol > 0 {
			want = keyText(input[opts.Key])
		}
		switch {
		case hasRow:
			if rowNum < plan.layout.dataStart || rowNum > plan.reg.R2 {
				return nil, nil, w.cellError(plan.sheet, max(plan.keyCol, plan.reg.C1), rowNum,
					fmt.Sprintf("row %d is outside the data rows %d to %d; the sheet changed since it was read", rowNum, plan.layout.dataStart, plan.reg.R2))
			}
			if plan.keyCol > 0 {
				found, err := w.keyAt(plan, rowNum)
				if err != nil {
					return nil, nil, err
				}
				if found != want {
					return nil, nil, w.cellError(plan.sheet, plan.keyCol, rowNum,
						fmt.Sprintf("expected key %q, found %q; the sheet changed since it was read", want, found))
				}
			}
			targets = append(targets, rowTarget{index: i, row: rowNum, input: input})
		case opts.Key == RowNumberKey:
			return nil, nil, fmt.Errorf("%s %s: rows[%d] has no _row to address with key _row", w.base, plan.sheet, i)
		case want == "":
			return nil, nil, fmt.Errorf("%s %s: rows[%d] has no %s", w.base, plan.sheet, i, opts.Key)
		default:
			matches := byKey[want]
			switch {
			case len(matches) > 1:
				return nil, nil, w.sheetError(plan.sheet, fmt.Sprintf("key %q appears at rows %s", want, joinInts(matches)))
			case len(matches) == 1:
				targets = append(targets, rowTarget{index: i, row: matches[0], input: input})
			default:
				switch opts.Missing {
				case MissingSkip:
					warn(fmt.Sprintf("%s: key %q not found; row skipped", plan.sheet, want))
				case MissingAppend:
					appends = append(appends, input)
				case MissingFail:
					return nil, nil, w.sheetError(plan.sheet, fmt.Sprintf("key %q not found", want))
				default:
					return nil, nil, w.sheetError(plan.sheet, fmt.Sprintf("unknown missing mode %q", opts.Missing))
				}
			}
		}
	}
	// Two inputs for one sheet row would race on the same cells, and the
	// change check compares against the sheet as it was read.
	seenRows := make(map[int]int, len(targets))
	for _, target := range targets {
		if first, dup := seenRows[target.row]; dup {
			return nil, nil, w.sheetError(plan.sheet, fmt.Sprintf("rows[%d] and rows[%d] both address row %d", first, target.index, target.row))
		}
		seenRows[target.row] = target.index
	}
	return targets, appends, nil
}

func (w *file) keyAt(plan *updatePlan, row int) (string, error) {
	oc, or := plan.merges.origin(plan.keyCol, row)
	value, err := w.cellValue(plan.sheet, oc, or, cellAt(plan.grid, oc, or), ReadOptions{}, func(string) {})
	if err != nil {
		return "", err
	}
	return keyText(value), nil
}

// keyText normalizes a key for comparison: trimmed text, with numbers
// written the shortest way so 1 and 1.0 match.
func keyText(v any) string {
	return trimSpace(valueString(v))
}

// sameValue reports whether writing value would leave a cell as it is. The
// comparison is type-aware: the number 7 and the text "7" differ, so a
// requested change of cell type is written, while 7 and 7.0 or two equal
// dates do not count as a change.
func sameValue(existing, value any) bool {
	if existing == nil || value == nil {
		return existing == nil && value == nil
	}
	existingKind, valueKind := detectKind(normalizeScalar(existing)), detectKind(normalizeScalar(value))
	if existingKind == string(TypeInteger) || existingKind == string(TypeNumber) {
		existingKind = string(TypeNumber)
	}
	if valueKind == string(TypeInteger) || valueKind == string(TypeNumber) {
		valueKind = string(TypeNumber)
	}
	if existingKind != valueKind {
		return false
	}
	if existingKind == string(TypeNumber) {
		a, _ := toFloat(existing)
		b, _ := toFloat(value)
		return a == b
	}
	return keyText(existing) == keyText(value)
}

// applyRow writes the set columns of one input row into a sheet row and
// reports how many cells changed.
func (w *file) applyRow(plan *updatePlan, row int, input Row, appended bool) (int, error) {
	changed := 0
	for _, sc := range plan.set {
		var value any
		if sc.value.IsLiteral {
			value = sc.value.Literal
		} else {
			field, present := input[sc.value.Field]
			if !present {
				// A row that does not carry the field leaves the cell as it
				// is; only an explicit null clears it.
				continue
			}
			value = field
		}
		var kind ColumnType
		if sc.value.IsLiteral {
			kind = sc.value.Type
		}
		out, err := outValue(value, kind, w.date1904)
		if err != nil {
			return 0, w.cellError(plan.sheet, sc.column, row, err.Error())
		}
		if !appended {
			oc, or := plan.merges.origin(sc.column, row)
			existing, err := w.cellValue(plan.sheet, oc, or, cellAt(plan.grid, oc, or), ReadOptions{}, func(string) {})
			if err != nil {
				return 0, err
			}
			// The value is compared as it will be written, so a literal
			// pinned to a number replaces the text that reads the same.
			if sameValue(existing, comparable(out)) {
				continue
			}
		}
		cell := cellName(sc.column, row)
		if out == nil {
			if !appended {
				if err := w.f.SetCellStr(plan.sheet, cell, ""); err != nil {
					return 0, w.cellError(plan.sheet, sc.column, row, err.Error())
				}
				changed++
			}
			continue
		}
		// The style the cell had is read before the value lands, since the
		// library gives a time value a date format of its own when the cell
		// has none; the kind the value or the literal's type calls for is
		// applied over the cell's own style instead.
		base := w.styleAt(plan.sheet, sc.column, row)
		if appended {
			base = w.styleAt(plan.sheet, sc.column, plan.appendBase())
		}
		if err := w.setCell(plan.sheet, sc.column, row, out); err != nil {
			return 0, err
		}
		w.styleWrittenCell(plan.sheet, sc.column, row, base, out, kind)
		changed++
	}
	return changed, nil
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, " and ")
}
