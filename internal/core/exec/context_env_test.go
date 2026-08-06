// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContext_ManagedDAGRunEnvsAreProtectedAndAvailableToDAGEnv(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

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
	for _, key := range slices.Sorted(maps.Keys(expected)) {
		require.NotEmpty(t, expected[key], "test setup should populate %s", key)

		dag.Env = append(dag.Env,
			key+"=wrong-from-dag-env",
			"REF_"+key+"=${"+key+"}",
		)
		optionEnvs = append(optionEnvs, key+"=wrong-from-options")
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
