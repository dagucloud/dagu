// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/require"
)

func TestValidateOutputReferencesFromOutputSchema(t *testing.T) {
	t.Parallel()

	t.Run("RejectsUnknownFieldForClosedOutputSchema", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-closed-schema
type: graph
steps:
  - id: classify
    run: echo '{"category":"bug"}'
    output_schema:
      type: object
      additionalProperties: false
      properties:
        category:
          type: string
  - id: route
    depends: [classify]
    run: echo ${classify.output.priority}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
		requireBuildWarningContains(t, dag.BuildWarnings, `route`, `${classify.output.priority}`, `priority`)
	})

	t.Run("AllowsUnknownFieldForOpenOutputSchema", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-open-schema
type: graph
steps:
  - id: classify
    run: echo '{"category":"bug","priority":"high"}'
    output_schema:
      type: object
      properties:
        category:
          type: string
  - id: route
    depends: [classify]
    run: echo ${classify.output.priority}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
	})

	t.Run("AllowsOptionalKnownFieldForClosedOutputSchema", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-optional-field
type: graph
steps:
  - id: classify
    run: echo '{"category":"bug"}'
    output_schema:
      type: object
      additionalProperties: false
      properties:
        category:
          type: string
  - id: route
    depends: [classify]
    run: echo ${classify.output.category}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
	})

	t.Run("AllowsReferenceWithUnconstrainedExplicitOutputSchema", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-empty-schema

type: graph
steps:
  - id: classify
    run: echo '{"category":"bug"}'
    output_schema: {}
  - id: route
    depends: [classify]
    run: echo ${classify.output.category}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
	})
}

func TestValidateOutputReferencesFromStructuredOutputMapping(t *testing.T) {
	t.Parallel()

	t.Run("RejectsUnknownFieldForObjectFormOutput", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-mapping
type: graph
steps:
  - id: build
    run: echo '{"version":"v1.2.3"}'
    output:
      version:
        from: stdout
        decode: json
        select: .version
  - id: publish
    depends: [build]
    run: echo ${build.output.artifact}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
		requireBuildWarningContains(t, dag.BuildWarnings, `publish`, `${build.output.artifact}`, `version`)
	})

	t.Run("AllowsKnownFieldForObjectFormOutput", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `name: output-ref-mapping-known
type: graph
steps:
  - id: build
    run: echo '{"version":"v1.2.3"}'
    output:
      version:
        from: stdout
        decode: json
        select: .version
  - id: publish
    depends: [build]
    run: echo ${build.output.version}
`)

		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())
	})
}

func requireBuildWarningContains(t *testing.T, warnings []string, parts ...string) {
	t.Helper()

	for _, warning := range warnings {
		matched := true
		for _, part := range parts {
			if !strings.Contains(warning, part) {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}

	require.Failf(t, "missing warning", "expected one warning to contain %v\nwarnings:\n%s", parts, strings.Join(warnings, "\n"))
}
