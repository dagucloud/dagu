// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshBaseSMTP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	basePath := filepath.Join(root, "base.yaml")
	workspaceDir := filepath.Join(root, "workspaces")
	workspacePath := filepath.Join(workspaceDir, "ops", "base.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workspacePath), 0750))
	require.NoError(t, os.WriteFile(basePath, []byte("smtp:\n  host: old.example\n  password: old-secret\nenv:\n  ORIGINAL: original\n"), 0600))
	definition := []byte("name: parent\nlabels: [workspace=ops]\nsteps:\n  - run: echo parent\n---\nname: child\nsteps:\n  - run: echo child\n")
	opts := []spec.LoadOption{spec.WithBaseConfig(basePath), spec.WithWorkspaceBaseConfigDir(workspaceDir)}
	dag, err := spec.LoadYAML(ctx, definition, opts...)
	require.NoError(t, err)
	snapshot, err := spec.PrepareDAGSnapshot(dag)
	require.NoError(t, err)
	t.Run("NoBasePath", func(t *testing.T) {
		current, err := spec.RefreshBaseSMTP(dag)
		require.NoError(t, err)
		current, err = spec.RebuildFromYAML(ctx, current)
		require.NoError(t, err)
		assert.Nil(t, current.SMTP)
		assert.Contains(t, current.Env, "ORIGINAL=original")
	})
	require.NoError(t, os.WriteFile(basePath, []byte("smtp:\n  host: global.example\n  password: global-secret\nenv:\n  ORIGINAL: changed\n"), 0600))
	require.NoError(t, os.WriteFile(workspacePath, []byte("smtp:\n  host: workspace.example\n"), 0600))

	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			input := snapshot.Clone()
			if legacy {
				input.BaseConfigData = dag.BaseConfigData
				input.BaseConfigWorkspace = nil
				input.LocalDAGs = map[string]*ir.DAG{"child": dag.LocalDAGs["child"].Clone()}
				input.LocalDAGs["child"].BaseConfigWorkspace = nil
			}
			refreshed, err := spec.RefreshBaseSMTP(input, opts...)
			require.NoError(t, err)
			for _, candidate := range []*ir.DAG{refreshed, refreshed.LocalDAGs["child"]} {
				rebuilt, err := spec.RebuildFromYAML(ctx, candidate)
				require.NoError(t, err)
				require.NotNil(t, rebuilt.SMTP)
				assert.Equal(t, "workspace.example", rebuilt.SMTP.Host)
				assert.Equal(t, "global-secret", rebuilt.SMTP.Password)
				assert.Contains(t, rebuilt.Env, "ORIGINAL=original")
			}
			assert.Equal(t, definition, refreshed.YamlData)
		})
	}
	// The child's workspace remains known when retried independently of its file.
	child, err := spec.RefreshBaseSMTP(snapshot.LocalDAGs["child"], opts...)
	require.NoError(t, err)
	child, err = spec.RebuildFromYAML(ctx, child)
	require.NoError(t, err)
	assert.Equal(t, "workspace.example", child.SMTP.Host)

	require.NoError(t, os.Remove(workspacePath))
	current, err := spec.RefreshBaseSMTP(snapshot, opts...)
	require.NoError(t, err)
	current, err = spec.RebuildFromYAML(ctx, current)
	require.NoError(t, err)
	assert.Equal(t, "global.example", current.SMTP.Host)
	require.NoError(t, os.Remove(basePath))
	current, err = spec.RefreshBaseSMTP(snapshot, opts...)
	require.NoError(t, err)
	current, err = spec.RebuildFromYAML(ctx, current)
	require.NoError(t, err)
	assert.Nil(t, current.SMTP)
	assert.Equal(t, "old.example", dag.SMTP.Host)
}

