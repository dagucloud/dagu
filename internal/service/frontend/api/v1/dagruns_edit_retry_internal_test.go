// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

func TestPreviewEditRetryDAGRun_SelectsCompletedOutputSteps(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")

	resp, err := api.PreviewEditRetryDAGRun(ctx, openapiv1.PreviewEditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.PreviewEditRetryDAGRunJSONRequestBody{
			Spec: editRetryEditedYAML(),
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.PreviewEditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Empty(t, body.Errors)
	require.Equal(t, dag.Name, body.DagName)
	require.Equal(t, []string{"build"}, body.SkippedSteps)
	require.Equal(t, []string{"consume", "notify"}, body.RunnableSteps)
	require.Len(t, body.Steps, 3)
	require.Equal(t, "build", body.Steps[0].Name)
	require.Equal(t, "consume", body.Steps[1].Name)
	require.Equal(t, "notify", body.Steps[2].Name)
	require.Empty(t, body.IneligibleSteps)
}

func TestPreviewEditRetryDAGRun_SelectsPreviousEditRetrySkippedSteps(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySkippedSourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")

	resp, err := api.PreviewEditRetryDAGRun(ctx, openapiv1.PreviewEditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.PreviewEditRetryDAGRunJSONRequestBody{
			Spec: editRetryEditedYAML(),
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.PreviewEditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Empty(t, body.Errors)
	require.Equal(t, []string{"build"}, body.SkippedSteps)
	require.Equal(t, []string{"consume", "notify"}, body.RunnableSteps)
	require.Empty(t, body.IneligibleSteps)
}

func TestPreviewEditRetryDAGRun_UsesPersistedParamsListInsteadOfRawPositionalParams(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAMLWithParams())
	seedEditRetrySourceAttemptWithParams(t, ctx, api.dagRunStore, dag, "source-run")

	resp, err := api.PreviewEditRetryDAGRun(ctx, openapiv1.PreviewEditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.PreviewEditRetryDAGRunJSONRequestBody{
			Spec: editRetryEditedYAMLWithParams(),
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.PreviewEditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Empty(t, body.Errors)
	require.Equal(t, []string{"build"}, body.SkippedSteps)
	require.Equal(t, []string{"consume"}, body.RunnableSteps)
}

func TestPreviewEditRetryDAGRun_ReturnsEmptyArraysOnValidationError(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")

	resp, err := api.PreviewEditRetryDAGRun(ctx, openapiv1.PreviewEditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.PreviewEditRetryDAGRunJSONRequestBody{
			Spec: "",
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.PreviewEditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Equal(t, []string{"spec is required"}, body.Errors)
	require.NotNil(t, body.SkippedSteps)
	require.NotNil(t, body.RunnableSteps)
	require.NotNil(t, body.Steps)
	require.NotNil(t, body.IneligibleSteps)
	require.NotNil(t, body.Warnings)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"skippedSteps":[]`)
	require.Contains(t, string(raw), `"runnableSteps":[]`)
	require.Contains(t, string(raw), `"steps":[]`)
	require.Contains(t, string(raw), `"ineligibleSteps":[]`)
	require.Contains(t, string(raw), `"warnings":[]`)
}

func TestEditRetryDAGRun_DispatchesSeededRetryWithSkippedOutputs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")
	recorder := &retryCoordinatorRecorder{}
	api.coordinatorCli = recorder

	resp, err := api.EditRetryDAGRun(ctx, openapiv1.EditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.EditRetryDAGRunJSONRequestBody{
			DagRunId: ptrOf("edit-run"),
			Spec:     editRetryEditedYAMLWithWorkerSelector(),
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.EditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Equal(t, openapiv1.DAGRunId("edit-run"), body.DagRunId)
	require.False(t, body.Queued)
	require.Equal(t, []string{"build"}, body.SkippedSteps)
	require.Equal(t, []string{"consume", "notify"}, body.StartedSteps)

	attempt, err := api.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "edit-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	require.Len(t, status.Nodes, 3)
	require.Equal(t, core.NodeSkipped, status.Nodes[0].Status)
	require.True(t, status.Nodes[0].SkippedByRetry)
	require.NotNil(t, status.Nodes[0].OutputVariables)
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=from-source", raw)
	require.Equal(t, core.NodeNotStarted, status.Nodes[1].Status)

	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	require.Equal(t, coordinatorv1.Operation_OPERATION_RETRY, task.Operation)
	require.Equal(t, "edit-run", task.DagRunId)
	require.NotNil(t, task.PreviousStatus)
	previousStatus, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	require.NoError(t, err)
	require.Equal(t, core.Queued, previousStatus.Status)
	require.True(t, previousStatus.Nodes[0].SkippedByRetry)
}

func TestEditRetryDAGRun_CopiesWorkDirAndRewritesSkippedOutputs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())

	attempt, err := api.dagRunStore.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), "source-run", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	sourceWorkDir := attempt.WorkDir()
	sourceOutputPath := filepath.Join(sourceWorkDir, "result.txt")

	status := transform.NewStatusBuilder(dag).Create(
		"source-run",
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("consume failed"),
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = core.NodeSucceeded
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT="+sourceOutputPath)
	status.Nodes[1].Status = core.NodeFailed
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, os.WriteFile(sourceOutputPath, []byte("from-source-work-dir"), 0o600))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	recorder := &retryCoordinatorRecorder{}
	api.coordinatorCli = recorder
	resp, err := api.EditRetryDAGRun(ctx, openapiv1.EditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.EditRetryDAGRunJSONRequestBody{
			DagRunId: ptrOf("edit-run"),
			Spec:     editRetryEditedYAMLWithWorkerSelector(),
		},
	})
	require.NoError(t, err)
	_, ok := resp.(openapiv1.EditRetryDAGRun200JSONResponse)
	require.True(t, ok)

	newAttempt, err := api.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "edit-run"))
	require.NoError(t, err)
	newWorkDir := newAttempt.WorkDir()
	require.NotEqual(t, sourceWorkDir, newWorkDir)

	newStatus, err := newAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	raw, ok := newStatus.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	newOutputPath := filepath.Join(newWorkDir, "result.txt")
	require.Equal(t, "RESULT="+newOutputPath, raw)

	content, err := os.ReadFile(newOutputPath) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "from-source-work-dir", string(content))
	require.Len(t, recorder.dispatched, 1)
}

func TestEditRetryDAGRun_ExplicitEmptySkipStepsRunsAllSteps(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")
	recorder := &retryCoordinatorRecorder{}
	api.coordinatorCli = recorder

	resp, err := api.EditRetryDAGRun(ctx, openapiv1.EditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.EditRetryDAGRunJSONRequestBody{
			DagRunId:  ptrOf("edit-run"),
			SkipSteps: ptrOf([]string{}),
			Spec:      editRetryEditedYAMLWithWorkerSelector(),
		},
	})
	require.NoError(t, err)

	body, ok := resp.(openapiv1.EditRetryDAGRun200JSONResponse)
	require.True(t, ok)
	require.Empty(t, body.SkippedSteps)
	require.Equal(t, []string{"build", "consume", "notify"}, body.StartedSteps)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"skippedSteps":[]`)

	require.Len(t, recorder.dispatched, 1)
	previousStatus, err := convert.ProtoToDAGRunStatus(recorder.dispatched[0].PreviousStatus)
	require.NoError(t, err)
	require.Len(t, previousStatus.Nodes, 3)
	require.Equal(t, core.NodeNotStarted, previousStatus.Nodes[0].Status)
	require.False(t, previousStatus.Nodes[0].SkippedByRetry)
}

func TestEditRetryDAGRun_RejectsIneligibleRequestedSkipStep(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunStore, dag, "source-run")

	resp, err := api.EditRetryDAGRun(ctx, openapiv1.EditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.EditRetryDAGRunJSONRequestBody{
			DagRunId:  ptrOf("edit-run"),
			SkipSteps: ptrOf([]string{"consume"}),
			Spec:      editRetryEditedYAML(),
		},
	})
	require.Nil(t, resp)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Contains(t, apiErr.Message, `skipSteps contains ineligible step "consume"`)
}

func TestLoadInlineDAGDoesNotFreezeProcessWorkingDir(t *testing.T) {
	ctx := context.Background()
	oldRunWorkDir, err := os.MkdirTemp("", "dagu-inline-old-workdir-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(oldRunWorkDir)
	})

	previousWD, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	require.NoError(t, os.Chdir(oldRunWorkDir))

	api := &API{}
	dag, cleanup, err := api.loadInlineDAG(ctx, `
name: inline_workdir_test
steps:
  - name: run
    run: pwd
`, ptrOf("inline_workdir_test"), "new-run")
	require.NoError(t, err)
	defer cleanup()

	require.False(t, dag.WorkingDirExplicit)
	require.Empty(t, dag.WorkingDir)
}

func setupEditRetryAPI(t *testing.T, tmpDir string, yamlContent string) (*API, *core.DAG) {
	t.Helper()

	dag, err := spec.LoadYAML(context.Background(), []byte(yamlContent))
	require.NoError(t, err)

	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	api := &API{
		dagRunStore: dagRunStore,
		config: &config.Config{
			Paths: config.PathsConfig{
				LogDir:      filepath.Join(tmpDir, "logs"),
				ArtifactDir: filepath.Join(tmpDir, "artifacts"),
			},
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionRunDAGs: true,
				},
			},
		},
		defaultExecMode: config.ExecutionModeLocal,
	}
	return api, dag
}

func seedEditRetrySourceAttempt(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	dagRunID string,
) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		dagRunID,
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("consume failed"),
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = core.NodeSucceeded
	status.Nodes[0].StartedAt = exec.FormatTime(time.Now().Add(-2 * time.Minute))
	status.Nodes[0].FinishedAt = exec.FormatTime(time.Now().Add(-90 * time.Second))
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[1].Status = core.NodeFailed
	status.Nodes[1].StartedAt = exec.FormatTime(time.Now().Add(-80 * time.Second))
	status.Nodes[1].FinishedAt = exec.FormatTime(time.Now().Add(-70 * time.Second))
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func seedEditRetrySkippedSourceAttempt(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	dagRunID string,
) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		dagRunID,
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("consume failed"),
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = core.NodeSkipped
	status.Nodes[0].SkippedByRetry = true
	status.Nodes[0].StartedAt = exec.FormatTime(time.Now().Add(-2 * time.Minute))
	status.Nodes[0].FinishedAt = exec.FormatTime(time.Now().Add(-90 * time.Second))
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[1].Status = core.NodeFailed
	status.Nodes[1].StartedAt = exec.FormatTime(time.Now().Add(-80 * time.Second))
	status.Nodes[1].FinishedAt = exec.FormatTime(time.Now().Add(-70 * time.Second))
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func seedEditRetrySourceAttemptWithParams(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	dagRunID string,
) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		dagRunID,
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("consume failed"),
	)
	status.Params = "one two three"
	status.ParamsList = []string{"problem=one two three"}
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = core.NodeSucceeded
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[1].Status = core.NodeFailed
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func editRetrySourceYAML() string {
	return `
name: edit_retry_test
type: graph
steps:
  - name: build
    run: echo "RESULT=from-source"
    output: RESULT
  - name: consume
    run: exit 1
    depends:
      - build
`
}

func editRetrySourceYAMLWithParams() string {
	return `
name: edit_retry_test
type: graph
params:
  - problem: ""
steps:
  - name: build
    run: echo "$problem"
    output: RESULT
  - name: consume
    run: exit 1
    depends:
      - build
`
}

func editRetryEditedYAML() string {
	return `
name: edit_retry_test
type: graph
steps:
  - name: build
    run: exit 99
    output: RESULT
  - name: consume
    run: echo "$RESULT"
    depends:
      - build
  - name: notify
    run: echo done
    depends:
      - consume
`
}

func editRetryEditedYAMLWithParams() string {
	return `
name: edit_retry_test
type: graph
params:
  - problem: ""
steps:
  - name: build
    run: exit 99
    output: RESULT
  - name: consume
    run: echo "$RESULT $problem"
    depends:
      - build
`
}

func editRetryEditedYAMLWithWorkerSelector() string {
	return `
name: edit_retry_test
type: graph
worker_selector:
  region: apac
steps:
  - name: build
    run: exit 99
    output: RESULT
  - name: consume
    run: echo "$RESULT"
    depends:
      - build
  - name: notify
    run: echo done
    depends:
      - consume
`
}
