// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGContext_UserEnvsMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) context.Context
		expected map[string]string
	}{
		{
			name: "ExcludesOSEnvironment",
			setup: func(ctx context.Context) context.Context {
				dag := &core.DAG{
					Env: []string{"USER_VAR=user_value"},
				}
				return exec.NewContext(ctx, dag, "test-run", "test.log")
			},
			expected: map[string]string{
				"USER_VAR": "user_value",
			},
		},
		{
			name: "SecretOverridesEnvs",
			setup: func(ctx context.Context) context.Context {
				dag := &core.DAG{
					Env: []string{"KEY=from_dag"},
				}
				secrets := []string{"KEY=from_secret"}
				return exec.NewContext(ctx, dag, "test-run", "test.log",
					exec.WithSecrets(secrets),
				)
			},
			expected: map[string]string{
				"KEY": "from_secret",
			},
		},
		{
			name: "CombinesAllSources",
			setup: func(ctx context.Context) context.Context {
				dag := &core.DAG{
					Env: []string{"DAG_VAR=dag_value"},
				}
				secrets := []string{"SECRET_VAR=secret_value"}
				return exec.NewContext(ctx, dag, "test-run", "test.log",
					exec.WithSecrets(secrets),
				)
			},
			expected: map[string]string{
				"DAG_VAR":    "dag_value",
				"SECRET_VAR": "secret_value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			ctx = tt.setup(ctx)
			rCtx := exec.GetContext(ctx)

			result := rCtx.UserEnvsMap()

			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, result[key], "key %s should have value %s", key, expectedValue)
			}
			// Ensure OS env is not included (PATH should not be in result)
			_, hasPath := result["PATH"]
			assert.False(t, hasPath, "UserEnvsMap should not include OS environment variables like PATH")
		})
	}
}

func TestNewContext_DAGDocsDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		docsDir   string
		dagName   string
		labels    core.Labels
		expected  string
		expectSet bool
	}{
		{
			name:      "ConfigHasDocsDir",
			docsDir:   "/var/dagu/docs",
			dagName:   "my-workflow",
			expected:  filepath.Join("/var/dagu/docs", "my-workflow"),
			expectSet: true,
		},
		{
			name:    "WorkspaceLabelUsesWorkspaceScopedDocsDir",
			docsDir: "/var/dagu/docs",
			dagName: "my-workflow",
			labels: core.Labels{
				{Key: "workspace", Value: "ops"},
			},
			expected:  filepath.Join("/var/dagu/docs", "ops", "my-workflow"),
			expectSet: true,
		},
		{
			name:    "ConflictingWorkspaceLabelsUseLegacyDocsDir",
			docsDir: "/var/dagu/docs",
			dagName: "my-workflow",
			labels: core.Labels{
				{Key: "workspace", Value: "ops"},
				{Key: "workspace", Value: "prod"},
			},
			expected:  filepath.Join("/var/dagu/docs", "my-workflow"),
			expectSet: true,
		},
		{
			name:    "InvalidWorkspaceLabelUsesLegacyDocsDir",
			docsDir: "/var/dagu/docs",
			dagName: "my-workflow",
			labels: core.Labels{
				{Key: "workspace", Value: "../shared"},
			},
			expected:  filepath.Join("/var/dagu/docs", "my-workflow"),
			expectSet: true,
		},
		{
			name:      "DocsDirEmpty",
			docsDir:   "",
			dagName:   "my-workflow",
			expectSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tt.docsDir != "" {
				cfg := &config.Config{}
				cfg.Paths.DocsDir = tt.docsDir
				ctx = config.WithConfig(ctx, cfg)
			}

			dag := &core.DAG{Name: tt.dagName, Labels: tt.labels}
			ctx = exec.NewContext(ctx, dag, "run-1", "test.log")
			rCtx := exec.GetContext(ctx)
			result := rCtx.UserEnvsMap()

			if tt.expectSet {
				assert.Equal(t, tt.expected, result[exec.EnvKeyDAGDocsDir])
			} else {
				_, ok := result[exec.EnvKeyDAGDocsDir]
				assert.False(t, ok, "DAG_DOCS_DIR should not be set")
			}
		})
	}

	t.Run("NoConfigInContext", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		dag := &core.DAG{Name: "my-workflow"}
		ctx = exec.NewContext(ctx, dag, "run-1", "test.log")
		rCtx := exec.GetContext(ctx)
		result := rCtx.UserEnvsMap()

		_, ok := result[exec.EnvKeyDAGDocsDir]
		assert.False(t, ok, "DAG_DOCS_DIR should not be set when no config in context")
	})
}

