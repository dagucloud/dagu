// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestXlsxReadActions(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - id: read
    action: xlsx.read
    with:
      path: orders.xlsx
      sheet: Orders
      range: A1:H
      header: [1, 2]
      columns: [Status, {Invoice No: invoice_no}]
      types: {Amount: number}
      where: {Status: ""}
      max_rows: 100
  - id: info
    action: xlsx.info
    with:
      path: orders.xlsx
  - id: sheets
    action: xlsx.list_sheets
    with:
      path: ${params.BOOK}
  - id: write
    action: xlsx.write
    with:
      path: report.xlsx
      rows: ${steps.read.outputs.rows}
      columns: ${steps.read.outputs.headers}
      wait_for_unlock: 5m
  - id: append
    action: xlsx.append
    with:
      path: report.xlsx
      input: more.csv
  - id: mark
    action: xlsx.update_rows
    with:
      path: orders.xlsx
      key: Invoice No
      rows: ${steps.each.outputs.results}
      set: {Status: status}
      missing: skip
  - id: check
    action: xlsx.validate
    with:
      path: orders.xlsx
      required: [Invoice No]
      on_problem: fail
  - id: fill
    action: xlsx.write_cells
    with:
      path: template.xlsx
      output: invoice.xlsx
      cells: {B2: Acme, Total: {formula: SUM(E2:E9)}}
  - id: month
    action: xlsx.sheet
    with:
      path: report.xlsx
      operation: copy
      sheet: Template
      to: ${params.MONTH}
  - id: export
    action: xlsx.convert
    with:
      path: orders.xlsx
      output: orders.csv
      encoding: cp932
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 10)
	assert.Equal(t, "write", dag.Steps[3].Commands[0].Command)
	assert.Equal(t, "append", dag.Steps[4].Commands[0].Command)
	assert.Equal(t, "update_rows", dag.Steps[5].Commands[0].Command)
	assert.Equal(t, "validate", dag.Steps[6].Commands[0].Command)
	assert.Equal(t, "write_cells", dag.Steps[7].Commands[0].Command)
	assert.Equal(t, "sheet", dag.Steps[8].Commands[0].Command)
	assert.Equal(t, "convert", dag.Steps[9].Commands[0].Command)
	assert.Equal(t, "invoice.xlsx", dag.Steps[7].ExecutorConfig.Config["output"])

	read := dag.Steps[0]
	assert.Equal(t, "xlsx", read.ExecutorConfig.Type)
	require.Len(t, read.Commands, 1)
	assert.Equal(t, "read", read.Commands[0].Command)
	assert.Equal(t, "orders.xlsx", read.ExecutorConfig.Config["path"])
	assert.Equal(t, "Orders", read.ExecutorConfig.Config["sheet"])
	assert.Equal(t, "info", dag.Steps[1].Commands[0].Command)
	assert.Equal(t, "list_sheets", dag.Steps[2].Commands[0].Command)
}

func TestXlsxArtifactEnablesArtifacts(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - action: xlsx.write
    with:
      path: report.xlsx
      rows: "[]"
      artifact: true
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled)

	plain, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - action: xlsx.write
    with:
      path: report.xlsx
      rows: "[]"
`))
	require.NoError(t, err)
	assert.Nil(t, plain.Artifacts)

	// A value only known at run time may resolve to true, so storage is
	// enabled rather than failing the step later.
	referenced, err := spec.LoadYAML(context.Background(), []byte(`
params:
  - KEEP: "false"
steps:
  - action: xlsx.write
    with:
      path: report.xlsx
      rows: "[]"
      artifact: ${params.KEEP}
`))
	require.NoError(t, err)
	require.NotNil(t, referenced.Artifacts)
	assert.True(t, referenced.Artifacts.Enabled)
}

// A reference in one set literal defers set to the run; a set without one
// is still checked at load.
func TestXlsxUpdateRowsSetReference(t *testing.T) {
	t.Parallel()

	const yaml = `
steps:
  - id: reg
    run: echo 1
    output:
      ticket: {from: stdout}
  - id: mark
    depends: [reg]
    action: xlsx.update_rows
    with:
      path: orders.xlsx
      key: _row
      rows: '[{"_row": 2}]'
      set:
        Checked: {value: done}
        Status: %s
