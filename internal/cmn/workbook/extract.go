// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"fmt"
	"strings"
)

// ReadCellsOptions names the cells to read, one per field.
type ReadCellsOptions struct {
	Password string
	Sheet    string
	// Range, when set, is the area an address must fall in.
	Range string
	// Cells maps a field to the address of its cell; an empty address is a
	// field the sheet does not have.
	Cells map[string]string
	// Types pins how a field's cell is read.
	Types    map[string]ColumnType
	Trim     bool
	Formulas FormulaMode
}

// ReadCellsResult is what reading named cells yields.
type ReadCellsResult struct {
	Sheet string
	// Values holds each field's typed value; nil for an empty cell or an
	// absent field.
	Values map[string]any
	// Cells holds the cell each field was read from, as Sheet!B7, or an
	// empty string for an absent field.
	Cells    map[string]string
	Warnings []string
}

// AddressError is an answer for a field that does not name one readable
// cell of the sheet and area asked for.
type AddressError struct {
	Workbook string
	Field    string
	Address  string
	// Msg is the predicate: "is not a single cell", "is outside Sheet1!A1:D20".
	Msg string
}

func (e *AddressError) Error() string {
	return fmt.Sprintf("%s: field %q: %q %s", e.Workbook, e.Field, e.Address, e.Msg)
}

// ReadCells reads the typed value of one cell per field. An address that
// is not one cell, that is on another sheet, or that lies outside Range
// is an *AddressError; a cell that fails its pinned type is a *CellError.
func ReadCells(ctx context.Context, path string, opts ReadCellsOptions) (*ReadCellsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	sheet, err := w.resolveSheet(opts.Sheet)
	if err != nil {
		return nil, err
	}
	var bounds *region
	if strings.TrimSpace(opts.Range) != "" {
		reg, err := w.parseRange(sheet, opts.Range)
		if err != nil {
			return nil, err
		}
		if reg, err = w.closeRegion(reg); err != nil {
			return nil, err
		}
		sheet = reg.Sheet
		bounds = &reg
	}
	grid, err := w.grid(sheet)
	if err != nil {
		return nil, err
	}
	merges, err := w.mergeMap(sheet)
	if err != nil {
		return nil, err
	}
	result := &ReadCellsResult{Sheet: sheet, Values: map[string]any{}, Cells: map[string]string{}, Warnings: []string{}}
	warn := func(msg string) { result.Warnings = append(result.Warnings, msg) }
	readOpts := ReadOptions{Trim: opts.Trim, Formulas: opts.Formulas}
	for _, field := range sortedKeys(opts.Cells) {
		addr := strings.TrimSpace(opts.Cells[field])
		if addr == "" {
			result.Values[field] = nil
			result.Cells[field] = ""
			continue
		}
		// A range written as one, such as A1:A, is refused before it is
		// resolved, since an open end closes to one cell on a one-row sheet.
		if strings.Contains(addr, ":") {
			return nil, &AddressError{Workbook: w.base, Field: field, Address: addr, Msg: "is not a single cell"}
		}
		reg, err := w.parseRange(sheet, addr)
		if err != nil {
			return nil, &AddressError{Workbook: w.base, Field: field, Address: addr, Msg: "is not a cell address"}
		}
		if reg.R2 == 0 || reg.C1 != reg.C2 || reg.R1 != reg.R2 {
			return nil, &AddressError{Workbook: w.base, Field: field, Address: addr, Msg: "is not a single cell"}
		}
		if reg.Sheet != sheet {
			return nil, &AddressError{Workbook: w.base, Field: field, Address: addr, Msg: "is not on sheet " + sheet}
		}
		if bounds != nil && (reg.C1 < bounds.C1 || reg.C1 > bounds.C2 || reg.R1 < bounds.R1 || reg.R1 > bounds.R2) {
			return nil, &AddressError{Workbook: w.base, Field: field, Address: addr, Msg: "is outside " + bounds.String()}
		}
		col, row := merges.origin(reg.C1, reg.R1)
		value, err := w.cellValue(sheet, col, row, cellAt(grid, col, row), readOpts, warn)
		if err != nil {
			return nil, err
		}
		if t := opts.Types[field]; t != "" && value != nil {
			value, err = coerce(value, t, w.date1904)
			if err != nil {
				return nil, w.cellError(sheet, col, row, err.Error())
			}
		}
		result.Values[field] = value
		result.Cells[field] = sheet + "!" + cellName(col, row)
	}
	return result, nil
}
