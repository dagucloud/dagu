// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package workbook reads and writes .xlsx workbooks without a spreadsheet
// application. It is the single implementation behind the xlsx actions, the
// dagu xlsx command, and the MCP workbook read target, so every consumer sees
// the same typing, header detection, and change summaries.
package workbook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// DefaultMaxRows is the number of rows a read returns when max_rows is unset.
const DefaultMaxRows = 5000

// Row is one sheet row keyed by header. Values are nil, string, int64,
// float64, or bool; dates are ISO 8601 strings. RowNumberKey holds the
// 1-based sheet row as an int.
type Row map[string]any

// RowNumberKey is the row field carrying the 1-based sheet row number.
const RowNumberKey = "_row"

// CellError names the workbook, sheet, and cell of a problem.
type CellError struct {
	Workbook string
	Sheet    string
	Cell     string
	Msg      string
}

func (e *CellError) Error() string {
	if e.Cell == "" {
		return fmt.Sprintf("%s %s: %s", e.Workbook, e.Sheet, e.Msg)
	}
	return fmt.Sprintf("%s %s!%s: %s", e.Workbook, e.Sheet, e.Cell, e.Msg)
}

// SheetNotFoundError lists the sheets a workbook has when a name misses.
type SheetNotFoundError struct {
	Workbook string
	Sheet    string
	Present  []string
}

func (e *SheetNotFoundError) Error() string {
	return fmt.Sprintf("%s: sheet %q not found; sheets present: %s", e.Workbook, e.Sheet, strings.Join(e.Present, ", "))
}

// LockedError reports a workbook another program holds open.
type LockedError struct {
	Path string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("%s is open in another program; close it and retry", filepath.Base(e.Path))
}

// NotFoundError reports a missing workbook. It unwraps to fs.ErrNotExist.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s: workbook not found", filepath.Base(e.Path))
}

// Unwrap lets errors.Is(err, fs.ErrNotExist) hold.
func (*NotFoundError) Unwrap() error { return fs.ErrNotExist }

// ErrUnsupportedFormat is wrapped into errors for files that are not .xlsx.
var ErrUnsupportedFormat = errors.New("only .xlsx and .xlsm workbooks are supported; save as .xlsx")

// ErrNotWorkbook is wrapped into errors for files that cannot be parsed.
var ErrNotWorkbook = errors.New("not a valid .xlsx workbook")

// CheckExtension rejects paths whose extension is not .xlsx or .xlsm.
func CheckExtension(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xlsm":
		return nil
	default:
		return fmt.Errorf("%s: %w", filepath.Base(path), ErrUnsupportedFormat)
	}
}

// file is an open workbook with the caches reads need.
type file struct {
	f        *excelize.File
	path     string
	base     string
	date1904 bool
	sheets   []string
	kinds    map[int]cellKind
	grids    map[string][][]string
	// dated memoizes styles derived for dates written into plain cells.
	dated map[datedKey]int
}

func open(path, password string) (*file, error) {
	if err := CheckExtension(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &NotFoundError{Path: path}
		}
		return nil, classifyError(path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s: is a directory", filepath.Base(path))
	}
	opts := excelize.Options{Password: password}
	f, err := excelize.OpenFile(path, opts)
	if err != nil {
		if locked := classifyError(path, err); locked != nil && errors.As(locked, new(*LockedError)) {
			return nil, locked
		}
		return nil, fmt.Errorf("%s: %w: %v", filepath.Base(path), ErrNotWorkbook, err)
	}
	w := &file{f: f, path: path, base: filepath.Base(path), kinds: map[int]cellKind{}, grids: map[string][][]string{}}
	w.sheets = f.GetSheetList()
	if props, err := f.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		w.date1904 = *props.Date1904
	}
	return w, nil
}

func (w *file) close() {
	if w != nil && w.f != nil {
		_ = w.f.Close()
	}
}

// resolveSheet returns the sheet a name refers to: the first sheet when the
// name is empty, an exact match, or a unique case-insensitive match.
func (w *file) resolveSheet(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		if len(w.sheets) == 0 {
			return "", fmt.Errorf("%s: workbook has no sheets", w.base)
		}
		return w.sheets[0], nil
	}
	for _, s := range w.sheets {
		if s == name {
			return s, nil
		}
	}
	match := ""
	for _, s := range w.sheets {
		if strings.EqualFold(s, name) {
			if match != "" {
				match = ""
				break
			}
			match = s
		}
	}
	if match != "" {
		return match, nil
	}
	return "", &SheetNotFoundError{Workbook: w.base, Sheet: name, Present: append([]string(nil), w.sheets...)}
}

func (w *file) cellError(sheet string, col, row int, msg string) *CellError {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	return &CellError{Workbook: w.base, Sheet: sheet, Cell: cell, Msg: msg}
}

func (w *file) sheetError(sheet, msg string) *CellError {
	return &CellError{Workbook: w.base, Sheet: sheet, Msg: msg}
}

// grid returns every cell of a sheet as raw strings indexed from row 1 and
// column 1 (index 0 is unused). Rows keep their own length, so a sparse
// sheet with one far-right cell does not allocate a full rectangle; cellAt
// treats a missing column as empty. Grids are cached per sheet for the life
// of the open workbook; writers must invalidate them.
func (w *file) grid(sheet string) ([][]string, error) {
	if cached, ok := w.grids[sheet]; ok {
		return cached, nil
	}
	rows, err := w.f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", w.base, sheet, err)
	}
	grid := make([][]string, len(rows)+1)
	grid[0] = nil
	for i, r := range rows {
		shifted := make([]string, len(r)+1)
		copy(shifted[1:], r)
		grid[i+1] = shifted
	}
	w.grids[sheet] = grid
	return grid, nil
}

// forget drops a sheet's cached grid after cells change.
func (w *file) forget(sheet string) {
	delete(w.grids, sheet)
}

func cellAt(grid [][]string, col, row int) string {
	if row < 1 || row >= len(grid) || col < 1 || col >= len(grid[row]) {
		return ""
	}
	return grid[row][col]
}

func rowIsEmpty(grid [][]string, row, c1, c2 int) bool {
	for c := c1; c <= c2; c++ {
		if strings.TrimSpace(cellAt(grid, c, row)) != "" {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
