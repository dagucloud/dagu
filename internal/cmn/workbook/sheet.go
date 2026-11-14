// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SheetOperation is what Sheet does to a workbook.
type SheetOperation string

// Sheet operations.
const (
	// SheetAdd creates an empty sheet.
	SheetAdd SheetOperation = "add"
	// SheetCopy duplicates a sheet under a new name.
	SheetCopy SheetOperation = "copy"
	// SheetRename gives a sheet a new name.
	SheetRename SheetOperation = "rename"
	// SheetDelete removes a sheet.
	SheetDelete SheetOperation = "delete"
)

// ExistsMode says what happens when the sheet an operation would create is
// already there.
type ExistsMode string

// Exists modes.
const (
	// ExistsFail fails the operation.
	ExistsFail ExistsMode = "fail"
	// ExistsSkip leaves the workbook alone with a warning.
	ExistsSkip ExistsMode = "skip"
	// ExistsReplace replaces the existing sheet's contents.
	ExistsReplace ExistsMode = "replace"
)

// SheetOptions controls Sheet.
type SheetOptions struct {
	Password  string
	Operation SheetOperation
	// Sheet is the sheet operated on: the new name for add, the source for
	// copy, the sheet to rename or delete otherwise.
	Sheet string
	// To is the new name for copy and rename.
	To string
	// IfExists applies to the sheet add, copy, and rename would create;
	// empty means ExistsFail.
	IfExists ExistsMode
	// Missing applies to the source of copy, rename, and delete; empty
	// means MissingFail, and MissingSkip leaves the workbook alone with a
	// warning.
	Missing MissingMode
	// Position is the 1-based place of the sheet add or copy creates; zero
	// puts an added sheet last and a copy right after its source.
	Position int
	InPlace  bool
	DryRun   bool
	Lock     LockOptions
}

// SheetResult is what a sheet operation publishes: the writer result plus
// the sheet names afterwards.
type SheetResult struct {
	WriteResult
	Sheets []string `json:"sheets"`
	// Skipped is true when IfExists or Missing left the workbook as it was.
	Skipped bool `json:"skipped"`
}

// Sheet adds, copies, renames, or deletes a sheet. Other sheets are kept as
// they are. A rename does not rewrite formulas on other sheets that name
// the sheet, a delete leaves such references dangling and drops the names
// scoped to the sheet, and a copy carries cells, styles, widths, merged
// regions, and validations but not tables, images, or charts: those are
// limits of the underlying library.
func Sheet(ctx context.Context, path string, opts SheetOptions) (*SheetResult, error) {
	if opts.IfExists == "" {
		opts.IfExists = ExistsFail
	}
	if opts.Missing == "" {
		opts.Missing = MissingFail
	}
	// A misspelled mode is refused before anything is opened, so it cannot
	// pass unnoticed on a run that never reaches the branch it governs.
	switch opts.IfExists {
	case ExistsFail, ExistsSkip, ExistsReplace:
	default:
		return nil, fmt.Errorf("%s: if_exists must be fail, skip, or replace, not %q", Base(path), opts.IfExists)
	}
	switch opts.Missing {
	case MissingFail, MissingSkip:
	case MissingAppend:
		return nil, fmt.Errorf("%s: missing must be fail or skip for a sheet operation; append applies to update_rows", Base(path))
	default:
		return nil, fmt.Errorf("%s: missing must be fail or skip for a sheet operation, not %q", Base(path), opts.Missing)
	}
	return withLock(ctx, path, opts.Lock, func() (*SheetResult, error) {
		return sheetOnce(ctx, path, opts)
	})
}

func sheetOnce(ctx context.Context, path string, opts SheetOptions) (*SheetResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &SheetResult{Path: path, DryRun: opts.DryRun, Warnings: []string{}, Sheets: []string{}}
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

	op := &sheetOp{w: w, opts: opts, result: result, warn: warn}
	var sheet string
	switch opts.Operation {
	case SheetAdd:
		sheet, err = op.add()
	case SheetCopy:
		sheet, err = op.copy()
	case SheetRename:
		sheet, err = op.rename()
	case SheetDelete:
		sheet, err = op.delete()
	default:
		err = fmt.Errorf("%s: unknown sheet operation %q", w.base, opts.Operation)
	}
	if err != nil {
		return nil, err
	}
	w.sheets = w.f.GetSheetList()
	result.Sheets = append(result.Sheets, w.sheets...)
	result.Sheet = sheet
	result.Changes.Sheet = sheet
	if opts.DryRun || result.Skipped {
		return result, nil
	}
	if err := w.save(opts.InPlace); err != nil {
		return nil, err
	}
	return result, nil
}

// sheetOp carries one operation's state.
type sheetOp struct {
	w      *file
	opts   SheetOptions
	result *SheetResult
	warn   func(string)
}

// existing returns the sheet a name refers to, or false when there is none.
func (op *sheetOp) existing(name string) (string, bool, error) {
	sheet, err := op.w.resolveSheet(name)
	if err == nil {
		return sheet, true, nil
	}
	if _, ok := errors.AsType[*SheetNotFoundError](err); ok {
		return "", false, nil
	}
	return "", false, err
}

// source resolves the sheet an operation starts from; with MissingSkip a
// missing sheet becomes a warning and ok is false.
func (op *sheetOp) source(name string) (sheet string, ok bool, err error) {
	sheet, err = op.w.resolveSheet(name)
	if err == nil {
		return sheet, true, nil
	}
	var missing *SheetNotFoundError
	if op.opts.Missing == MissingSkip && errors.As(err, &missing) {
		op.warn(fmt.Sprintf("sheet %q not found; nothing %s", name, pastTense(op.opts.Operation)))
		op.result.Skipped = true
		return "", false, nil
	}
	return "", false, err
}

