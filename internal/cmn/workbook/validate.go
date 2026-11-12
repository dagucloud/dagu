// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"strings"
)

// ProblemCode names the kind of a validation problem, for programs that
// act on it.
type ProblemCode string

// Problem codes.
const (
	// ProblemMissingColumn is a column named by a rule that the header row
	// does not have; the rule is skipped.
	ProblemMissingColumn ProblemCode = "missing_column"
	// ProblemBlank is an empty cell in a column that must hold a value.
	ProblemBlank ProblemCode = "blank"
	// ProblemType is a cell that cannot convert to its column's type.
	ProblemType ProblemCode = "type"
	// ProblemDuplicate is a value seen earlier in a column that must be
	// unique.
	ProblemDuplicate ProblemCode = "duplicate"
	// ProblemNotAllowed is a value outside a column's allowed list.
	ProblemNotAllowed ProblemCode = "not_allowed"
)

// Problem is one failed check. Cell and Row are empty for a missing
// column, which is a problem of the header row, not of one cell.
type Problem struct {
	Code    ProblemCode `json:"code"`
	Sheet   string      `json:"sheet"`
	Cell    string      `json:"cell,omitempty"`
	Row     int         `json:"row,omitempty"`
	Column  string      `json:"column"`
	Message string      `json:"message"`
}

// String formats the problem the way cell errors read: Sheet!Cell: message,
// or Sheet: message when no cell is involved.
func (p Problem) String() string {
	if p.Cell == "" {
		return p.Sheet + ": " + p.Message
	}
	return p.Sheet + "!" + p.Cell + ": " + p.Message
}

// DefaultMaxProblems caps the problems a validation keeps.
const DefaultMaxProblems = 1000

// ValidateOptions says which sheet to check and against which rules. Names
// in the rules refer to headers as read (exact, or a loose match ignoring
// case and surrounding space) or to aliases given in Columns.
type ValidateOptions struct {
	Password string
	Sheet    string
	Range    string
	Header   HeaderSpec
	Columns  []ColumnSelect
	Merged   MergedMode
	Trim     bool
	Formulas FormulaMode
	// SkipHidden leaves out rows hidden by a filter or by hand.
	SkipHidden bool

	// Required lists columns the header row must have.
	Required []string
	// NotBlank lists columns no row may leave empty.
	NotBlank []string
	// Unique lists columns whose non-empty values may not repeat.
	Unique []string
	// Types pins columns; a cell that cannot convert is a problem.
	Types map[string]ColumnType
	// Allowed lists the values a column's non-empty cells may hold.
	Allowed map[string][]any
	// MaxProblems caps the problems kept; zero means DefaultMaxProblems.
	// Count still reports every problem found.
	MaxProblems int
}

// ValidateResult is what a validation publishes.
type ValidateResult struct {
	OK       bool      `json:"ok"`
	Problems []Problem `json:"problems"`
	// Count is every problem found, including those past MaxProblems.
	Count int `json:"count"`
	// Rows is the number of non-empty data rows checked.
	Rows      int      `json:"rows"`
	Headers   []string `json:"headers"`
	Sheet     string   `json:"sheet"`
	Range     string   `json:"range"`
	Warnings  []string `json:"warnings"`
	Truncated bool     `json:"truncated"`
}

// Validate checks the rows of a sheet against the rules in opts and reports
// every problem found, without failing on any of them. Rows whose cells
// are all empty are skipped.
func Validate(ctx context.Context, path string, opts ValidateOptions) (*ValidateResult, error) {
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	return w.validate(ctx, opts)
}

// rule is one check bound to a header column.
type rule struct {
	column string // the header as read
	index  int    // position in the header row
}