func TestNewContext_DAGParamsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		paramsJSON string
		expectSet  bool
	}{
		{
			name:       "JSONPresent",
			paramsJSON: `{"key":"value"}`,
			expectSet:  true,
		},
		{
			name:       "JSONEmpty",
			paramsJSON: "",
			expectSet:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dag := &core.DAG{Name: "test-dag", ParamsJSON: tt.paramsJSON}
			ctx = exec.NewContext(ctx, dag, "run-1", "test.log")
			rCtx := exec.GetContext(ctx)
			result := rCtx.UserEnvsMap()

			if tt.expectSet {
				assert.Equal(t, tt.paramsJSON, result[exec.EnvKeyDAGParamsJSONCompat])
				assert.Equal(t, tt.paramsJSON, result[exec.EnvKeyDAGParamsJSON])
			} else {
				_, ok1 := result[exec.EnvKeyDAGParamsJSONCompat]
				_, ok2 := result[exec.EnvKeyDAGParamsJSON]
				assert.False(t, ok1, "DAG_PARAMS_JSON should not be set")
				assert.False(t, ok2, "DAGU_PARAMS_JSON should not be set")
			}
		})
	}
}

func TestNewContext_DAGRunWorkDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workDir   string
		expectSet bool
	}{
		{name: "WorkDirSet", workDir: "/data/dag-runs/my-dag/work", expectSet: true},
		{name: "WorkDirEmpty", workDir: "", expectSet: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dag := &core.DAG{Name: "test-dag"}
			var opts []exec.ContextOption
			if tt.workDir != "" {
				opts = append(opts, exec.WithWorkDir(tt.workDir))
			}
			ctx = exec.NewContext(ctx, dag, "run-1", "test.log", opts...)
			rCtx := exec.GetContext(ctx)
			result := rCtx.UserEnvsMap()
			if tt.expectSet {
				assert.Equal(t, tt.workDir, result[exec.EnvKeyDAGRunWorkDir])
			} else {
				_, ok := result[exec.EnvKeyDAGRunWorkDir]
				assert.False(t, ok, "DAG_RUN_WORK_DIR should not be set")
			}
		})
	}
}

func TestNewContext_DAGRunArtifactsDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		artifactDir string
		expectSet   bool
	}{
		{name: "ArtifactDirSet", artifactDir: "/data/artifacts/test-dag/dag-run_20260412_000000Z_run-1", expectSet: true},
		{name: "ArtifactDirEmpty", artifactDir: "", expectSet: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dag := &core.DAG{Name: "test-dag"}
			var opts []exec.ContextOption
			if tt.artifactDir != "" {
				opts = append(opts, exec.WithArtifactDir(tt.artifactDir))
			}
			ctx = exec.NewContext(ctx, dag, "run-1", "test.log", opts...)
			rCtx := exec.GetContext(ctx)
			result := rCtx.UserEnvsMap()
			if tt.expectSet {
				assert.Equal(t, tt.artifactDir, result[exec.EnvKeyDAGRunArtifactsDir])
			} else {
				_, ok := result[exec.EnvKeyDAGRunArtifactsDir]
				assert.False(t, ok, "DAG_RUN_ARTIFACTS_DIR should not be set")
			}
		})
	}
}

