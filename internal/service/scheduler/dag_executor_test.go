// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

func TestDAGExecutor(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	testDAG := th.DAG(t, `
steps:
  - name: test-step
    run: echo "test"
`)
	coordinatorCli := coordinator.New(th.ServiceRegistry, coordinator.DefaultConfig())

	dagExecutor := scheduler.NewDAGExecutor(coordinatorCli, th.SubCmdBuilder, config.ExecutionModeLocal, "", nil)
	t.Cleanup(func() {
		dagExecutor.Close(th.Context)
	})

	loadDAGWithWorkerSelector := func(t *testing.T) *core.DAG {
		t.Helper()
		dag, err := spec.Load(context.Background(), testDAG.Location)
		require.NoError(t, err)
		dag.WorkerSelector = map[string]string{"type": "test-worker"}
		return dag
	}

	t.Run("HandleJob_DistributedStart_EnqueuesDAG", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.HandleJob(
			context.Background(),
			dag,
			coordinatorv1.Operation_OPERATION_START,
			"handle-job-test-123",
			core.TriggerTypeScheduler,
			time.Time{},
		)

		require.NoError(t, err)
	})

	t.Run("ExecuteDAG_Distributed_DispatchesDirectly", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.ExecuteDAG(
			context.Background(),
			dag,
			coordinatorv1.Operation_OPERATION_START,
			"execute-dag-test-456",
			nil,
			core.TriggerTypeScheduler,
			"",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to dispatch task")
	})

	t.Run("HandleJob_Local_ExecutesDirectly", func(t *testing.T) {
		localExecutor := scheduler.NewDAGExecutor(nil, th.SubCmdBuilder, config.ExecutionModeLocal, "", nil)

		dag, err := spec.Load(context.Background(), testDAG.Location)
		require.NoError(t, err)

		err = localExecutor.HandleJob(
			context.Background(),
			dag,
			coordinatorv1.Operation_OPERATION_START,
			"handle-job-local-789",
			core.TriggerTypeScheduler,
			time.Time{},
		)
		require.NoError(t, err, "local execution with nil coordinator should succeed")
	})

	t.Run("HandleJob_Retry_BypassesEnqueue", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.HandleJob(
			context.Background(),
			dag,
			coordinatorv1.Operation_OPERATION_RETRY,
			"handle-job-retry-999",
			core.TriggerTypeScheduler,
			time.Time{},
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to dispatch task")
	})
}

func TestDAGExecutor_DistributedRetryPassesQueuedParams(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "", nil)

	dag := &core.DAG{
		Name:           "queued-param-dag",
		YamlData:       []byte("name: queued-param-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}
	previousStatus := &exec1.DAGRunStatus{
		Status:     core.Queued,
		Params:     "content_hash=sha256:abc123",
		ParamsList: []string{"content_hash=sha256:abc123"},
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(),
		dag,
		coordinatorv1.Operation_OPERATION_RETRY,
		"queued-param-run",
		previousStatus,
		core.TriggerTypeManual,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.task)
	require.Equal(t, "content_hash=sha256:abc123", dispatcher.task.Params)
	require.NotNil(t, dispatcher.task.PreviousStatus)
}

type capturingDispatcher struct {
	task *coordinatorv1.Task
}

func (d *capturingDispatcher) Dispatch(_ context.Context, task *coordinatorv1.Task) error {
	d.task = task
	return nil
}

func (d *capturingDispatcher) Cleanup(context.Context) error {
	return nil
}

func (d *capturingDispatcher) GetDAGRunStatus(context.Context, string, string, *exec1.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	return nil, nil
}

func (d *capturingDispatcher) RequestCancel(context.Context, string, string, *exec1.DAGRunRef) error {
	return nil
}
