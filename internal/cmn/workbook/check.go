// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ColumnCheck names a header column a step will need, and the with field
// that names it, for the message when it is missing. Exact requires the
// header to match as written, the way update_rows resolves its key and set
// columns; otherwise a match ignoring case and surrounding space counts,
// the way a read resolves its columns.
type ColumnCheck struct {
	Field string
	Name  string
	Exact bool
}

// CheckOptions says what a step will need from a workbook.
type CheckOptions struct {
	Password string
	// Sheet is checked for existence; empty means the first sheet.
	Sheet string
	// Range and Header locate the header row the columns are looked up in.
	Range  string
	Header HeaderSpec
	// Columns are header names that must exist, by exact or loose match.
	Columns []ColumnCheck
}

// Check reports, without changing anything, what a step would fail on: a
// workbook that does not exist, a sheet that is not in it, or a column that
// is not in the header row. Each problem names the with field it concerns
// and several are joined. What a dry run cannot judge, such as a locked or
// protected workbook, is not reported.
func Check(ctx context.Context, path string, opts CheckOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w, err := open(path, opts.Password)
	if err != nil {
		if _, ok := errors.AsType[*NotFoundError](err); ok {
			return fmt.Errorf("field 'with.path': %w", err)
		}
		return nil
	}
	defer w.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	sheet, err := w.resolveSheet(opts.Sheet)
	if err != nil {
		if _, ok := errors.AsType[*SheetNotFoundError](err); ok {
			return fmt.Errorf("field 'with.sheet': %w", err)
		}
		return nil
	}
	if strings.TrimSpace(opts.Range) == "" && (len(opts.Columns) == 0 || opts.Header.Mode == HeaderNone) {
		return nil
	}
	loc, err := w.locate(sheet, opts.Range, opts.Header)
	if err != nil {
		// A range such as Nope!A1:B2 names its own sheet.
		if _, ok := errors.AsType[*SheetNotFoundError](err); ok {
			return fmt.Errorf("field 'with.range': %w", err)
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(opts.Columns) == 0 || opts.Header.Mode == HeaderNone {
		return nil
	}
	if !loc.ok {
		// A read of an empty sheet succeeds with no rows, and a validation
		// reports the columns as problems; only an operation that must
		// find its columns, such as update_rows, fails there.
		var problems []error
		for _, c := range opts.Columns {
			if c.Exact {
				problems = append(problems, fmt.Errorf("field 'with.%s': column %q not found; the sheet %q is empty", c.Field, c.Name, sheet))
			}
		}
		return errors.Join(problems...)
	}
	layout, err := layoutHeader(loc.reg, opts.Header)
	if err != nil {
		return nil
	}
	merges, err := w.mergeMap(sheet)
	if err != nil {
		return nil
	}
	headers := headerNames(loc.reg, layout, loc.grid, merges, func(string) {})
	var problems []error
	for _, c := range opts.Columns {
		i, near := findColumn(headers, c.Name)
		switch {
		case i >= 0, near != "" && !c.Exact:
		case near != "":
			problems = append(problems, fmt.Errorf("field 'with.%s': column %q not found in header row %d; did you mean %q?", c.Field, c.Name, layout.rows[0], near))
		default:
			problems = append(problems, fmt.Errorf("field 'with.%s': column %q not found in header row %d; headers present: %s", c.Field, c.Name, layout.rows[0], strings.Join(headers, ", ")))
		}
	}
	return errors.Join(problems...)
}
