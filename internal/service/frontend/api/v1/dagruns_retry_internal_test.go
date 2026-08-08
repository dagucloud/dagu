// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/stretchr/testify/require"
)

type retryCoordinatorRecorder struct {
	stubCoordinatorClient
	dispatched  []*exec.DispatchTask
	dispatchErr error
}

var _ coordinator.Client = (*retryCoordinatorRecorder)(nil)

func (c *retryCoordinatorRecorder) Dispatch(_ context.Context, req exec.DispatchRequest) error {
	c.dispatched = append(c.dispatched, req.Task)
	return c.dispatchErr
}

func TestRetryDAGRun_DispatchesRetryToCoordinator(t *testing.T) {
	ctx := auth.WithUser(context.Background(), &auth.User{Username: "alice"})
	tmpDir := t.TempDir()

	dagFile := filepath.Join(tmpDir, "distributed-retry.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: distributed_retry_dag
worker_selector:
  region: apac
steps:
  - name: main
    run: echo distributed retry
`), 0o600))

	dag, err := spec.Load(ctx, dagFile)
	require.NoError(t, err)

	dagRunStore := dagrun.New(filepath.Join(tmpDir, "dag-runs"))
	attempt, err := dagRunStore.CreateAttempt(
		ctx,
		dag,
		time.Now().Add(-2*time.Minute),
		"distributed-run",
		exec.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		"distributed-run",
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("step failed"),
	)
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeFailed
	status.Nodes[0].Error = "step failed"
	status.Nodes[0].FinishedAt = exec.FormatTime(time.Now().Add(-time.Minute))

	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	coordinatorCli := &retryCoordinatorRecorder{}
	api := &API{
		dagRunStore: dagRunStore,
		config: &config.Config{
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionRunDAGs: true,
				},
			},
		},
		coordinatorCli:  coordinatorCli,
		defaultExecMode: config.ExecutionModeLocal,
	}

	resp, err := api.RetryDAGRun(ctx, openapiv1.RetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "distributed-run",
		Body: &openapiv1.RetryDAGRunJSONRequestBody{
			DagRunId: "distributed-run",
		},
	})
	require.NoError(t, err)
	_, ok := resp.(openapiv1.RetryDAGRun200Response)
	require.True(t, ok)

	require.Len(t, coordinatorCli.dispatched, 1)
	task := coordinatorCli.dispatched[0]
	require.Equal(t, exec.DispatchOperationRetry, task.Operation)
	require.Equal(t, dag.Name, task.Target)
	require.Equal(t, "distributed-run", task.DAGRunID)
	require.Equal(t, dag.WorkerSelector, task.WorkerSelector)
	require.Equal(t, "alice", task.TriggerActor)
	require.NotNil(t, task.PreviousStatus)
}

func TestRetryDAGRun_RejectsDistributedBuildWorkflow(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dagFile := filepath.Join(tmpDir, "build-retry.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: build_retry_dag
type: build
worker_selector:
  region: apac
steps:
  - name: main
    run: echo build retry
`), 0o600))
	dag, err := spec.Load(ctx, dagFile)
	require.NoError(t, err)

	dagRunStore := dagrun.New(filepath.Join(tmpDir, "dag-runs"))
	attempt, err := dagRunStore.CreateAttempt(
		ctx,
		dag,
		time.Now().Add(-2*time.Minute),
		"build-run",
		exec.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)
	status := transform.NewStatusBuilder(dag).Create(
		"build-run",
		core.Failed,
		0,
		time.Now().Add(-2*time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-time.Minute)),
		transform.WithError("step failed"),
	)
	require.NotEmpty(t, status.Nodes)
	status.Nodes[0].Status = core.NodeFailed
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	coordinatorCli := &retryCoordinatorRecorder{}
	api := &API{
		dagRunStore: dagRunStore,
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionRunDAGs: true,
		}}},
		coordinatorCli:  coordinatorCli,
		defaultExecMode: config.ExecutionModeLocal,
	}

	resp, err := api.RetryDAGRun(ctx, openapiv1.RetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "build-run",
		Body: &openapiv1.RetryDAGRunJSONRequestBody{
			DagRunId: "build-run",
		},
	})
	require.Nil(t, resp)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Contains(t, apiErr.Message, "build workflows require local execution")
	require.Empty(t, coordinatorCli.dispatched)
}

