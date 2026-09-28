// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestDAGExecutorPrepareScheduledDAGUsesWorkspaceBaseConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	dagsDir := filepath.Join(root, "dags")
	workspaceBaseConfigDir := workspace.BaseConfigDir(dagsDir)
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceBaseConfigDir, "ops"), 0o750))

	baseConfigPath := filepath.Join(root, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(`
env:
  - GREETING: from-global
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceBaseConfigDir, "ops", "base.yaml"), []byte(`
env:
  - GREETING: from-workspace
  - OPS_ONLY: only-in-workspace
actions:
  ops.hello:
    input_schema:
      type: object
      additionalProperties: false
    template:
      run: echo hello
`), 0o600))

	dagPath := filepath.Join(dagsDir, "hello.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: hello
schedule: "* * * * *"
labels:
  - workspace=ops
steps:
  - id: hello
    action: ops.hello
`), 0o600))

	dag, err := spec.Load(ctx, dagPath, spec.OnlyMetadata(), spec.WithoutEval(), spec.SkipSchemaValidation())
	require.NoError(t, err)

	executor := NewDAGExecutor(
		nil,
		nil,
		config.ExecutionModeLocal,
		baseConfigPath,
		WithDAGExecutorWorkspaceBaseConfigDir(workspaceBaseConfigDir),
	)
	prepared, err := executor.prepareDAGForSubprocess(ctx, dag, "")
	require.NoError(t, err)

	env := buildenv.ToMap(prepared.Env)
	require.Equal(t, "from-workspace", env["GREETING"])
	require.Equal(t, "only-in-workspace", env["OPS_ONLY"])
}

// Scheduled runs must see dotenv values exactly as `dagu start` does (#2934).
// Planner entries are metadata-only and carry no dotenv list.
func TestPrepareScheduledDAGDotenv(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	envFile := filepath.Join(root, "app.env")
	require.NoError(t, os.WriteFile(envFile, []byte("FOO=bar\n"), 0o600))

	dagPath := filepath.Join(root, "dags", "dotenv.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(dagPath), 0o750))
	require.NoError(t, os.WriteFile(dagPath, fmt.Appendf(nil, `
name: dotenv
schedule: "* * * * *"
dotenv:
  - %q
env:
  - GREETING: hello-${FOO}
steps:
  - name: print
    run: echo "$FOO"
`, envFile), 0o600))

	direct, err := spec.Load(ctx, dagPath)
	require.NoError(t, err)
	want, err := runtimeenv.Resolve(ctx, direct)
	require.NoError(t, err)

	entry, err := spec.Load(ctx, dagPath, spec.OnlyMetadata(), spec.WithoutEval(), spec.SkipSchemaValidation())
	require.NoError(t, err)
	executor := NewDAGExecutor(nil, nil, config.ExecutionModeLocal, "")
	prepared, err := executor.prepareDAGForSubprocess(ctx, entry, "")
	require.NoError(t, err)

	env := subprocessEnv(t, prepared)
	require.Equal(t, "bar", env["FOO"])
	require.Equal(t, buildenv.ToMap(want.Env), env)
}

// subprocessEnv returns the environment a `dagu start` subprocess resolves
// from the snapshot handed over by the scheduler.
func subprocessEnv(t *testing.T, prepared *ir.DAG) map[string]string {
	t.Helper()

	ctx := context.Background()
	snapshot := buildenv.NewSnapshot(prepared.Env, prepared.RuntimeResolved)
	var opts []spec.LoadOption
	if len(snapshot.Env) > 0 || snapshot.RuntimeResolved {
		opts = append(opts, spec.WithBuildEnvSnapshot(snapshot))
	}
	child, err := spec.Load(ctx, prepared.Location, opts...)
	require.NoError(t, err)
	resolved, err := runtimeenv.Resolve(ctx, child)
	require.NoError(t, err)
	return buildenv.ToMap(resolved.Env)
}
