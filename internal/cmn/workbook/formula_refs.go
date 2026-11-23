// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/efp"
	"github.com/xuri/excelize/v2"
)

// formulaRefs is what a formula's text says its result can read. A formula
// whose text does not pin what it reads is unknown, and an unknown formula
// is treated as depending on every change.
type formulaRefs struct {
	cells []cellRef
	names []nameRef
	// tables names the tables of structured references; "" is the table
	// that holds the formula.
	tables  []string
	unknown bool
}

// cellRef is one A1 reference. abs marks the coordinates a shared formula
// keeps when it is filled into another cell, in the order C1, R1, C2, R2.
type cellRef struct {
	sheet string // as written; "" is the formula's own sheet
	reg   region
	abs   [4]bool
}

// nameRef is a defined name and the sheet that qualifies it, if any.
type nameRef struct {
	sheet, name string
}

// dynamicRefFunctions compute their references or read the workbook's
// structure, so their arguments do not say what the result reads.
var dynamicRefFunctions = map[string]bool{
	"INDIRECT": true, "OFFSET": true, "ANCHORARRAY": true,
	"CELL": true, "INFO": true, "SHEET": true, "SHEETS": true,
}

// maxNameDepth bounds defined names that refer through other names.
const maxNameDepth = 8

func parseFormulaRefs(text string) formulaRefs {
	var refs formulaRefs
	parser := efp.ExcelParser()
	for _, token := range parser.Parse(text) {
		switch token.TType {
		case efp.TokenTypeUnknown:
			refs.unknown = true
		case efp.TokenTypeFunction:
			if token.TSubType == efp.TokenSubTypeStart && !plainFunction(token.TValue) {
				refs.unknown = true
			}
		case efp.TokenTypeOperand:
			if token.TSubType == efp.TokenSubTypeRange {
				refs.addOperand(token.TValue)
			}
		}
	}
	return refs
}

// plainFunction reports whether a function's result follows from its
// arguments alone. A user-defined function, or a token the tokenizer took
// for a function name, such as "A1:INDEX", is not.
func plainFunction(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "_XLUDF.") {
		return false
	}
	upper = strings.TrimPrefix(strings.TrimPrefix(upper, "_XLFN."), "_XLWS.")
	if upper == "" || dynamicRefFunctions[upper] {
		return false
	}
	return !strings.ContainsFunc(upper, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_'
	})
}

func (r *formulaRefs) addOperand(text string) {
	sheet, ref := "", text
	if i := strings.LastIndex(text, "!"); i >= 0 {
		sheet, ref = text[:i], text[i+1:]
	}
	switch {
	case strings.Contains(sheet, "["):
		// Another workbook, which no edit of this one changes.
	case strings.Contains(ref, "["):
		r.tables = append(r.tables, strings.TrimSpace(ref[:strings.Index(ref, "[")]))
	case strings.Contains(sheet, ":"):
		// A 3-D reference spans sheets by their position.
		r.unknown = true
	default:
		if cell, ok := parseCellRef(ref); ok {
			cell.sheet = sheet
			r.cells = append(r.cells, cell)
		} else {
			r.names = append(r.names, nameRef{sheet: sheet, name: ref})
		}
	}
}

// parseCellRef reads a cell, a range, whole columns, or whole rows. A lone
// column or row is not a reference, and neither is text such as a name.
func parseCellRef(text string) (cellRef, bool) {
	start, end, ranged := strings.Cut(text, ":")
	first, ok := parseRefEnd(start)
	if !ok {
		return cellRef{}, false
	}
	last := first
	if ranged {
		if last, ok = parseRefEnd(end); !ok {
			return cellRef{}, false
		}
	}
	if (first.col == 0) != (last.col == 0) || (first.row == 0) != (last.row == 0) ||
		!ranged && (first.col == 0 || first.row == 0) {
		return cellRef{}, false
	}
	ref := cellRef{abs: [4]bool{first.abs[0], first.abs[1], last.abs[0], last.abs[1]}}
	ref.reg = region{C1: first.col, R1: first.row, C2: last.col, R2: last.row}
	if first.col == 0 {
		ref.reg.C1, ref.reg.C2, ref.abs[0], ref.abs[2] = 1, excelize.MaxColumns, true, true
	}
	if first.row == 0 {
		ref.reg.R1, ref.reg.R2, ref.abs[1], ref.abs[3] = 1, excelize.TotalRows, true, true
	}
	return ref, true
}

// refEnd is one end of a reference: "$A$1", "A", or "1". A part that is
// absent is zero; abs marks a "$" before the column and before the row.
type refEnd struct {
	col, row int
	abs      [2]bool
}