func TestRetryDAGRun_RejectsMismatchedBodyDagRunID(t *testing.T) {
	ctx := context.Background()

	api := &API{
		config: &config.Config{
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionRunDAGs: true,
				},
			},
		},
	}

	resp, err := api.RetryDAGRun(ctx, openapiv1.RetryDAGRunRequestObject{
		Name:     "distributed_retry_dag",
		DagRunId: "path-run",
		Body: &openapiv1.RetryDAGRunJSONRequestBody{
			DagRunId: "body-run",
		},
	})
	require.Nil(t, resp)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
	require.Contains(t, apiErr.Message, "must match the path parameter")
}

func TestRetryDAGRun_RejectsWaitingDAGAndStepRetry(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{
		Name:  "waiting_retry_dag",
		Steps: []core.Step{{Name: "approve"}},
	}
	dagRunStore := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	attempt, err := dagRunStore.CreateAttempt(
		ctx,
		dag,
		time.Now().Add(-time.Minute),
		"waiting-run",
		exec.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		"waiting-run",
		core.Waiting,
		0,
		time.Now().Add(-time.Minute),
		transform.WithAttemptID(attempt.ID()),
	)
	require.Len(t, status.Nodes, 1)
	status.Nodes[0].Status = core.NodeSucceeded
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	apiServer := &API{
		dagRunStore: dagRunStore,
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionRunDAGs: true,
		}}},
	}

	tests := []struct {
		name     string
		stepName string
	}{
		{name: "DAG"},
		{name: "Step", stepName: "approve"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &openapiv1.RetryDAGRunJSONRequestBody{DagRunId: "waiting-run"}
			if test.stepName != "" {
				body.StepName = &test.stepName
			}
			resp, err := apiServer.RetryDAGRun(ctx, openapiv1.RetryDAGRunRequestObject{
				Name:     dag.Name,
				DagRunId: "waiting-run",
				Body:     body,
			})
			require.Nil(t, resp)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
			require.Equal(t, openapiv1.ErrorCodeConflict, apiErr.Code)
			require.Contains(t, apiErr.Message, "is waiting and cannot be retried")
		})
	}
}

