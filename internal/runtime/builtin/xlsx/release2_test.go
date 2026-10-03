// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/japanese"
)

func TestWriteCellsFillsATemplateCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "template.xlsx", []any{"Customer", ""}, []any{"Date", ""}, []any{"Total", ""})
	tw, err := newTestWriter(t, dir, opWriteCells, map[string]any{
		"path":   "template.xlsx",
		"output": "out/invoice.xlsx",
		"cells": map[string]any{
			"B1": "Acme",
			"B2": "2026-10-01",
			"B3": map[string]any{"formula": "=SUM(B1:B2)"},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "out"), 0o750))
	require.NoError(t, tw.exec.Run(context.Background()))

	outputs := tw.exec.GetOutputs()
	assert.Equal(t, filepath.Join(dir, "out", "invoice.xlsx"), outputs["path"])
	assert.Equal(t, "Sheet1", outputs["sheet"])
	changes := outputs["changes"].(workbook.Changes)
	assert.Equal(t, 3, changes.CellsChanged)
	assert.Equal(t, "Sheet1!B1:B3", changes.Range)
	assert.Equal(t, false, outputs["dry_run"])
	assert.Equal(t, "Wrote 3 cells to invoice.xlsx Sheet1\n", tw.stdout.String())
	filled, err := workbook.Read(context.Background(), filepath.Join(dir, "out", "invoice.xlsx"), workbook.ReadOptions{Header: workbook.HeaderSpec{Mode: workbook.HeaderNone}})
	require.NoError(t, err)
	assert.Equal(t, "Acme", filled.Rows[0]["B"])
	assert.Equal(t, "2026-10-01", filled.Rows[1]["B"])
	template, err := workbook.Read(context.Background(), filepath.Join(dir, "template.xlsx"), workbook.ReadOptions{Header: workbook.HeaderSpec{Mode: workbook.HeaderNone}})
	require.NoError(t, err)
	assert.Nil(t, template.Rows[0]["B"], "the template is untouched")

	dry, err := newTestWriter(t, dir, opWriteCells, map[string]any{"path": "template.xlsx", "cells": map[string]any{"B1": "x"}, "dry_run": true})
	require.NoError(t, err)
	require.NoError(t, dry.exec.Run(context.Background()))
	assert.Equal(t, "Wrote 1 cell to template.xlsx Sheet1 (dry run)\n", dry.stdout.String())

	merged, err := newTestWriter(t, dir, opWriteCells, map[string]any{"path": "template.xlsx", "cells": map[string]any{"A7": "Notes"}, "merge": []any{"A7:C7"}})
	require.NoError(t, err)
	require.NoError(t, merged.exec.Run(context.Background()))
	assert.Equal(t, "Wrote 1 cell to template.xlsx Sheet1 (1 range merged)\n", merged.stdout.String())
	assert.Equal(t, 1, merged.exec.GetOutputs()["changes"].(workbook.Changes).Merged)

	missing, err := newTestWriter(t, dir, opWriteCells, map[string]any{"path": "none.xlsx", "cells": map[string]any{"B1": "x"}})
	require.NoError(t, err)
	err = missing.exec.Run(context.Background())
	require.ErrorContains(t, err, "workbook not found")
	assert.Equal(t, 1, missing.exec.ExitCode())
}

