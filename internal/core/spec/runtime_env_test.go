// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

	dag, err := Load(context.Background(), dagPath)
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

	env, err := ResolveEnv(context.Background(), dag, nil, ResolveEnvOptions{})
	require.NoError(t, err)
	assert.Contains(t, env, "SOURCE=from-snapshot")
	assert.NotContains(t, env, "SOURCE=from-disk")
}
