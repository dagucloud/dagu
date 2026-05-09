// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContext_ManagedDAGRunEnvsAreProtectedAndAvailableToDAGEnv(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Paths.DocsDir = filepath.Join(t.TempDir(), "docs")
	ctx := config.WithConfig(context.Background(), cfg)

	dag := &core.DAG{
		Name:       "test-dag",
		ParamsJSON: `{"a":"b"}`,
	}
	dagRunID := "run-1"
	logFile := filepath.Join(t.TempDir(), "run.log")
	options := &contextOptions{
		workDir:     filepath.Join(t.TempDir(), "work"),
		artifactDir: filepath.Join(t.TempDir(), "artifacts"),
	}

	expected := buildManagedDAGRunEnvs(ctx, dag, dagRunID, logFile, options)
	require.NotEmpty(t, expected)

	var optionEnvs []string
	seen := make(map[string]struct{}, len(managedDAGRunEnvs))
	for _, env := range managedDAGRunEnvs {
		require.NotEmpty(t, env.key)
		require.NotContains(t, seen, env.key)
		seen[env.key] = struct{}{}

		value, ok := expected[env.key]
		if !ok {
			continue
		}
		require.NotEmpty(t, value, "test setup should populate %s", env.key)

		dag.Env = append(dag.Env,
			env.key+"=wrong-from-dag-env",
			"REF_"+env.key+"=${"+env.key+"}",
		)
		optionEnvs = append(optionEnvs, env.key+"=wrong-from-options")
	}

	ctx = NewContext(ctx, dag, dagRunID, logFile,
		WithWorkDir(options.workDir),
		WithArtifactDir(options.artifactDir),
		WithEnvVars(optionEnvs...),
	)

	result := GetContext(ctx).UserEnvsMap()
	for key, expectedValue := range expected {
		assert.Equal(t, expectedValue, result[key], "%s should not be overridden", key)
		assert.Equal(t, expectedValue, result["REF_"+key], "%s should be available while evaluating DAG env", key)
	}
}