func parseRefEnd(text string) (refEnd, bool) {
	var end refEnd
	s := text
	dollar := strings.HasPrefix(s, "$")
	if dollar {
		s = s[1:]
	}
	letters := 0
	for letters < len(s) && (s[letters] >= 'A' && s[letters] <= 'Z' || s[letters] >= 'a' && s[letters] <= 'z') {
		letters++
	}
	if letters > 0 {
		n, err := excelize.ColumnNameToNumber(s[:letters])
		if letters > 3 || err != nil {
			return refEnd{}, false
		}
		end.col, end.abs[0] = n, dollar
		s = s[letters:]
		if dollar = strings.HasPrefix(s, "$"); dollar {
			s = s[1:]
		}
	}
	if s == "" {
		return end, end.col > 0 && !dollar
	}
	if strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return refEnd{}, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > excelize.TotalRows {
		return refEnd{}, false
	}
	end.row, end.abs[1] = n, dollar
	return end, true
}

// at returns the region the reference covers in a formula filled dc
// columns and dr rows from the cell it was written in. It is false when
// that falls off the sheet, where the formula shows #REF!.
func (c cellRef) at(dc, dr int) (region, bool) {
	coords := [4]int{c.reg.C1, c.reg.R1, c.reg.C2, c.reg.R2}
	deltas := [4]int{dc, dr, dc, dr}
	for i := range coords {
		if !c.abs[i] {
			coords[i] += deltas[i]
		}
	}
	reg := region{
		C1: min(coords[0], coords[2]), R1: min(coords[1], coords[3]),
		C2: max(coords[0], coords[2]), R2: max(coords[1], coords[3]),
	}
	return reg, reg.C1 >= 1 && reg.R1 >= 1 && reg.C2 <= excelize.MaxColumns && reg.R2 <= excelize.TotalRows
}

// refResolver turns a formula's references into the regions it reads, with
// the workbook's defined names and tables.
type refResolver struct {
	names  []excelize.DefinedName
	tables []bookTable
}

// footprint returns the regions a formula in sheet at (col, row) reads, its
// relative references shifted by (dc, dr), or true when they are unknown.
// Each region's Sheet is a folded sheet name.
func (r *refResolver) footprint(refs formulaRefs, sheet string, col, row, dc, dr int) ([]region, bool) {
	if refs.unknown {
		return nil, true
	}
	regs := make([]region, 0, len(refs.cells))
	for _, c := range refs.cells {
		if reg, ok := c.at(dc, dr); ok {
			reg.Sheet = sheetKey(cmp.Or(c.sheet, sheet))
			regs = append(regs, reg)
		}
	}
	for _, name := range refs.tables {
		reg, ok := r.table(name, sheet, col, row)
		if !ok {
			return nil, true
		}
		regs = append(regs, reg)
	}
	for _, name := range refs.names {
		named, unknown := r.name(name, sheet, 0)
		if unknown {
			return nil, true
		}
		regs = append(regs, named...)
	}
	return regs, false
}

// name resolves a defined name used on sheet. A name the workbook does not
// define reads nothing: it is #NAME? whatever changes, or a parameter of LET
// or LAMBDA, bound by references the formula already lists. A table's name
// alone refers to its data.
func (r *refResolver) name(ref nameRef, sheet string, depth int) ([]region, bool) {
	def, ok := r.definedName(ref.name, cmp.Or(ref.sheet, sheet))
	if !ok {
		if reg, ok := r.table(ref.name, "", 0, 0); ok && ref.sheet == "" {
			return []region{reg}, false
		}
		return nil, false
	}
	if depth == maxNameDepth {
		return nil, true
	}
	refs := parseFormulaRefs(strings.TrimPrefix(def.RefersTo, "="))
	if refs.unknown {
		return nil, true
	}
	var regs []region
	for _, c := range refs.cells {
		// A name without a sheet or with a relative reference reads cells
		// relative to the formula that uses it.
		if c.sheet == "" || slices.Contains(c.abs[:], false) {
			return nil, true
		}
		reg := c.reg
		reg.Sheet = sheetKey(c.sheet)
		regs = append(regs, reg)
	}
	for _, name := range refs.tables {
		reg, ok := r.table(name, "", 0, 0)
		if !ok {
			return nil, true
		}
		regs = append(regs, reg)
	}
	scope := sheet
	if def.Scope != "Workbook" {
		scope = def.Scope
	}
	for _, inner := range refs.names {
		named, unknown := r.name(inner, scope, depth+1)
		if unknown {
			return nil, true
		}
		regs = append(regs, named...)
	}
	return regs, false
}

