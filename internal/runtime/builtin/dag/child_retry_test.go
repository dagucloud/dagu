// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func TestParallelChildRetryReusesSiblings(t *testing.T) {
	child := &core.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: target\n    run: echo child\n"),
		Steps:    []core.Step{{Name: "target"}},
	}
	parent := &core.DAG{Name: "root", LocalDAGs: map[string]*core.DAG{child.Name: child}}
	runner := &recordingChildRetryRunner{}
	baseCtx := executor.WithSubWorkflowRunner(context.Background(), runner)
	rootRef := exec.NewDAGRunRef(parent.Name, "root-run")
	route := exec.ChildRetryRoute{
		TargetStep: "target",
		Segments: []exec.ChildRetrySegment{{
			ParentStep: "parallel-child",
			DAGRunID:   "child-selected",
			DAGName:    child.Name,
			Runs: []exec.SubDAGRun{
				{DAGRunID: "child-succeeded", DAGName: child.Name, Params: "ITEM=one"},
				{DAGRunID: "child-selected", DAGName: child.Name, Params: "ITEM=two"},
			},
		}},
	}
	ctx := runtime.NewContext(
		baseCtx,
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithChildRetryRoute(route),
	)
	step := core.Step{
		Name:           "parallel-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeParallel},
		SubDAG:         &core.SubDAG{Name: child.Name},
		Parallel:       &core.ParallelConfig{MaxConcurrent: 2},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	parallel := impl.(executor.ParallelExecutor)
	parallel.SetParamsList([]executor.RunParams{
		{RunID: "child-succeeded", DAGName: child.Name, Params: "ITEM=one"},
		{RunID: "child-selected", DAGName: child.Name, Params: "ITEM=two"},
	})
	require.NoError(t, impl.Run(ctx))

	runRequests, retryRequests := runner.requests()
	require.Len(t, runRequests, 1)
	require.Equal(t, "child-succeeded", runRequests[0].RunID)
	require.True(t, runRequests[0].ReuseExisting)
	require.Len(t, retryRequests, 1)
	require.Equal(t, "child-selected", retryRequests[0].RunID)
	require.Equal(t, "target", retryRequests[0].StepName)
	require.Equal(t, "target", retryRequests[0].ChildRetryRoute.TargetStep)
}

type recordingChildRetryRunner struct {
	mu            sync.Mutex
	runRequests   []executor.SubWorkflowRequest
	retryRequests []executor.SubWorkflowRetryRequest
}

func (r *recordingChildRetryRunner) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *recordingChildRetryRunner) Run(_ context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	r.runRequests = append(r.runRequests, req)
	r.mu.Unlock()
	return &exec.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: core.Succeeded}, nil
}

func (r *recordingChildRetryRunner) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	r.retryRequests = append(r.retryRequests, req)
	r.mu.Unlock()
	return &exec.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: core.Succeeded}, nil
}

func (*recordingChildRetryRunner) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}

func (r *recordingChildRetryRunner) requests() ([]executor.SubWorkflowRequest, []executor.SubWorkflowRetryRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]executor.SubWorkflowRequest(nil), r.runRequests...), append([]executor.SubWorkflowRetryRequest(nil), r.retryRequests...)
}
