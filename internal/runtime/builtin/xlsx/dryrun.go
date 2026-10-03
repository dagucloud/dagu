// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// dryRunCheck is the xlsx executor's dry-run check: it reports a workbook
// that does not exist, a sheet that is not in it, or header columns a step
// names that are not in the header row. A with field whose value is still
// a reference, such as a step output, is skipped; so is a problem the dry
// run cannot judge, such as a locked workbook. Validation errors are not
// repeated here, since dagu dry reports them before any step is checked.
func dryRunCheck(ctx context.Context, step ir.Step) error {
	cfg, op, err := loadConfig(step, true)
	if err != nil || cfg.deferred["path"] {
		return nil
	}
	workDir := runtime.GetEnv(ctx).WorkingDir
	path, err := resolvePath(workDir, cfg.Path)
	if err != nil {
		return nil
	}
	switch op {
	case opWrite, opAppend:
		// The workbook may be created; only the input file can be checked.
		if !cfg.provided("input") {
			return nil
		}
		input, err := resolvePath(workDir, cfg.Input)
		if err != nil {
			return nil
		}
		if _, err := os.Stat(input); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("field 'with.input': %s: file not found", workbook.Base(input))
		}
		return nil
	case opInfo, opListSheets:
		return workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password})
	case opExtract:
		// The workbook and the sheet can be checked; which cells hold the
		// fields is for the model at run time.
		opts := workbook.CheckOptions{Password: cfg.Password}
		if !cfg.deferred["sheet"] {
			opts.Sheet = cfg.Sheet
		}
		if !cfg.deferred["range"] {
			opts.Range = cfg.Range
		}
		return workbook.Check(ctx, path, opts)
	case opSheet:
		// Whether a source sheet is needed depends on the operation and
		// the missing mode; while either is still a reference, only the
		// workbook can be checked.
		operation := strings.ToLower(strings.TrimSpace(cfg.Operation))
		needsSource := operation != string(workbook.SheetAdd) && cfg.Missing != string(workbook.MissingSkip)
		if !needsSource || cfg.deferred["operation"] || cfg.deferred["missing"] || cfg.deferred["sheet"] {
			return workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password})
		}
		return workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password, Sheet: cfg.Sheet})
	case opWriteCells:
		// The default sheet and every sheet an address names must exist.
		var problems []error
		if !cfg.deferred["sheet"] {
			problems = append(problems, workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password, Sheet: cfg.Sheet}))
		}
		for _, name := range addressedSheets(cfg.cells) {
			problems = append(problems, workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password, Sheet: name}))
		}
		if len(problems) == 0 {
			return workbook.Check(ctx, path, workbook.CheckOptions{Password: cfg.Password})
		}
		return errors.Join(problems...)
	}
	opts := workbook.CheckOptions{Password: cfg.Password, Header: cfg.header}
	if !cfg.deferred["sheet"] {
		opts.Sheet = cfg.Sheet
	}
	if !cfg.deferred["range"] {
		opts.Range = cfg.Range
	}
	// Columns are looked up in the header row, which only the resolved
	// sheet, range, and header locate.
	located := !cfg.deferred["sheet"] && !cfg.deferred["range"] && !cfg.deferred["header"]
	// update_rows resolves its key and set columns exactly, as the run
	// does; the reading operations accept a loose match. aliased says
	// whether a name may be an alias given in columns, which where and the
	// validation rules allow while the types of a read or convert, like
	// columns itself, name headers of the sheet.
	add := func(field string, aliased bool, names ...string) {
		if !located {
			return
		}
		exact := op == opUpdateRows
		for _, name := range names {
			if aliased {
				name = cfg.sourceOf(name)
			}
			opts.Columns = append(opts.Columns, workbook.ColumnCheck{Field: field, Name: name, Exact: exact})
		}
	}
	switch op {
	case opRead, opValidate, opConvert:
		for _, sel := range cfg.columns {
			add("columns", false, sel.Source)
		}
		add("types", op == opValidate, sortedNames(cfg.types)...)
		add("where", true, sortedNames(cfg.Where)...)
		add("required", true, cfg.Required...)
		add("not_blank", true, cfg.NotBlank...)
		add("unique", true, cfg.Unique...)
		add("allowed", true, sortedNames(cfg.allowed)...)
	case opUpdateRows:
		if key := strings.TrimSpace(cfg.Key); key != "" && key != workbook.RowNumberKey {
			add("key", false, key)
		}
		add("set", false, sortedNames(cfg.set)...)
	}
	return workbook.Check(ctx, path, opts)
}

// sourceOf maps a name a rule uses to the header it refers to when it is
// an alias given in columns; any other name is returned as it is.
func (cfg config) sourceOf(name string) string {
	for _, sel := range cfg.columns {
		if sel.As == name {
			return sel.Source
		}
	}
	return name
}

// addressedSheets lists the sheets the cell addresses name, once each and
// in order, so a dry run can check them the way the run would.
func addressedSheets(cells map[string]workbook.CellValue) []string {
	seen := map[string]bool{}
	var names []string
	for addr := range cells {
		i := strings.LastIndex(addr, "!")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(addr[:i])
		if len(name) >= 2 && strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'") {
			name = strings.ReplaceAll(name[1:len(name)-1], "''", "'")
		}
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// sortedNames returns a map's keys in order, so warnings read the same
// from run to run.
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
