// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEnv_PrefersYamlDataOverLocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dagPath := filepath.Join(dir, "runtime-env.yaml")
	originalYAML := []byte(`
name: runtime-env
env:
  - SOURCE: from-snapshot
steps:
  - name: print
    run: echo ok
`)
	require.NoError(t, os.WriteFile(dagPath, originalYAML, 0o600))

	dag, err := spec.Load(context.Background(), dagPath)
	require.NoError(t, err)
	require.NotEmpty(t, dag.Location)
	require.NotEmpty(t, dag.YamlData)

	updatedYAML := []byte(`
name: runtime-env
env:
  - SOURCE: from-disk
steps:
  - name: print
    run: echo ok
`)
	require.NoError(t, os.WriteFile(dagPath, updatedYAML, 0o600))

	dag.Env = nil

	result, err := Resolve(context.Background(), dag, nil, Options{})
	require.NoError(t, err)
	assert.Contains(t, result.Env, "SOURCE=from-snapshot")
	assert.NotContains(t, result.Env, "SOURCE=from-disk")
}
