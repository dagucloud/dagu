// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYAMLExposesOutputValueReferenceFields(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
name: output-fields
params:
  - name: environment
    default: prod
steps:
  - name: produce
    run: "echo ok"
    stdout:
      outputs:
        fields:
          status:
            value: "${params.environment}"
    output:
      result:
        value: "${params.environment}"
      report:
        from: file
        path: "outputs/${params.environment}/report.txt"
`)), spec.WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	fields := core.ReferenceFields(dag)
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field.Path] = field.Value
	}

	assert.Equal(t, "${params.environment}", values["steps[0].stdout.outputs.fields.status.value"])
	assert.Equal(t, "${params.environment}", values["steps[0].output.result.value"])
	assert.Equal(t, "outputs/${params.environment}/report.txt", values["steps[0].output.report.path"])
}

func TestLoadYAMLPreservesUndeclaredOutputValueReferences(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
name: output-fields
params:
  - name: environment
    default: prod
steps:
  - name: produce
    run: "echo ok"
    stdout:
      outputs:
        fields:
          status:
            value: "${params.missing}"
    output:
      result:
        value: "${params.missing}"
      report:
        from: file
        path: "outputs/${params.missing}/report.txt"
`)), spec.WithoutEval())
	require.NoError(t, err)
	assert.Empty(t, dag.BuildWarnings)
}
