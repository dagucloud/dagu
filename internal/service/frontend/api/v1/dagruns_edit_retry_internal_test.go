// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/spec"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPreviewEditRetryDAGRun_SelectsCompletedOutputSteps(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")

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
	seedEditRetrySkippedSourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")

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
	seedEditRetrySourceAttemptWithParams(t, ctx, api.dagRunRepository, dag, "source-run")

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
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")

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
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")
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

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "edit-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Queued, status.Status)
	require.Len(t, status.Nodes, 3)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.True(t, status.Nodes[0].SkippedByRetry)
	require.NotNil(t, status.Nodes[0].OutputVariables)
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=from-source", raw)
	require.NotNil(t, status.Nodes[0].OutputValue)
	require.Equal(t, "from-source", *status.Nodes[0].OutputValue)
	require.NotNil(t, status.Nodes[0].OutputsValue)
	require.JSONEq(t, `{"legacy":"from-source"}`, *status.Nodes[0].OutputsValue)
	require.NotNil(t, status.Nodes[0].StepOutputsValue)
	require.JSONEq(t, `{"artifact":"from-source"}`, *status.Nodes[0].StepOutputsValue)
	require.Equal(t, ir.NodeNotStarted, status.Nodes[1].Status)

	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	require.Equal(t, dispatch.DispatchOperationRetry, task.Operation)
	require.Equal(t, "edit-run", task.DAGRunID)
	require.NotNil(t, task.PreviousStatus)
	previousStatus := task.PreviousStatus
	require.Equal(t, ir.Queued, previousStatus.Status)
	require.True(t, previousStatus.Nodes[0].SkippedByRetry)
}

func TestEditRetryDAGRun_RejectsDistributedBuildWorkflow(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")
	recorder := &retryCoordinatorRecorder{}
	api.coordinatorCli = recorder

	resp, err := api.EditRetryDAGRun(ctx, openapiv1.EditRetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "source-run",
		Body: &openapiv1.EditRetryDAGRunJSONRequestBody{
			DagRunId: ptrOf("edit-run"),
			Spec:     editRetryBuildYAMLWithWorkerSelector(),
		},
	})
	require.Nil(t, resp)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Contains(t, apiErr.Message, "build workflows require local execution")
	require.Empty(t, recorder.dispatched)
}

func TestSkippedEditRetryNodeStatePreservesHumanTaskCompletion(t *testing.T) {
	outputs := `{"decision":"approve"}`
	source := &ir.Node{
		HumanTaskInput:         json.RawMessage(`{"decision":"approve"}`),
		HumanTaskCompletedBy:   "Alice",
		HumanTaskCompletedByID: "user-1",
		StepOutputsValue:       &outputs,
	}

	state := skippedEditRetryNodeState(source)

	require.JSONEq(t, `{"decision":"approve"}`, string(state.HumanTaskInput))
	require.Equal(t, "Alice", state.HumanTaskCompletedBy)
	require.Equal(t, "user-1", state.HumanTaskCompletedByID)
	require.NotNil(t, state.StepOutputsValue)
	require.JSONEq(t, outputs, *state.StepOutputsValue)
}

func TestEditRetryDAGRun_InheritsRuntimeProfile(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())

	profileStore, err := persiststore.NewProfileStore(testutil.NewMemoryBackend().Collection("profiles"))
	require.NoError(t, err)
	prof, err := profilepkg.New(profilepkg.CreateInput{Name: "prod", CreatedBy: "test"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))
	api.profileStore = profileStore

	seedEditRetrySourceAttemptWithProfileName(t, ctx, api.dagRunRepository, dag, "source-run", "prod")
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

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "edit-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "prod", status.ProfileName)

	require.Len(t, recorder.dispatched, 1)
	require.Equal(t, "prod", recorder.dispatched[0].ProfileName)
	require.NotNil(t, recorder.dispatched[0].PreviousStatus)
	require.Equal(t, "prod", recorder.dispatched[0].PreviousStatus.ProfileName)
}

