// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// HeaderMode says where column names come from.
type HeaderMode int

// Header modes.
const (
	// HeaderFirstRow takes the first row of the range as column names.
	HeaderFirstRow HeaderMode = iota
	// HeaderNone names columns by letter: A, B, C.
	HeaderNone
	// HeaderRows takes the listed absolute sheet rows, joined with a space.
	HeaderRows
)

// HeaderSpec is a parsed header option.
type HeaderSpec struct {
	Mode HeaderMode
	Rows []int
}

// ParseHeader reads a header option: true, false, a row number, or a list
// of row numbers. Numbers decoded from YAML or JSON may arrive as float64.
func ParseHeader(v any) (HeaderSpec, error) {
	switch x := v.(type) {
	case nil:
		return HeaderSpec{Mode: HeaderFirstRow}, nil
	case bool:
		if x {
			return HeaderSpec{Mode: HeaderFirstRow}, nil
		}
		return HeaderSpec{Mode: HeaderNone}, nil
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "", "true":
			return HeaderSpec{Mode: HeaderFirstRow}, nil
		case "false":
			return HeaderSpec{Mode: HeaderNone}, nil
		}
		var rows []int
		for part := range strings.SplitSeq(s, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 1 {
				return HeaderSpec{}, fmt.Errorf("header must be true, false, a row number, or a list of row numbers")
			}
			rows = append(rows, n)
		}
		return HeaderSpec{Mode: HeaderRows, Rows: rows}, nil
	case []any:
		if len(x) == 0 {
			return HeaderSpec{}, fmt.Errorf("header list must not be empty")
		}
		rows := make([]int, 0, len(x))
		for _, item := range x {
			n, ok := toRowNumber(item)
			if !ok {
				return HeaderSpec{}, fmt.Errorf("header list must hold row numbers")
			}
			rows = append(rows, n)
		}
		sort.Ints(rows)
		return HeaderSpec{Mode: HeaderRows, Rows: rows}, nil
	default:
		n, ok := toRowNumber(v)
		if !ok {
			return HeaderSpec{}, fmt.Errorf("header must be true, false, a row number, or a list of row numbers")
		}
		return HeaderSpec{Mode: HeaderRows, Rows: []int{n}}, nil
	}
}

func toRowNumber(v any) (int, bool) {
	f, ok := toFloat(v)
	if !ok || f < 1 || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

// ColumnSelect picks a column and optionally renames it.
type ColumnSelect struct {
	Source string
	As     string
}

// ParseColumns reads a columns option: a list whose items are names or
// single-entry {name: alias} maps, or a JSON string holding such a list.
func ParseColumns(v any) ([]ColumnSelect, error) {
	if v == nil {
		return nil, nil
	}
	if text, ok := v.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil, nil
		}
		if strings.HasPrefix(trimmed, "[") {
			decoded, err := decodeJSON(trimmed)
			if err != nil {
				return nil, fmt.Errorf("columns: %w", err)
			}
			v = decoded
		} else {
			var items []any
			for part := range strings.SplitSeq(trimmed, ",") {
				items = append(items, strings.TrimSpace(part))
			}
			v = items
		}
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("columns must be a list of column names or {name: alias} entries")
	}
	var out []ColumnSelect
	for i, item := range list {
		switch x := item.(type) {
		case string:
			name := strings.TrimSpace(x)
			if name == "" {
				return nil, fmt.Errorf("columns[%d] is empty", i)
			}
			if src, alias, ok := strings.Cut(name, ":"); ok && strings.TrimSpace(alias) != "" && strings.TrimSpace(src) != "" {
				out = append(out, ColumnSelect{Source: strings.TrimSpace(src), As: strings.TrimSpace(alias)})
				continue
			}
			out = append(out, ColumnSelect{Source: name, As: name})
		case map[string]any:
			if len(x) != 1 {
				return nil, fmt.Errorf("columns[%d] must map one column to one alias", i)
			}
			for src, alias := range x {
				as, ok := alias.(string)
				if !ok || strings.TrimSpace(as) == "" {
					return nil, fmt.Errorf("columns[%d]: alias for %q must be a non-empty string", i, src)
				}
				out = append(out, ColumnSelect{Source: strings.TrimSpace(src), As: strings.TrimSpace(as)})
			}
		default:
			return nil, fmt.Errorf("columns[%d] must be a column name or {name: alias}", i)
		}
	}
	// Projected rows are keyed by alias, so two columns with one alias
	// would silently drop a value.
	seen := make(map[string]struct{}, len(out))
	for _, column := range out {
		if _, dup := seen[column.As]; dup {
			return nil, fmt.Errorf("columns: duplicate output name %q", column.As)
		}
		seen[column.As] = struct{}{}
	}
	return out, nil
}

// MergedMode says how merged body cells are read.
type MergedMode string

