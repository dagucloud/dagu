// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestAPIEditRetrySkipsCompletedStepsAndPreservesOutputs(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "intg_edit_retry", MaxActiveRuns: 1},
		}
	}))

	dagName := "intg_edit_retry"
	sourceSpec := fmt.Sprintf(`name: %s
queue: intg_edit_retry
type: graph
steps:
  - name: build
    run: %s
    output: RESULT
  - name: consume
%s
    depends:
      - build
`, dagName, editRetryEchoResultCommand(), indentStepField(portableDirectFailureStepYAML(t)))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &sourceSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	sourceRunID := "source-edit-retry"
	startResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{DagRunId: &sourceRunID},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.Equal(t, api.DAGRunId(sourceRunID), startBody.DagRunId)

	test.ProcessQueuedInlineRun(t, server, dagName)
	sourceStatus := waitForEditRetryStoredStatus(t, server, dagName, sourceRunID, core.Failed)
	require.Equal(t, core.NodeSucceeded, sourceStatus.Nodes[0].Status)
	require.Equal(t, core.NodeFailed, sourceStatus.Nodes[1].Status)

	editedSpec := fmt.Sprintf(`name: %s
queue: intg_edit_retry
type: graph
steps:
  - name: build
%s
    output: RESULT
  - name: consume
    run: %s
    output: CONSUMED
    depends:
      - build
  - name: notify
    run: %s
    depends:
      - consume
`, dagName, indentStepField(portableDirectFailureStepYAML(t)), editRetryEchoConsumedCommand(), editRetryEchoDoneCommand())

	previewResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/edit-retry/preview", dagName, sourceRunID),
		api.PreviewEditRetryDAGRunJSONRequestBody{Spec: editedSpec},
	).ExpectStatus(http.StatusOK).Send(t)

	var preview api.PreviewEditRetryDAGRun200JSONResponse
	previewResp.Unmarshal(t, &preview)
	require.Empty(t, preview.Errors)
	require.Equal(t, []string{"build"}, preview.SkippedSteps)
	require.Equal(t, []string{"consume", "notify"}, preview.RunnableSteps)

	retryRunID := "edited-retry"
	editResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/edit-retry", dagName, sourceRunID),
		api.EditRetryDAGRunJSONRequestBody{
			DagRunId: &retryRunID,
			Spec:     editedSpec,
		},
	).ExpectStatus(http.StatusOK).Send(t)

	var editBody api.EditRetryDAGRun200JSONResponse
	editResp.Unmarshal(t, &editBody)
	require.Equal(t, api.DAGRunId(retryRunID), editBody.DagRunId)
	require.True(t, editBody.Queued)
	require.Equal(t, []string{"build"}, editBody.SkippedSteps)

	test.ProcessQueuedInlineRun(t, server, dagName)
	retryStatus := waitForEditRetryStoredStatus(t, server, dagName, retryRunID, core.Succeeded)
	require.Len(t, retryStatus.Nodes, 3)
	require.Equal(t, core.NodeSkipped, retryStatus.Nodes[0].Status)
	require.True(t, retryStatus.Nodes[0].SkippedByRetry)
	require.Equal(t, core.NodeSucceeded, retryStatus.Nodes[1].Status)
	require.Equal(t, core.NodeSucceeded, retryStatus.Nodes[2].Status)

	rawResult, ok := retryStatus.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=from-source", rawResult)
	rawConsumed, ok := retryStatus.Nodes[1].OutputVariables.Load("CONSUMED")
	require.True(t, ok)
	require.Equal(t, "CONSUMED=from-source", rawConsumed)
}

func TestAPIEditRetryPreviewUsesPersistedParamsList(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "intg_edit_retry_params", MaxActiveRuns: 1},
		}
	}))

	dagName := "intg_edit_retry_params"
	sourceSpec := fmt.Sprintf(`name: %s
queue: intg_edit_retry_params
type: graph
params:
  - problem: ""
steps:
  - name: build
    run: %s
    output: RESULT
  - name: consume
%s
    depends:
      - build
`, dagName, editRetryEchoResultCommand(), indentStepField(portableDirectFailureStepYAML(t)))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &sourceSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	sourceRunID := "source-edit-retry-params"
	params := `problem="one two three"`
	server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{DagRunId: &sourceRunID, Params: &params},
	).ExpectStatus(http.StatusOK).Send(t)

	test.ProcessQueuedInlineRun(t, server, dagName)
	sourceStatus := waitForEditRetryStoredStatus(t, server, dagName, sourceRunID, core.Failed)
	sourceStatus.Params = "one two three"
	sourceStatus.ParamsList = []string{"problem=one two three"}

	attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, sourceRunID))
	require.NoError(t, err)
	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, *sourceStatus))
	require.NoError(t, attempt.Close(server.Context))

	editedSpec := fmt.Sprintf(`name: %s
queue: intg_edit_retry_params
type: graph
params:
  - problem: ""
steps:
  - name: build
%s
    output: RESULT
  - name: consume
    run: %s
    depends:
      - build
`, dagName, indentStepField(portableDirectFailureStepYAML(t)), editRetryEchoConsumedCommand())

	previewResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/edit-retry/preview", dagName, sourceRunID),
		api.PreviewEditRetryDAGRunJSONRequestBody{Spec: editedSpec},
	).ExpectStatus(http.StatusOK).Send(t)

	var preview api.PreviewEditRetryDAGRun200JSONResponse
	previewResp.Unmarshal(t, &preview)
	require.Empty(t, preview.Errors)
	require.Equal(t, []string{"build"}, preview.SkippedSteps)
	require.Equal(t, []string{"consume"}, preview.RunnableSteps)
}

func waitForEditRetryStoredStatus(t *testing.T, server test.Server, dagName, dagRunID string, expected core.Status) *exec.DAGRunStatus {
	t.Helper()

	var status *exec.DAGRunStatus
	require.Eventually(t, func() bool {
		attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, dagRunID))
		if err != nil {
			return false
		}
		status, err = attempt.ReadStatus(server.Context)
		return err == nil && status != nil && status.Status == expected
	}, intgTestTimeout(15*time.Second), 200*time.Millisecond)
	return status
}

func indentStepField(value string) string {
	return "    " + strings.ReplaceAll(value, "\n", "\n    ")
}

func editRetryEchoResultCommand() string {
	return `echo "from-source"`
}

func editRetryEchoConsumedCommand() string {
	return `echo "${RESULT}"`
}

func editRetryEchoDoneCommand() string {
	return `echo done`
}