func TestEditRetryDAGRun_CopiesWorkDirAndRewritesSkippedOutputs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())

	attempt, err := api.dagRunRepository.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), "source-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	sourceRef := ir.NewDAGRunRef(dag.Name, "source-run")
	sourceWorkDir, err := api.dagRunRepository.MaterializeWorkspace(ctx, dagrun.WorkspaceRef{DAGRun: sourceRef})
	require.NoError(t, err)
	sourceOutputPath := filepath.Join(sourceWorkDir, "result.txt")

	status := ir.NewStatusBuilder(dag).Create(
		"source-run",
		ir.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		ir.WithAttemptID(attempt.ID()),
		ir.WithFinishedAt(time.Now().Add(-time.Minute)),
		ir.WithError("consume failed"),
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = ir.NodeSucceeded
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT="+sourceOutputPath)
	status.Nodes[1].Status = ir.NodeFailed
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, os.WriteFile(sourceOutputPath, []byte("from-source-work-dir"), 0o600))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	require.NoError(t, api.dagRunRepository.SnapshotWorkspace(ctx, dagrun.WorkspaceRef{DAGRun: sourceRef}, sourceWorkDir))

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

	newAttempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "edit-run"))
	require.NoError(t, err)
	newWorkDir, err := api.dagRunRepository.MaterializeWorkspace(ctx, dagrun.WorkspaceRef{
		DAGRun: ir.NewDAGRunRef(dag.Name, "edit-run"),
	})
	require.NoError(t, err)
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
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")
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
	previousStatus := recorder.dispatched[0].PreviousStatus
	require.NotNil(t, previousStatus)
	require.Len(t, previousStatus.Nodes, 3)
	require.Equal(t, ir.NodeNotStarted, previousStatus.Nodes[0].Status)
	require.False(t, previousStatus.Nodes[0].SkippedByRetry)
}

func TestEditRetryDAGRun_RejectsIneligibleRequestedSkipStep(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "source-run")

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

func TestRescheduleDAGRunPreservesSnapshotWorkingDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	yamlWorkingDir := filepath.Join(tmpDir, "from-yaml")
	snapshotWorkingDir := filepath.Join(tmpDir, "from-snapshot")
	api, dag := setupEditRetryAPI(t, tmpDir, fmt.Sprintf(`
name: reschedule_workdir
working_dir: %q
steps:
  - name: run
    run: echo ok
`, yamlWorkingDir))
	dag.WorkingDir = snapshotWorkingDir

	attempt, err := api.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), "source-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	status := ir.NewStatusBuilder(dag).Create("source-run", ir.Succeeded, 0, time.Now(), ir.WithAttemptID(attempt.ID()))
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	api.config.Queues.Enabled = true
	api.queueStore = persiststore.NewQueueStore(testutil.NewMemoryBackend().Collection("queue"))
	result, err := api.rescheduleDAGRun(ctx, dag.Name, "source-run", rescheduleDAGRunOptions{newDagRunID: "rescheduled-run"})
	require.NoError(t, err)
	require.True(t, result.queued)

	rescheduled, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, result.newDagRunID))
	require.NoError(t, err)
	rescheduledDAG, err := rescheduled.ReadDAG(ctx)
	require.NoError(t, err)
	rescheduledDAG, err = spec.RebuildFromYAML(ctx, rescheduledDAG)
	require.NoError(t, err)
	require.Equal(t, snapshotWorkingDir, rescheduledDAG.WorkingDir)
	require.True(t, rescheduledDAG.WorkingDirExplicit)
}

