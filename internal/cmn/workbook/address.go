// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// region is a rectangular cell block on one sheet. R2 is 0 while the end row
// is still open, that is, until the used range resolves it.
type region struct {
	Sheet string
	C1    int
	R1    int
	C2    int
	R2    int
}

func (r region) String() string {
	start, _ := excelize.CoordinatesToCellName(r.C1, r.R1)
	end, _ := excelize.CoordinatesToCellName(r.C2, max(r.R2, r.R1))
	return r.Sheet + "!" + start + ":" + end
}

func (r region) rows() int {
	if r.R2 < r.R1 {
		return 0
	}
	return r.R2 - r.R1 + 1
}

var cellRangePattern = regexp.MustCompile(`^\$?([A-Za-z]{1,3})\$?(\d*)(?::\$?([A-Za-z]{1,3})\$?(\d*))?$`)

// parseRange resolves a range reference: A2:F200, A2:F, A:F, B3,
// Sheet1!A2:F, 'My Sheet'!A2, a defined name, or a table name. A sheet
// named in the reference wins over sheet.
func (w *file) parseRange(sheet, ref string) (region, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return region{}, fmt.Errorf("%s: range must not be empty", w.base)
	}
	target := sheet
	body := ref
	if i := strings.LastIndex(ref, "!"); i >= 0 {
		name := strings.TrimSpace(ref[:i])
		if len(name) >= 2 && strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'") {
			name = strings.ReplaceAll(name[1:len(name)-1], "''", "'")
		}
		resolved, err := w.resolveSheet(name)
		if err != nil {
			return region{}, err
		}
		target = resolved
		body = strings.TrimSpace(ref[i+1:])
	}
	if m := cellRangePattern.FindStringSubmatch(body); m != nil {
		return w.cellRegion(target, m)
	}
	if reg, ok, err := w.definedNameRegion(target, body); ok || err != nil {
		return reg, err
	}
	if reg, ok, err := w.tableRegion(body); ok || err != nil {
		return reg, err
	}
	return region{}, fmt.Errorf("%s: range %q is not a cell range, named range, or table", w.base, ref)
}

func (w *file) cellRegion(sheet string, m []string) (region, error) {
	c1, err := excelize.ColumnNameToNumber(strings.ToUpper(m[1]))
	if err != nil {
		return region{}, fmt.Errorf("%s: invalid range %q", w.base, m[0])
	}
	reg := region{Sheet: sheet, C1: c1, R1: 1}
	if m[2] != "" {
		reg.R1, _ = strconv.Atoi(m[2])
	}
	if m[3] == "" {
		// A single cell.
		reg.C2 = c1
		reg.R2 = reg.R1
		if m[2] == "" {
			// A bare column such as C: the whole column.
			return w.closeRegion(reg)
		}
		return reg, nil
	}
	c2, err := excelize.ColumnNameToNumber(strings.ToUpper(m[3]))
	if err != nil {
		return region{}, fmt.Errorf("%s: invalid range %q", w.base, m[0])
	}
	reg.C2 = c2
	if m[4] != "" {
		reg.R2, _ = strconv.Atoi(m[4])
	}
	if reg.C2 < reg.C1 {
		reg.C1, reg.C2 = reg.C2, reg.C1
	}
	if reg.R2 != 0 && reg.R2 < reg.R1 {
		reg.R1, reg.R2 = reg.R2, reg.R1
	}
	if reg.R1 < 1 {
		return region{}, fmt.Errorf("%s: invalid range %q", w.base, m[0])
	}
	return w.closeRegion(reg)
}

// closeRegion resolves an open end row from the sheet's used range.
func (w *file) closeRegion(reg region) (region, error) {
	if reg.R2 != 0 {
		return reg, nil
	}
	used, err := w.usedRange(reg.Sheet)
	if err != nil {
		return region{}, err
	}
	reg.R2 = used.R2
	if reg.R2 < reg.R1 {
		reg.R2 = reg.R1 - 1
	}
	return reg, nil
}

// usedRange returns A1 to the last cell that holds anything, or A1:A1 for
// an empty sheet. It is computed from the cells rather than the stored
// dimension, which a workbook written by another program may leave stale.
func (w *file) usedRange(sheet string) (region, error) {
	grid, err := w.grid(sheet)
	if err != nil {
		return region{}, err
	}
	reg := region{Sheet: sheet, C1: 1, R1: 1, C2: 1, R2: 1}
	for r := 1; r < len(grid); r++ {
		for c := len(grid[r]) - 1; c >= 1; c-- {
			if grid[r][c] != "" {
				reg.R2 = max(reg.R2, r)
				reg.C2 = max(reg.C2, c)
				break
			}
		}
	}
	return reg, nil
}

func (w *file) definedNameRegion(sheet, name string) (region, bool, error) {
	var global, scoped *excelize.DefinedName
	for _, dn := range w.f.GetDefinedName() {
		dn := dn
		if !strings.EqualFold(dn.Name, name) {
			continue
		}
		switch {
		case dn.Scope == "" || strings.EqualFold(dn.Scope, "Workbook"):
			if global == nil {
				global = &dn
			}
		case dn.Scope == sheet:
			scoped = &dn
		}
	}
	pick := scoped
	if pick == nil {
		pick = global
	}
	if pick == nil {
		return region{}, false, nil
	}
	refers := strings.ReplaceAll(pick.RefersTo, "$", "")
	if !strings.Contains(refers, "!") {
		refers = sheet + "!" + refers
	}
	reg, err := w.parseRange(sheet, refers)
	if err != nil {
		return region{}, true, fmt.Errorf("%s: named range %q refers to %q, which is not a cell range", w.base, pick.Name, pick.RefersTo)
	}
	return reg, true, nil
}

func (w *file) tableRegion(name string) (region, bool, error) {
	for _, sheet := range w.sheets {
		tables, err := w.f.GetTables(sheet)
		if err != nil {
			continue
		}
		for _, t := range tables {
			if strings.EqualFold(t.Name, name) {
				reg, err := w.parseRange(sheet, sheet+"!"+t.Range)
				if err != nil {
					return region{}, true, err
				}
				return reg, true, nil
			}
		}
	}
	return region{}, false, nil
}

// detectTable finds the data block of a sheet that has no explicit range:
// leading empty rows and columns are skipped and the header is the first row
// with two or more non-empty cells. ok is false when the sheet is empty.
func (w *file) detectTable(sheet string, grid [][]string) (reg region, ok bool, err error) {
	used, err := w.usedRange(sheet)
	if err != nil {
		return region{}, false, err
	}
	header := 0
	firstNonEmpty := 0
	for r := used.R1; r <= used.R2; r++ {
		count := 0
		for c := used.C1; c <= used.C2; c++ {
			if strings.TrimSpace(cellAt(grid, c, r)) != "" {
				count++
			}
		}
		if count > 0 && firstNonEmpty == 0 {
			firstNonEmpty = r
		}
		if count >= 2 {
			header = r
			break
		}
	}
	if header == 0 {
		header = firstNonEmpty
	}
	if header == 0 {
		return region{}, false, nil
	}
	c1, c2, r2 := 0, 0, header
	for r := header; r <= used.R2; r++ {
		for c := used.C1; c <= used.C2; c++ {
			if strings.TrimSpace(cellAt(grid, c, r)) == "" {
				continue
			}
			if c1 == 0 || c < c1 {
				c1 = c
			}
			c2 = max(c2, c)
			r2 = r
		}
	}
	return region{Sheet: sheet, C1: c1, R1: header, C2: c2, R2: r2}, true, nil
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}