func TestSnapshotOAuthSMTP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("steps:\n  - run: echo hello\n"), spec.WithBaseConfigContent([]byte(`smtp:
  username: sender@example.com
  oauth:
    provider: microsoft
    tenant_id: tenant
    client_id: client
    client_secret: base-secret
`)))
	require.NoError(t, err)
	snapshot, err := spec.PrepareDAGSnapshot(dag)
	require.NoError(t, err)
	assert.Equal(t, "{}", strings.TrimSpace(string(snapshot.BaseConfigData)))
	rebuilt, err := spec.RebuildFromYAML(ctx, snapshot)
	require.NoError(t, err)
	assert.Nil(t, rebuilt.SMTP)
	assert.Equal(t, "base-secret", dag.SMTP.OAuth.ClientSecret)

	current, err := spec.RefreshBaseSMTP(snapshot, spec.WithBaseConfigContent([]byte("smtp:\n  host: password.example\n  password: new-secret\n")))
	require.NoError(t, err)
	current, err = spec.RebuildFromYAML(ctx, current)
	require.NoError(t, err)
	assert.Equal(t, "password.example", current.SMTP.Host)
	assert.Equal(t, "new-secret", current.SMTP.Password)
	assert.Nil(t, current.SMTP.OAuth)
}

func TestRebuildFromYAML_PreservesJSONSerializedFields(t *testing.T) {
	t.Parallel()

	// Fields that survive dag.json, typically inherited from base.yaml.
	dag := &ir.DAG{
		Name:           "test-dag",
		Queue:          "Default",
		WorkerSelector: map[string]string{"env": "prod"},
		MaxActiveRuns:  5,
		MaxActiveSteps: 3,
		LogDir:         "/custom/logs",
		Labels:         ir.NewLabels([]string{"important", "production"}),
		Location:       "/path/to/dag.yaml",
		YamlData:       []byte("steps:\n  - name: test\n    command: echo hello"),
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)

	assert.Equal(t, "Default", result.Queue)
	assert.Equal(t, map[string]string{"env": "prod"}, result.WorkerSelector)
	assert.Equal(t, 5, result.MaxActiveRuns)
	assert.Equal(t, 3, result.MaxActiveSteps)
	assert.Equal(t, "/custom/logs", result.LogDir)
	assert.Equal(t, []string{"important", "production"}, result.Labels.Strings())
	assert.Equal(t, "/path/to/dag.yaml", result.Location)

	assert.Same(t, dag, result)
}

func TestRebuildFromYAML_EmptyYAML(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		Queue:    "Default",
		YamlData: nil,
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)

	assert.Same(t, dag, result)
	assert.Equal(t, "Default", result.Queue)
}

func TestRebuildFromYAML_ParamsOverrideReplacesSnapshotParams(t *testing.T) {
	t.Parallel()

	// Both production callers pass runtime params captured from the run's status,
	// which must win over the params the snapshot carries and over the YAML default.
	dag := &ir.DAG{
		Name:   "params-override",
		Params: []string{`topic="from-snapshot"`},
		YamlData: []byte(`
params:
  - topic: from-default
steps:
  - run: echo ${topic}
`),
	}

	restored, err := spec.RebuildFromYAML(context.Background(), dag, []string{`topic="from-override"`})
	require.NoError(t, err)

	assert.Equal(t, []string{"topic=from-override"}, restored.Params)
	assert.JSONEq(t, `{"topic":"from-override"}`, restored.ParamsJSON)
}

func TestRebuildFromYAML_RebuildsEnvFromYAML(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		Queue:    "Default",
		Location: "/path/to/dag.yaml",
		YamlData: []byte("env:\n  - MY_VAR: my_value\nsteps:\n  - name: test\n    command: echo $MY_VAR"),
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)

	assert.Equal(t, "Default", result.Queue)
	assert.Contains(t, result.Env, "MY_VAR=my_value")
}

