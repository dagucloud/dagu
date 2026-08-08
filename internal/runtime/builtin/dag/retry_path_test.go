// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func TestParallelRetryPathReusesSiblings(t *testing.T) {
	child := &ir.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: target\n    run: echo child\n"),
		Steps:    []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{}
	baseCtx := executor.WithSubWorkflowRunner(context.Background(), runner)
	rootRef := exec.NewDAGRunRef(parent.Name, "root-run")
	path := exec.RetryPath{
		Step: "target",
		Hops: []exec.RetryHop{{
			Step:  "parallel-child",
			RunID: "child-selected",
		}},
	}
	ctx := runtime.NewContext(
		baseCtx,
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(path),
	)
	step := ir.Step{
		Name:           "parallel-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeParallel},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Parallel:       &ir.ParallelConfig{MaxConcurrent: 2},
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
	require.True(t, runRequests[0].Reuse)
	require.Len(t, retryRequests, 1)
	require.Equal(t, "child-selected", retryRequests[0].RunID)
	require.Equal(t, "target", retryRequests[0].StepName)
	require.Equal(t, "target", retryRequests[0].RetryPath.Step)
}

// TestSubDAGRetryPathRejectsUnknownTarget asserts that a retry aimed at a child
// DAG run the step does not produce fails instead of starting a fresh child run.
func TestSubDAGRetryPathRejectsUnknownTarget(t *testing.T) {
	child := &ir.DAG{
		Name:     "child",
		YamlData: []byte("name: child\nsteps:\n  - name: target\n    run: echo child\n"),
		Steps:    []ir.Step{{Name: "target"}},
	}
	parent := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	runner := &retryRecorder{}
	baseCtx := executor.WithSubWorkflowRunner(context.Background(), runner)
	rootRef := exec.NewDAGRunRef(parent.Name, "root-run")
	ctx := runtime.NewContext(
		baseCtx,
		parent,
		rootRef.ID,
		"",
		runtime.WithRootDAGRun(rootRef),
		runtime.WithRetryPath(exec.RetryPath{
			Step: "target",
			Hops: []exec.RetryHop{{Step: "run-child", RunID: "child-stale"}},
		}),
	)
	step := ir.Step{
		Name:           "run-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAG},
		SubDAG:         &ir.SubDAG{Name: child.Name},
	}

	impl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	impl.(executor.DAGExecutor).SetParams(executor.RunParams{
		RunID:   "child-rebuilt",
		DAGName: child.Name,
	})

	err = impl.Run(ctx)
	require.ErrorContains(t, err, "target child DAG run child-stale is not present in step run-child")

	runRequests, retryRequests := runner.requests()
	require.Empty(t, runRequests)
	require.Empty(t, retryRequests)
}

type retryRecorder struct {
	mu      sync.Mutex
	runs    []executor.SubWorkflowRequest
	retries []executor.SubWorkflowRetryRequest
}

func (r *retryRecorder) ShouldRun(context.Context, executor.SubWorkflowRequest) bool {
	return true
}

func (r *retryRecorder) Run(_ context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	r.runs = append(r.runs, req)
	r.mu.Unlock()
	return &exec.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: ir.Succeeded}, nil
}

func (r *retryRecorder) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	r.mu.Lock()
	r.retries = append(r.retries, req)
	r.mu.Unlock()
	return &exec.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Params: req.Params, Status: ir.Succeeded}, nil
}

func (*retryRecorder) Cancel(context.Context, executor.SubWorkflowCancelRequest) error {
	return nil
}

func (r *retryRecorder) requests() ([]executor.SubWorkflowRequest, []executor.SubWorkflowRetryRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]executor.SubWorkflowRequest(nil), r.runs...), append([]executor.SubWorkflowRetryRequest(nil), r.retries...)
}
