// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 6)
	assert.Equal(t, "write", dag.Steps[3].Commands[0].Command)
	assert.Equal(t, "append", dag.Steps[4].Commands[0].Command)
	assert.Equal(t, "update_rows", dag.Steps[5].Commands[0].Command)

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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