func (w *file) validate(ctx context.Context, opts ValidateOptions) (*ValidateResult, error) {
	result := &ValidateResult{Problems: []Problem{}, Headers: []string{}, Warnings: []string{}}
	warn := func(msg string) { result.Warnings = append(result.Warnings, msg) }
	maxProblems := opts.MaxProblems
	if maxProblems <= 0 {
		maxProblems = DefaultMaxProblems
	}
	add := func(p Problem) {
		result.Count++
		if len(result.Problems) < maxProblems {
			result.Problems = append(result.Problems, p)
		} else {
			result.Truncated = true
		}
	}

	loc, err := w.locate(opts.Sheet, opts.Range, opts.Header)
	if err != nil {
		return nil, err
	}
	sheet, reg, grid := loc.sheet, loc.reg, loc.grid
	result.Sheet = sheet
	if !loc.ok {
		// An empty sheet has no header row, so every named column is
		// missing.
		result.Range = region{Sheet: sheet, C1: 1, R1: 1, C2: 1, R2: 1}.String()
		for _, name := range ruleColumns(opts) {
			add(Problem{Code: ProblemMissingColumn, Sheet: sheet, Column: name,
				Message: fmt.Sprintf("column %q not found; the sheet is empty", name)})
		}
		result.OK = result.Count == 0
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
	readOpts := ReadOptions{Columns: opts.Columns, Merged: opts.Merged, Trim: opts.Trim, Formulas: opts.Formulas}
	plan, err := w.planColumns(sheet, headers, readOpts)
	if err != nil {
		return nil, err
	}
	result.Headers = plan.names()
	result.Range = reg.String()

	// Bind every rule to a header column; a name no header matches is a
	// problem of its own, reported once however many rules name it, and
	// those rules are skipped.
	reported := map[string]bool{}
	bind := func(name string) (rule, bool) {
		column, ok := plan.alias(name)
		if !ok {
			if !reported[name] {
				reported[name] = true
				add(Problem{Code: ProblemMissingColumn, Sheet: sheet, Column: name,
					Message: fmt.Sprintf("column %q not found; headers present: %s", name, strings.Join(headers, ", "))})
			}
			return rule{}, false
		}
		for i, h := range headers {
			if h == column {
				return rule{column: column, index: i}, true
			}
		}
		return rule{}, false
	}
	for _, name := range opts.Required {
		bind(name)
	}
	var notBlank, unique []rule
	for _, name := range opts.NotBlank {
		if r, ok := bind(name); ok {
			notBlank = append(notBlank, r)
		}
	}
	for _, name := range opts.Unique {
		if r, ok := bind(name); ok {
			unique = append(unique, r)
		}
	}
	type typed struct {
		rule
		kind ColumnType
	}
	var types []typed
	for _, name := range sortedKeys(opts.Types) {
		if r, ok := bind(name); ok {
			types = append(types, typed{rule: r, kind: opts.Types[name]})
		}
	}
	type allowed struct {
		rule
		values []any
		list   string
	}
	var allowedRules []allowed
	for _, name := range sortedKeys(opts.Allowed) {
		if r, ok := bind(name); ok {
			values := opts.Allowed[name]
			parts := make([]string, 0, len(values))
			for _, v := range values {
				parts = append(parts, valueString(v))
			}
			allowedRules = append(allowedRules, allowed{rule: r, values: values, list: strings.Join(parts, ", ")})
		}
	}

	seen := make([]map[string]int, len(unique)) // value text -> first row
	for i := range seen {
		seen[i] = map[string]int{}
	}
	cell := func(r rule, row int) string { return cellName(reg.C1+r.index, row) }
	for r := layout.dataStart; r <= reg.R2; r++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.SkipHidden && w.rowHidden(sheet, grid, r) {
			continue
		}
		full, empty, err := w.readRow(sheet, reg, r, grid, merges, headers, nil, readOpts, warn)
		if err != nil {
			return nil, err
		}
		if empty {
			continue
		}
		result.Rows++
		for _, rl := range notBlank {
			if isBlank(full[rl.column]) {
				add(Problem{Code: ProblemBlank, Sheet: sheet, Cell: cell(rl, r), Row: r, Column: rl.column,
					Message: fmt.Sprintf("%s is blank", rl.column)})
			}
		}
		for _, t := range types {
			value := full[t.column]
			if value == nil {
				continue
			}
			if _, cerr := coerce(value, t.kind, w.date1904); cerr != nil {
				add(Problem{Code: ProblemType, Sheet: sheet, Cell: cell(t.rule, r), Row: r, Column: t.column, Message: cerr.Error()})
			}
		}
		for i, rl := range unique {
			value := full[rl.column]
			if isBlank(value) {
				continue
			}
			key := keyText(value)
			if first, dup := seen[i][key]; dup {
				add(Problem{Code: ProblemDuplicate, Sheet: sheet, Cell: cell(rl, r), Row: r, Column: rl.column,
					Message: fmt.Sprintf("duplicate value %q; first at row %d", key, first)})
				continue
			}
			seen[i][key] = r
		}
		for _, a := range allowedRules {
			value := full[a.column]
			if isBlank(value) {
				continue
			}
			found := false
			for _, v := range a.values {
				if equalValue(value, v) {
					found = true
					break
				}
			}
			if !found {
				add(Problem{Code: ProblemNotAllowed, Sheet: sheet, Cell: cell(a.rule, r), Row: r, Column: a.column,
					Message: fmt.Sprintf("value %q is not one of %s", keyText(value), a.list)})
			}
		}
	}
	result.OK = result.Count == 0
	return result, nil
}

// isBlank reports whether a cell holds nothing or only white space.
func isBlank(v any) bool {
	return v == nil || trimSpace(valueString(v)) == ""
}

// ruleColumns lists every column name the rules mention, once each, in a
// stable order.
func ruleColumns(opts ValidateOptions) []string {
	seen := map[string]bool{}
	var names []string
	addAll := func(list []string) {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	addAll(opts.Required)
	addAll(opts.NotBlank)
	addAll(opts.Unique)
	addAll(sortedKeys(opts.Types))
	addAll(sortedKeys(opts.Allowed))
	return names
}
