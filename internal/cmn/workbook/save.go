// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

// Changes summarizes what a write did, or with dry run would do.
type Changes struct {
	Sheet        string `json:"sheet"`
	Range        string `json:"range"`
	RowsUpdated  int    `json:"rows_updated"`
	RowsAppended int    `json:"rows_appended"`
	ColumnsAdded int    `json:"columns_added"`
	CellsChanged int    `json:"cells_changed"`
	// Merged counts the ranges write_cells newly merged.
	Merged int `json:"merged,omitempty"`
}

// WriteResult is what a writer publishes.
type WriteResult struct {
	Path     string   `json:"path"`
	Sheet    string   `json:"sheet"`
	Changes  Changes  `json:"changes"`
	DryRun   bool     `json:"dry_run"`
	Warnings []string `json:"warnings"`
	// Artifact is the copy kept with the run, relative to its artifacts
	// directory, when the caller asked for one.
	Artifact string `json:"artifact,omitempty"`
}

// openOrCreate opens an existing workbook or starts a new one whose first
// sheet is named sheet (Sheet1 when empty). created reports which.
func openOrCreate(path, password, sheet string) (w *file, created bool, err error) {
	if err := CheckExtension(path); err != nil {
		return nil, false, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		w, err := open(path, password)
		return w, false, err
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, false, classifyError(path, statErr)
	}
	f := excelize.NewFile()
	if sheet != "" && sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			_ = f.Close()
			return nil, false, fmt.Errorf("%s: invalid sheet name %q: %v", filepath.Base(path), sheet, err)
		}
	}
	w = &file{f: f, path: path, base: filepath.Base(path), kinds: map[int]cellKind{}, grids: map[string][][]string{}}
	w.sheets = f.GetSheetList()
	return w, true, nil
}

// save writes the workbook to its path. Atomic saves go through a short
// temporary name in the same directory that is renamed over the target, so
// a crash never leaves a half-written workbook; the target's permission
// bits are kept, and a symbolic link is followed so the workbook it points
// to is replaced rather than the link. A sharing violation, or a rename
// another program's hold on the workbook refuses, becomes LockedError.
func (w *file) save(inPlace bool) error {
	if inPlace {
		return classifyError(w.path, w.f.SaveAs(w.path))
	}
	// excelize chooses the container format from the extension, so the
	// temporary name keeps it.
	return replaceAtomically(w.path, filepath.Ext(w.base), func(tmp string) error {
		return w.f.SaveAs(tmp)
	})
}

// replaceAtomically fills a short temporary name beside target and renames
// it over target, keeping the target's permission bits and following a
// symbolic link so the file it points to is replaced. The temporary name
// keeps ext, and stays short so a long name near the filesystem limit still
// gets a valid temporary name.
func replaceAtomically(path, ext string, fill func(tmp string) error) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(target), ".dagu-"+hex.EncodeToString(suffix[:])+ext)
	if err := fill(tmp); err != nil {
		_ = os.Remove(tmp)
		return classifyError(path, err)
	}
	if info, err := os.Stat(target); err == nil {
		_ = os.Chmod(tmp, info.Mode().Perm())
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return classifyRenameError(path, target, err)
	}
	return nil
}

// withLock runs a whole open-modify-save sequence, retrying while the
// workbook is held by another program and the lock options allow.
func withLock[T any](ctx context.Context, path string, opts LockOptions, attempt func() (*T, error)) (*T, error) {
	var result *T
	err := withLockRetry(ctx, path, opts, func() error {
		var err error
		result, err = attempt()
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// writeFileAtomic writes a file through write. With inPlace the target is
// written directly; otherwise write fills a temporary file in the target's
// directory that is renamed over the target the way save does.
func writeFileAtomic(target string, inPlace bool, write func(io.Writer) error) error {
	fill := func(name string) error {
		f, err := os.Create(name) //nolint:gosec // the path is the output file the step names
		if err != nil {
			return err
		}
		if err := write(f); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
	if inPlace {
		return classifyError(target, fill(target))
	}
	// A text file needs no extension on its temporary name, and a long
	// one would push the name past what the filesystem allows.
	return replaceAtomically(target, "", fill)
}