// Merged modes.
const (
	// MergedFill repeats a merged value into every cell it covers.
	MergedFill MergedMode = "fill"
	// MergedFirst keeps the value in the top-left cell only.
	MergedFirst MergedMode = "first"
)

// mergeFill lists the merge regions of a sheet so a covered cell can be
// resolved to the region's top-left cell on demand. Regions are kept as
// rectangles rather than expanded per cell, because one merge can cover a
// whole column.
type mergeFill []region

func (w *file) mergeMap(sheet string) (mergeFill, error) {
	cells, err := w.f.GetMergeCells(sheet, true)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", w.base, sheet, err)
	}
	fill := make(mergeFill, 0, len(cells))
	for _, mc := range cells {
		c1, r1, err := excelize.CellNameToCoordinates(mc.GetStartAxis())
		if err != nil {
			continue
		}
		c2, r2, err := excelize.CellNameToCoordinates(mc.GetEndAxis())
		if err != nil {
			continue
		}
		fill = append(fill, region{Sheet: sheet, C1: min(c1, c2), R1: min(r1, r2), C2: max(c1, c2), R2: max(r1, r2)})
	}
	return fill, nil
}

// origin returns the coordinates whose value a cell shows: its own, or the
// top-left cell of the merge region covering it.
func (m mergeFill) origin(col, row int) (int, int) {
	for _, reg := range m {
		if col >= reg.C1 && col <= reg.C2 && row >= reg.R1 && row <= reg.R2 {
			return reg.C1, reg.R1
		}
	}
	return col, row
}

// headerLayout is where a read's header rows sit and where data begins.
type headerLayout struct {
	rows      []int // absolute sheet rows holding header text; empty for HeaderNone
	dataStart int   // first data row
}

func layoutHeader(reg region, spec HeaderSpec) (headerLayout, error) {
	switch spec.Mode {
	case HeaderNone:
		return headerLayout{dataStart: reg.R1}, nil
	case HeaderRows:
		rows := append([]int(nil), spec.Rows...)
		sort.Ints(rows)
		for _, r := range rows {
			if r < reg.R1 || r > reg.R2 {
				return headerLayout{}, fmt.Errorf("header row %d is outside %s", r, reg.String())
			}
		}
		return headerLayout{rows: rows, dataStart: rows[len(rows)-1] + 1}, nil
	case HeaderFirstRow:
		return headerLayout{rows: []int{reg.R1}, dataStart: reg.R1 + 1}, nil
	default:
		return headerLayout{}, fmt.Errorf("unknown header mode %d", spec.Mode)
	}
}

// headerNames builds column names for a region: multi-row headers are
// joined with a space, line breaks become spaces, empty names fall back to
// the column letter, and duplicates get a numeric suffix.
func headerNames(reg region, layout headerLayout, grid [][]string, merges mergeFill, warn func(string)) []string {
	names := make([]string, 0, reg.C2-reg.C1+1)
	// _row is reserved for the row number, so a header spelled that way is
	// renamed like a duplicate.
	seen := map[string]int{RowNumberKey: 1}
	for c := reg.C1; c <= reg.C2; c++ {
		letter, _ := excelize.ColumnNumberToName(c)
		name := ""
		if len(layout.rows) > 0 {
			var parts []string
			for _, r := range layout.rows {
				oc, or := merges.origin(c, r)
				text := cleanHeader(cellAt(grid, oc, or))
				if text != "" {
					parts = append(parts, text)
				}
			}
			name = strings.Join(parts, " ")
			if name == "" {
				name = letter
				warn(fmt.Sprintf("%s!%s%d: header is empty; column named %s", reg.Sheet, letter, layout.rows[0], letter))
			}
		} else {
			name = letter
		}
		if n := seen[name]; n > 0 {
			// Pick the first suffix no header uses yet, and record it so a
			// later header cannot collide with the generated name either.
			unique := ""
			for {
				n++
				unique = fmt.Sprintf("%s_%d", name, n)
				if seen[unique] == 0 {
					break
				}
			}
			seen[name] = n
			warn(fmt.Sprintf("%s: duplicate header %q renamed %s", reg.Sheet, name, unique))
			name = unique
		}
		seen[name] = max(seen[name], 1)
		names = append(names, name)
	}
	return names
}

func cleanHeader(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	s = trimSpace(s)
	return strings.Join(strings.Fields(s), " ")
}

// findColumn locates a header by exact name, then case-insensitively and
// ignoring surrounding space; near reports the loose match when exact fails.
func findColumn(headers []string, name string) (index int, near string) {
	for i, h := range headers {
		if h == name {
			return i, ""
		}
	}
	want := strings.ToLower(trimSpace(name))
	for _, h := range headers {
		if strings.ToLower(trimSpace(h)) == want {
			return -1, h
		}
	}
	return -1, ""
}
