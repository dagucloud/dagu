// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/stretchr/testify/require"
)

func rescheduleEventuallyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 20
	}
	return base
}

func CreateInlineDAGRunForReschedule(t *testing.T, server Server, dagName string, enqueue bool) (string, string) {
	t.Helper()

	inlineSpec := `params:
  - name: KEY
    default: fallback
  - name: COUNT
    default: 1
steps:
  - name: print
    run: echo "${KEY}|${COUNT}"`
	params := `KEY="hello world" COUNT=3`

	var dagRunID string
	if enqueue {
		resp := server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
			Spec:   inlineSpec,
			Name:   &dagName,
			Params: &params,
		}).ExpectStatus(http.StatusOK).Send(t)

		var body api.EnqueueDAGRunFromSpec200JSONResponse
		resp.Unmarshal(t, &body)
		dagRunID = body.DagRunId
	} else {
		resp := server.Client().Post("/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{
			Spec:   inlineSpec,
			Name:   &dagName,
			Params: &params,
		}).ExpectStatus(http.StatusOK).Send(t)

		var body api.ExecuteDAGRunFromSpec200JSONResponse
		resp.Unmarshal(t, &body)
		dagRunID = body.DagRunId
	}
	require.NotEmpty(t, dagRunID)

	if enqueue {
		ProcessQueuedInlineRun(t, server, dagName)
		require.Eventually(t, func() bool {
			status, err := latestStoredAttemptStatus(server, dagName, dagRunID)
			return err == nil && status.Status == core.Succeeded
		}, rescheduleEventuallyTimeout(10*time.Second), 200*time.Millisecond)
	}

	attempt, dag := WaitForAttemptSnapshotWithDAG(t, server, dagName, dagRunID)
	require.Contains(t, string(dag.YamlData), `echo "${KEY}|${COUNT}"`)

	status, err := attempt.ReadStatus(server.Context)
	require.NoError(t, err)
	require.Equal(t, []string{"KEY=hello world", "COUNT=3"}, status.ParamsList)

	location := dag.Location
	if location == "" {
		location = ExpectedInlineTempPath(dagName, dagRunID)
	}

	return dagRunID, location
}

func AssertInlineRescheduledRunParams(t *testing.T, server Server, dagName, dagRunID string) {
	t.Helper()

	require.Eventually(t, func() bool {
		status, err := latestStoredAttemptStatus(server, dagName, dagRunID)
		if err != nil {
			return false
		}
		return status.Status == core.Succeeded
	}, rescheduleEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	status, err := latestStoredAttemptStatus(server, dagName, dagRunID)
	require.NoError(t, err)
	require.Equal(t, []string{"KEY=hello world", "COUNT=3"}, status.ParamsList)
}

func latestStoredAttemptStatus(server Server, dagName, dagRunID string) (*exec.DAGRunStatus, error) {
	store := filedagrun.New(
		server.Config.Paths.DAGRunsDir,
		filedagrun.WithLatestStatusToday(server.Config.Server.LatestStatusToday),
		filedagrun.WithLocation(server.Config.Core.Location),
	)

	attempt, err := store.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, dagRunID))
	if err != nil {
		return nil, err
	}

	return attempt.ReadStatus(server.Context)
}

func WaitForAttemptSnapshot(t *testing.T, server Server, dagName, dagRunID string) exec.DAGRunAttempt {
	t.Helper()

	store := filedagrun.New(
		server.Config.Paths.DAGRunsDir,
		filedagrun.WithLatestStatusToday(server.Config.Server.LatestStatusToday),
		filedagrun.WithLocation(server.Config.Core.Location),
	)

	var attempt exec.DAGRunAttempt
	require.Eventually(t, func() bool {
		var err error
		attempt, err = store.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, dagRunID))
		return err == nil
	}, rescheduleEventuallyTimeout(10*time.Second), 100*time.Millisecond)

	return attempt
}

func WaitForAttemptSnapshotWithDAG(t *testing.T, server Server, dagName, dagRunID string) (exec.DAGRunAttempt, *core.DAG) {
	t.Helper()

	attempt := WaitForAttemptSnapshot(t, server, dagName, dagRunID)

	var dag *core.DAG
	require.Eventually(t, func() bool {
		var err error
		dag, err = attempt.ReadDAG(server.Context)
		return err == nil && dag != nil && len(dag.YamlData) > 0
	}, rescheduleEventuallyTimeout(10*time.Second), 100*time.Millisecond)

	return attempt, dag
}

func ExpectedInlineTempPath(name, dagRunID string) string {
	return filepath.Join(os.TempDir(), name, dagRunID, fmt.Sprintf("%s.yaml", name))
}

func ProcessQueuedInlineRun(t *testing.T, server Server, queueName string) {
	t.Helper()

	// This keeps queue execution isolated to the test path without mirroring full server wiring.
	queueProcessor := scheduler.NewQueueProcessor(
		server.QueueStore,
		server.DAGRunStore,
		server.ProcStore,
		scheduler.NewDAGExecutor(
			coordinator.New(server.ServiceRegistry, coordinator.DefaultConfig()),
			server.SubCmdBuilder,
			server.Config.DefaultExecMode,
			server.Config.Paths.BaseConfig,
			nil,
		),
		config.Queues{
			Enabled: true,
			Config: []config.QueueConfig{
				{Name: queueName, MaxActiveRuns: 1},
			},
		},
	)
	require.Eventually(t, func() bool {
		queueProcessor.ProcessQueueItems(server.Context, queueName)
		items, err := server.QueueStore.List(server.Context, queueName)
		return err == nil && len(items) == 0
	}, rescheduleEventuallyTimeout(15*time.Second), 200*time.Millisecond)
}
