// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestDAGExecutor(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	testDAG := th.DAG(t, `
params: {mode: default}
steps:
  - name: test-step
    run: echo "test"
`)
	coordinatorCli := coordinator.New(th.ServiceRegistry, test.CoordinatorClientConfig(th.Config.Paths.DataDir))

	dagExecutor := scheduler.NewDAGExecutor(coordinatorCli, th.SubCmdBuilder, config.ExecutionModeLocal, "")
	t.Cleanup(func() {
		dagExecutor.Close(th.Context)
	})

	loadDAGWithWorkerSelector := func(t *testing.T) *ir.DAG {
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
			scheduler.DAGEntry{DefinitionID: testDAG.Location, DAG: dag},
			dispatch.DispatchOperationStart,
			"handle-job-test-123",
			ir.TriggerTypeScheduler,
			time.Time{},
			`{"mode":"z"}`,
		)

		require.NoError(t, err)
		status := test.ReadRunStatus(th.Context, t, th.DAGRunRepository, ir.NewDAGRunRef(dag.Name, "handle-job-test-123"))
		require.Contains(t, status.ParamsList, "mode=z")
	})

	t.Run("ExecuteDAG_Distributed_DispatchesDirectly", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.ExecuteDAG(
			context.Background(),
			dag,
			dispatch.DispatchOperationStart,
			"execute-dag-test-456",
			nil,
			ir.TriggerTypeScheduler,
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
			dispatch.DispatchOperationUnspecified,
			"execute-dag-invalid-operation",
			nil,
			ir.TriggerTypeScheduler,
			"",
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "operation not specified")

		err = dagExecutor.ExecuteDAG(
			context.Background(),
			dag,
			dispatch.DispatchOperation(99),
			"execute-dag-unknown-operation",
			nil,
			ir.TriggerTypeScheduler,
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
			scheduler.DAGEntry{DefinitionID: testDAG.Location, DAG: dag},
			dispatch.DispatchOperationStart,
			"handle-job-local-789",
			ir.TriggerTypeScheduler,
			time.Time{},
			"",
		)
		require.NoError(t, err, "local execution with nil coordinator should succeed")
	})

	t.Run("HandleJob_Retry_BypassesEnqueue", func(t *testing.T) {
		dag := loadDAGWithWorkerSelector(t)

		err := dagExecutor.HandleJob(
			context.Background(),
			scheduler.DAGEntry{DefinitionID: testDAG.Location, DAG: dag},
			dispatch.DispatchOperationRetry,
			"handle-job-retry-999",
			ir.TriggerTypeScheduler,
			time.Time{},
			"",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to dispatch task")
	})
}

func TestDAGExecutorLocalParams(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell executable to capture subprocess input")
	}

	dir := t.TempDir()
	executable := filepath.Join(dir, "capture-input")
	require.NoError(t, os.WriteFile(executable, []byte(`#!/bin/sh
