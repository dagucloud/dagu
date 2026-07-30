// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func loadRawDAG(t *testing.T, yaml string) *core.DAG {
	t.Helper()
	dag, err := spec.LoadYAML(t.Context(), []byte(yaml), spec.WithoutEval())
	require.NoError(t, err)
	return dag
}

func TestResolveChildRunParamsSnapshot(t *testing.T) {
	t.Setenv("SELECTED_FACILITY", "serverA")

	child := loadRawDAG(t, `
name: child
params:
  - name: FACILITY
    eval: "$SELECTED_FACILITY"
worker_selector:
  host: ${FACILITY}
steps:
  - name: step
    command: echo child
`)
	params, err := resolveChildRunParams(t.Context(), child, executor.RunParams{})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"host": "serverA"}, params.WorkerSelector)
	require.Equal(t, `FACILITY="serverA"`, params.Params)
}

func TestResolveChildRunParamsError(t *testing.T) {
	t.Parallel()

	child := loadRawDAG(t, `
name: child
params:
  - name: FACILITY
    type: string
    required: true
worker_selector:
  host: ${FACILITY}
steps:
  - name: step
    command: echo child
`)
	_, err := resolveChildRunParams(t.Context(), child, executor.RunParams{})
	require.ErrorContains(t, err, "FACILITY")
}
