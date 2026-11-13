// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// maxStaleCells caps the cells a stale formula warning names.
const maxStaleCells = 5

// staleCells collects the formula cells of one sheet whose cached value is
// not what their formula evaluates to, for one bounded warning. A nil
// collector, used when formulas are not read cached, checks nothing.
type staleCells struct {
	w     *file
	sheet string
	cells []string
	count int
	// checked holds the formula cells evaluated so far, so each is
	// evaluated and counted once: a merge origin stands for every cell its
	// region covers, and two fields may name one cell.
	checked map[string]bool
}

// staleChecker returns the collector a read in mode checks its cells with:
// nil, which checks nothing, unless formulas are read cached.
func (w *file) staleChecker(sheet string, mode FormulaMode) *staleCells {
	if !mode.cached() {
		return nil
	}
	return &staleCells{w: w, sheet: sheet, checked: map[string]bool{}}
}

// check records the cell when it holds a formula whose evaluation
// disagrees with raw, its cached value.
func (s *staleCells) check(col, row int, raw string) {
	if s == nil || raw == "" {
		return
	}
	cell := cellName(col, row)
	formula, err := s.w.f.GetCellFormula(s.sheet, cell)
	if err != nil || formula == "" || s.checked[cell] {
		return
	}
	s.checked[cell] = true
	if !s.w.staleCache(s.sheet, cell, raw) {
		return
	}
	s.count++
	if len(s.cells) < maxStaleCells {
		s.cells = append(s.cells, s.sheet+"!"+cell)
	}
}

// checkRow checks the cells of one returned row in the columns read. A
// covered cell shows its merge origin's value, so the origin is checked in
// its place.
func (s *staleCells) checkRow(row int, cols []int, grid [][]string, merges mergeFill) {
	if s == nil {
		return
	}
	for _, c := range cols {
		oc, or := merges.origin(c, row)
		s.check(oc, or, cellAt(grid, oc, or))
	}
}

// report adds the warning naming the stale cells, when there are any.
func (s *staleCells) report(warn func(string)) {
	if s == nil || s.count == 0 {
		return
	}
	names := strings.Join(s.cells, ", ")
	if more := s.count - len(s.cells); more > 0 {
		names += fmt.Sprintf(" and %d more cells", more)
	}
	warn(names + ": cached formula value is stale; set formulas: calculate to read current values")
}

// staleCache reports whether the formula in cell evaluates to something
// other than raw, its cached value. Only a successful evaluation counts: a
// formula the engine cannot evaluate, or one that evaluates to an Excel
// error, says nothing about the cache. An error can come back as a value
// with no error, as IF(1,NA(),1) does, so the result is also checked by
// name, but only for a cell that is not text: a text cell whose formula
// yields "#N/A" has evaluated to that text. Nor does an empty evaluation
// of a number cell count: the engine yields nothing for a reference to a
// blank cell, where Excel caches 0. An empty text result, as from `=""`,
// is a value and is compared.
func (w *file) staleCache(sheet, cell, raw string) bool {
	calculated, err := w.f.CalcCellValue(sheet, cell, rawValues)
	if err != nil {
		return false
	}
	cellType, err := w.f.GetCellType(sheet, cell)
	if err != nil {
		return false
	}
	if !isTextCell(cellType) && (calculated == "" || excelErrors[calculated]) {
		return false
	}
	return !sameCellValue(raw, calculated, cellType)
}

// isTextCell reports whether a cell type holds text: a formula's string
// result, a shared string, or an inline string.
func isTextCell(t excelize.CellType) bool {
	return t == excelize.CellTypeFormula || t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString
}

// sameCellValue compares a cached value with an evaluated one by the
// cell's own type: a text cell exactly, leading zeros included; a boolean
// as its stored 1 or 0; a number as a number, so the digits a writer kept
// do not matter.
func sameCellValue(cached, calculated string, cellType excelize.CellType) bool {
	switch {
	case isTextCell(cellType), cellType == excelize.CellTypeDate:
		return cached == calculated
	case cellType == excelize.CellTypeBool:
		return boolDigit(cached) == boolDigit(calculated)
	}
	// A number, error, or unset cell compares as numbers when both parse,
	// else as text.
	a, aerr := strconv.ParseFloat(cached, 64)
	b, berr := strconv.ParseFloat(calculated, 64)
	if aerr != nil || berr != nil {
		return cached == calculated
	}
	return math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
}

// boolDigit maps the TRUE and FALSE an evaluation yields to the 1 and 0 a
// boolean cell stores.
func boolDigit(s string) string {
	switch {
	case strings.EqualFold(s, "TRUE"):
		return "1"
	case strings.EqualFold(s, "FALSE"):
		return "0"
	}
	return s
}