func TestRebuildFromYAML_ReappliesBaseConfigContent(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
steps:
  - name: test
    run: echo hello
`),
		BaseConfigData: []byte(`
env:
  - BASE_ONLY: "from-base-config"
`),
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)
	assert.Contains(t, result.Env, "BASE_ONLY=from-base-config")
}

func TestRebuildFromYAML_UsesTransportedBuildEnv(t *testing.T) {
	extraEnv, cleanup, err := buildenv.Prepare(buildenv.NewSnapshot([]string{
		"HOST_VALUE=from-transport-host",
		"BACKTICK_VALUE=from-transport-backtick",
	}, false))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })

	for _, entry := range extraEnv {
		key, value, ok := strings.Cut(entry, "=")
		require.True(t, ok)
		t.Setenv(key, value)
	}

	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
env:
  - HOST_VALUE: "${MISSING_NON_WHITELISTED_ENV}"
  - BACKTICK_VALUE: "` + "`command_that_does_not_exist_12345`" + `"
steps:
  - name: test
    run: echo hello
`),
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)
	assert.Contains(t, result.Env, "HOST_VALUE=from-transport-host")
	assert.Contains(t, result.Env, "BACKTICK_VALUE=from-transport-backtick")
}

func TestRebuildFromYAML_EnvPrecedenceWhenSourcesConflict(t *testing.T) {
	extraEnv, cleanup, err := buildenv.Prepare(buildenv.NewSnapshot([]string{
		"DECLARED=from-transport",
		"CONFLICT=from-transport",
		"TRANSPORT_ONLY=from-transport",
	}, false))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })

	for _, entry := range extraEnv {
		key, value, ok := strings.Cut(entry, "=")
		require.True(t, ok)
		t.Setenv(key, value)
	}

	dag := &ir.DAG{
		Name: "env-precedence",
		YamlData: []byte(`
env:
  - DECLARED: from-yaml
steps:
  - run: echo hello
`),
		PresolvedBuildEnv: map[string]string{
			"CONFLICT":      "from-snapshot",
			"SNAPSHOT_ONLY": "from-snapshot",
		},
	}

	restored, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)

	// A key carried by the build env overrides its own env: declaration instead of
	// being re-evaluated, so a rebuild reuses the value resolved when the run was
	// first built rather than whatever the YAML would resolve to now.
	assert.Contains(t, restored.Env, "DECLARED=from-transport")
	assert.NotContains(t, restored.Env, "DECLARED=from-yaml")

	// Both build env sources resolve a shared key the same way in the map handed to
	// the loader and in the fallbacks appended afterwards: the transported value wins.
	assert.Contains(t, restored.Env, "CONFLICT=from-transport")
	assert.NotContains(t, restored.Env, "CONFLICT=from-snapshot")

	// A key only one source defines survives from that source.
	assert.Contains(t, restored.Env, "TRANSPORT_ONLY=from-transport")
	assert.Contains(t, restored.Env, "SNAPSHOT_ONLY=from-snapshot")

	// The root env span still marks only the declared entry after the rebuild.
	span := restored.RootEnvSpan
	assert.Equal(t, []string{"DECLARED=from-transport"}, restored.Env[span.Start:span.End])
}

func TestRebuildFromYAML_RestoresHarnessConfig(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "snapshot-harness",
		YamlData: []byte(`
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt
harness:
  provider: gemini
  model: gemini-2.5-pro
  fallback:
    - provider: claude
      model: sonnet
steps:
  - run: "Review the repository"
`),
	}

	restored, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)
	require.Same(t, dag, restored)

	require.NotNil(t, restored.Harness)
	assert.Equal(t, "gemini", restored.Harness.Config["provider"])
	assert.Equal(t, "gemini-2.5-pro", restored.Harness.Config["model"])
	require.Len(t, restored.Harness.Fallback, 1)
	assert.Equal(t, "claude", restored.Harness.Fallback[0]["provider"])

	require.NotNil(t, restored.Harnesses)
	require.Contains(t, restored.Harnesses, "gemini")
	require.NotNil(t, restored.Harnesses["gemini"])
	assert.Equal(t, "gemini", restored.Harnesses["gemini"].Binary)
	assert.Equal(t, ir.HarnessPromptModeFlag, restored.Harnesses["gemini"].PromptMode)
	assert.Equal(t, "--prompt", restored.Harnesses["gemini"].PromptFlag)
}

