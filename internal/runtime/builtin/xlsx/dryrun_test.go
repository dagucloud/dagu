// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dryRun(t *testing.T, workDir, op string, cfg map[string]any) error {
	t.Helper()
	step := ir.Step{
		Name:           "xlsx-step",
		Commands:       []ir.CommandEntry{{Command: op}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "xlsx-dry", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	return dryRunCheck(ctx, step)
}

func TestDryRunCheckPerOperation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"Invoice No", "Amount", "Status"}, []any{"INV-1", 10, "Done"})

	for name, tc := range map[string]struct {
		op   string
		cfg  map[string]any
		want string
	}{
		"read ok":                 {opRead, map[string]any{"path": "orders.xlsx", "columns": []any{"Amount"}, "types": map[string]any{"status": "string"}}, ""},
		"read missing workbook":   {opRead, map[string]any{"path": "none.xlsx"}, "field 'with.path': none.xlsx: workbook not found"},
		"read missing sheet":      {opRead, map[string]any{"path": "orders.xlsx", "sheet": "Nope"}, `field 'with.sheet': orders.xlsx: sheet "Nope" not found`},
		"read missing column":     {opRead, map[string]any{"path": "orders.xlsx", "columns": []any{"Nope"}}, `field 'with.columns': column "Nope" not found in header row 1`},
		"read where column":       {opRead, map[string]any{"path": "orders.xlsx", "where": map[string]any{"Nope": ""}}, `field 'with.where': column "Nope" not found`},
		"read header none":        {opRead, map[string]any{"path": "orders.xlsx", "header": false}, ""},
		"read types alias":        {opRead, map[string]any{"path": "orders.xlsx", "columns": []any{map[string]any{"Amount": "total"}}, "types": map[string]any{"total": "number"}}, `field 'with.types': column "total" not found`},
		"validate types alias":    {opValidate, map[string]any{"path": "orders.xlsx", "columns": []any{map[string]any{"Amount": "total"}}, "types": map[string]any{"total": "number"}}, ""},
		"convert types alias":     {opConvert, map[string]any{"path": "orders.xlsx", "output": "out.csv", "columns": []any{map[string]any{"Amount": "total"}}, "types": map[string]any{"total": "number"}}, `field 'with.types': column "total" not found`},
		"info missing":            {opInfo, map[string]any{"path": "none.xlsx"}, "field 'with.path'"},
		"list_sheets ok":          {opListSheets, map[string]any{"path": "orders.xlsx"}, ""},
		"validate rule columns":   {opValidate, map[string]any{"path": "orders.xlsx", "required": []any{"Nope"}, "unique": []any{"Invoice No"}}, `field 'with.required': column "Nope" not found`},
		"convert columns":         {opConvert, map[string]any{"path": "orders.xlsx", "output": "out.csv", "columns": []any{"Invoice"}}, `field 'with.columns': column "Invoice" not found in header row 1; headers present: Invoice No, Amount, Status`},
		"convert loose column":    {opConvert, map[string]any{"path": "orders.xlsx", "output": "out.csv", "columns": []any{"invoice no"}}, ""},
		"update_rows key":         {opUpdateRows, map[string]any{"path": "orders.xlsx", "key": "invoice no", "rows": "[]"}, `field 'with.key': column "invoice no" not found in header row 1; did you mean "Invoice No"?`},
		"update_rows set":         {opUpdateRows, map[string]any{"path": "orders.xlsx", "key": "_row", "rows": "[]", "set": map[string]any{"Nope": "x"}}, `field 'with.set': column "Nope" not found`},
		"update_rows ok":          {opUpdateRows, map[string]any{"path": "orders.xlsx", "key": "Invoice No", "rows": "[]", "set": map[string]any{"Status": "s"}}, ""},
		"write creates":           {opWrite, map[string]any{"path": "new.xlsx", "rows": "[]"}, ""},
		"write missing input":     {opWrite, map[string]any{"path": "new.xlsx", "input": "rows.csv"}, "field 'with.input': rows.csv: file not found"},
		"append present input":    {opAppend, map[string]any{"path": "new.xlsx", "input": "orders.xlsx"}, ""},
		"write_cells sheet":       {opWriteCells, map[string]any{"path": "orders.xlsx", "sheet": "Nope", "cells": map[string]any{"A1": 1}}, "field 'with.sheet'"},
		"write_cells ok":          {opWriteCells, map[string]any{"path": "orders.xlsx", "cells": map[string]any{"Sheet1!A1": 1, "B2": 2}}, ""},
		"write_cells address":     {opWriteCells, map[string]any{"path": "orders.xlsx", "cells": map[string]any{"Nope!A1": 1}}, `field 'with.sheet': orders.xlsx: sheet "Nope" not found`},
		"write_cells merge sheet": {opWriteCells, map[string]any{"path": "orders.xlsx", "cells": map[string]any{"A1": 1}, "merge": []any{"Nope!A1:B1"}}, `field 'with.sheet': orders.xlsx: sheet "Nope" not found`},
		"write_cells merge ok":    {opWriteCells, map[string]any{"path": "orders.xlsx", "cells": map[string]any{"A1": 1}, "merge": "Sheet1!A9:B9"}, ""},
		"sheet copy source":       {opSheet, map[string]any{"path": "orders.xlsx", "operation": "copy", "sheet": "Nope", "to": "X"}, "field 'with.sheet'"},
		"sheet copy missing skip": {opSheet, map[string]any{"path": "orders.xlsx", "operation": "copy", "sheet": "Nope", "to": "X", "missing": "skip"}, ""},
		"sheet add":               {opSheet, map[string]any{"path": "orders.xlsx", "operation": "add", "sheet": "Nope"}, ""},
		"sheet delete missing":    {opSheet, map[string]any{"path": "orders.xlsx", "operation": "delete", "sheet": "Nope"}, "field 'with.sheet'"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := dryRun(t, dir, tc.op, tc.cfg)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDryRunCheckSkipsReferencesAndJoinsProblems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx", []any{"Invoice No"}, []any{"INV-1"})

	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "${steps.find.outputs.path}", "columns": []any{"Nope"}}),
		"a path still holding a reference cannot be checked")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "columns": "${steps.find.outputs.headers}"}),
		"a column list still holding a reference is skipped")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "sheet": "${params.SHEET}"}),
		"a sheet still holding a reference is skipped")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "strip": true}),
		"a validation error is reported by validation, not the dry run")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "range": "${params.RANGE}", "columns": []any{"Nope"}}),
		"columns are not checked until the range that locates the header row resolves")
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "columns": []any{map[string]any{"Invoice No": "invoice"}}, "where": map[string]any{"invoice": "INV-1"}}),
		"a where key may be an alias given in columns")
	require.NoError(t, dryRun(t, dir, opSheet, map[string]any{"path": "orders.xlsx", "operation": "${params.OP}", "sheet": "Nope"}),
		"whether a source sheet is needed is unknown while the operation is a reference")
	require.NoError(t, dryRun(t, dir, opSheet, map[string]any{"path": "orders.xlsx", "operation": "delete", "sheet": "Nope", "missing": "${params.MISSING}"}),
		"and while the missing mode is a reference")
	err := dryRun(t, dir, opWriteCells, map[string]any{"path": "orders.xlsx", "cells": map[string]any{"Nope!A1": 1, "'Also Nope'!B2": 2}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `sheet "Also Nope" not found`)
	assert.Contains(t, err.Error(), `sheet "Nope" not found`, "every sheet an address names is checked")
	err = dryRun(t, dir, opRead, map[string]any{"path": "orders.xlsx", "range": "Nope!A1:B2"})
	require.ErrorContains(t, err, `field 'with.range': orders.xlsx: sheet "Nope" not found`)

	err = dryRun(t, dir, opUpdateRows, map[string]any{"path": "orders.xlsx", "key": "Nope", "rows": "[]", "set": map[string]any{"Also": "x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `field 'with.key': column "Nope" not found`)
	assert.Contains(t, err.Error(), `field 'with.set': column "Also" not found`)

	held := filepath.Join(dir, "held.xlsx")
	require.NoError(t, os.WriteFile(held, []byte("not a workbook"), 0o600))
	require.NoError(t, dryRun(t, dir, opRead, map[string]any{"path": "held.xlsx"}),
		"a workbook the dry run cannot open is left to the run")
}
