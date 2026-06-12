// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package v3schema_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/v3schema"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkflow(t *testing.T) {
	t.Run("accepts minimal workflow", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  - name: hello
    run: echo hello
`))
		require.NoError(t, err)
	})

	t.Run("rejects entrypoint name", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
name: deploy
steps:
  - name: deploy
    run: ./deploy.sh
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "entrypoint")
		require.Contains(t, err.Error(), "name")
	})

	t.Run("rejects steps mapping", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  build:
    run: ./build.sh
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "steps")
		require.Contains(t, err.Error(), "sequence")
	})

	t.Run("rejects missing steps", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
description: empty
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "steps")
	})

	t.Run("rejects empty steps", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps: []
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "steps")
		require.Contains(t, err.Error(), "non-empty")
	})

	t.Run("rejects unknown root field", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
unknown: true
steps:
  - name: hello
    run: echo hello
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown root field")
	})

	t.Run("accepts inline subdag", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child
---
name: child
steps:
  - name: child-step
    run: echo child
`))
		require.NoError(t, err)
	})

	t.Run("rejects later document without name", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  - name: first
    run: echo first
---
steps:
  - name: second
    run: echo second
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "document 2")
		require.Contains(t, err.Error(), "name")
	})

	t.Run("rejects duplicate document names", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  - name: first
    run: echo first
---
name: child
steps:
  - name: second
    run: echo second
---
name: child
steps:
  - name: third
    run: echo third
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "child")
		require.Contains(t, err.Error(), "unique")
	})

	t.Run("rejects duplicate root keys", func(t *testing.T) {
		err := v3schema.ValidateWorkflow([]byte(`
steps:
  - name: first
    run: echo first
steps:
  - name: second
    run: echo second
`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "already defined")
	})
}