func TestRebuildFromYAML_RestoresExecutorDefaults(t *testing.T) {
	t.Parallel()

	// WorkingDir is persisted in dag.json while WorkingDirExplicit is not, so a
	// snapshot arrives with the path set and the flag cleared. The YAML declares a
	// different path so the assertions below distinguish the snapshot's persisted
	// value from the one a rebuild would produce.
	snapshotWorkingDir := t.TempDir()
	yamlWorkingDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "snapshot-executor-defaults",
		WorkingDir: snapshotWorkingDir,
		YamlData: fmt.Appendf(nil, `
working_dir: %q
s3:
  region: us-west-2
  bucket: snapshot-bucket
redis:
  host: redis.internal
  port: 6380
kubernetes:
  namespace: dag-ns
steps:
  - run: echo hello
`, yamlWorkingDir),
	}

	restored, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)
	require.Same(t, dag, restored)

	require.NotNil(t, restored.S3)
	assert.Equal(t, "us-west-2", restored.S3.Region)
	assert.Equal(t, "snapshot-bucket", restored.S3.Bucket)

	require.NotNil(t, restored.Redis)
	assert.Equal(t, "redis.internal", restored.Redis.Host)
	assert.Equal(t, 6380, restored.Redis.Port)

	require.NotNil(t, restored.Kubernetes)
	assert.Equal(t, "dag-ns", restored.Kubernetes["namespace"])

	// WorkingDirExplicit is json:"-", so only the rebuild can restore it.
	assert.True(t, restored.WorkingDirExplicit)
	// WorkingDir already survived in dag.json, so the rebuild must neither blank
	// it nor replace it with the value it just parsed out of the YAML.
	assert.Equal(t, snapshotWorkingDir, restored.WorkingDir)
}

func TestRebuildFromYAML_PreservesPresolvedBuildEnv(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "snapshot-presolved-env",
		YamlData: []byte(`
env:
  - DECLARED: from-yaml
steps:
  - run: echo hello
`),
		// Resolved at original build time from a source the retrying process may
		// no longer have. The DAG's own env block does not declare it, so
		// rebuilding from YAML cannot recover it.
		PresolvedBuildEnv: map[string]string{"SNAPSHOT_ONLY": "kept"},
	}

	restored, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)
	require.Same(t, dag, restored)

	assert.Contains(t, restored.Env, "DECLARED=from-yaml")
	assert.Contains(t, restored.Env, "SNAPSHOT_ONLY=kept")
}

// The params whose eval waits for the run get their values from its dates
// and context, and so do the env entries and later params that read them.
func TestEvaluateDeferredParams(t *testing.T) {
	t.Parallel()

	yaml := []byte(`
dates:
  target_day: -1d
params:
  - name: day
    eval: "${dates.target_day}"
  - name: target
    eval: "/data/$day"
env:
  - DAY: ${params.day}
steps:
  - run: echo ${params.target}
`)
	dag, err := spec.LoadYAML(context.Background(), yaml)
	require.NoError(t, err)
	require.Equal(t, []string{"day", "target"}, dag.DeferredParams)

	dates := []ir.DateValue{{Name: "target_day", Value: "2026-11-09"}}
	evaluated, err := spec.EvaluateDeferredParams(context.Background(), dag, dates, cmnvalue.BuiltinContext{})
	require.NoError(t, err)

	assert.Equal(t, []string{"day=2026-11-09", "target=/data/2026-11-09"}, evaluated.Params)
	assert.JSONEq(t, `{"day":"2026-11-09","target":"/data/2026-11-09"}`, evaluated.ParamsJSON)
	assert.Contains(t, evaluated.Env, "DAY=2026-11-09")
	assert.Empty(t, evaluated.DeferredParams)
}