`
	_, err := spec.LoadYAML(context.Background(), []byte(fmt.Sprintf(yaml, `{value: "${steps.reg.outputs.ticket}"}`)))
	require.NoError(t, err)

	_, err = spec.LoadYAML(context.Background(), []byte(fmt.Sprintf(yaml, `{value: done, extra: 1}`)))
	require.ErrorContains(t, err, "set.Status: use a field name or {value: literal}")
}

func TestXlsxReadActionsRejectInvalidConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing path",
			yaml: "steps:\n  - action: xlsx.read\n    with:\n      sheet: Orders\n",
			want: "path is required for read",
		},
		{
			name: "unknown field",
			yaml: "steps:\n  - action: xlsx.read\n    with:\n      path: a.xlsx\n      strip: true\n",
			want: "strip",
		},
		{
			name: "fixed outputs",
			yaml: "steps:\n  - id: r\n    action: xlsx.read\n    output: ROWS\n    with:\n      path: a.xlsx\n",
			want: "xlsx actions have fixed outputs",
		},
		{
			name: "bad merged",
			yaml: "steps:\n  - action: xlsx.read\n    with:\n      path: a.xlsx\n      merged: middle\n",
			want: "middle does not equal any of: [fill first]",
		},
		{
			name: "read field on info",
			yaml: "steps:\n  - action: xlsx.info\n    with:\n      path: a.xlsx\n      range: A1:B2\n",
			want: "with.range is not valid for xlsx.info",
		},
		{
			name: "validate without rules",
			yaml: "steps:\n  - action: xlsx.validate\n    with:\n      path: a.xlsx\n",
			want: "validate requires at least one of with.required, with.not_blank, with.unique, with.types, or with.allowed",
		},
		{
			name: "sheet with unknown operation",
			yaml: "steps:\n  - action: xlsx.sheet\n    with:\n      path: a.xlsx\n      operation: move\n      sheet: A\n",
			want: "move does not equal any of: [add copy rename delete]",
		},
		{
			name: "write_cells without cells",
			yaml: "steps:\n  - action: xlsx.write_cells\n    with:\n      path: a.xlsx\n",
			want: "write_cells requires with.cells",
		},
		{
			name: "convert without output",
			yaml: "steps:\n  - action: xlsx.convert\n    with:\n      path: a.xlsx\n",
			want: "convert requires with.output",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestXlsxExtractAction(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
llm:
  provider: anthropic
  model: claude-sonnet-5
steps:
  - id: fields
    action: xlsx.extract
    with:
      path: inbox/quote.xlsx
      sheet: Sheet1
      instruction: Find the quote number and the total
      schema:
        type: object
        properties:
          quote_no: {type: string, description: 見積番号}
          total: {type: number, description: 合計金額}
      send_values: false
  - id: own_model
    action: xlsx.extract
    with:
      path: inbox/quote.xlsx
      instruction: Find the total
      schema:
        type: object
      llm:
        provider: openai
        model: gpt-5
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 2)

	fields := dag.Steps[0]
	assert.Equal(t, ir.ExecutorTypeXlsx, fields.ExecutorConfig.Type)
	require.Len(t, fields.Commands, 1)
	assert.Equal(t, "extract", fields.Commands[0].Command)
	assert.Equal(t, "Find the quote number and the total", fields.ExecutorConfig.Config["instruction"])
	assert.Equal(t, false, fields.ExecutorConfig.Config["send_values"])
	assert.NotContains(t, fields.ExecutorConfig.Config, "llm")
	require.NotNil(t, fields.LLM, "the DAG llm block is inherited")
	assert.Equal(t, "claude-sonnet-5", fields.LLM.Model)
	assert.ElementsMatch(t, []ir.StepOutputDeclaration{
		{Name: "quote_no", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "total", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "cells", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "sheet", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "source", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "warnings", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
	}, fields.Outputs)

	own := dag.Steps[1]
	assert.NotContains(t, own.ExecutorConfig.Config, "llm", "with.llm moves to the step")
	require.NotNil(t, own.LLM)
	assert.Equal(t, "gpt-5", own.LLM.Model, "with.llm replaces the DAG llm block")
	assert.ElementsMatch(t, []ir.StepOutputDeclaration{
		{Name: "cells", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "sheet", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "source", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "warnings", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
	}, own.Outputs, "a schema without properties still declares the fixed outputs")
}

func TestXlsxExtractActionRejections(t *testing.T) {
	t.Parallel()
	const source = `
llm:
  provider: anthropic
  model: claude-sonnet-5
steps:
  - id: fields
    action: xlsx.extract
    with:
      path: inbox/quote.xlsx
      instruction: Find the total
      schema:
        type: object
        properties:
          total: {type: number}
`
	for _, tc := range []struct{ name, from, to, message string }{
		{"authored output", "    with:\n      path: inbox/quote.xlsx", "    output: FIELDS\n    with:\n      path: inbox/quote.xlsx", "xlsx actions have fixed outputs"},
		{"property named like a fixed output", "total: {type: number}", "source: {type: string}", `schema property "source" collides with an output of xlsx.extract`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, source, tc.from)
			_, err := spec.LoadYAML(context.Background(), []byte(strings.Replace(source, tc.from, tc.to, 1)))
			require.ErrorContains(t, err, tc.message)
		})
	}
}
