// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
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
				dag := &ir.DAG{
					Env: []string{"USER_VAR=user_value"},
				}
				return runctx.NewContext(ctx, dag, "test-run", "test.log")
			},
			expected: map[string]string{
				"USER_VAR": "user_value",
			},
		},
		{
			name: "SecretOverridesEnvs",
			setup: func(ctx context.Context) context.Context {
				dag := &ir.DAG{
					Env: []string{"KEY=from_dag"},
				}
				secrets := []string{"KEY=from_secret"}
				return runctx.NewContext(ctx, dag, "test-run", "test.log",
					runctx.WithSecrets(secrets),
				)
			},
			expected: map[string]string{
				"KEY": "from_secret",
			},
		},
		{
			name: "CombinesAllSources",
			setup: func(ctx context.Context) context.Context {
				dag := &ir.DAG{
					Env: []string{"DAG_VAR=dag_value"},
				}
				secrets := []string{"SECRET_VAR=secret_value"}
				return runctx.NewContext(ctx, dag, "test-run", "test.log",
					runctx.WithSecrets(secrets),
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
			rCtx := runctx.GetContext(ctx)

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
			dag := &ir.DAG{Name: "test-dag", ParamsJSON: tt.paramsJSON}
			ctx = runctx.NewContext(ctx, dag, "run-1", "test.log")
			rCtx := runctx.GetContext(ctx)
			result := rCtx.UserEnvsMap()

			if tt.expectSet {
				assert.Equal(t, tt.paramsJSON, result[runenv.EnvKeyDAGParamsJSON])
				assert.Equal(t, tt.paramsJSON, result[runenv.EnvKeyDAGParamsJSONCompat])
			} else {
				_, ok := result[runenv.EnvKeyDAGParamsJSON]
				assert.False(t, ok, "DAG_PARAMS_JSON should not be set")
				_, ok = result[runenv.EnvKeyDAGParamsJSONCompat]
				assert.False(t, ok, "DAGU_PARAMS_JSON should not be set")
			}
		})
	}
}

func TestNewContext_DAGWikiDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wikiDir   string
		labels    []string
		expected  string
		expectSet bool
	}{
		{
			name:      "ConfigHasWikiDir",
			wikiDir:   "/tmp/wiki",
			expected:  filepath.Join("/tmp/wiki", "test-dag"),
			expectSet: true,
		},
		{
			name:      "WorkspaceLabelUsesWorkspaceScopedWikiDir",
			wikiDir:   "/tmp/wiki",
			labels:    []string{"workspace=ops"},
			expected:  filepath.Join("/tmp/wiki", "ops", "test-dag"),
			expectSet: true,
		},
		{
			name:      "ConflictingWorkspaceLabelsUseUnscopedWikiDir",
			wikiDir:   "/tmp/wiki",
			labels:    []string{"workspace=ops", "workspace=prod"},
			expected:  filepath.Join("/tmp/wiki", "test-dag"),
			expectSet: true,
		},
		{
			name:      "WikiDirEmpty",
			wikiDir:   "",
			expectSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{}
			cfg.Paths.WikiDir = tt.wikiDir
			ctx := config.WithConfig(context.Background(), cfg)
			dag := &ir.DAG{Name: "test-dag", Labels: ir.NewLabels(tt.labels)}
			ctx = runctx.NewContext(ctx, dag, "run-1", "test.log")
			rCtx := runctx.GetContext(ctx)
			result := rCtx.UserEnvsMap()

			if tt.expectSet {
				assert.Equal(t, tt.expected, result[runenv.EnvKeyDAGWikiDir])
				assert.Equal(t, tt.expected, result[runenv.EnvKeyDAGDocsDir])
			} else {
				_, wikiSet := result[runenv.EnvKeyDAGWikiDir]
				_, docsSet := result[runenv.EnvKeyDAGDocsDir]
				assert.False(t, wikiSet, "DAG_WIKI_DIR should not be set")
				assert.False(t, docsSet, "DAG_DOCS_DIR should not be set")
			}
		})
	}
}

func TestNewContext_DAGWikiDirRequiresConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag := &ir.DAG{Name: "test-dag"}
	ctx = runctx.NewContext(ctx, dag, "run-1", "test.log")
	rCtx := runctx.GetContext(ctx)
	result := rCtx.UserEnvsMap()

	_, wikiSet := result[runenv.EnvKeyDAGWikiDir]
	_, docsSet := result[runenv.EnvKeyDAGDocsDir]
	assert.False(t, wikiSet, "DAG_WIKI_DIR should not be set when no config is in context")
	assert.False(t, docsSet, "DAG_DOCS_DIR should not be set when no config is in context")
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
			dag := &ir.DAG{Name: "test-dag"}
			var opts []runctx.ContextOption
			if tt.workDir != "" {
				opts = append(opts, runctx.WithWorkDir(tt.workDir))
			}
			ctx = runctx.NewContext(ctx, dag, "run-1", "test.log", opts...)
			rCtx := runctx.GetContext(ctx)
			result := rCtx.UserEnvsMap()
			if tt.expectSet {
				assert.Equal(t, tt.workDir, result[runenv.EnvKeyDAGRunWorkDir])
			} else {
				_, ok := result[runenv.EnvKeyDAGRunWorkDir]
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
			dag := &ir.DAG{Name: "test-dag"}
			var opts []runctx.ContextOption
			if tt.artifactDir != "" {
				opts = append(opts, runctx.WithArtifactDir(tt.artifactDir))
			}
			ctx = runctx.NewContext(ctx, dag, "run-1", "test.log", opts...)
			rCtx := runctx.GetContext(ctx)
			result := rCtx.UserEnvsMap()
			if tt.expectSet {
				assert.Equal(t, tt.artifactDir, result[runenv.EnvKeyDAGRunArtifactsDir])
			} else {
				_, ok := result[runenv.EnvKeyDAGRunArtifactsDir]
				assert.False(t, ok, "DAG_RUN_ARTIFACTS_DIR should not be set")
			}
		})
	}
}