// definedName finds a name as sheet sees it: its own name first, then the
// workbook's.
func (r *refResolver) definedName(name, sheet string) (excelize.DefinedName, bool) {
	var global *excelize.DefinedName
	for i, def := range r.names {
		if !strings.EqualFold(def.Name, name) {
			continue
		}
		if strings.EqualFold(def.Scope, sheet) {
			return def, true
		}
		if def.Scope == "Workbook" {
			global = &r.names[i]
		}
	}
	if global == nil {
		return excelize.DefinedName{}, false
	}
	return *global, true
}

// table returns a table's range by name, or, for an empty name, the table
// that holds the cell at (col, row) of sheet.
func (r *refResolver) table(name, sheet string, col, row int) (region, bool) {
	for _, t := range r.tables {
		if name == "" && t.reg.Sheet == sheetKey(sheet) && t.reg.contains(col, row) ||
			name != "" && strings.EqualFold(t.name, name) {
			return t.reg, true
		}
	}
	return region{}, false
}

func (r region) contains(col, row int) bool {
	return col >= r.C1 && col <= r.C2 && row >= r.R1 && row <= r.R2
}

// sheetKey folds a sheet name: Excel compares sheet names without case.
func sheetKey(name string) string {
	return strings.ToLower(name)
}

// formulaNode is one formula cell in the dependency analysis of a save.
type formulaNode struct {
	part     *heldPart
	col, row int
	deps     []region
	unknown  bool
	// group is the range an array or data-table formula fills, when it is
	// more than its own cell; the other cells carry no formula of their own.
	group   region
	cleared bool
}

// output is what changes when the node's cache is cleared: its cell, or
// the range it fills.
func (n *formulaNode) output() region {
	if n.group.Sheet != "" {
		return n.group
	}
	return region{Sheet: sheetKey(n.part.sheet), C1: n.col, R1: n.row, C2: n.col, R2: n.row}
}

// markDependents clears every formula that can depend on a change: one
// that is unknown or reads a changed cell, and then, in turn, one that
// reads a formula already cleared. Passes alternate direction, so a chain
// running down or up a column settles in a few passes.
func markDependents(nodes []*formulaNode, dirty dirtyCells) {
	for forward := true; ; forward = !forward {
		progress := false
		for i := range nodes {
			n := nodes[i]
			if !forward {
				n = nodes[len(nodes)-1-i]
			}
			if n.cleared || !n.unknown && !slices.ContainsFunc(n.deps, dirty.touches) {
				continue
			}
			n.cleared, progress = true, true
			dirty.add(n.output())
		}
		if !progress {
			return
		}
	}
}

// dirtyCells indexes the cells whose value a save changes: by folded sheet
// name, then column, as sorted disjoint row spans.
type dirtyCells map[string]*dirtySheet

type dirtySheet struct {
	all  bool
	cols map[int][]span
}

type span struct{ r1, r2 int }

// wideChange is the column count past which a change counts as one to the
// whole sheet rather than to each column it spans.
const wideChange = 1024

func (d dirtyCells) add(reg region) {
	s := d[reg.Sheet]
	if s == nil {
		s = &dirtySheet{cols: map[int][]span{}}
		d[reg.Sheet] = s
	}
	if s.all {
		return
	}
	if reg.C2-reg.C1 >= wideChange {
		s.all, s.cols = true, nil
		return
	}
	for c := reg.C1; c <= reg.C2; c++ {
		s.cols[c] = addSpan(s.cols[c], span{reg.R1, reg.R2})
	}
}

func (d dirtyCells) touches(reg region) bool {
	s := d[reg.Sheet]
	switch {
	case s == nil:
		return false
	case s.all:
		return true
	case reg.C2-reg.C1 < len(s.cols):
		for c := reg.C1; c <= reg.C2; c++ {
			if overlaps(s.cols[c], reg.R1, reg.R2) {
				return true
			}
		}
		return false
	}
	for c, spans := range s.cols {
		if c >= reg.C1 && c <= reg.C2 && overlaps(spans, reg.R1, reg.R2) {
			return true
		}
	}
	return false
}

func addSpan(spans []span, s span) []span {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].r2 >= s.r1-1 })
	j := i
	for j < len(spans) && spans[j].r1 <= s.r2+1 {
		s.r1, s.r2 = min(s.r1, spans[j].r1), max(s.r2, spans[j].r2)
		j++
	}
	return slices.Replace(spans, i, j, s)
}

func overlaps(spans []span, r1, r2 int) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].r2 >= r1 })
	return i < len(spans) && spans[i].r1 <= r2
}
