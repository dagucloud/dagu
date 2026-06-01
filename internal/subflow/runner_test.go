// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/subflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ exec.Dispatcher = (*mockDispatcher)(nil)

func TestRunnerShouldRun(t *testing.T) {
	t.Parallel()

	dispatcher := &mockDispatcher{}
	validReq := runtimeexec.SubWorkflowRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: exec.NewDAGRunRef("parent", "root-1"),
		RunID:      "child-1",
	}

	tests := []struct {
		name   string
		runner *subflow.Runner
		req    runtimeexec.SubWorkflowRequest
		want   bool
	}{
		{
			name: "nil runner",
			req:  validReq,
			want: false,
		},
		{
			name:   "missing dispatcher",
			runner: subflow.New(nil, config.ExecutionModeDistributed),
			req:    validReq,
			want:   false,
		},
		{
			name:   "missing child DAG",
			runner: subflow.New(dispatcher, config.ExecutionModeDistributed),
			req: runtimeexec.SubWorkflowRequest{
				RootDAGRun: exec.NewDAGRunRef("parent", "root-1"),
				RunID:      "child-1",
			},
			want: false,
		},
		{
			name:   "missing run ID",
			runner: subflow.New(dispatcher, config.ExecutionModeDistributed),
			req: runtimeexec.SubWorkflowRequest{
				DAG:        &core.DAG{Name: "child"},
				RootDAGRun: exec.NewDAGRunRef("parent", "root-1"),
			},
			want: false,
		},
		{
			name:   "missing root DAG run",
			runner: subflow.New(dispatcher, config.ExecutionModeDistributed),
			req: runtimeexec.SubWorkflowRequest{
				DAG:   &core.DAG{Name: "child"},
				RunID: "child-1",
			},
			want: false,
		},
		{
			name:   "force local wins over distributed mode and selector",
			runner: subflow.New(dispatcher, config.ExecutionModeDistributed),
			req: runtimeexec.SubWorkflowRequest{
				DAG:            &core.DAG{Name: "child", ForceLocal: true},
				RootDAGRun:     exec.NewDAGRunRef("parent", "root-1"),
				RunID:          "child-1",
				WorkerSelector: map[string]string{"role": "gpu"},
			},
			want: false,
		},
		{
			name:   "worker selector uses distributed path in local mode",
			runner: subflow.New(dispatcher, config.ExecutionModeLocal),
			req: runtimeexec.SubWorkflowRequest{
				DAG:            &core.DAG{Name: "child"},
				RootDAGRun:     exec.NewDAGRunRef("parent", "root-1"),
				RunID:          "child-1",
				WorkerSelector: map[string]string{"role": "gpu"},
			},
			want: true,
		},
		{
			name:   "distributed default mode uses distributed path",
			runner: subflow.New(dispatcher, config.ExecutionModeDistributed),
			req:    validReq,
			want:   true,
		},
		{
			name:   "local default mode stays local without selector",
			runner: subflow.New(dispatcher, config.ExecutionModeLocal),
			req:    validReq,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.runner.ShouldRun(context.Background(), tt.req))
		})
	}
}

func TestRunnerRunDispatchesWorkflowRequest(t *testing.T) {
	t.Parallel()

	outputValue := `{"typed":true}`
	var outputVars collections.SyncMap
	outputVars.Store("RESULT", "RESULT=ok")

	dispatcher := &mockDispatcher{
		statuses: []*exec.DAGRunStatusResult{
			{
				Found: true,
				Status: &exec.DAGRunStatus{
					Name:     "child",
					DAGRunID: "child-1",
					Status:   core.Succeeded,
					Params:   "ITEM=1",
					Nodes: []*exec.Node{
						{
							OutputVariables: &outputVars,
							OutputsValue:    &outputValue,
						},
					},
				},
			},
		},
	}
	runner := newFastRunner(dispatcher)

	req := runtimeexec.SubWorkflowRequest{
		DAG: &core.DAG{
			Name:           "child",
			YamlData:       []byte("name: child"),
			BaseConfigData: []byte("child-base"),
		},
		ParentDAG: &core.DAG{
			Name:           "parent",
			BaseConfigData: []byte("parent-base"),
		},
		RootDAGRun:        exec.NewDAGRunRef("parent", "root-1"),
		ParentDAGRun:      exec.NewDAGRunRef("parent", "parent-1"),
		RunID:             "child-1",
		Params:            "ITEM=1",
		WorkerSelector:    map[string]string{"role": "gpu"},
		ExternalStepRetry: true,
	}

	result, err := runner.Run(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, dispatcher.dispatches, 1)
	task := dispatcher.dispatches[0]
	assert.Equal(t, exec.DispatchOperationStart, task.Operation)
	assert.Equal(t, "child-1", task.DAGRunID)
	assert.Equal(t, "child", task.Target)
	assert.Equal(t, "ITEM=1", task.Params)
	assert.Equal(t, "parent", task.RootDAGRunName)
	assert.Equal(t, "root-1", task.RootDAGRunID)
	assert.Equal(t, "parent", task.ParentDAGRunName)
	assert.Equal(t, "parent-1", task.ParentDAGRunID)
	assert.Equal(t, "child-base", task.BaseConfig)
	assert.Equal(t, map[string]string{"role": "gpu"}, task.WorkerSelector)
	assert.True(t, task.ExternalStepRetry)

	assert.Equal(t, core.Succeeded, result.Status)
	assert.Equal(t, "ok", result.Outputs["RESULT"])
	assert.Equal(t, true, result.OutputValues["typed"])
}