func TestRetryDAGRun_ResolvesLatestPathDagRunID(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	dagFile := filepath.Join(tmpDir, "distributed-retry-latest.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: distributed_retry_latest_dag
worker_selector:
  region: apac
steps:
  - name: main
    run: echo distributed retry latest
`), 0o600))

	dag, err := spec.Load(ctx, dagFile)
	require.NoError(t, err)

	dagRunStore := dagrun.New(filepath.Join(tmpDir, "dag-runs"))
	attempt, err := dagRunStore.CreateAttempt(
		ctx,
		dag,
		time.Now().Add(-time.Minute),
		"latest-run",
		exec.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)

	status := transform.NewStatusBuilder(dag).Create(
		"latest-run",
		core.Failed,
		0,
		time.Now().Add(-time.Minute),
		transform.WithAttemptID(attempt.ID()),
		transform.WithFinishedAt(time.Now().Add(-30*time.Second)),
		transform.WithError("step failed"),
	)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	coordinatorCli := &retryCoordinatorRecorder{}
	api := &API{
		dagRunStore: dagRunStore,
		config: &config.Config{
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionRunDAGs: true,
				},
			},
		},
		coordinatorCli:  coordinatorCli,
		defaultExecMode: config.ExecutionModeLocal,
	}

	resp, err := api.RetryDAGRun(ctx, openapiv1.RetryDAGRunRequestObject{
		Name:     dag.Name,
		DagRunId: "latest",
	})
	require.NoError(t, err)
	_, ok := resp.(openapiv1.RetryDAGRun200Response)
	require.True(t, ok)

	require.Len(t, coordinatorCli.dispatched, 1)
	require.Equal(t, "latest-run", coordinatorCli.dispatched[0].DAGRunID)
}

func TestRetryDAGRun_TargetsPersistedChildStepFromRoot(t *testing.T) {
	ctx := context.Background()
	rootRef := exec.NewDAGRunRef("root_retry_dag", "root-run")
	rootStep := core.Step{
		Name:           "parallel-children",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeParallel},
		SubDAG:         &core.SubDAG{Name: "child_retry_dag"},
		Parallel:       &core.ParallelConfig{},
	}
	rootDAG := &core.DAG{
		Name:           rootRef.Name,
		Steps:          []core.Step{rootStep},
		WorkerSelector: map[string]string{"region": "apac"},
	}
	childStep := core.Step{Name: "target-step"}
	childDAG := &core.DAG{Name: "child_retry_dag", Steps: []core.Step{childStep}}
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))

	rootAttempt, err := store.CreateAttempt(ctx, rootDAG, time.Now().Add(-time.Minute), rootRef.ID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	rootStatus := exec.DAGRunStatus{
		Root:      rootRef,
		Name:      rootRef.Name,
		DAGRunID:  rootRef.ID,
		AttemptID: rootAttempt.ID(),
		Status:    core.Failed,
		Nodes: []*exec.Node{{
			Step:   rootStep,
			Status: core.NodeFailed,
			SubRuns: []exec.SubDAGRun{
				{DAGRunID: "child-success", DAGName: childDAG.Name, Params: "ITEM=one"},
				{DAGRunID: "child-target", DAGName: childDAG.Name, Params: "ITEM=two"},
			},
		}},
	}
	require.NoError(t, rootAttempt.Open(ctx))
	require.NoError(t, rootAttempt.Write(ctx, rootStatus))
	require.NoError(t, rootAttempt.Close(ctx))

	childAttempt, err := store.CreateAttempt(ctx, childDAG, time.Now(), "child-target", exec.NewDAGRunAttemptOptions{RootDAGRun: &rootRef})
	require.NoError(t, err)
	childStatus := exec.DAGRunStatus{
		Root:      rootRef,
		Parent:    rootRef,
		Name:      childDAG.Name,
		DAGRunID:  "child-target",
		AttemptID: childAttempt.ID(),
		Status:    core.Succeeded,
		Nodes:     []*exec.Node{{Step: childStep, Status: core.NodeSucceeded}},
	}
	require.NoError(t, childAttempt.Open(ctx))
	require.NoError(t, childAttempt.Write(ctx, childStatus))
	require.NoError(t, childAttempt.Close(ctx))

	coordinatorCli := &retryCoordinatorRecorder{}
	apiServer := &API{
		dagRunStore: store,
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionRunDAGs: true,
		}}},
		coordinatorCli:  coordinatorCli,
		defaultExecMode: config.ExecutionModeLocal,
	}
	subRunID := "child-target"
	stepName := childStep.Name
	request := openapiv1.RetryDAGRunRequestObject{
		Name:     rootRef.Name,
		DagRunId: rootRef.ID,
		Body: &openapiv1.RetryDAGRunJSONRequestBody{
			DagRunId:    rootRef.ID,
			SubDAGRunId: &subRunID,
			StepName:    &stepName,
		},
	}

	resp, err := apiServer.RetryDAGRun(ctx, request)
	require.NoError(t, err)
	_, ok := resp.(openapiv1.RetryDAGRun200Response)
	require.True(t, ok)
	require.Len(t, coordinatorCli.dispatched, 1)
	task := coordinatorCli.dispatched[0]
	require.Equal(t, rootStep.Name, task.Step)
	require.NotNil(t, task.PreviousStatus)
	require.Equal(t, core.Failed, task.PreviousStatus.Status)
	path, err := exec.ParseRetryPath(task.RetryPath)
	require.NoError(t, err)
	require.Equal(t, childStep.Name, path.Step)
	require.Equal(t, subRunID, path.Hops[0].RunID)
}
