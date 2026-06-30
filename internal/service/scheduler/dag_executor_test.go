// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
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

	dagExecutor := scheduler.NewDAGExecutor(coordinatorCli, th.SubCmdBuilder, config.ExecutionModeLocal, "")
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
			exec.DispatchOperationStart,
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
			exec.DispatchOperationStart,
			"execute-dag-test-456",
			nil,
			core.TriggerTypeScheduler,
			"",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to dispatch task")
	})

	t.Run("ExecuteDAG_Distributed_RejectsInvalidOperation", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.ExecuteDAG(
			context.Background(),
			dag,
			exec.DispatchOperationUnspecified,
			"execute-dag-invalid-operation",
			nil,
			core.TriggerTypeScheduler,
			"",
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "operation not specified")

		err = dagExecutor.ExecuteDAG(
			context.Background(),
			dag,
			exec.DispatchOperation(99),
			"execute-dag-unknown-operation",
			nil,
			core.TriggerTypeScheduler,
			"",
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown operation")
	})

	t.Run("HandleJob_Local_ExecutesDirectly", func(t *testing.T) {
		localExecutor := scheduler.NewDAGExecutor(nil, th.SubCmdBuilder, config.ExecutionModeLocal, "")

		dag, err := spec.Load(context.Background(), testDAG.Location)
		require.NoError(t, err)

		err = localExecutor.HandleJob(
			context.Background(),
			dag,
			exec.DispatchOperationStart,
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
			exec.DispatchOperationRetry,
			"handle-job-retry-999",
			core.TriggerTypeScheduler,
			time.Time{},
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to dispatch task")
	})
}

func TestDAGExecutor_DistributedRetryUsesPreviousStatusParamsList(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")

	dag := &core.DAG{
		Name:           "queued-param-dag",
		YamlData:       []byte("name: queued-param-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}
	previousStatus := &exec.DAGRunStatus{
		Status:     core.Queued,
		Params:     "content_hash=sha256:abc123 message=hello world",
		ParamsList: []string{"content_hash=sha256:abc123", "message=hello world"},
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(),
		dag,
		exec.DispatchOperationRetry,
		"queued-param-run",
		previousStatus,
		core.TriggerTypeManual,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Empty(t, dispatcher.req.Task.Params)
	require.NotNil(t, dispatcher.req.Task.PreviousStatus)
	require.Equal(t, previousStatus.ParamsList, dispatcher.req.Task.PreviousStatus.ParamsList)
}

func TestDAGExecutor_DistributedRetryPassesLegacyQueuedParams(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")

	dag := &core.DAG{
		Name:           "legacy-queued-param-dag",
		YamlData:       []byte("name: legacy-queued-param-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}
	previousStatus := &exec.DAGRunStatus{
		Status: core.Queued,
		Params: "content_hash=sha256:abc123",
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(),
		dag,
		exec.DispatchOperationRetry,
		"legacy-queued-param-run",
		previousStatus,
		core.TriggerTypeManual,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Equal(t, "content_hash=sha256:abc123", dispatcher.req.Task.Params)
	require.NotNil(t, dispatcher.req.Task.PreviousStatus)
}

func TestDAGExecutor_DistributedRetryCarriesAdmissionReservationToken(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")

	dag := &core.DAG{
		Name:           "admitted-dag",
		YamlData:       []byte("name: admitted-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}

	err := dagExecutor.ExecuteDAGWithAdmission(
		context.Background(),
		dag,
		exec.DispatchOperationRetry,
		"admitted-run",
		&exec.DAGRunStatus{Status: core.Queued},
		core.TriggerTypeManual,
		"",
		"reservation-token-a",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Equal(t, "reservation-token-a", dispatcher.req.AdmissionReservationToken)
}

type capturingDispatcher struct {
	req exec.DispatchRequest
}

func (d *capturingDispatcher) Dispatch(_ context.Context, req exec.DispatchRequest) error {
	d.req = req
	return nil
}

func (d *capturingDispatcher) Cleanup(context.Context) error {
	return nil
}

func (d *capturingDispatcher) GetDAGRunStatus(context.Context, string, string, *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	return nil, nil
}

func (d *capturingDispatcher) RequestCancel(context.Context, string, string, *exec.DAGRunRef) error {
	return nil
}