func TestRunnerRetryDispatchesPreviousStatus(t *testing.T) {
	t.Parallel()

	previous := &exec.DAGRunStatus{
		Name:      "child",
		DAGRunID:  "child-1",
		ProcGroup: "queue-a",
		Status:    core.Queued,
	}
	dispatcher := &mockDispatcher{
		statuses: []*exec.DAGRunStatusResult{
			{Found: true, Status: previous},
			{
				Found: true,
				Status: &exec.DAGRunStatus{
					Name:     "child",
					DAGRunID: "child-1",
					Status:   core.Succeeded,
				},
			},
		},
	}
	runner := newFastRunner(dispatcher)

	result, err := runner.Retry(context.Background(), runtimeexec.SubWorkflowRetryRequest{
		SubWorkflowRequest: runtimeexec.SubWorkflowRequest{
			DAG: &core.DAG{
				Name:     "child",
				YamlData: []byte("name: child"),
			},
			RootDAGRun:   exec.NewDAGRunRef("parent", "root-1"),
			ParentDAGRun: exec.NewDAGRunRef("parent", "parent-1"),
			RunID:        "child-1",
		},
		StepName: "flaky",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, dispatcher.dispatches, 1)
	task := dispatcher.dispatches[0]
	assert.Equal(t, exec.DispatchOperationRetry, task.Operation)
	assert.Equal(t, "flaky", task.Step)
	assert.Equal(t, previous, task.PreviousStatus)
	assert.Equal(t, "queue-a", task.QueueName)
	assert.Equal(t, core.Succeeded, result.Status)
}

func TestRunnerRetryRejectsEmptyStepName(t *testing.T) {
	t.Parallel()

	dispatcher := &mockDispatcher{}
	runner := newFastRunner(dispatcher)

	result, err := runner.Retry(context.Background(), runtimeexec.SubWorkflowRetryRequest{
		SubWorkflowRequest: runtimeexec.SubWorkflowRequest{
			DAG: &core.DAG{
				Name:     "child",
				YamlData: []byte("name: child"),
			},
			RootDAGRun: exec.NewDAGRunRef("parent", "root-1"),
			RunID:      "child-1",
		},
	})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "retry step name is not set")
	assert.Empty(t, dispatcher.dispatches)
}

func TestRunnerCancelRequestsDispatcherCancel(t *testing.T) {
	t.Parallel()

	dispatcher := &mockDispatcher{}
	runner := newFastRunner(dispatcher)
	root := exec.NewDAGRunRef("parent", "root-1")

	err := runner.Cancel(context.Background(), runtimeexec.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-1",
	})
	require.NoError(t, err)

	require.Len(t, dispatcher.cancels, 1)
	cancel := dispatcher.cancels[0]
	assert.Equal(t, "child", cancel.name)
	assert.Equal(t, "child-1", cancel.id)
	require.NotNil(t, cancel.root)
	assert.Equal(t, root, *cancel.root)
}

func newFastRunner(dispatcher exec.Dispatcher) *subflow.Runner {
	return subflow.New(
		dispatcher,
		config.ExecutionModeDistributed,
		subflow.WithPollInterval(time.Millisecond),
		subflow.WithLogInterval(time.Hour),
	)
}

type mockDispatcher struct {
	dispatches []*exec.DispatchTask
	statuses   []*exec.DAGRunStatusResult
	cancels    []cancelRequest
}

type cancelRequest struct {
	name string
	id   string
	root *exec.DAGRunRef
}

func (m *mockDispatcher) Dispatch(_ context.Context, task *exec.DispatchTask) error {
	m.dispatches = append(m.dispatches, task)
	return nil
}

func (m *mockDispatcher) Cleanup(context.Context) error {
	return nil
}

func (m *mockDispatcher) GetDAGRunStatus(
	_ context.Context,
	_ string,
	_ string,
	_ *exec.DAGRunRef,
) (*exec.DAGRunStatusResult, error) {
	if len(m.statuses) == 0 {
		return &exec.DAGRunStatusResult{Found: false}, nil
	}
	status := m.statuses[0]
	m.statuses = m.statuses[1:]
	return status, nil
}

func (m *mockDispatcher) RequestCancel(_ context.Context, name, id string, root *exec.DAGRunRef) error {
	m.cancels = append(m.cancels, cancelRequest{name: name, id: id, root: root})
	return nil
}
