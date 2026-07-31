// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
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

	dag, err := loadDAGMetadata(ctx, dagPath)
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