[ "$1" = start ] && [ "$2" = --params-stdin ] || exit 1
cat > "$0.tmp"
mv "$0.tmp" "$0.params"
`), 0700))
	path := filepath.Join(dir, "report.yaml")
	require.NoError(t, os.WriteFile(path, []byte("params: {mode: default}\nsteps:\n  - run: echo report\n"), 0600))
	dag, err := spec.Load(t.Context(), path)
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Paths.Executable = executable
	executor := scheduler.NewDAGExecutor(nil, launcher.NewSubCmdBuilder(cfg), config.ExecutionModeLocal, "")
	const params = `{"mode":"y"}`
	err = executor.HandleJob(t.Context(), scheduler.DAGEntry{DAG: dag},
		dispatch.DispatchOperationStart, "scheduled-report", ir.TriggerTypeScheduler,
		time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC), params)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(executable + ".params")
		return err == nil && string(data) == params
	}, 5*time.Second, 20*time.Millisecond)
}

func TestDAGExecutorBaseConfig(t *testing.T) {
	t.Parallel()
	for _, embedded := range []bool{false, true} {
		for _, smtp := range []bool{false, true} {
			name := "absent"
			if embedded {
				name = "empty"
			}
			if smtp {
				name += "-smtp"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				basePath := filepath.Join(t.TempDir(), "base.yaml")
				base := "env:\n  BASE_SETTING: retained\n"
				if smtp {
					base += "smtp:\n  host: current.example\n"
				}
				require.NoError(t, os.WriteFile(basePath, []byte(base), 0600))
				dag, err := spec.LoadYAML(ctx, []byte("name: queued\nsteps:\n  - run: echo ok\n"))
				require.NoError(t, err)
				if embedded {
					dag.BaseConfigData = []byte("{}")
				}
				dispatcher := &capturingDispatcher{}
				executor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, basePath)
				require.NoError(t, executor.ExecuteDAG(ctx, dag, dispatch.DispatchOperationRetry,
					"queued-run", &ir.DAGRunStatus{Status: ir.Queued}, ir.TriggerTypeManual, ""))
				workerDAG, err := spec.LoadYAML(ctx, dag.YamlData,
					spec.WithBaseConfigContent([]byte(dispatcher.req.Task.BaseConfig)))
				require.NoError(t, err)
				if embedded {
					require.Empty(t, workerDAG.Env)
				} else {
					require.Contains(t, workerDAG.Env, "BASE_SETTING=retained")
				}
				if smtp {
					require.NotNil(t, workerDAG.SMTP)
					require.Equal(t, "current.example", workerDAG.SMTP.Host)
				} else {
					require.Nil(t, workerDAG.SMTP)
				}
			})
		}
	}
}

func TestDAGExecutor_DistributedRetryUsesPreviousStatusParamsList(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")

	dag := &ir.DAG{
		Name:           "queued-param-dag",
		YamlData:       []byte("name: queued-param-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}
	previousStatus := &ir.DAGRunStatus{
		Status:     ir.Queued,
		Params:     "content_hash=sha256:abc123 message=hello world",
		ParamsList: []string{"content_hash=sha256:abc123", "message=hello world"},
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(),
		dag,
		dispatch.DispatchOperationRetry,
		"queued-param-run",
		previousStatus,
		ir.TriggerTypeManual,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Empty(t, dispatcher.req.Task.Params)
	require.NotNil(t, dispatcher.req.Task.PreviousStatus)
	require.Equal(t, previousStatus.ParamsList, dispatcher.req.Task.PreviousStatus.ParamsList)
}

func TestDAGExecutor_DistributedRetryTargetsManagedAgentOwner(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")
	dag := &ir.DAG{Name: "agent-dag", YamlData: []byte("name: agent-dag\n")}
	previousStatus := &ir.DAGRunStatus{
		Status: ir.Waiting,
		Nodes: []*ir.Node{{
			Status: ir.NodeWaiting,
			AgentSession: &ir.AgentSession{
				Provider: "opencode", State: ir.AgentSessionWaiting, OwnerWorkerID: "worker-a",
			},
		}},
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(), dag, dispatch.DispatchOperationRetry, "agent-run",
		previousStatus, ir.TriggerTypeManual, "",
	)

	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Equal(t, "worker-a", dispatcher.req.Task.TargetWorkerID)
}

func TestDAGExecutor_DistributedRetryPassesLegacyQueuedParams(t *testing.T) {
	dispatcher := &capturingDispatcher{}
	dagExecutor := scheduler.NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")

	dag := &ir.DAG{
		Name:           "legacy-queued-param-dag",
		YamlData:       []byte("name: legacy-queued-param-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}
	previousStatus := &ir.DAGRunStatus{
		Status: ir.Queued,
		Params: "content_hash=sha256:abc123",
	}

	err := dagExecutor.ExecuteDAG(
		context.Background(),
		dag,
		dispatch.DispatchOperationRetry,
		"legacy-queued-param-run",
		previousStatus,
		ir.TriggerTypeManual,
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

	dag := &ir.DAG{
		Name:           "admitted-dag",
		YamlData:       []byte("name: admitted-dag\n"),
		WorkerSelector: map[string]string{"type": "test-worker"},
	}

	err := dagExecutor.ExecuteDAGWithAdmission(
		context.Background(),
		dag,
		dispatch.DispatchOperationRetry,
		"admitted-run",
		&ir.DAGRunStatus{Status: ir.Queued},
		ir.TriggerTypeManual,
		"",
		"reservation-token-a",
	)
	require.NoError(t, err)
	require.NotNil(t, dispatcher.req.Task)
	require.Equal(t, "reservation-token-a", dispatcher.req.AdmissionReservationToken)
}

type capturingDispatcher struct {
	req dispatch.DispatchRequest
}

func (d *capturingDispatcher) Dispatch(_ context.Context, req dispatch.DispatchRequest) error {
	d.req = req
	return nil
}

func (d *capturingDispatcher) Cleanup(context.Context) error {
	return nil
}

func (d *capturingDispatcher) GetDAGRunStatus(context.Context, string, string, *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	return nil, nil
}

func (d *capturingDispatcher) RequestCancel(context.Context, string, string, *ir.DAGRunRef) error {
	return nil
}
