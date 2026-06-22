// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema_test

import (
	"encoding/json"
	"testing"

	dagschema "github.com/dagucloud/dagu/internal/cmn/schema"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDAGSchemaSpec018ParallelItems(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "flat mapping item is valid",
			spec: `
steps:
  - action: dag.run
    with:
      dag: child
    parallel:
      items:
        - name: alpha
          count: 2
          enabled: true
`,
		},
		{
			name: "nested mapping item value is invalid",
			spec: `
steps:
  - action: dag.run
    with:
      dag: child
    parallel:
      items:
        - nested:
            name: alpha
`,
			wantErr: "steps",
		},
		{
			name: "nested array item value is invalid",
			spec: `
steps:
  - action: dag.run
    with:
      dag: child
    parallel:
      items:
        - nested: [alpha]
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaSpec018Foreach(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "valid foreach body",
			spec: `
steps:
  - id: summarize
    foreach:
      items: [alpha, beta]
      as: episode
      key: ${foreach.episode}
      max_concurrent: 2
      steps:
        - id: write
          run: echo ${foreach.episode}
      collect:
        value: ${steps.write.stdout}
`,
		},
		{
			name: "requires steps",
			spec: `
steps:
  - id: summarize
    foreach:
      items: [alpha]
`,
			wantErr: "steps",
		},
		{
			name: "rejects reserved alias",
			spec: `
steps:
  - id: summarize
    foreach:
      items: [alpha]
      as: key
      steps:
        - run: echo ${foreach.key}
`,
			wantErr: "steps",
		},
		{
			name: "rejects unknown field",
			spec: `
steps:
  - id: summarize
    foreach:
      items: [alpha]
      mode: all
      steps:
        - run: echo ${foreach.item}
`,
			wantErr: "steps",
		},
		{
			name: "rejects non string collect value",
			spec: `
steps:
  - id: summarize
    foreach:
      items: [alpha]
      steps:
        - run: echo ${foreach.item}
      collect:
        value: 1
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func mustResolveDAGSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(dagschema.DAGSchemaJSON, &schema))

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	return resolved
}

func mustParseYAMLDocument(t *testing.T, spec string) any {
	t.Helper()

	var doc any
	require.NoError(t, yaml.Unmarshal([]byte(spec), &doc))
	return doc
}