func setupEditRetryAPI(t *testing.T, tmpDir string, yamlContent string) (*API, *ir.DAG) {
	t.Helper()

	dag, err := spec.LoadYAML(context.Background(), []byte(yamlContent))
	require.NoError(t, err)

	dagRunRepository := runtestutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	api := &API{
		dagRunRepository: dagRunRepository,
		config: &config.Config{
			Paths: config.PathsConfig{
				LogDir:      filepath.Join(tmpDir, "logs"),
				ArtifactDir: filepath.Join(tmpDir, "artifacts"),
			},
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionRunDAGs:   true,
					config.PermissionWriteDAGs: true,
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
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	dagRunID string,
) {
	t.Helper()
	seedEditRetrySourceAttemptWithProfileName(t, ctx, repository, dag, dagRunID, "")
}

func seedEditRetrySourceAttemptWithProfileName(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	dagRunID string,
	profileName string,
) {
	t.Helper()
	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	opts := []ir.StatusOption{
		ir.WithAttemptID(attempt.ID()),
		ir.WithFinishedAt(time.Now().Add(-time.Minute)),
		ir.WithError("consume failed"),
	}
	if profileName != "" {
		opts = append(opts, ir.WithRuntimeProfile(profileName, "", nil))
	}
	status := ir.NewStatusBuilder(dag).Create(
		dagRunID,
		ir.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		opts...,
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = ir.NodeSucceeded
	status.Nodes[0].StartedAt = stringutil.FormatTime(time.Now().Add(-2 * time.Minute))
	status.Nodes[0].FinishedAt = stringutil.FormatTime(time.Now().Add(-90 * time.Second))
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[0].OutputValue = ptrOf("from-source")
	status.Nodes[0].OutputsValue = ptrOf(`{"legacy":"from-source"}`)
	status.Nodes[0].StepOutputsValue = ptrOf(`{"artifact":"from-source"}`)
	status.Nodes[1].Status = ir.NodeFailed
	status.Nodes[1].StartedAt = stringutil.FormatTime(time.Now().Add(-80 * time.Second))
	status.Nodes[1].FinishedAt = stringutil.FormatTime(time.Now().Add(-70 * time.Second))
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func seedEditRetrySkippedSourceAttempt(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	dagRunID string,
) {
	t.Helper()

	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	status := ir.NewStatusBuilder(dag).Create(
		dagRunID,
		ir.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		ir.WithAttemptID(attempt.ID()),
		ir.WithFinishedAt(time.Now().Add(-time.Minute)),
		ir.WithError("consume failed"),
	)
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = ir.NodeSkipped
	status.Nodes[0].SkippedByRetry = true
	status.Nodes[0].StartedAt = stringutil.FormatTime(time.Now().Add(-2 * time.Minute))
	status.Nodes[0].FinishedAt = stringutil.FormatTime(time.Now().Add(-90 * time.Second))
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[1].Status = ir.NodeFailed
	status.Nodes[1].StartedAt = stringutil.FormatTime(time.Now().Add(-80 * time.Second))
	status.Nodes[1].FinishedAt = stringutil.FormatTime(time.Now().Add(-70 * time.Second))
	status.Nodes[1].Error = "consume failed"

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func seedEditRetrySourceAttemptWithParams(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	dagRunID string,
) {
	t.Helper()

	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	status := ir.NewStatusBuilder(dag).Create(
		dagRunID,
		ir.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		ir.WithAttemptID(attempt.ID()),
		ir.WithFinishedAt(time.Now().Add(-time.Minute)),
		ir.WithError("consume failed"),
	)
	status.Params = "one two three"
	status.ParamsList = []string{"problem=one two three"}
	require.Len(t, status.Nodes, 2)
	status.Nodes[0].Status = ir.NodeSucceeded
	status.Nodes[0].OutputVariables = &collections.SyncMap{}
	status.Nodes[0].OutputVariables.Store("RESULT", "RESULT=from-source")
	status.Nodes[1].Status = ir.NodeFailed
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

func editRetryBuildYAMLWithWorkerSelector() string {
	return `
name: edit_retry_test
type: build
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
