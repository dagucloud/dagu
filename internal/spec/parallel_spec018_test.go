// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestParallelSpec018Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		parallel  string
		wantParts []string
	}{
		{
			name: "rejects unknown object field",
			parallel: `
      items: [one]
      unexpected: value`,
			wantParts: []string{"parallel", "unexpected"},
		},
		{
			name: "rejects max_concurrent above limit",
			parallel: `
      items: [one]
      max_concurrent: 1001`,
			wantParts: []string{"parallel.max_concurrent", "1000"},
		},
		{
			name: "rejects non integer max_concurrent",
			parallel: `
      items: [one]
      max_concurrent: 1.5`,
			wantParts: []string{"parallel.max_concurrent", "integer"},
		},
		{
			name: "rejects unknown separator",
			parallel: `
      items: ${ITEMS}
      separator: colon`,
			wantParts: []string{"parallel.separator", "newline", "comma", "semicolon", "pipe", "tab", "space"},
		},
		{
			name: "rejects non string separator",
			parallel: `
      items: ${ITEMS}
      separator: 1`,
			wantParts: []string{"parallel.separator", "string"},
		},
		{
			name: "rejects separator with static items",
			parallel: `
      items: [one]
      separator: newline`,
			wantParts: []string{"parallel.separator", "string item source"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
steps:
  - name: fanout
    action: dag.run
    with:
      dag: child
    parallel:`+tt.parallel+`

---

name: child
steps:
  - name: ok
    run: "true"
`)), spec.SkipSchemaValidation(), spec.WithoutEval())
			require.Error(t, err)
			for _, part := range tt.wantParts {
				require.Contains(t, err.Error(), part)
			}
		})
	}
}

func TestParallelSpec018Separator(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
steps:
  - name: fanout
    action: dag.run
    with:
      dag: child
    parallel:
      items: ${ITEMS}
      separator: Newline

---

name: child
steps:
  - name: ok
    run: "true"
`)), spec.SkipSchemaValidation(), spec.WithoutEval())
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[0].Parallel)
	require.Equal(t, "${ITEMS}", dag.Steps[0].Parallel.Variable)
	require.Equal(t, "newline", dag.Steps[0].Parallel.Separator)
}