func pastTense(operation SheetOperation) string {
	switch operation {
	case SheetAdd:
		return "added"
	case SheetCopy:
		return "copied"
	case SheetRename:
		return "renamed"
	case SheetDelete:
		return "deleted"
	default:
		return "done"
	}
}

func (op *sheetOp) newName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%s: %s must not be empty", op.w.base, field)
	}
	return name, nil
}

func (op *sheetOp) create(name string) error {
	if _, err := op.w.f.NewSheet(name); err != nil {
		return fmt.Errorf("%s: invalid sheet name %q: %v", op.w.base, name, err)
	}
	return nil
}

// place moves a sheet that was just created at the end to position, when
// one was asked for; count is the number of sheets now.
func (op *sheetOp) place(name string, position int) error {
	if position == 0 {
		return nil
	}
	list := op.w.f.GetSheetList()
	if position < 1 || position > len(list) {
		return fmt.Errorf("%s: position %d is outside 1 to %d", op.w.base, position, len(list))
	}
	if position == len(list) {
		return nil
	}
	return op.w.f.MoveSheet(name, list[position-1])
}

func (op *sheetOp) add() (string, error) {
	name, err := op.newName("sheet", op.opts.Sheet)
	if err != nil {
		return "", err
	}
	if existing, ok, err := op.existing(name); err != nil {
		return "", err
	} else if ok {
		switch op.opts.IfExists {
		case ExistsSkip:
			op.warn(fmt.Sprintf("sheet %q already exists; nothing added", existing))
			op.result.Skipped = true
			return existing, nil
		case ExistsReplace:
			return existing, op.w.clearSheet(existing)
		case ExistsFail:
			return "", fmt.Errorf("%s: sheet %q already exists", op.w.base, existing)
		default:
			return "", fmt.Errorf("%s: unknown if_exists mode %q", op.w.base, op.opts.IfExists)
		}
	}
	if err := op.create(name); err != nil {
		return "", err
	}
	return name, op.place(name, op.opts.Position)
}

func (op *sheetOp) copy() (string, error) {
	source, ok, err := op.source(op.opts.Sheet)
	if err != nil || !ok {
		return source, err
	}
	to, err := op.newName("to", op.opts.To)
	if err != nil {
		return "", err
	}
	fromIndex, err := op.w.f.GetSheetIndex(source)
	if err != nil {
		return "", err
	}
	if existing, ok, err := op.existing(to); err != nil {
		return "", err
	} else if ok {
		if existing == source {
			return "", fmt.Errorf("%s: sheet %q cannot be copied onto itself", op.w.base, source)
		}
		switch op.opts.IfExists {
		case ExistsSkip:
			op.warn(fmt.Sprintf("sheet %q already exists; nothing copied", existing))
			op.result.Skipped = true
			return existing, nil
		case ExistsReplace:
			// Copying over the existing sheet keeps its position and the
			// names scoped to it.
			toIndex, err := op.w.f.GetSheetIndex(existing)
			if err != nil {
				return "", err
			}
			return existing, op.w.f.CopySheet(fromIndex, toIndex)
		case ExistsFail:
			return "", fmt.Errorf("%s: sheet %q already exists", op.w.base, existing)
		default:
			return "", fmt.Errorf("%s: unknown if_exists mode %q", op.w.base, op.opts.IfExists)
		}
	}
	if err := op.create(to); err != nil {
		return "", err
	}
	toIndex, err := op.w.f.GetSheetIndex(to)
	if err != nil {
		return "", err
	}
	if err := op.w.f.CopySheet(fromIndex, toIndex); err != nil {
		return "", fmt.Errorf("%s: copy sheet %q: %v", op.w.base, source, err)
	}
	position := op.opts.Position
	if position == 0 {
		// Right after the source, where a person would expect the copy.
		position = fromIndex + 2
	}
	return to, op.place(to, position)
}

func (op *sheetOp) rename() (string, error) {
	source, ok, err := op.source(op.opts.Sheet)
	if err != nil || !ok {
		return source, err
	}
	to, err := op.newName("to", op.opts.To)
	if err != nil {
		return "", err
	}
	if to == source {
		op.warn(fmt.Sprintf("sheet %q already has that name; nothing renamed", source))
		op.result.Skipped = true
		return source, nil
	}
	if existing, ok, err := op.existing(to); err != nil {
		return "", err
	} else if ok && existing != source {
		switch op.opts.IfExists {
		case ExistsSkip:
			op.warn(fmt.Sprintf("sheet %q already exists; nothing renamed", existing))
			op.result.Skipped = true
			return existing, nil
		case ExistsReplace:
			if err := op.w.f.DeleteSheet(existing); err != nil {
				return "", fmt.Errorf("%s: delete sheet %q: %v", op.w.base, existing, err)
			}
		case ExistsFail:
			return "", fmt.Errorf("%s: sheet %q already exists", op.w.base, existing)
		default:
			return "", fmt.Errorf("%s: unknown if_exists mode %q", op.w.base, op.opts.IfExists)
		}
	}
	if err := op.w.f.SetSheetName(source, to); err != nil {
		return "", fmt.Errorf("%s: invalid sheet name %q: %v", op.w.base, to, err)
	}
	return to, nil
}

func (op *sheetOp) delete() (string, error) {
	target, ok, err := op.source(op.opts.Sheet)
	if err != nil || !ok {
		return target, err
	}
	if len(op.w.sheets) == 1 {
		return "", fmt.Errorf("%s: cannot delete the only sheet %q", op.w.base, target)
	}
	if err := op.w.f.DeleteSheet(target); err != nil {
		return "", fmt.Errorf("%s: delete sheet %q: %v", op.w.base, target, err)
	}
	op.w.forget(target)
	return target, nil
}
