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

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

type retryCoordinatorRecorder struct {
	stubCoordinatorClient
	dispatched  []*coordinatorv1.Task
	dispatchErr error
}

var _ coordinator.Client = (*retryCoordinatorRecorder)(nil)

func (c *retryCoordinatorRecorder) Dispatch(_ context.Context, task *coordinatorv1.Task) error {
	c.dispatched = append(c.dispatched, task)
	return c.dispatchErr
}

func TestRetryDAGRun_DispatchesRetryToCoordinator(t *testing.T) {
	ctx := context.Background()
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

	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
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
	require.Equal(t, coordinatorv1.Operation_OPERATION_RETRY, task.Operation)
	require.Equal(t, dag.Name, task.Target)
	require.Equal(t, "distributed-run", task.DagRunId)
	require.Equal(t, dag.WorkerSelector, task.WorkerSelector)
	require.NotNil(t, task.PreviousStatus)
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

	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
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
	require.Equal(t, "latest-run", coordinatorCli.dispatched[0].DagRunId)
}
