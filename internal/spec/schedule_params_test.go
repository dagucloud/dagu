// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestScheduleParams(t *testing.T) {
	t.Parallel()
	dag, err := spec.LoadYAML(t.Context(), []byte(`
name: report
params:
  - name: mode
    default: default
  - name: number
    type: integer
    default: 0
schedule:
  - expression: "0 5 * * *"
    params:
      mode: y
  - expression: "0 6 * * *"
    params:
      mode: '${HOME}'
      number: 9007199254740993
steps:
  - run: echo report
`), spec.WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.Schedule, 2)
	require.Equal(t, `{"mode":"y"}`, string(dag.Schedule[0].Params))
	encoded, err := json.Marshal(dag.Schedule[1])
	require.NoError(t, err)
	var restored ir.Schedule
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, `{"mode":"${HOME}","number":9007199254740993}`, string(restored.Params))
	require.Equal(t, dag.Schedule[1].Fingerprint(), restored.Fingerprint())

	run, err := spec.ResolveRuntimeParams(t.Context(), dag, string(restored.Params), spec.ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	require.Contains(t, run.Params, "mode=${HOME}")
	require.Contains(t, run.Params, "number=9007199254740993")
	require.Contains(t, dag.Params, "mode=default")
}

func TestScheduleParamsValidation(t *testing.T) {
	t.Parallel()
	const workflow = `name: report
params:
  - name: count
    type: integer
    default: 1
steps:
  - run: echo report
`
	for _, tc := range []struct {
		schedule string
		want     string
	}{
		{"schedule:\n  - expression: '0 5 * * *'\n    params: {count: invalid}\n", "schedule[0].params"},
		{"schedule:\n  - expression: '0 5 * * *'\n    params: invalid\n", "mapping of named overrides"},
		{"schedule:\n  stop:\n    expression: '0 5 * * *'\n    params: {count: 2}\n", "only supported for start schedules"},
	} {
		_, err := spec.LoadYAML(t.Context(), []byte(workflow+tc.schedule), spec.WithoutEval())
		require.ErrorContains(t, err, tc.want)
	}
}