func TestNewContext_DAGEnvCanReferenceRuntimeManagedDirs(t *testing.T) {
	t.Parallel()

	artifactDir := filepath.Join(t.TempDir(), "artifacts", "run-1")

	dag := &ir.DAG{
		Name: "test-dag",
		Env: []string{
			"DAG_RUN_ARTIFACTS_DIR=/tmp/wrong-artifacts",
			"WORK_DIR=${DAG_RUN_ARTIFACTS_DIR}",
			"CURRENT_IDEA_PATH=${WORK_DIR}/current_idea.md",
		},
	}

	ctx := context.Background()
	ctx = runctx.NewContext(ctx, dag, "run-1", "test.log",
		runctx.WithArtifactDir(artifactDir),
	)

	result := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, artifactDir, result["WORK_DIR"])
	assert.Equal(t, filepath.Join(artifactDir, "current_idea.md"), filepath.Clean(result["CURRENT_IDEA_PATH"]))
	assert.Equal(t, artifactDir, result[runenv.EnvKeyDAGRunArtifactsDir])
}

func TestNewContext_DAGEnvCanReferenceBuiltInRunContext(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "run.log")
	workDir := filepath.Join(tmpDir, "work")
	artifactDir := filepath.Join(tmpDir, "artifacts")
	startedAt := "2026-03-13T10:00:01Z"
	scheduledAt := "2026-03-13T10:00:00Z"
	profileResolvedAt := "2026-03-13T09:59:00Z"

	dag := &ir.DAG{
		Name: "daily",
		Env: []string{
			"DAG_REF=${context.dag.name}",
			"RUN_REF=${context.run.id}",
			"ATTEMPT_REF=${context.attempt.id}",
			"TRIGGER_REF=${context.trigger.type}",
			"TRIGGER_ACTOR_REF=${context.trigger.actor}",
			"STARTED_REF=${context.attempt.started_at}",
			"SCHEDULED_REF=${context.run.scheduled_at}",
			"ROOT_NAME_REF=${context.run.root_name}",
			"ROOT_ID_REF=${context.run.root_id}",
			"LOG_REF=${context.paths.log_file}",
			"WORK_REF=${context.paths.work_dir}",
			"ARTIFACT_REF=${context.paths.artifacts_dir}",
			"PROFILE_REF=${context.profile.name}",
			"PROFILE_AT_REF=${context.profile.resolved_at}",
		},
	}

	ctx := runctx.NewContext(context.Background(), dag, "run-1", logFile,
		runctx.WithAttemptID("attempt-1"),
		runctx.WithRootDAGRun(ir.NewDAGRunRef("root", "root-run-1")),
		runctx.WithTriggerType(ir.TriggerTypeScheduler),
		runctx.WithTriggerActor("alice"),
		runctx.WithRunStartedAt(startedAt),
		runctx.WithScheduleTime(scheduledAt),
		runctx.WithWorkDir(workDir),
		runctx.WithArtifactDir(artifactDir),
		runctx.WithRuntimeProfile("prod", profileResolvedAt, nil),
	)

	envs := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, "daily", envs["DAG_REF"])
	assert.Equal(t, "run-1", envs["RUN_REF"])
	assert.Equal(t, "attempt-1", envs["ATTEMPT_REF"])
	assert.Equal(t, "scheduler", envs["TRIGGER_REF"])
	assert.Equal(t, "alice", envs["TRIGGER_ACTOR_REF"])
	assert.Equal(t, startedAt, envs["STARTED_REF"])
	assert.Equal(t, scheduledAt, envs["SCHEDULED_REF"])
	assert.Equal(t, "root", envs["ROOT_NAME_REF"])
	assert.Equal(t, "root-run-1", envs["ROOT_ID_REF"])
	assert.Equal(t, logFile, envs["LOG_REF"])
	assert.Equal(t, workDir, envs["WORK_REF"])
	assert.Equal(t, artifactDir, envs["ARTIFACT_REF"])
	assert.Equal(t, "prod", envs["PROFILE_REF"])
	assert.Equal(t, profileResolvedAt, envs["PROFILE_AT_REF"])
}