func TestNewContext_DAGEnvCanReferenceRuntimeManagedDirs(t *testing.T) {
	t.Parallel()

	artifactDir := filepath.Join(t.TempDir(), "artifacts", "run-1")
	docsDir := filepath.Join(t.TempDir(), "docs")
	cfg := &config.Config{}
	cfg.Paths.DocsDir = docsDir

	dag := &core.DAG{
		Name: "test-dag",
		Env: []string{
			"OUTPUT_FILE=current-plan.md",
			"DAG_DOCS_DIR=/tmp/wrong-docs",
			"PLAN_PATH=${DAG_DOCS_DIR}/${OUTPUT_FILE}",
			"DAG_RUN_ARTIFACTS_DIR=/tmp/wrong-artifacts",
			"WORK_DIR=${DAG_RUN_ARTIFACTS_DIR}",
			"CURRENT_IDEA_PATH=${WORK_DIR}/current_idea.md",
		},
	}

	ctx := config.WithConfig(context.Background(), cfg)
	ctx = exec.NewContext(ctx, dag, "run-1", "test.log",
		exec.WithArtifactDir(artifactDir),
	)

	result := exec.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, filepath.Join(docsDir, dag.Name, "current-plan.md"), filepath.Clean(result["PLAN_PATH"]))
	assert.Equal(t, artifactDir, result["WORK_DIR"])
	assert.Equal(t, filepath.Join(artifactDir, "current_idea.md"), filepath.Clean(result["CURRENT_IDEA_PATH"]))
	assert.Equal(t, artifactDir, result[exec.EnvKeyDAGRunArtifactsDir])
}

func TestNewContext_AllEnvsUsesFilteredBaseEnv(t *testing.T) {
	t.Setenv("EXEC_CONTEXT_HOST_ONLY", "host-value")

	cfg := &config.Config{}
	cfg.Core.BaseEnv = config.NewBaseEnv([]string{
		"PATH=/usr/bin:/bin",
		"EXEC_CONTEXT_ALLOWED=allowed",
		"KUBERNETES_SERVICE_HOST=10.0.0.1",
		"KUBERNETES_SERVICE_PORT=443",
	})

	ctx := config.WithConfig(context.Background(), cfg)
	dag := &core.DAG{
		Name: "test-dag",
		Env:  []string{"DAG_VAR=dag"},
	}

	ctx = exec.NewContext(ctx, dag, "run-1", "test.log")
	rCtx := exec.GetContext(ctx)
	envs := rCtx.AllEnvs()

	assert.Contains(t, envs, "PATH=/usr/bin:/bin")
	assert.Contains(t, envs, "EXEC_CONTEXT_ALLOWED=allowed")
	assert.Contains(t, envs, "KUBERNETES_SERVICE_HOST=10.0.0.1")
	assert.Contains(t, envs, "KUBERNETES_SERVICE_PORT=443")
	assert.Contains(t, envs, "DAG_VAR=dag")
	assert.NotContains(t, envs, "EXEC_CONTEXT_HOST_ONLY=host-value")
}

func TestPendingStepRetryJSON(t *testing.T) {
	t.Parallel()

	t.Run("MarshalUsesDurationString", func(t *testing.T) {
		t.Parallel()

		data, err := json.Marshal(exec.PendingStepRetry{
			StepName: "step1",
			Interval: 2 * time.Second,
		})
		require.NoError(t, err)
		assert.JSONEq(t, `{"stepName":"step1","interval":"2s"}`, string(data))
	})

	t.Run("UnmarshalSupportsLegacyNumericInterval", func(t *testing.T) {
		t.Parallel()

		var retry exec.PendingStepRetry
		err := json.Unmarshal([]byte(`{"stepName":"step1","interval":2000000000}`), &retry)
		require.NoError(t, err)
		assert.Equal(t, "step1", retry.StepName)
		assert.Equal(t, 2*time.Second, retry.Interval)
	})
}
