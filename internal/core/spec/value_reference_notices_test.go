// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYAMLWithResultReturnsValueReferenceNotices(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
name: notices
consts:
  - image: ${consts.missing}
steps:
  - run: echo ok
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	require.Len(t, result.ValueReferenceNotices, 1)

	got := result.ValueReferenceNotices[0]
	assert.Equal(t, "consts.image", got.FieldPath)
	assert.Equal(t, "${consts.missing}", got.Token)
	assert.Contains(t, got.Message, "was left unchanged")
}

func TestLoadYAMLWithResultInspectsWorkflowFieldsForValueReferenceNotices(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
name: notices
params:
  - name: environment
    required: true
steps:
  - run: echo ${params.environment}
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	require.Len(t, result.ValueReferenceNotices, 1)

	got := result.ValueReferenceNotices[0]
	assert.Equal(t, "steps[0].run", got.FieldPath)
	assert.Equal(t, "${params.environment}", got.Token)
	assert.Contains(t, got.Message, "was left unchanged")
}
