// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRebuildFromYAML_PreservesJSONSerializedFields(t *testing.T) {
	t.Parallel()

	// Fields that survive dag.json, typically inherited from base.yaml.
	dag := &core.DAG{
		Name:           "test-dag",
		Queue:          "Default",
		WorkerSelector: map[string]string{"env": "prod"},
		MaxActiveRuns:  5,
		MaxActiveSteps: 3,
		LogDir:         "/custom/logs",
		Labels:         core.NewLabels([]string{"important", "production"}),
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

	dag := &core.DAG{
		Name:     "test-dag",
		Queue:    "Default",
		YamlData: nil,
	}

	result, err := spec.RebuildFromYAML(context.Background(), dag)
	require.NoError(t, err)

	assert.Same(t, dag, result)
	assert.Equal(t, "Default", result.Queue)
}

func TestRebuildFromYAML_RebuildsEnvFromYAML(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
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

	dag := &core.DAG{
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
	extraEnv, cleanup, err := buildenv.Prepare([]string{
		"HOST_VALUE=from-transport-host",
		"BACKTICK_VALUE=from-transport-backtick",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })

	for _, entry := range extraEnv {
		key, value, ok := strings.Cut(entry, "=")
		require.True(t, ok)
		t.Setenv(key, value)
	}

	dag := &core.DAG{
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

func TestRebuildFromYAML_RestoresHarnessConfig(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
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
	assert.Equal(t, core.HarnessPromptModeFlag, restored.Harnesses["gemini"].PromptMode)
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
	dag := &core.DAG{
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

	dag := &core.DAG{
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
