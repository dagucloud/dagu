// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
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
		dag.DAG,
		runID,
		core.TriggerTypeCatchUp,
		scheduleTime,
	)
	require.NoError(t, err)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)

	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)

	require.Equal(t, core.Queued, status.Status)
	require.Equal(t, core.TriggerTypeCatchUp, status.TriggerType)
	require.Equal(t, stringutil.FormatTime(scheduleTime), status.ScheduleTime)
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
		metadataOnly,
		runID,
		core.TriggerTypeCatchUp,
		scheduleTime,
	)
	require.NoError(t, err)

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, runID))
	require.NoError(t, err)

	persisted, err := attempt.ReadDAG(th.Context)
	require.NoError(t, err)
	require.Len(t, persisted.Secrets, 1)
	assert.Equal(t, core.SecretRef{
		Name:     "EXPORTED_SECRET",
		Provider: "env",
		Key:      "SECRET_SOURCE",
	}, persisted.Secrets[0])
	require.Equal(t, []string{".env", ".env.secret"}, persisted.Dotenv)
}