func TestNewContext_DAGEnvDoesNotExposeRootFieldsForRootRun(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "root",
		Env: []string{
			"ROOT_NAME_REF=${context.run.root_name}",
			"ROOT_ID_REF=${context.run.root_id}",
		},
	}

	ctx := runctx.NewContext(context.Background(), dag, "run-1", "dag.log",
		runctx.WithRootDAGRun(ir.NewDAGRunRef("root", "run-1")),
	)

	envs := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, "${context.run.root_name}", envs["ROOT_NAME_REF"])
	assert.Equal(t, "${context.run.root_id}", envs["ROOT_ID_REF"])
}

func TestNewContext_DAGEnvUsesRuntimeParamsOption(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:   "test-dag",
		Params: []string{"target=stored"},
		ParamDefs: []ir.ParamDef{{
			Name: "target",
			Type: ir.ParamDefTypeString,
		}},
		Env: []string{
			"TARGET=${params.target}",
		},
	}

	ctx := runctx.NewContext(context.Background(), dag, "run-1", "test.log",
		runctx.WithParams([]string{"target=runtime"}),
	)

	result := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, "runtime", result["TARGET"])
}

func TestNewContext_DAGEnvOverridesParamsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment variables are case-insensitive")
	}

	dag := &ir.DAG{
		Name:   "test-dag",
		Params: []string{"target=stored"},
		ParamDefs: []ir.ParamDef{{
			Name: "target",
			Type: ir.ParamDefTypeString,
		}},
		Env: []string{
			"TARGET=${params.target}",
		},
	}

	ctx := runctx.NewContext(context.Background(), dag, "run-1", "test.log",
		runctx.WithParams([]string{"target=runtime"}),
	)

	result := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, "runtime", result["TARGET"])
	assert.NotContains(t, result, "target")
}

func TestNewContext_DefaultProfileEnvsHaveLowestUserPrecedence(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "test-dag",
		Env: []string{
			"FROM_DEFAULT=${DEFAULT_ONLY}",
			"SHARED=dag",
			"SECRET_SHARED=dag",
		},
	}

	ctx := runctx.NewContext(context.Background(), dag, "run-1", "test.log",
		runctx.WithDefaultEnvVars("DEFAULT_ONLY=global", "SHARED=global"),
		runctx.WithDefaultSecrets([]string{"SECRET_ONLY=default-secret", "SECRET_SHARED=default-secret"}),
		runctx.WithEnvVars("SHARED=selected-profile", "SECRET_SHARED=selected-profile"),
	)

	result := runctx.GetContext(ctx).UserEnvsMap()
	assert.Equal(t, "global", result["DEFAULT_ONLY"])
	assert.Equal(t, "global", result["FROM_DEFAULT"])
	assert.Equal(t, "selected-profile", result["SHARED"])
	assert.Equal(t, "default-secret", result["SECRET_ONLY"])
	assert.Equal(t, "selected-profile", result["SECRET_SHARED"])
}

func TestInheritedEnvs(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "parent", Env: []string{"SHARED=dag", "DAG_ONLY=dag"}}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "test.log",
		runctx.WithRuntimeProfileValues(
			[]string{"DEFAULT_ONLY=default"},
			[]string{"DEFAULT_SECRET=default-secret"},
			[]string{"SHARED=profile", "PROFILE_ONLY=profile"},
			[]string{"PROFILE_SECRET=profile-secret"},
		),
		runctx.WithEnvVars("EXTRA=extra"),
		runctx.WithSecrets([]string{"DAG_SECRET=dag-secret"}),
	)

	inherited := stringutil.KeyValuesToMap(runctx.GetContext(ctx).InheritedEnvs())
	assert.Equal(t, "dag", inherited["SHARED"])
	assert.Equal(t, "dag", inherited["DAG_ONLY"])
	assert.Equal(t, "extra", inherited["EXTRA"])
	assert.Equal(t, "dag-secret", inherited["DAG_SECRET"])
	assert.NotContains(t, inherited, "DEFAULT_ONLY")
	assert.NotContains(t, inherited, "DEFAULT_SECRET")
	assert.NotContains(t, inherited, "PROFILE_ONLY")
	assert.NotContains(t, inherited, "PROFILE_SECRET")
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
	dag := &ir.DAG{
		Name: "test-dag",
		Env:  []string{"DAG_VAR=dag"},
	}

	ctx = runctx.NewContext(ctx, dag, "run-1", "test.log")
	rCtx := runctx.GetContext(ctx)
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

		data, err := json.Marshal(ir.PendingStepRetry{
			StepName: "step1",
			Interval: 2 * time.Second,
		})
		require.NoError(t, err)
		assert.JSONEq(t, `{"stepName":"step1","interval":"2s"}`, string(data))
	})

	t.Run("UnmarshalSupportsLegacyNumericInterval", func(t *testing.T) {
		t.Parallel()

		var retry ir.PendingStepRetry
		err := json.Unmarshal([]byte(`{"stepName":"step1","interval":2000000000}`), &retry)
		require.NoError(t, err)
		assert.Equal(t, "step1", retry.StepName)
		assert.Equal(t, 2*time.Second, retry.Interval)
	})
}
