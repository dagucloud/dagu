// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
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
