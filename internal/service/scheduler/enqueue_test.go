// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueCatchupRun_PersistsQueuedCatchupMetadata(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: enqueue-catchup-dag
steps:
  - name: step1
    run: echo enqueue
`)

	runID := "catchup-run-1"
	scheduleTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	err := scheduler.EnqueueCatchupRun(
		th.Context,
		th.DAGRunStore,
		th.QueueStore,
		th.Config.Paths.LogDir,
		th.Config.Paths.ArtifactDir,
		th.Config.Paths.BaseConfig,
		"",
		dag.DAG,
		runID,
		ir.TriggerTypeCatchUp,
		scheduleTime,
		"prod",
	)
	require.NoError(t, err)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)

	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)

	require.Equal(t, ir.Queued, status.Status)
	require.Equal(t, ir.TriggerTypeCatchUp, status.TriggerType)
	require.Equal(t, stringutil.FormatTime(scheduleTime), status.ScheduleTime)
	require.Equal(t, "prod", status.ProfileName)
	require.NotEmpty(t, status.Log)
	assert.Contains(t, status.Log, filepath.Join(th.Config.Paths.LogDir, dag.Name))

	items, err := th.QueueStore.List(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	require.Len(t, items, 1)

	ref, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, exec.NewDAGRunRef(dag.Name, runID), *ref)
}

func TestEnqueueCatchupRun_RehydratesFullDAGBeforePersisting(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: enqueue-catchup-dag-full
dotenv: .env.secret
secrets:
  - name: EXPORTED_SECRET
    provider: env
    key: SECRET_SOURCE
steps:
  - name: step1
    run: echo enqueue
`)

	metadataOnly, err := spec.Load(
		th.Context,
		dag.Location,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	require.NoError(t, err)
	require.Empty(t, metadataOnly.Secrets)
	require.Empty(t, metadataOnly.Dotenv)

	runID := "catchup-run-full-dag"
	scheduleTime := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	err = scheduler.EnqueueCatchupRun(
		th.Context,
		th.DAGRunStore,
		th.QueueStore,
		th.Config.Paths.LogDir,
		th.Config.Paths.ArtifactDir,
		th.Config.Paths.BaseConfig,
		"",
		metadataOnly,
		runID,
		ir.TriggerTypeCatchUp,
		scheduleTime,
		"",
	)
	require.NoError(t, err)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)

	persisted, err := attempt.ReadDAG(th.Context)
	require.NoError(t, err)
	require.Len(t, persisted.Secrets, 1)
	assert.Equal(t, ir.SecretRef{
		Name:     "EXPORTED_SECRET",
		Provider: "env",
		Key:      "SECRET_SOURCE",
	}, persisted.Secrets[0])
	require.Equal(t, []string{".env", ".env.secret"}, persisted.Dotenv)
}

func TestEnqueueCatchupRun_RehydratesWorkspaceBaseConfig(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	workspaceBaseConfigDir := workspace.BaseConfigDir(th.Config.Paths.DAGsDir)
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceBaseConfigDir, "ops"), 0o750))
	require.NoError(t, os.WriteFile(th.Config.Paths.BaseConfig, []byte(`
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

	dag := th.DAG(t, `name: enqueue-catchup-workspace
labels:
  - workspace=ops
steps:
  - id: hello
    action: ops.hello
`)
	metadataOnly, err := spec.Load(
		th.Context,
		dag.Location,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	require.NoError(t, err)

	runID := "catchup-run-workspace"
	err = scheduler.EnqueueCatchupRun(
		th.Context,
		th.DAGRunStore,
		th.QueueStore,
		th.Config.Paths.LogDir,
		th.Config.Paths.ArtifactDir,
		th.Config.Paths.BaseConfig,
		workspaceBaseConfigDir,
		metadataOnly,
		runID,
		ir.TriggerTypeCatchUp,
		time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		"",
	)
	require.NoError(t, err)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)
	persisted, err := attempt.ReadDAG(th.Context)
	require.NoError(t, err)

	resolvedEnv, err := spec.ResolveEnv(th.Context, persisted, nil, spec.ResolveEnvOptions{})
	require.NoError(t, err)
	env := buildenv.ToMap(resolvedEnv)
	require.Equal(t, "from-workspace", env["GREETING"])
	require.Equal(t, "only-in-workspace", env["OPS_ONLY"])
}