func TestSheetOperationsPublishSheets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "book.xlsx", []any{"a"}, []any{1})

	copied, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "copy", "sheet": "Sheet1", "to": "October"})
	require.NoError(t, err)
	require.NoError(t, copied.exec.Run(context.Background()))
	outputs := copied.exec.GetOutputs()
	assert.Equal(t, []string{"Sheet1", "October"}, outputs["sheets"])
	assert.Equal(t, "October", outputs["sheet"])
	assert.Equal(t, "Copied sheet \"Sheet1\" to \"October\" in book.xlsx\n", copied.stdout.String())

	skipped, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "add", "sheet": "october", "if_exists": "skip"})
	require.NoError(t, err)
	require.NoError(t, skipped.exec.Run(context.Background()))
	assert.Equal(t, "Sheet \"October\" left as it is in book.xlsx; skipped\n", skipped.stdout.String())
	assert.Equal(t, "warning: sheet \"October\" already exists; nothing added\n", skipped.stderr.String())

	renamed, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "rename", "sheet": "October", "to": "Archive", "position": 1})
	require.ErrorContains(t, err, "position is only valid for add and copy")
	renamed, err = newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "rename", "sheet": "October", "to": "Archive"})
	require.NoError(t, err)
	require.NoError(t, renamed.exec.Run(context.Background()))
	assert.Equal(t, "Renamed sheet \"October\" to \"Archive\" in book.xlsx\n", renamed.stdout.String())

	deleted, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "delete", "sheet": "Archive", "dry_run": true})
	require.NoError(t, err)
	require.NoError(t, deleted.exec.Run(context.Background()))
	assert.Equal(t, "Deleted sheet \"Archive\" from book.xlsx (dry run)\n", deleted.stdout.String())
	assert.Equal(t, []string{"Sheet1"}, deleted.exec.GetOutputs()["sheets"])
	sheets, err := workbook.ListSheets(filepath.Join(dir, "book.xlsx"), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Sheet1", "Archive"}, sheets, "a dry run changes nothing")

	last, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "delete", "sheet": "Sheet1", "missing": "skip"})
	require.NoError(t, err)
	require.NoError(t, last.exec.Run(context.Background()))
	only, err := newTestWriter(t, dir, opSheet, map[string]any{"path": "book.xlsx", "operation": "delete", "sheet": "Archive"})
	require.NoError(t, err)
	require.ErrorContains(t, only.exec.Run(context.Background()), `cannot delete the only sheet "Archive"`)
}

func TestConvertWritesAFileAndKeepsItAsArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifacts := filepath.Join(dir, "artifacts")
	writeBook(t, dir, "orders.xlsx", []any{"品名", "Qty"}, []any{"りんご", 3})
	step := map[string]any{"path": "orders.xlsx", "output": "export/orders.csv", "encoding": "cp932", "delimiter": ";", "artifact": true}
	tw, err := newTestWriterWithArtifacts(t, dir, artifacts, opConvert, step)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "export"), 0o750))
	require.NoError(t, tw.exec.Run(context.Background()))

	outputs := tw.exec.GetOutputs()
	assert.Equal(t, filepath.Join(dir, "export", "orders.csv"), outputs["path"])
	assert.Equal(t, "csv", outputs["format"])
	assert.Equal(t, 1, outputs["count"])
	assert.Equal(t, "Sheet1!A1:B2", outputs["range"])
	assert.Equal(t, "Converted 1 row from orders.xlsx Sheet1!A1:B2 to orders.csv\n", tw.stdout.String())
	raw, err := os.ReadFile(filepath.Join(dir, "export", "orders.csv"))
	require.NoError(t, err)
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "品名;Qty\nりんご;3\n", string(decoded))
	artifact, ok := outputs["artifact"].(string)
	require.True(t, ok, "the converted file is the artifact")
	assert.Equal(t, filepath.ToSlash(filepath.Join("xlsx", "xlsx-write", "orders.csv")), filepath.ToSlash(artifact))
	kept, err := os.ReadFile(filepath.Join(artifacts, filepath.FromSlash(artifact)))
	require.NoError(t, err)
	assert.Equal(t, raw, kept)
}

// newTestWriterWithArtifacts is newTestWriter with the run artifacts
// directory set, so artifact: true has somewhere to copy to.
func newTestWriterWithArtifacts(t *testing.T, workDir, artifacts, op string, cfg map[string]any) (*testWriter, error) {
	t.Helper()
	step := ir.Step{
		Name:           "xlsx-write",
		Commands:       []ir.CommandEntry{{Command: op}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	dag := &ir.DAG{Name: "xlsx-test", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	env := runtime.NewEnv(ctx, step)
	env.Scope = value.NewEnvScope(nil, false).WithEntry(runenv.EnvKeyDAGRunArtifactsDir, artifacts, value.EnvSourceDAGEnv)
	ctx = runtime.WithEnv(ctx, env)
	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	write, ok := exec.(*writeExecutor)
	require.True(t, ok)
	tw := &testWriter{exec: write, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	write.SetStdout(tw.stdout)
	write.SetStderr(tw.stderr)
	return tw, nil
}
