// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestAPIRescheduleQueuedFileRunUsesStoredSourceFile(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "intg_reschedule_source_file", MaxActiveRuns: 1},
		}
	}))

	const dagName = "intg_reschedule_source_file"
	initialSpec := `queue: intg_reschedule_source_file
steps:
  - name: main
    run: echo stored snapshot`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &initialSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	enqResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/enqueue", dagName),
		api.EnqueueDAGDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var enqBody api.EnqueueDAGDAGRun200JSONResponse
	enqResp.Unmarshal(t, &enqBody)
	require.NotEmpty(t, enqBody.DagRunId)

	dagPath := filepath.Join(server.Config.Paths.DAGsDir, dagName+".yaml")
	attempt, dag := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, enqBody.DagRunId)
	require.NotNil(t, attempt)
	require.Empty(t, dag.Location)
	require.Equal(t, dagPath, dag.SourceFile)

	assertQueuedRunSpecFromFile(t, server, dagName, enqBody.DagRunId, true)

	currentSpec := `queue: intg_reschedule_source_file
steps:
  - name: main
    run: echo current file`
	require.NoError(t, os.WriteFile(dagPath, []byte(currentSpec), 0o600))

	useCurrentDagFile := true
	rescheduleResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", dagName, enqBody.DagRunId),
		api.RescheduleDAGRunJSONRequestBody{UseCurrentDagFile: &useCurrentDagFile},
	).ExpectStatus(http.StatusOK).Send(t)

	var rescheduleBody api.RescheduleDAGRun200JSONResponse
	rescheduleResp.Unmarshal(t, &rescheduleBody)
	require.NotEmpty(t, rescheduleBody.DagRunId)
	require.True(t, rescheduleBody.Queued)

	test.ProcessQueuedInlineRun(t, server, "intg_reschedule_source_file")

	_, rescheduledDAG := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, rescheduleBody.DagRunId)
	require.Contains(t, string(rescheduledDAG.YamlData), "echo current file")
	require.Equal(t, dagPath, rescheduledDAG.SourceFile)
}

func assertQueuedRunSpecFromFile(t *testing.T, server test.Server, dagName, dagRunID string, want bool) {
	t.Helper()

	resp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, dagRunID),
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &body)
	got := body.DagRunDetails.SpecFromFile != nil && *body.DagRunDetails.SpecFromFile
	require.Equal(t, want, got)
}
