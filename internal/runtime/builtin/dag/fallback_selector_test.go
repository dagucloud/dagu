// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	dagbuiltin "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureSubWorkflowRunner struct {
	requests []executor.SubWorkflowRequest
	result   *exec.RunStatus
}

func (r *captureSubWorkflowRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *captureSubWorkflowRunner) Run(_ context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	r.requests = append(r.requests, req)
	return r.result, nil
}

func (r *captureSubWorkflowRunner) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	r.requests = append(r.requests, req.SubWorkflowRequest)
	return r.result, nil
}

func (r *captureSubWorkflowRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}

const paramDrivenSelectorChildYAML = `
name: child
params:
  - FACILITY: serverA
worker_selector:
  host: ${FACILITY}
steps:
  - name: step
    command: echo child
`

func newFallbackContext(t *testing.T, parent *core.DAG, runner executor.SubWorkflowRunner) context.Context {
	t.Helper()
	th := test.Setup(t)
	root := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(th.Context, parent, root.ID, "", runtime.WithRootDAGRun(root))
	return executor.WithSubWorkflowRunner(ctx, runner)
}

func parentWithChild(t *testing.T, yaml string, opts ...spec.LoadOption) *core.DAG {
	t.Helper()
	child, err := spec.LoadYAML(t.Context(), []byte(yaml), opts...)
	require.NoError(t, err)
	return &core.DAG{
		Name:      "parent",
		LocalDAGs: map[string]*core.DAG{"child": child},
	}
}

func runFallbackExecutor(
	t *testing.T,
	parent *core.DAG,
	executorType string,
	runParams executor.RunParams,
) ([]executor.SubWorkflowRequest, error) {
	t.Helper()
	runner := &captureSubWorkflowRunner{
		result: &exec.RunStatus{Name: "child", DAGRunID: "child-run", Status: core.Succeeded},
	}
	ctx := newFallbackContext(t, parent, runner)
	step := core.Step{
		Name:           "run-child",
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
		SubDAG:         &core.SubDAG{Name: "child"},
	}
	if executorType == core.ExecutorTypeParallel {
		step.Parallel = &core.ParallelConfig{}
	}

	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	switch typed := execImpl.(type) {
	case executor.DAGExecutor:
		typed.SetParams(runParams)
	case executor.ParallelExecutor:
		typed.SetParamsList([]executor.RunParams{runParams})
	default:
		t.Fatalf("unexpected executor type %T", execImpl)
	}

	err = execImpl.Run(ctx)
	return runner.requests, err
}

func singleRequest(t *testing.T, requests []executor.SubWorkflowRequest) executor.SubWorkflowRequest {
	t.Helper()
	require.Len(t, requests, 1)
	return requests[0]
}

func TestSubDAGExecutorFallbackReflectsParamOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorType string
	}{
		{name: "DAG", executorType: core.ExecutorTypeDAG},
		{name: "Parallel", executorType: core.ExecutorTypeParallel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requests, err := runFallbackExecutor(
				t,
				parentWithChild(t, paramDrivenSelectorChildYAML),
				tt.executorType,
				executor.RunParams{RunID: "child-run", DAGName: "child", Params: "FACILITY=serverB"},
			)
			require.NoError(t, err)
			request := singleRequest(t, requests)
			assert.Equal(t, map[string]string{"host": "serverB"}, request.WorkerSelector)
			assert.Equal(t, `FACILITY="serverB"`, request.Params)
		})
	}
}

func TestDAGExecutorFallbackCarriesResolvedParamSnapshot(t *testing.T) {
	t.Setenv("SELECTED_FACILITY", "serverA")

	parent := parentWithChild(t, `
name: child
params:
  - name: FACILITY
    eval: "$SELECTED_FACILITY"
worker_selector:
  host: ${FACILITY}
steps:
  - name: step
    command: echo child
`, spec.WithoutEval())
	requests, err := runFallbackExecutor(
		t,
		parent,
		core.ExecutorTypeDAG,
		executor.RunParams{RunID: "child-run", DAGName: "child"},
	)
	require.NoError(t, err)
	request := singleRequest(t, requests)
	assert.Equal(t, map[string]string{"host": "serverA"}, request.WorkerSelector)
	assert.Equal(t, `FACILITY="serverA"`, request.Params)
}

func TestDAGExecutorFallbackReturnsParamResolutionError(t *testing.T) {
	t.Parallel()

	parent := parentWithChild(t, `
name: child
params:
  - name: FACILITY
    type: string
    required: true
worker_selector:
  host: ${FACILITY}
steps:
  - name: step
    command: echo child
`, spec.WithoutEval())
	requests, err := runFallbackExecutor(
		t,
		parent,
		core.ExecutorTypeDAG,
		executor.RunParams{RunID: "child-run", DAGName: "child"},
	)
	require.ErrorContains(t, err, "FACILITY")
	assert.Empty(t, requests)
}

func TestDAGExecutorApprovalGuardOnFallback(t *testing.T) {
	t.Parallel()

	parent := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:           "child",
				YamlData:       []byte(paramDrivenSelectorChildYAML),
				WorkerSelector: map[string]string{"host": "serverA"},
				Steps: []core.Step{
					{Name: "gate", Approval: &core.ApprovalConfig{Prompt: "approve?"}},
				},
			},
		},
	}
	requests, err := runFallbackExecutor(
		t,
		parent,
		core.ExecutorTypeDAG,
		executor.RunParams{RunID: "child-run", DAGName: "child", Params: "FACILITY=serverB"},
	)
	require.ErrorIs(t, err, dagbuiltin.ErrApprovalStepsWithWorker)
	assert.Empty(t, requests)
}
